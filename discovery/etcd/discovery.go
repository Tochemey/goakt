// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package etcd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/namespace"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/internal/locker"
)

// Discovery is the etcd service discovery implementation.
// It provides methods to register and discover actors in an etcd cluster.
type Discovery struct {
	_               locker.NoCopy
	config          *Config
	initialized     *atomic.Bool
	registered      *atomic.Bool
	mu              *sync.RWMutex
	leaseID         clientv3.LeaseID
	client          *clientv3.Client
	key             string
	cancelKeepAlive context.CancelFunc
	prefix          string
	namespaceKV     clientv3.KV
	namespaceLE     clientv3.Lease
}

var _ discovery.Provider = (*Discovery)(nil)

// NewDiscovery creates a new instance of Discovery with the provided configuration.
func NewDiscovery(config *Config) *Discovery {
	// shallow-copy the user's Config so Initialize defaults don't mutate the caller's pointer
	cfg := *config
	return &Discovery{
		config:      &cfg,
		initialized: atomic.NewBool(false),
		registered:  atomic.NewBool(false),
		mu:          &sync.RWMutex{},
		key:         net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.DiscoveryPort)),
		prefix:      fmt.Sprintf("%s/", cfg.ActorSystemName),
	}
}

// ID implements discovery.Provider.
func (x *Discovery) ID() string {
	return discovery.ProviderEtcd
}

// Initialize implements discovery.Provider.
func (x *Discovery) Initialize() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	if x.config.Context == nil {
		x.config.Context = context.Background()
	}

	if err := x.config.Validate(); err != nil {
		return err
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   x.config.Endpoints,
		DialTimeout: x.config.DialTimeout,
		TLS:         x.config.TLS,
		Username:    x.config.Username,
		Password:    x.config.Password,
		Context:     x.config.Context,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(x.config.Context, x.config.DialTimeout)
	defer cancel()

	// MemberList probes the cluster via any reachable endpoint, so a single bad
	// entry in Endpoints doesn't fail Initialize.
	if _, err = client.MemberList(ctx); err != nil {
		err = fmt.Errorf("failed to connect to etcd: %w", err)
		if cerr := client.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("failed to close etcd client: %w", cerr))
		}
		return err
	}

	x.client = client
	x.namespaceKV = namespace.NewKV(x.client.KV, x.prefix)
	x.namespaceLE = namespace.NewLease(x.client.Lease, x.prefix)
	x.initialized.Store(true)
	return nil
}

// Register implements discovery.Provider.
func (x *Discovery) Register() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.initialized.Load() {
		return discovery.ErrNotInitialized
	}

	if x.registered.Load() {
		return discovery.ErrAlreadyRegistered
	}

	ctx, cancel := context.WithTimeout(x.config.Context, x.config.Timeout)
	defer cancel()

	lease, err := x.namespaceLE.Grant(ctx, x.config.TTL)
	if err != nil {
		return fmt.Errorf("failed to create lease: %w", err)
	}

	if _, err = x.namespaceKV.Put(ctx, x.key, x.key, clientv3.WithLease(lease.ID)); err != nil {
		// orphaned lease will expire after TTL, but revoke eagerly to keep etcd clean
		_, _ = x.namespaceLE.Revoke(ctx, lease.ID)
		return fmt.Errorf("failed to register service: %w", err)
	}

	// Start lease keep-alive
	keepAliveCtx, keepAliveCancel := context.WithCancel(x.config.Context)
	ch, kaerr := x.client.KeepAlive(keepAliveCtx, lease.ID)
	if kaerr != nil {
		keepAliveCancel()
		_, _ = x.namespaceLE.Revoke(ctx, lease.ID)
		return fmt.Errorf("failed to start keep-alive: %w", kaerr)
	}

	x.leaseID = lease.ID
	x.cancelKeepAlive = keepAliveCancel

	// Drain keep-alive responses so the channel never blocks the etcd client.
	// ch is closed by etcd when keepAliveCtx is cancelled, ending this goroutine.
	go func() {
		for ka := range ch {
			_ = ka
		}
	}()

	x.registered.Store(true)
	return nil
}

// DiscoverPeers implements discovery.Provider.
func (x *Discovery) DiscoverPeers() ([]string, error) {
	x.mu.RLock()
	if !x.initialized.Load() {
		x.mu.RUnlock()
		return nil, discovery.ErrNotInitialized
	}
	if !x.registered.Load() {
		x.mu.RUnlock()
		return nil, discovery.ErrNotRegistered
	}
	kv := x.namespaceKV
	clientCtx := x.client.Ctx()
	x.mu.RUnlock()

	ctx, cancel := context.WithTimeout(clientCtx, x.config.Timeout)
	defer cancel()

	resp, err := kv.Get(ctx, "", clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to discover peers: %w", err)
	}

	// etcd keys are unique, so no set dedup is needed.
	peers := make([]string, 0, len(resp.Kvs))
	for _, item := range resp.Kvs {
		if string(item.Key) == x.key {
			continue
		}
		peers = append(peers, string(item.Value))
	}
	return peers, nil
}

// Deregister implements discovery.Provider.
func (x *Discovery) Deregister() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.initialized.Load() {
		return discovery.ErrNotInitialized
	}

	if !x.registered.Load() {
		return discovery.ErrNotRegistered
	}

	x.deregisterLocked()
	return nil
}

// deregisterLocked cancels the keep-alive and revokes the lease.
// Caller must hold x.mu in write mode and must have verified registered==true.
func (x *Discovery) deregisterLocked() {
	defer x.registered.Store(false)

	if x.cancelKeepAlive != nil {
		x.cancelKeepAlive()
		x.cancelKeepAlive = nil
	}

	if x.leaseID != 0 {
		ctx, cancel := context.WithTimeout(x.client.Ctx(), x.config.Timeout)
		defer cancel()

		// Best-effort: the lease will expire naturally if revoke fails (e.g. etcd unreachable).
		_, _ = x.namespaceLE.Revoke(ctx, x.leaseID)
		x.leaseID = 0
	}
}

// Close implements discovery.Provider.
func (x *Discovery) Close() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.registered.Load() {
		x.deregisterLocked()
	}

	if x.client != nil {
		if err := x.client.Close(); err != nil {
			return fmt.Errorf("failed to close etcd client: %w", err)
		}
		x.client = nil
	}

	x.initialized.Store(false)
	return nil
}
