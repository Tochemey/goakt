/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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
	"github.com/tochemey/goakt/discovery"
	internalpb "github.com/tochemey/goakt/internal/v1"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

const (
	NatsServer      string = "nats-server"       // NatsServer specifies the Nats server Address
	NatsSubject     string = "nats-subject"      // NatsSubject specifies the NATs subject
	ActorSystemName        = "actor_system_name" // ActorSystemName specifies the actor system name
	ApplicationName        = "app_name"          // ApplicationName specifies the application name. This often matches the actor system name
	Timeout                = "timeout"           // Timeout specifies the discovery timeout. The default value is 1 second
)

// discoConfig represents the nats provider discoConfig
type discoConfig struct {
	// NatsServer defines the nats server
	// nats://host:port of a nats server
	NatsServer string
	// NatsSubject defines the custom NATS subject
	NatsSubject string
	// The actor system name
	ActorSystemName string
	// ApplicationName specifies the running application
	ApplicationName string
	// Timeout defines the nodes discovery timeout
	Timeout time.Duration
}

// Discovery represents the kubernetes discovery
type Discovery struct {
	config *discoConfig
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
func NewDiscovery(opts ...Option) *Discovery {
	// create an instance of
	discovery := &Discovery{
		mu:          sync.Mutex{},
		initialized: atomic.NewBool(false),
		registered:  atomic.NewBool(false),
		config:      &discoConfig{},
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
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider is running
	if d.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	// set the default discovery timeout
	if d.config.Timeout <= 0 {
		d.config.Timeout = time.Second
	}

	// grab the host node
	hostNode, err := discovery.HostNode()
	// handle the error
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
	// handle the retry error
	err = retrier.Run(func() error {
		// connect to the NATs server
		connection, err = opts.Connect()
		if err != nil {
			return err
		}
		// successful connection
		return nil
	})

	// create the NATs connection encoder
	encodedConn, err := nats.NewEncodedConn(connection, protobuf.PROTOBUF_ENCODER)
	// handle the error
	if err != nil {
		return err
	}
	// set the connection
	d.natsConnection = encodedConn
	// set initialized
	d.initialized = atomic.NewBool(true)
	// set the host node
	d.hostNode = hostNode
	return nil
}

// Register registers this node to a service discovery directory.
func (d *Discovery) Register() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider has started
	// avoid to re-register the discovery
	if d.registered.Load() {
		return discovery.ErrAlreadyRegistered
	}

	// create the subscription handler
	subscriptionHandler := func(subj, reply string, msg *internalpb.NatsMessage) {
		switch msg.GetMessageType() {
		case internalpb.NatsMessageType_NATS_MESSAGE_TYPE_DEREGISTER:
			// add logging information
			d.logger.Infof("received an de-registration request from peer[name=%s, host=%s, port=%d]",
				msg.GetName(), msg.GetHost(), msg.GetPort())
		case internalpb.NatsMessageType_NATS_MESSAGE_TYPE_REGISTER:
			// add logging information
			d.logger.Infof("received an registration request from peer[name=%s, host=%s, port=%d]",
				msg.GetName(), msg.GetHost(), msg.GetPort())
		case internalpb.NatsMessageType_NATS_MESSAGE_TYPE_REQUEST:
			// add logging information
			d.logger.Infof("received an identification request from peer[name=%s, host=%s, port=%d]",
				msg.GetName(), msg.GetHost(), msg.GetPort())
			// send the reply
			replyMessage := &internalpb.NatsMessage{
				Host:        d.hostNode.Host,
				Port:        int32(d.hostNode.GossipPort),
				Name:        d.hostNode.Name,
				MessageType: internalpb.NatsMessageType_NATS_MESSAGE_TYPE_RESPONSE,
			}

			// send the reply and handle the error
			if err := d.natsConnection.Publish(reply, replyMessage); err != nil {
				d.logger.Errorf("failed to reply for identification request from peer[name=%s, host=%s, port=%d]",
					msg.GetName(), msg.GetHost(), msg.GetPort())
			}
		}
	}
	// start listening to incoming messages
	subscription, err := d.natsConnection.Subscribe(d.config.NatsSubject, subscriptionHandler)
	// return any eventual error
	if err != nil {
		return err
	}

	// add the subscription to the list of subscriptions
	d.subscriptions = append(d.subscriptions, subscription)
	// set the registration flag to true
	d.registered = atomic.NewBool(true)
	return nil
}

// Deregister removes this node from a service discovery directory.
func (d *Discovery) Deregister() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// unregister
	defer func() {
		d.registered.Store(false)
	}()

	// first check whether the discovery provider has been registered or not
	if !d.registered.Load() {
		return discovery.ErrNotRegistered
	}

	// shutdown all the subscriptions
	for _, subscription := range d.subscriptions {
		// unsubscribe and return when there is an error
		if err := subscription.Unsubscribe(); err != nil {
			return err
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
	return nil
}

// SetConfig registers the underlying discovery configuration
func (d *Discovery) SetConfig(config discovery.Config) error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider is running
	if d.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	return d.setConfig(config)
}

// DiscoverPeers returns a list of known nodes.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider is running
	if !d.initialized.Load() {
		return nil, discovery.ErrNotInitialized
	}

	// later check the provider is registered
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

	// send registration request and return in case of error
	if err = d.natsConnection.PublishRequest(d.config.NatsSubject, inbox, &internalpb.NatsMessage{
		Host:        d.hostNode.Host,
		Port:        int32(d.hostNode.GossipPort),
		Name:        d.hostNode.Name,
		MessageType: internalpb.NatsMessageType_NATS_MESSAGE_TYPE_REQUEST,
	}); err != nil {
		return nil, err
	}

	// define the list of peers to lookup
	var peers []string
	// define the timeout
	timeout := time.After(d.config.Timeout)
	// get the host node gossip address
	me := d.hostNode.GossipAddress()

	// loop till we have enough nodes
	for {
		select {
		case m, ok := <-recv:
			if !ok {
				// Subscription is closed
				return peers, nil
			}

			// get the found peer address
			addr := net.JoinHostPort(m.GetHost(), strconv.Itoa(int(m.GetPort())))
			// ignore this node
			if addr == me {
				// Ignore a reply from self
				continue
			}

			// add the peer to the list of discovered peers
			peers = append(peers, addr)

		case <-timeout:
			// close the receiving channel
			_ = sub.Unsubscribe()
			close(recv)
		}
	}
}

// Close closes the provider
func (d *Discovery) Close() error {
	// first check whether the discovery provider has started
	if !d.initialized.Load() {
		return discovery.ErrNotInitialized
	}
	// set the initialized to false
	d.initialized.Store(false)
	// set registered to false
	d.registered.Store(false)

	if d.natsConnection != nil {
		// close all the subscriptions
		for _, subscription := range d.subscriptions {
			// unsubscribe and ignore the error
			_ = subscription.Unsubscribe()
		}

		// flush all messages
		return d.natsConnection.Flush()
	}
	return nil
}

// setConfig sets the kubernetes discoConfig
func (d *Discovery) setConfig(config discovery.Config) (err error) {
	// create an instance of option
	option := new(discoConfig)
	// extract the nats server address
	option.NatsServer, err = config.GetString(NatsServer)
	// handle the error in case the nats server value is not properly set
	if err != nil {
		return err
	}
	// extract the nats subject address
	option.NatsSubject, err = config.GetString(NatsSubject)
	// handle the error in case the nats subject value is not properly set
	if err != nil {
		return err
	}

	// extract the actor system name
	option.ActorSystemName, err = config.GetString(ActorSystemName)
	// handle the error in case the actor system name value is not properly set
	if err != nil {
		return err
	}
	// extract the application name
	option.ApplicationName, err = config.GetString(ApplicationName)
	// handle the error in case the application name value is not properly set
	if err != nil {
		return err
	}
	// in case none of the above extraction fails then set the option
	d.config = option
	return nil
}
