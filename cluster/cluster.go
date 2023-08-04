package cluster

import (
	"context"
	"time"

	"github.com/buraksezer/olric"
	"github.com/buraksezer/olric/config"
	olriconfig "github.com/buraksezer/olric/config"
	"github.com/buraksezer/olric/hasher"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/telemetry"
)

// Cluster represents the Cluster
type Cluster struct {
	// specifies the total number of partitions
	// the default values is 20
	partitionsCount uint64

	// specifies the logger
	logger log.Logger

	// specifies the cluster name
	name string

	// specifies the cluster server
	server *olric.Olric
	// specifies the cluster client
	// this help set and fetch data from the cluster
	client olric.Client

	// specifies the distributed key value store
	kvStore olric.DMap

	// specifies the cluster host
	host *node

	// specifies the hasher
	hasher hasher.Hasher

	// specifies the discovery provider
	discoveryProvider discovery.Provider
	// specifies the discovery options
	discoveryOptions discovery.Config

	writeTimeout    time.Duration
	readTimeout     time.Duration
	shutdownTimeout time.Duration
}

// New creates an instance of Cluster
func New(name string, serviceDiscovery *discovery.ServiceDiscovery, opts ...Option) (*Cluster, error) {
	// create an instance of the cluster
	cl := &Cluster{
		partitionsCount:   20,
		logger:            log.DefaultLogger,
		name:              name,
		discoveryProvider: serviceDiscovery.Provider(),
		discoveryOptions:  serviceDiscovery.Config(),
		writeTimeout:      time.Second,
		readTimeout:       time.Second,
		shutdownTimeout:   3 * time.Second,
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(cl)
	}

	// get the host info
	hostNode, err := getHostNode()
	// handle the error
	if err != nil {
		cl.logger.Error(errors.Wrap(err, "failed get the host node.💥"))
		return nil, err
	}

	// set the host node
	cl.host = hostNode

	return cl, nil
}

// Start starts the Cluster.
func (c *Cluster) Start(ctx context.Context) error {
	// set the logger
	logger := c.logger

	// add some logging information
	logger.Infof("Starting GoAkt cluster service on (%s)....🤔", c.host.ClusterAddress())

	// let us delay the start for sometime to make sure we have discovered enough nodes to form a cluster
	time.Sleep(time.Second)

	// build the cluster engine config
	conf := c.buildConfig()

	// set the hasher
	c.hasher = conf.Hasher

	// create the member list config
	m, err := olriconfig.NewMemberlistConfig("lan")
	// panic when there is an error
	if err != nil {
		logger.Error(errors.Wrap(err, "failed to configure the cluster memberlist.💥"))
		return err
	}

	// sets the bindings
	m.BindAddr = c.host.Host
	m.BindPort = c.host.GossipPort
	m.AdvertisePort = c.host.GossipPort
	conf.MemberlistConfig = m

	// set the discovery provider
	discoveryWrapper := &discoveryProvider{
		provider: c.discoveryProvider,
		log:      c.logger.StdLogger(),
	}
	// set the discovery service
	conf.ServiceDiscovery = map[string]any{
		"plugin":  discoveryWrapper,
		"id":      c.discoveryProvider.ID(),
		"options": c.discoveryOptions,
	}

	// let us start the cluster
	startCtx, cancel := context.WithCancel(ctx)
	// cancel the context the server has started
	conf.Started = func() {
		// cancel the start context
		defer cancel()
		// add some logging information
		logger.Info("GoAkt cluster Server successfully started. 🤌")
	}

	// let us create an instance of the cluster engine
	eng, err := olric.New(conf)
	// handle the error
	if err != nil {
		logger.Error(errors.Wrap(err, "failed to start the cluster engine.💥"))
		return err
	}

	// set the server
	c.server = eng
	go func() {
		// start the cluster engine
		err = c.server.Start()
		// handle the error in case there is an early error
		if err != nil {
			logger.Error(errors.Wrap(err, "failed to start the cluster engine.💥"))
			// let us stop the started engine
			if e := c.server.Shutdown(ctx); e != nil {
				logger.Panic(e)
			}
		}
	}()

	// wait for start
	<-startCtx.Done()

	// set the client
	c.client = c.server.NewEmbeddedClient()
	// create the instance of the distributed map
	dmp, err := c.client.NewDMap(c.name)
	// handle the error
	if err != nil {
		logger.Error(errors.Wrap(err, "failed to start the cluster engine.💥"))
		// let us stop the started engine
		return c.server.Shutdown(ctx)
	}

	// set the distributed map
	c.kvStore = dmp
	// we are done bootstrapping the cluster
	logger.Info("GoAkt cluster successfully started. 🎉")
	return nil
}

// Stop stops the Cluster gracefully
func (c *Cluster) Stop(ctx context.Context) error {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, c.shutdownTimeout)
	defer cancelFn()

	// set the logger
	logger := c.logger

	// add some logging information
	logger.Info("Stopping GoAkt cluster....🤔")

	// close the cluster client
	if err := c.client.Close(ctx); err != nil {
		logger.Error(errors.Wrap(err, "failed to shutdown the cluster client.💥"))
		return err
	}

	// let us stop the server
	if err := c.server.Shutdown(ctx); err != nil {
		logger.Error(errors.Wrap(err, "failed to Stop  GoAkt cluster....💥"))
		return err
	}

	logger.Info("GoAkt cluster successfully stopped.🎉")
	return nil
}

// NodeHost returns the cluster node Host
func (c *Cluster) NodeHost() string {
	return c.host.Host
}

// PutActor replicates onto the cluster the metadata of an actor
func (c *Cluster) PutActor(ctx context.Context, actor *goaktpb.WireActor) error {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, c.writeTimeout)
	defer cancelFn()

	// add a span to trace this call
	ctx, span := telemetry.SpanContext(ctx, "PutActor")
	defer span.End()

	// set the logger
	logger := c.logger

	// add some logging information
	logger.Infof("replicating actor (%s).🤔", actor.GetActorName())

	// let us marshal it
	data, err := encode(actor)
	// handle the marshaling error
	if err != nil {
		// add a logging to the stderr
		logger.Error(errors.Wrapf(err, "failed to persist actor=%s data in the cluster.💥", actor.GetActorName()))
		// here we cancel the request
		return errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName())
	}

	// send the record into the cluster
	err = c.kvStore.Put(ctx, actor.GetActorName(), data)
	// handle the error
	if err != nil {
		// log the error
		logger.Error(errors.Wrapf(err, "failed to replicate actor=%s record.💥", actor.GetActorName()))
		return err
	}

	// Ahoy we are successful
	logger.Infof("actor (%s) successfully replicated.🎉", actor.GetActorName())
	return nil
}

// GetActor fetches an actor from the cluster
func (c *Cluster) GetActor(ctx context.Context, actorName string) (*goaktpb.WireActor, error) {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, c.readTimeout)
	defer cancelFn()

	// add a span to trace this call
	ctx, span := telemetry.SpanContext(ctx, "GetActor")
	defer span.End()

	// set the logger
	logger := c.logger

	// add some logging information
	logger.Infof("retrieving actor (%s) from the cluster.🤔", actorName)

	// grab the record from the distributed store
	resp, err := c.kvStore.Get(ctx, actorName)
	// handle the error
	if err != nil {
		// we could not find the given actor
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("actor=%s is not found in the cluster", actorName)
			return nil, ErrActorNotFound
		}
		// log the error
		logger.Error(errors.Wrapf(err, "failed to get actor=%s record.💥", actorName))
		return nil, err
	}

	// grab the base64 representation of the wire actor
	base64ActorStr, err := resp.String()
	// handle the error
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to read the record at:{%s}.💥", actorName))
		return nil, err
	}

	// decode it
	actor, err := decode(base64ActorStr)
	// let us unpack the byte array
	if err != nil {
		// log the error and return
		logger.Error(errors.Wrapf(err, "failed to decode actor=%s record.💥", actorName))
		return nil, err
	}

	// Ahoy we are successful
	logger.Infof("actor (%s) successfully retrieved from the cluster.🎉", actor.GetActorName())
	// return the response
	return actor, nil
}

// GetPartition returns the partition where a given actor is stored
func (c *Cluster) GetPartition(actorName string) int {
	// create the byte array of the actor name
	key := []byte(actorName)
	// compute the hash key
	hkey := c.hasher.Sum64(key)
	// compute the partition and return it
	return int(hkey % c.partitionsCount)
}

// buildConfig builds the cluster configuration
func (c *Cluster) buildConfig() *config.Config {
	// define the log level
	logLevel := "INFO"
	switch c.logger.LogLevel() {
	case log.DebugLevel:
		logLevel = "DEBUG"
	case log.ErrorLevel, log.FatalLevel, log.PanicLevel:
		logLevel = "ERROR"
	case log.WarningLevel:
		logLevel = "WARN"
	}

	// create the config and return it
	return &config.Config{
		BindAddr:                   c.host.Host,
		BindPort:                   c.host.ClusterPort,
		ReadRepair:                 false,
		ReplicaCount:               config.MinimumReplicaCount,
		WriteQuorum:                config.DefaultWriteQuorum,
		ReadQuorum:                 config.DefaultReadQuorum,
		MemberCountQuorum:          config.DefaultMemberCountQuorum,
		Peers:                      []string{},
		DMaps:                      &olriconfig.DMaps{},
		KeepAlivePeriod:            config.DefaultKeepAlivePeriod,
		PartitionCount:             c.partitionsCount,
		BootstrapTimeout:           config.DefaultBootstrapTimeout,
		ReplicationMode:            olriconfig.SyncReplicationMode,
		RoutingTablePushInterval:   config.DefaultRoutingTablePushInterval,
		JoinRetryInterval:          config.DefaultJoinRetryInterval,
		MaxJoinAttempts:            config.DefaultMaxJoinAttempts,
		LogLevel:                   logLevel,
		Logger:                     c.logger.StdLogger(),
		LogVerbosity:               config.DefaultLogVerbosity,
		EnableClusterEventsChannel: true,
	}
}
