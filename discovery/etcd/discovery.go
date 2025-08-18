/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package etcd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	goset "github.com/deckarep/golang-set/v2"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/namespace"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/internal/locker"
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
}

var _ discovery.Provider = (*Discovery)(nil)

func New(config *Config) *Discovery {
	return &Discovery{
		config:      config,
		initialized: atomic.NewBool(false),
		registered:  atomic.NewBool(false),
		mu:          &sync.RWMutex{},
		key:         net.JoinHostPort(config.Host, strconv.Itoa(config.DiscoveryPort)),
	}
}

// ID implements discovery.Provider.
func (d *Discovery) ID() string {
	return discovery.ProviderEtcd
}

// Initialize implements discovery.Provider.
func (d *Discovery) Initialize() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	if d.config.Context == nil {
		d.config.Context = context.Background()
	}

	if err := d.config.Validate(); err != nil {
		return err
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   d.config.Endpoints,
		DialTimeout: d.config.DialTimeout,
		TLS:         d.config.TLS,
		Username:    d.config.Username,
		Password:    d.config.Password,
		Context:     d.config.Context,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(d.config.Context, d.config.DialTimeout)
	defer cancel()

	// TODO: maybe assert more the response from etcd
	_, err = client.Status(ctx, d.config.Endpoints[0])
	if err != nil {
		if cerr := client.Close(); cerr != nil {
			return errors.Join(err, fmt.Errorf("failed to close etcd client: %w", cerr))
		}
		return fmt.Errorf("failed to connect to etcd: %w", err)
	}

	d.client = client
	prefix := fmt.Sprintf("/%s/", d.config.ActorSystemName)
	d.client.KV = namespace.NewKV(d.client.KV, prefix)
	d.client.Lease = namespace.NewLease(d.client.Lease, prefix)
	d.initialized.Store(true)
	return nil
}

// Register implements discovery.Provider.
func (d *Discovery) Register() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.initialized.Load() {
		return discovery.ErrNotInitialized
	}

	if d.registered.Load() {
		return discovery.ErrAlreadyRegistered
	}

	ctx, cancel := context.WithTimeout(d.config.Context, d.config.Timeout)
	defer cancel()

	lease, err := d.client.Grant(ctx, d.config.TTL)
	if err != nil {
		return fmt.Errorf("failed to create lease: %w", err)
	}

	d.leaseID = lease.ID
	_, err = d.client.Put(ctx, d.key, d.key, clientv3.WithLease(lease.ID))
	if err != nil {
		return fmt.Errorf("failed to register service: %w", err)
	}

	// Start lease keep-alive
	keepAliveCtx, keepAliveCancel := context.WithCancel(d.config.Context)
	d.cancelKeepAlive = keepAliveCancel

	ch, kaerr := d.client.KeepAlive(keepAliveCtx, d.leaseID)
	if kaerr != nil {
		keepAliveCancel()
		return fmt.Errorf("failed to start keep-alive: %w", kaerr)
	}

	// Start goroutine to consume keep-alive responses
	go func() {
		for ka := range ch {
			// Consume keep-alive responses to prevent channel from blocking
			_ = ka
		}
	}()

	d.registered.Store(true)
	return nil
}

// DiscoverPeers implements discovery.Provider.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.initialized.Load() {
		return nil, discovery.ErrNotInitialized
	}

	if !d.registered.Load() {
		return nil, discovery.ErrNotRegistered
	}

	ctx, cancel := context.WithTimeout(d.client.Ctx(), d.config.Timeout)
	defer cancel()

	peers := goset.NewSet[string]()
	// Get all nodes under the actor system prefix
	prefix := fmt.Sprintf("/%s/", d.config.ActorSystemName)
	resp, err := d.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to discover peers: %w", err)
	}

	for _, kv := range resp.Kvs {
		key := string(kv.Key)
		address := string(kv.Value)

		// Skip our own registration
		if key == d.key {
			continue
		}

		// Validate that the key follows expected format
		if strings.HasPrefix(key, prefix) && address != "" {
			peers.Add(address)
		}
	}

	return peers.ToSlice(), nil
}

// Deregister implements discovery.Provider.
func (d *Discovery) Deregister() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.initialized.Load() {
		return discovery.ErrNotInitialized
	}

	if !d.registered.Load() {
		return discovery.ErrNotRegistered
	}

	// Cancel keep-alive
	if d.cancelKeepAlive != nil {
		d.cancelKeepAlive()
		d.cancelKeepAlive = nil
	}

	if d.leaseID != 0 {
		ctx, cancel := context.WithTimeout(d.client.Ctx(), d.config.Timeout)
		defer cancel()

		// An error shouldn't fail deregistration
		// The lease will expire naturally if revoke fails
		_, _ = d.client.Revoke(ctx, d.leaseID)
		d.leaseID = 0
	}
	d.registered.Store(false)
	return nil
}

// Close implements discovery.Provider.
func (d *Discovery) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Close etcd client
	if d.client != nil {
		if err := d.client.Close(); err != nil {
			return fmt.Errorf("failed to close etcd client: %w", err)
		}
		d.client = nil
	}

	d.initialized.Store(false)
	d.registered.Store(false)
	return nil
}
