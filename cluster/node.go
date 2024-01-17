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

package cluster

import (
	"context"
	"encoding/json"
	"time"

	"github.com/buraksezer/olric"
	"github.com/buraksezer/olric/config"
	olriconfig "github.com/buraksezer/olric/config"
	"github.com/buraksezer/olric/events"
	"github.com/buraksezer/olric/hasher"
	"github.com/go-redis/redis/v8"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/hash"
	internalpb "github.com/tochemey/goakt/internal/v1"
	"github.com/tochemey/goakt/log"
	eventspb "github.com/tochemey/goakt/pb/events/v1"
	"github.com/tochemey/goakt/pkg/queue"
	"github.com/tochemey/goakt/pkg/types"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Event defines the cluster event
type Event struct {
	Type *anypb.Any
}

// Interface defines the Node interface
type Interface interface {
	// Start starts the Node engine
	Start(ctx context.Context) error
	// Stop stops the Node engine
	Stop(ctx context.Context) error
	// NodeHost returns the cluster startNode host address
	NodeHost() string
	// NodeRemotingPort returns the cluster startNode remoting port
	NodeRemotingPort() int
	// PutActor replicates onto the Node the metadata of an actor
	PutActor(ctx context.Context, actor *internalpb.WireActor) error
	// GetActor fetches an actor from the Node
	GetActor(ctx context.Context, actorName string) (*internalpb.WireActor, error)
	// GetPartition returns the partition where a given actor is stored
	GetPartition(actorName string) int
	// SetKey sets a given key to the cluster
	SetKey(ctx context.Context, key string) error
	// KeyExists checks the existence of a given key
	KeyExists(ctx context.Context, key string) (bool, error)
	// RemoveActor removes a given actor from the cluster.
	// An actor is removed from the cluster when this actor has been passivated.
	RemoveActor(ctx context.Context, actorName string) error
	// Events returns a channel where cluster events are published
	Events() chan *Event
	// AdvertisedAddress returns the cluster node cluster address that is known by the
	// peers in the cluster
	AdvertisedAddress() string
}

// Node represents the Node
type Node struct {
	// specifies the total number of partitions
	// the default values is 20
	partitionsCount uint64

	// specifies the logger
	logger log.Logger

	// specifies the Node name
	name string

	// specifies the Node server
	server *olric.Olric
	// specifies the Node client
	// this help set and fetch data from the Node
	client olric.Client

	// specifies the distributed key value store
	kvStore olric.DMap

	// specifies the Node host
	host *discovery.Node

	// specifies the hasher
	hasher hash.Hasher

	// specifies the discovery provider
	discoveryProvider discovery.Provider
	// specifies the discovery options
	discoveryOptions discovery.Config

	writeTimeout    time.Duration
	readTimeout     time.Duration
	shutdownTimeout time.Duration

	events             *queue.Unbounded[*Event]
	eventsListener     *redis.PubSub
	listenerStopSignal chan types.Unit
}

// enforce compilation error
var _ Interface = &Node{}

// NewNode creates an instance of cluster Node
func NewNode(name string, serviceDiscovery *discovery.ServiceDiscovery, opts ...Option) (*Node, error) {
	// create an instance of the Node
	node := &Node{
		partitionsCount:    20,
		logger:             log.DefaultLogger,
		name:               name,
		discoveryProvider:  serviceDiscovery.Provider(),
		discoveryOptions:   serviceDiscovery.Config(),
		writeTimeout:       time.Second,
		readTimeout:        time.Second,
		shutdownTimeout:    3 * time.Second,
		hasher:             hash.DefaultHasher(),
		eventsListener:     nil,
		events:             queue.NewUnbounded[*Event](),
		listenerStopSignal: make(chan types.Unit, 1),
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(node)
	}

	// get the host info
	hostNode, err := discovery.HostNode()
	// handle the error
	if err != nil {
		node.logger.Error(errors.Wrap(err, "failed get the host startNode.💥"))
		return nil, err
	}

	// set the host startNode
	node.host = hostNode

	return node, nil
}

// Start starts the Node.
func (c *Node) Start(ctx context.Context) error {
	// set the logger
	logger := c.logger

	// add some logging information
	logger.Infof("Starting GoAkt cluster Node service on (%s)....🤔", c.host.ClusterAddress())

	// build the Node engine config
	conf := c.buildConfig()
	// set the hasher to the custom hasher
	conf.Hasher = &hasherWrapper{c.hasher}

	// create the member list config
	m, err := olriconfig.NewMemberlistConfig("lan")
	// panic when there is an error
	if err != nil {
		logger.Error(errors.Wrap(err, "failed to configure the cluster Node memberlist.💥"))
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

	// let us start the Node
	startCtx, cancel := context.WithCancel(ctx)
	// cancel the context the server has started
	conf.Started = func() {
		// cancel the start context
		defer cancel()
		// add some logging information
		logger.Infof("GoAkt cluster Node=(%s) successfully started. 🎉", c.name)
	}

	// let us create an instance of the Node engine
	eng, err := olric.New(conf)
	// handle the error
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to start the cluster Node=(%s).💥", c.name))
		return err
	}

	// set the server
	c.server = eng
	go func() {
		// start the Node engine
		err = c.server.Start()
		// handle the error in case there is an early error
		if err != nil {
			logger.Error(errors.Wrapf(err, "failed to start the cluster Node=(%s).💥", c.name))
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
		logger.Error(errors.Wrapf(err, "failed to start the cluster Node=(%s).💥", c.name))
		// let us stop the started engine
		return c.server.Shutdown(ctx)
	}
	// set the distributed map
	c.kvStore = dmp

	// create a subscriber to listen to cluster events
	ps, err := c.client.NewPubSub()
	// handle the error
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to start the cluster Node=(%s).💥", c.name))
		// let us stop the started engine
		return c.server.Shutdown(ctx)
	}

	// subscribe to the cluster events channel
	c.eventsListener = ps.Subscribe(ctx, events.ClusterEventsChannel)
	// start listening to cluster events
	go c.listen()
	// return successfully
	return nil
}

// Stop stops the Node gracefully
func (c *Node) Stop(ctx context.Context) error {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, c.shutdownTimeout)
	defer cancelFn()

	// set the logger
	logger := c.logger

	// add some logging information
	logger.Infof("Stopping GoAkt cluster Node=(%s)....🤔", c.name)

	// close the events listener
	if err := c.eventsListener.Close(); err != nil {
		logger.Error(errors.Wrap(err, "failed to shutdown the cluster events listener.💥"))
		return err
	}

	// close the Node client
	if err := c.client.Close(ctx); err != nil {
		logger.Error(errors.Wrapf(err, "failed to shutdown the cluster Node=(%s).💥", c.name))
		return err
	}

	// let us stop the server
	if err := c.server.Shutdown(ctx); err != nil {
		logger.Error(errors.Wrapf(err, "failed to Shutdown GoAkt cluster Node=(%s)....💥", c.name))
		return err
	}

	// close the events queue
	c.events.Close()

	// signal we are stopping listening to events
	c.listenerStopSignal <- types.Unit{}

	logger.Infof("GoAkt cluster Node=(%s) successfully stopped.🎉", c.name)
	return nil
}

// NodeHost returns the Node Host
func (c *Node) NodeHost() string {
	return c.host.Host
}

// NodeRemotingPort returns the Node remoting port
func (c *Node) NodeRemotingPort() int {
	return c.host.RemotingPort
}

// AdvertisedAddress returns the cluster node cluster address that is known by the
// peers in the cluster
func (c *Node) AdvertisedAddress() string {
	return c.host.ClusterAddress()
}

// PutActor replicates onto the Node the metadata of an actor
func (c *Node) PutActor(ctx context.Context, actor *internalpb.WireActor) error {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, c.writeTimeout)
	defer cancelFn()

	// set the logger
	logger := c.logger

	// add some logging information
	logger.Infof("replicating actor (%s).🤔", actor.GetActorName())

	// let us marshal it
	data, err := encode(actor)
	// handle the marshaling error
	if err != nil {
		// add a logging to the stderr
		logger.Error(errors.Wrapf(err, "failed to persist actor=%s data in the Node.💥", actor.GetActorName()))
		// here we cancel the request
		return errors.Wrapf(err, "failed to persist actor=%s data in the Node", actor.GetActorName())
	}

	// send the record into the Node
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

// GetActor fetches an actor from the Node
func (c *Node) GetActor(ctx context.Context, actorName string) (*internalpb.WireActor, error) {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, c.readTimeout)
	defer cancelFn()

	// set the logger
	logger := c.logger

	// add some logging information
	logger.Infof("retrieving actor (%s) from the Node.🤔", actorName)

	// grab the record from the distributed store
	resp, err := c.kvStore.Get(ctx, actorName)
	// handle the error
	if err != nil {
		// we could not find the given actor
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("actor=%s is not found in the Node", actorName)
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

// RemoveActor removes a given actor from the cluster.
// An actor is removed from the cluster when this actor has been passivated.
func (c *Node) RemoveActor(ctx context.Context, actorName string) error {
	// set the logger
	logger := c.logger

	// add some logging information
	logger.Infof("removing actor (%s).🤔", actorName)

	// remove the actor from the cluster
	_, err := c.kvStore.Delete(ctx, actorName)
	// handle the error
	if err != nil {
		// log the error
		logger.Error(errors.Wrapf(err, "failed to remove actor=%s record.💥", actorName))
		return err
	}

	// add a logging information
	logger.Infof("actor (%s) successfully removed from the cluster.🎉", actorName)
	return nil
}

// SetKey sets a given key to the cluster
func (c *Node) SetKey(ctx context.Context, key string) error {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, c.writeTimeout)
	defer cancelFn()

	// set the logger
	logger := c.logger

	// add some logging information
	logger.Infof("replicating key (%s).🤔", key)

	// send the record into the Node
	err := c.kvStore.Put(ctx, key, true)
	// handle the error
	if err != nil {
		// log the error
		logger.Error(errors.Wrapf(err, "failed to replicate key=%s record.💥", key))
		return err
	}

	// Ahoy we are successful
	logger.Infof("key (%s) successfully replicated.🎉", key)
	return nil
}

// KeyExists checks the existence of a given key
func (c *Node) KeyExists(ctx context.Context, key string) (bool, error) {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, c.readTimeout)
	defer cancelFn()

	// set the logger
	logger := c.logger

	// add some logging information
	logger.Infof("checking key (%s) existence in the cluster.🤔", key)

	// grab the record from the distributed store
	resp, err := c.kvStore.Get(ctx, key)
	// handle the error
	if err != nil {
		// we could not find the given actor
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("key=%s is not found in the Node", key)
			return false, nil
		}
		// log the error
		logger.Error(errors.Wrapf(err, "failed to check key=%s existence.💥", key))
		return false, err
	}
	return resp.Bool()
}

// GetPartition returns the partition where a given actor is stored
func (c *Node) GetPartition(actorName string) int {
	// create the byte array of the actor name
	key := []byte(actorName)
	// compute the hash key
	hkey := c.hasher.HashCode(key)
	// compute the partition and return it
	partition := int(hkey % c.partitionsCount)
	// add some debug log
	c.logger.Debugf("partition of actor (%s) is (%d)", actorName, partition)
	return partition
}

// Events returns a channel where cluster events are published
func (c *Node) Events() chan *Event {
	out := make(chan *Event, c.events.Len())
	defer close(out)
	for {
		msg, ok := c.events.Pop()
		if !ok {
			break
		}
		out <- msg
	}
	return out
}

// listen listens to the underlying cluster events
// and emit the wrapped startNode event
func (c *Node) listen() {
	for {
		select {
		case <-c.listenerStopSignal:
			return
		case message, ok := <-c.eventsListener.Channel():
			// break out of the loop when the channel is closed
			if !ok {
				return
			}
			// grab the message payload
			payload := message.Payload
			// get the message payload
			var event map[string]any
			// let us unmarshal the event
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				c.logger.Error(errors.Wrap(err, "failed to decode cluster event"))
				// TODO: should we continue or not
				continue
			}

			// grab the kind
			kind := event["kind"]
			// add some debug log
			c.logger.Debugf("received (%s) cluster event", kind)

			switch kind {
			case events.KindNodeJoinEvent:
				// create the node joined to unmarshal the event
				nodeJoined := new(events.NodeJoinEvent)
				// let us unmarshal the event
				if err := json.Unmarshal([]byte(payload), &nodeJoined); err != nil {
					c.logger.Error(errors.Wrap(err, "failed to decode NodeJoined cluster event"))
					// TODO: should we continue or not
					continue
				}

				// make sure to skip self
				if c.AdvertisedAddress() == nodeJoined.NodeJoin {
					// add some debug log
					c.logger.Debug("skipping self")
					continue
				}

				// TODO: need to cross check this calculation
				// convert the timestamp to milliseconds
				timeMilli := nodeJoined.Timestamp / int64(1e6)
				// create the startNode joined event
				event := &eventspb.NodeJoined{
					Address:   nodeJoined.NodeJoin,
					Timestamp: timestamppb.New(time.UnixMilli(timeMilli)),
				}
				// serialize as any pb
				eventType, _ := anypb.New(event)
				// send the event to queue
				c.events.Push(&Event{eventType})

			case events.KindNodeLeftEvent:
				// create the node left to unmarshal the event
				nodeLeft := new(events.NodeLeftEvent)
				// let us unmarshal the event
				if err := json.Unmarshal([]byte(payload), &nodeLeft); err != nil {
					c.logger.Error(errors.Wrap(err, "failed to decode NodeLeft cluster event"))
					// TODO: should we continue or not
					continue
				}

				// make sure to skip self
				if c.AdvertisedAddress() == nodeLeft.NodeLeft {
					// add some debug log
					c.logger.Debug("skipping self")
					continue
				}

				// TODO: need to cross check this calculation
				// convert the timestamp to milliseconds
				timeMilli := nodeLeft.Timestamp / int64(1e6)
				// create the startNode joined event
				event := &eventspb.NodeLeft{
					Address:   nodeLeft.NodeLeft,
					Timestamp: timestamppb.New(time.UnixMilli(timeMilli)),
				}
				// serialize as any pb
				eventType, _ := anypb.New(event)
				// send the event to queue
				c.events.Push(&Event{eventType})
			default:
				// skip
			}
		}
	}
}

// buildConfig builds the Node configuration
func (c *Node) buildConfig() *config.Config {
	// define the log level
	logLevel := "INFO"
	switch c.logger.LogLevel() {
	case log.DebugLevel:
		logLevel = "DEBUG"
	case log.ErrorLevel, log.FatalLevel, log.PanicLevel:
		logLevel = "ERROR"
	case log.WarningLevel:
		logLevel = "WARN"
	default:
		// pass
	}

	// create the config and return it
	conf := &config.Config{
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
		LogOutput:                  newLogWriter(c.logger),
		EnableClusterEventsChannel: true,
		Hasher:                     hasher.NewDefaultHasher(),
	}

	// set verbosity when debug is enabled
	if c.logger.LogLevel() == log.DebugLevel {
		conf.LogVerbosity = config.DefaultLogVerbosity
	}

	return conf
}
