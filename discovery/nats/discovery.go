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

package nats

import (
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/flowchartsman/retry"
	"github.com/nats-io/nats.go"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/locker"
	"github.com/tochemey/goakt/v4/log"
)

// peerResponseBuffer caps the in-flight peer responses held while DiscoverPeers waits
// out its timeout. NATS slow-consumer behavior takes over if a cluster fan-out exceeds this.
const peerResponseBuffer = 32

// Discovery represents the NATS discovery
type Discovery struct {
	_      locker.NoCopy
	config *Config
	mu     sync.Mutex

	initialized *atomic.Bool
	registered  *atomic.Bool

	// define the nats connection
	connection *nats.Conn
	// subscription is the single active subscription on NatsSubject (set by Register).
	subscription *nats.Subscription

	// define a logger
	logger log.Logger

	address string
}

// enforce compilation error
var _ discovery.Provider = &Discovery{}

// NewDiscovery returns an instance of the NATS discovery provider
func NewDiscovery(config *Config, opts ...Option) *Discovery {
	// shallow-copy the user's Config so Initialize defaults don't mutate the caller's pointer
	cfg := *config
	d := &Discovery{
		mu:          sync.Mutex{},
		initialized: atomic.NewBool(false),
		registered:  atomic.NewBool(false),
		config:      &cfg,
		logger:      log.DiscardLogger,
	}

	// apply the various options
	for _, opt := range opts {
		opt.Apply(d)
	}

	d.address = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.DiscoveryPort))
	return d
}

// ID returns the discovery provider id
func (d *Discovery) ID() string {
	return discovery.ProviderNATS
}

// Initialize initializes the plugin: registers some internal data structures, clients etc.
func (d *Discovery) Initialize() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	if err := d.config.Validate(); err != nil {
		return err
	}

	if d.config.Timeout <= 0 {
		d.config.Timeout = time.Second
	}

	if d.config.MaxJoinAttempts == 0 {
		d.config.MaxJoinAttempts = 5
	}

	if d.config.ReconnectWait <= 0 {
		d.config.ReconnectWait = 2 * time.Second
	}

	// create the nats connection option
	opts := nats.GetDefaultOptions()
	opts.Url = d.config.NatsServer
	opts.Name = d.address
	opts.ReconnectWait = d.config.ReconnectWait
	opts.MaxReconnect = -1

	var (
		connection *nats.Conn
		err        error
	)

	// let us connect using an exponential backoff mechanism
	// create a new instance of retrier that will try a maximum of five times, with
	// an initial delay and a maximum delay of opts.ReconnectWait
	retrier := retry.NewRetrier(d.config.MaxJoinAttempts, opts.ReconnectWait, opts.ReconnectWait)
	if err = retrier.Run(func() error {
		connection, err = opts.Connect()
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	// create the NATs connection
	d.connection = connection
	d.initialized.Store(true)
	return nil
}

// Register registers this node to a service discovery directory.
func (d *Discovery) Register() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.registered.Load() {
		return discovery.ErrAlreadyRegistered
	}

	// create the subscription handler
	handler := func(msg *nats.Msg) {
		message := new(internalpb.NatsMessage)
		if err := proto.Unmarshal(msg.Data, message); err != nil {
			// TODO: need to read more and see how to propagate the error
			d.connection.Opts.AsyncErrorCB(d.connection, msg.Sub, errors.New("nats: Got an error trying to unmarshal: "+err.Error()))
			return
		}

		switch message.GetMessageType() {
		case internalpb.NatsMessageType_NATS_MESSAGE_TYPE_DEREGISTER:
			d.logger.Infof("received deregistration request from peer[name=%s, host=%s, port=%d]",
				message.GetName(), message.GetHost(), message.GetPort())
		case internalpb.NatsMessageType_NATS_MESSAGE_TYPE_REQUEST:
			d.logger.Infof("received identification request from peer[name=%s, host=%s, port=%d]",
				message.GetName(), message.GetHost(), message.GetPort())

			response := &internalpb.NatsMessage{
				Host:        d.config.Host,
				Port:        int32(d.config.DiscoveryPort),
				Name:        d.address,
				MessageType: internalpb.NatsMessageType_NATS_MESSAGE_TYPE_RESPONSE,
			}

			bytea, _ := proto.Marshal(response)
			if err := d.connection.Publish(msg.Reply, bytea); err != nil {
				d.logger.Errorf("failed to reply to identification request from peer[name=%s, host=%s, port=%d]",
					message.GetName(), message.GetHost(), message.GetPort())
			}
		}
	}

	subscription, err := d.connection.Subscribe(d.config.NatsSubject, handler)
	if err != nil {
		return err
	}

	d.subscription = subscription
	d.registered.Store(true)
	return nil
}

// Deregister removes this node from a service discovery directory.
func (d *Discovery) Deregister() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.deregisterLocked()
}

// deregisterLocked unsubscribes and publishes a DEREGISTER notification.
// Caller must hold d.mu.
func (d *Discovery) deregisterLocked() error {
	if !d.registered.Load() {
		return discovery.ErrNotRegistered
	}

	defer d.registered.Store(false)

	if d.subscription != nil && d.subscription.IsValid() {
		if err := d.subscription.Unsubscribe(); err != nil {
			return err
		}
	}

	d.subscription = nil

	if d.connection != nil {
		message := &internalpb.NatsMessage{
			Host:        d.config.Host,
			Port:        int32(d.config.DiscoveryPort),
			Name:        d.address,
			MessageType: internalpb.NatsMessageType_NATS_MESSAGE_TYPE_DEREGISTER,
		}

		bytea, _ := proto.Marshal(message)
		return d.connection.Publish(d.config.NatsSubject, bytea)
	}
	return nil
}

// DiscoverPeers returns a list of known nodes.
func (d *Discovery) DiscoverPeers() (peers []string, err error) {
	d.mu.Lock()
	if !d.initialized.Load() {
		d.mu.Unlock()
		return nil, discovery.ErrNotInitialized
	}
	if !d.registered.Load() {
		d.mu.Unlock()
		return nil, discovery.ErrNotRegistered
	}
	conn := d.connection
	d.mu.Unlock()

	// Set up a reply channel, then broadcast for all peers to
	// report their presence.
	// collect as many responses as possible in the given timeout.
	inbox := nats.NewInbox()
	recv := make(chan *nats.Msg, peerResponseBuffer)

	// bind to receive messages
	sub, err := conn.ChanSubscribe(inbox, recv)
	if err != nil {
		return nil, err
	}

	defer func() {
		if uerr := sub.Unsubscribe(); uerr != nil {
			d.logger.Errorf("failed to unsubscribe from discovery inbox: %v", uerr)
			err = errors.Join(err, uerr)
		}
	}()

	request := &internalpb.NatsMessage{
		Host:        d.config.Host,
		Port:        int32(d.config.DiscoveryPort),
		Name:        d.address,
		MessageType: internalpb.NatsMessageType_NATS_MESSAGE_TYPE_REQUEST,
	}

	bytea, _ := proto.Marshal(request)
	if err = conn.PublishRequest(d.config.NatsSubject, inbox, bytea); err != nil {
		return nil, err
	}

	timeout := time.After(d.config.Timeout)
	me := d.address
	for {
		select {
		case msg := <-recv:
			message := new(internalpb.NatsMessage)
			if err = proto.Unmarshal(msg.Data, message); err != nil {
				d.logger.Errorf("failed to unmarshal nats message: %v", err)
				return nil, err
			}

			// get the found peer address
			addr := net.JoinHostPort(message.GetHost(), strconv.Itoa(int(message.GetPort())))
			if addr == me {
				continue
			}

			peers = append(peers, addr)

		case <-timeout:
			return peers, nil
		}
	}
}

// Close closes the provider
func (d *Discovery) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.registered.Load() {
		// best-effort: notify peers and clear subscription state. Errors are dropped
		// because Close has no meaningful recovery — the connection is about to die.
		_ = d.deregisterLocked()
	}
	d.initialized.Store(false)

	if d.connection == nil {
		return nil
	}
	err := d.connection.Flush()
	d.connection.Close()
	d.connection = nil
	return err
}
