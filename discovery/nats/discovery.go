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
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/flowchartsman/retry"
	"github.com/nats-io/nats.go"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/log"
)

// Discovery represents the kubernetes discovery
type Discovery struct {
	config *Config
	mu     sync.Mutex

	initialized *atomic.Bool
	registered  *atomic.Bool

	// define the nats connection
	connection *nats.Conn
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
func NewDiscovery(config *Config, hostNode *discovery.Node, opts ...Option) *Discovery {
	// create an instance of
	discovery := &Discovery{
		mu:          sync.Mutex{},
		initialized: atomic.NewBool(false),
		registered:  atomic.NewBool(false),
		config:      config,
		logger:      log.DefaultLogger,
		hostNode:    hostNode,
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

	if d.config.MaxJoinAttempts == 0 {
		d.config.MaxJoinAttempts = 5
	}

	if d.config.ReconnectWait <= 0 {
		d.config.ReconnectWait = 2 * time.Second
	}

	// create the nats connection option
	opts := nats.GetDefaultOptions()
	opts.Url = d.config.NatsServer
	opts.Name = d.hostNode.Name
	opts.ReconnectWait = d.config.ReconnectWait
	opts.MaxReconnect = -1

	var (
		connection *nats.Conn
		err        error
	)

	// let us connect using an exponential backoff mechanism
	// create a new instance of retrier that will try a maximum of five times, with
	// an initial delay of 100 ms and a maximum delay of opts.ReconnectWait
	retrier := retry.NewRetrier(d.config.MaxJoinAttempts, 100*time.Millisecond, opts.ReconnectWait)
	err = retrier.Run(func() error {
		connection, err = opts.Connect()
		if err != nil {
			return err
		}
		return nil
	})

	// create the NATs connection
	d.connection = connection
	d.initialized = atomic.NewBool(true)
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
			d.logger.Infof("received an de-registration request from peer[name=%s, host=%s, port=%d]",
				message.GetName(), message.GetHost(), message.GetPort())
		case internalpb.NatsMessageType_NATS_MESSAGE_TYPE_REGISTER:
			d.logger.Infof("received an registration request from peer[name=%s, host=%s, port=%d]",
				message.GetName(), message.GetHost(), message.GetPort())
		case internalpb.NatsMessageType_NATS_MESSAGE_TYPE_REQUEST:
			d.logger.Infof("received an identification request from peer[name=%s, host=%s, port=%d]",
				message.GetName(), message.GetHost(), message.GetPort())

			response := &internalpb.NatsMessage{
				Host:        d.hostNode.Host,
				Port:        int32(d.hostNode.DiscoveryPort),
				Name:        d.hostNode.Name,
				MessageType: internalpb.NatsMessageType_NATS_MESSAGE_TYPE_RESPONSE,
			}

			bytea, _ := proto.Marshal(response)
			if err := d.connection.Publish(msg.Reply, bytea); err != nil {
				d.logger.Errorf("failed to reply for identification request from peer[name=%s, host=%s, port=%d]",
					message.GetName(), message.GetHost(), message.GetPort())
			}
		}
	}

	subscription, err := d.connection.Subscribe(d.config.NatsSubject, handler)
	if err != nil {
		return err
	}

	d.subscriptions = append(d.subscriptions, subscription)
	d.registered = atomic.NewBool(true)
	return nil
}

// Deregister removes this node from a service discovery directory.
func (d *Discovery) Deregister() error {
	d.mu.Lock()
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
	if d.connection != nil {
		// send a message to deregister stating we are out
		message := &internalpb.NatsMessage{
			Host:        d.hostNode.Host,
			Port:        int32(d.hostNode.DiscoveryPort),
			Name:        d.hostNode.Name,
			MessageType: internalpb.NatsMessageType_NATS_MESSAGE_TYPE_DEREGISTER,
		}

		bytea, _ := proto.Marshal(message)
		return d.connection.Publish(d.config.NatsSubject, bytea)
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
	recv := make(chan *nats.Msg, 1)

	// bind to receive messages
	sub, err := d.connection.ChanSubscribe(inbox, recv)
	if err != nil {
		return nil, err
	}

	request := &internalpb.NatsMessage{
		Host:        d.hostNode.Host,
		Port:        int32(d.hostNode.DiscoveryPort),
		Name:        d.hostNode.Name,
		MessageType: internalpb.NatsMessageType_NATS_MESSAGE_TYPE_REQUEST,
	}

	bytea, _ := proto.Marshal(request)
	if err = d.connection.PublishRequest(d.config.NatsSubject, inbox, bytea); err != nil {
		return nil, err
	}

	var peers []string
	timeout := time.After(d.config.Timeout)
	me := d.hostNode.DiscoveryAddress()
	for {
		select {
		case msg, ok := <-recv:
			if !ok {
				// Subscription is closed
				return peers, nil
			}

			message := new(internalpb.NatsMessage)
			if err := proto.Unmarshal(msg.Data, message); err != nil {
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
	d.registered.Store(false)

	if d.connection != nil {
		defer func() {
			d.connection.Close()
			d.connection = nil
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

		return d.connection.Flush()
	}
	return nil
}
