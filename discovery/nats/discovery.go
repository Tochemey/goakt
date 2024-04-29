/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package nats

import (
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/flowchartsman/retry"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/encoders/protobuf"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/discovery"
	internalpb "github.com/tochemey/goakt/internal/internalpb"
	"github.com/tochemey/goakt/log"
)

// Discovery represents the kubernetes discovery
type Discovery struct {
	config *Config
	mu     sync.Mutex

	initialized *atomic.Bool
	registered  *atomic.Bool

	// define the nats connection
	natsConnection *nats.EncodedConn
	// define a slice of subscriptions
	subscriptions []*nats.Subscription

	// defines the host node
	hostNode *discovery.Node

	// define a logger
	logger log.Logger
}

// enforce compilation error
var _ discovery.Provider = &Discovery{}

// NewDiscovery returns an instance of the kubernetes discovery provider
func NewDiscovery(config *Config, opts ...Option) *Discovery {
	// create an instance of
	discovery := &Discovery{
		mu:          sync.Mutex{},
		initialized: atomic.NewBool(false),
		registered:  atomic.NewBool(false),
		config:      config,
		logger:      log.DefaultLogger,
	}

	// apply the various options
	for _, opt := range opts {
		opt.Apply(discovery)
	}

	return discovery
}

// ID returns the discovery provider id
func (d *Discovery) ID() string {
	return "nats"
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

	hostNode, err := discovery.HostNode()
	if err != nil {
		return err
	}

	// create the nats connection option
	opts := nats.GetDefaultOptions()
	opts.Url = d.config.NatsServer
	//opts.Servers = n.Config.Servers
	opts.Name = hostNode.Name
	opts.ReconnectWait = 2 * time.Second
	opts.MaxReconnect = -1

	var (
		connection *nats.Conn
	)

	// let us connect using an exponential backoff mechanism
	// we will attempt to connect 5 times to see whether there have started successfully or not.
	const (
		maxRetries = 5
	)
	// create a new instance of retrier that will try a maximum of five times, with
	// an initial delay of 100 ms and a maximum delay of opts.ReconnectWait
	retrier := retry.NewRetrier(maxRetries, 100*time.Millisecond, opts.ReconnectWait)
	err = retrier.Run(func() error {
		connection, err = opts.Connect()
		if err != nil {
			return err
		}
		return nil
	})

	// create the NATs connection encoder
	encodedConn, err := nats.NewEncodedConn(connection, protobuf.PROTOBUF_ENCODER)
	if err != nil {
		return err
	}
	d.natsConnection = encodedConn
	d.initialized = atomic.NewBool(true)
	d.hostNode = hostNode
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
	subscriptionHandler := func(subj, reply string, msg *internalpb.NatsMessage) {
		switch msg.GetMessageType() {
		case internalpb.NatsMessageType_NATS_MESSAGE_TYPE_DEREGISTER:
			d.logger.Infof("received an de-registration request from peer[name=%s, host=%s, port=%d]",
				msg.GetName(), msg.GetHost(), msg.GetPort())
		case internalpb.NatsMessageType_NATS_MESSAGE_TYPE_REGISTER:
			d.logger.Infof("received an registration request from peer[name=%s, host=%s, port=%d]",
				msg.GetName(), msg.GetHost(), msg.GetPort())
		case internalpb.NatsMessageType_NATS_MESSAGE_TYPE_REQUEST:
			d.logger.Infof("received an identification request from peer[name=%s, host=%s, port=%d]",
				msg.GetName(), msg.GetHost(), msg.GetPort())

			replyMessage := &internalpb.NatsMessage{
				Host:        d.hostNode.Host,
				Port:        int32(d.hostNode.GossipPort),
				Name:        d.hostNode.Name,
				MessageType: internalpb.NatsMessageType_NATS_MESSAGE_TYPE_RESPONSE,
			}

			if err := d.natsConnection.Publish(reply, replyMessage); err != nil {
				d.logger.Errorf("failed to reply for identification request from peer[name=%s, host=%s, port=%d]",
					msg.GetName(), msg.GetHost(), msg.GetPort())
			}
		}
	}

	subscription, err := d.natsConnection.Subscribe(d.config.NatsSubject, subscriptionHandler)
	if err != nil {
		return err
	}

	d.subscriptions = append(d.subscriptions, subscription)
	d.registered = atomic.NewBool(true)
	return nil
}

// Deregister removes this node from a service discovery directory.
func (d *Discovery) Deregister() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider has been registered or not
	if !d.registered.Load() {
		return discovery.ErrNotRegistered
	}

	// shutdown all the subscriptions
	for _, subscription := range d.subscriptions {
		// when subscription is defined
		if subscription != nil {
			// check whether the subscription is active or not
			if subscription.IsValid() {
				// unsubscribe and return when there is an error
				if err := subscription.Unsubscribe(); err != nil {
					return err
				}
			}
		}
	}

	// send the de-registration message to notify peers
	if d.natsConnection != nil {
		// send a message to deregister stating we are out
		return d.natsConnection.Publish(d.config.NatsSubject, &internalpb.NatsMessage{
			Host:        d.hostNode.Host,
			Port:        int32(d.hostNode.GossipPort),
			Name:        d.hostNode.Name,
			MessageType: internalpb.NatsMessageType_NATS_MESSAGE_TYPE_DEREGISTER,
		})
	}

	d.registered.Store(false)
	return nil
}

// DiscoverPeers returns a list of known nodes.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.initialized.Load() {
		return nil, discovery.ErrNotInitialized
	}

	if !d.registered.Load() {
		return nil, discovery.ErrNotRegistered
	}

	// Set up a reply channel, then broadcast for all peers to
	// report their presence.
	// collect as many responses as possible in the given timeout.
	inbox := nats.NewInbox()
	recv := make(chan *internalpb.NatsMessage)

	// bind to receive messages
	sub, err := d.natsConnection.BindRecvChan(inbox, recv)
	if err != nil {
		return nil, err
	}

	if err = d.natsConnection.PublishRequest(d.config.NatsSubject, inbox, &internalpb.NatsMessage{
		Host:        d.hostNode.Host,
		Port:        int32(d.hostNode.GossipPort),
		Name:        d.hostNode.Name,
		MessageType: internalpb.NatsMessageType_NATS_MESSAGE_TYPE_REQUEST,
	}); err != nil {
		return nil, err
	}

	var peers []string
	timeout := time.After(d.config.Timeout)
	me := d.hostNode.GossipAddress()

	for {
		select {
		case m, ok := <-recv:
			if !ok {
				// Subscription is closed
				return peers, nil
			}

			// get the found peer address
			addr := net.JoinHostPort(m.GetHost(), strconv.Itoa(int(m.GetPort())))
			if addr == me {
				continue
			}

			peers = append(peers, addr)

		case <-timeout:
			_ = sub.Unsubscribe()
			close(recv)
		}
	}
}

// Close closes the provider
func (d *Discovery) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.initialized.Store(false)

	if d.natsConnection != nil {
		defer func() {
			d.natsConnection.Close()
			d.natsConnection = nil
		}()

		for _, subscription := range d.subscriptions {
			if subscription != nil {
				if subscription.IsValid() {
					if err := subscription.Unsubscribe(); err != nil {
						return err
					}
				}
			}
		}

		return d.natsConnection.Flush()
	}
	return nil
}
