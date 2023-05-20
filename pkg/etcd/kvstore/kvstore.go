package kvstore

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/flowchartsman/retry"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/telemetry"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
	"go.etcd.io/etcd/client/v3/namespace"
	"go.opentelemetry.io/otel/trace"
)

const (
	sessionTTL               = 30 // used for etcd mutexes and liveness key
	getTimeout               = 5 * time.Second
	putTimeout               = 5 * time.Second
	deleteTimeout            = 5 * time.Second
	broadcastTimeout         = 5 * time.Second
	shutdownTimeout          = 5 * time.Second
	livenessBroadcastAttempt = 3
)

// Config defines the distributed store config
type Config struct {
	logger    log.Logger
	endPoints []string
	// define the etcd client
	client *clientv3.Client
}

// NewConfig creates an instance of Config
func NewConfig(logger log.Logger, client *clientv3.Client) *Config {
	return &Config{
		logger: logger,
		client: client,
	}
}

// KVStore defines a clustered key value store
type KVStore struct {
	clientv3.KV
	clientv3.Lease
	clientv3.Watcher
	// Namespaced Session for session and concurrency operations in the KVStore
	*concurrency.Session
	*clientv3.Client

	namespace       string
	stopChan        chan struct{}
	stopOnce        sync.Once
	namespaceClient *clientv3.Client
	logger          log.Logger
	config          *Config

	name string
}

// New creates an instance of KVStore
func New(config *Config) (*KVStore, error) {
	// create an instance of kv store
	store := &KVStore{
		stopChan: make(chan struct{}, 1),
		stopOnce: sync.Once{},
		logger:   config.logger,
		config:   config,
		Client:   config.client,
	}

	// create the namespaced store
	store.name = uuid.NewString()
	namespaceKey := fmt.Sprintf("goaktcluster-%s/", uuid.NewString())
	// Create namespaced interfaces
	kv := namespace.NewKV(store.Client.KV, namespaceKey)
	lease := namespace.NewLease(store.Client.Lease, namespaceKey)
	watcher := namespace.NewWatcher(store.Client.Watcher, namespaceKey)

	// let us create a new session (lease kept alive for the lifetime of a client)
	// This is currently used for:
	// * distributed locking (Mutex)
	// * representing liveness of the client

	// let us create a client with the namespaced variants KV, Lease and Watcher for creating a namespaced Session
	nc := clientv3.NewCtxClient(store.Client.Ctx())
	nc.KV = kv
	nc.Lease = lease
	nc.Watcher = watcher

	// let us create new Session from the new namespaced client
	session, err := concurrency.NewSession(nc, concurrency.WithTTL(sessionTTL))
	// handle the error
	if err != nil {
		return nil, errors.Wrap(err, "failed to create store session")
	}

	// set the store fields
	store.KV = kv
	store.Lease = lease
	store.Watcher = watcher
	store.Session = session
	store.namespace = namespaceKey
	store.stopChan = make(chan struct{}, 1)

	return store, nil
}

// Shutdown closes the store connections
func (s *KVStore) Shutdown() error {
	// revoke liveness
	if err := s.revokeLiveness(context.Background(), shutdownTimeout); err != nil {
		return err
	}

	s.stopOnce.Do(func() {
		close(s.stopChan)
	})

	return nil
}

// GetValue retrieves the value of a given key from the store
func (s *KVStore) GetValue(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	// create a cancellation context
	var cancel context.CancelFunc

	// make sure we have a valid context
	if ctx == context.TODO() {
		ctx, cancel = context.WithTimeout(context.Background(), getTimeout)
		defer cancel()
	} else {
		// create a span context
		var span trace.Span
		ctx, span = telemetry.SpanContext(ctx, "GetValue")
		defer span.End()
	}

	return s.Get(ctx, key, opts...)
}

// SetValue sets the value of a given key unto the store
func (s *KVStore) SetValue(ctx context.Context, key, val string, opts ...clientv3.OpOption) (*clientv3.PutResponse, error) {
	// create a cancellation context
	var cancel context.CancelFunc

	// make sure we have a valid context
	if ctx == context.TODO() {
		ctx, cancel = context.WithTimeout(context.Background(), putTimeout)
		defer cancel()
	} else {
		// create a span context
		var span trace.Span
		ctx, span = telemetry.SpanContext(ctx, "SetValue")
		defer span.End()
	}

	return s.Put(ctx, key, val, opts...)
}

// DeleteKey deletes a given key from the store
func (s *KVStore) DeleteKey(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.DeleteResponse, error) {
	// create a cancellation context
	var cancel context.CancelFunc

	// make sure we have a valid context
	if ctx == context.TODO() {
		ctx, cancel = context.WithTimeout(context.Background(), deleteTimeout)
		defer cancel()
	} else {
		// create a span context
		var span trace.Span
		ctx, span = telemetry.SpanContext(ctx, "DeleteKey")
		defer span.End()
	}

	return s.Delete(ctx, key, opts...)
}

// UpdateEndpoints updates the configured endpoints and saves them
func (s *KVStore) UpdateEndpoints() error {
	// synchronize the endpoints and return the error in case there is any
	if err := s.Sync(s.Ctx()); err != nil {
		return err
	}

	s.config.endPoints = s.Client.Endpoints()
	// TODO add the save feature
	return nil
}

// isStoreHealthy checks if store is reachable from the node.
// Get a random key.If we get the response without an error,
// the endpoint is healthy.
func (s *KVStore) isStoreHealthy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), getTimeout)
	defer cancel()
	_, err := s.Get(ctx, "health")
	return err == nil
}

// keepSessionAlive configures a new session for GDStore if existing
// session lease expires, or no longer being refreshed. It checks
// session lease information and store endpoint health on a regular interval.
// A session lease will get expire in many situations like if there is a
// reconnection with etcd server.
func (s *KVStore) keepSessionAlive() {
	var (
		ticker = time.NewTicker(time.Second * 5)
	)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			// check if lease is orphaned, expires, or no longer being refreshed.
			<-s.Session.Done()
			s.logger.Debugf("granted session=%d has expired", s.Session.Lease())

			// check whether the store is healthy
			if !s.isStoreHealthy() {
				s.logger.Warn("etcd server is not reachable from this node, " +
					"make sure network connection is active and etcd is running")
				continue
			}

			// add logging information
			s.logger.Debug("reconnection to etcd server has been detected")

			// create a new session for Store
			session, err := concurrency.NewSession(s.Client, concurrency.WithTTL(sessionTTL))
			// handle the error
			if err != nil {
				s.logger.Error(errors.Wrap(err, "failed to create an etcd session"))
				continue
			}

			// set session
			s.Session = session
			s.logger.Debug("new etcd session created successfully")

			// exponentially try to broadcast liveness
			// create a new retrier that will try a maximum of `livenessBroadcastAttempt` times, with
			// an initial delay of 100 ms and a maximum delay of 1 second
			retrier := retry.NewRetrier(livenessBroadcastAttempt, 100*time.Millisecond, time.Second)
			// attempt to broadcast liveness
			if err := retrier.RunContext(context.Background(), func(ctx context.Context) error {
				return s.broadcastLiveness(ctx, broadcastTimeout)
			}); err != nil {
				s.logger.Error(errors.Wrap(err, "failed to create an etcd session"))
			}
		}
	}
}
