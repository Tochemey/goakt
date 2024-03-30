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
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/goaktpb"
	"github.com/tochemey/goakt/hash"
	"github.com/tochemey/goakt/internal/internalpb"
	"github.com/tochemey/goakt/internal/types"
	"github.com/tochemey/goakt/log"
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
	Events() <-chan *Event
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

	events             chan *Event
	pubSub             *redis.PubSub
	messagesChan       <-chan *redis.Message
	messagesReaderChan chan types.Unit
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
		pubSub:             nil,
		events:             make(chan *Event, 20),
		messagesReaderChan: make(chan types.Unit, 1),
		messagesChan:       make(chan *redis.Message, 1),
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(node)
	}

	// get the host info
	hostNode, err := discovery.HostNode()
	// handle the error
	if err != nil {
		node.logger.Error(errors.Wrap(err, "failed get the host node.💥"))
		return nil, err
	}

	// set the host startNode
	node.host = hostNode

	return node, nil
}

// Start starts the Node.
func (n *Node) Start(ctx context.Context) error {
	logger := n.logger

	logger.Infof("Starting GoAkt cluster Node service on (%s)....🤔", n.host.ClusterAddress())

	conf := n.buildConfig()
	conf.Hasher = &hasherWrapper{n.hasher}

	m, err := olriconfig.NewMemberlistConfig("lan")
	if err != nil {
		logger.Error(errors.Wrap(err, "failed to configure the cluster Node memberlist.💥"))
		return err
	}

	// sets the bindings
	m.BindAddr = n.host.Host
	m.BindPort = n.host.GossipPort
	m.AdvertisePort = n.host.GossipPort
	conf.MemberlistConfig = m

	// set the discovery provider
	discoveryWrapper := &discoveryProvider{
		provider: n.discoveryProvider,
		log:      n.logger.StdLogger(),
	}

	conf.ServiceDiscovery = map[string]any{
		"plugin":  discoveryWrapper,
		"id":      n.discoveryProvider.ID(),
		"options": n.discoveryOptions,
	}

	// let us start the Node
	startCtx, cancel := context.WithCancel(ctx)
	// cancel the context the server has started
	conf.Started = func() {
		defer cancel()
		logger.Infof("GoAkt cluster Node=(%s) successfully started. 🎉", n.name)
	}

	// let us create an instance of the Node engine
	eng, err := olric.New(conf)
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to start the cluster Node=(%s).💥", n.name))
		return err
	}

	// set the server
	n.server = eng
	go func() {
		if err = n.server.Start(); err != nil {
			logger.Error(errors.Wrapf(err, "failed to start the cluster Node=(%s).💥", n.name))
			if e := n.server.Shutdown(ctx); e != nil {
				logger.Panic(e)
			}
		}
	}()

	<-startCtx.Done()

	// set the client
	n.client = n.server.NewEmbeddedClient()
	dmp, err := n.client.NewDMap(n.name)
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to start the cluster Node=(%s).💥", n.name))
		return n.server.Shutdown(ctx)
	}

	n.kvStore = dmp

	// create a subscriber to consume to cluster events
	ps, err := n.client.NewPubSub()
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to start the cluster Node=(%s).💥", n.name))
		return n.server.Shutdown(ctx)
	}

	n.pubSub = ps.Subscribe(ctx, events.ClusterEventsChannel)
	n.messagesChan = n.pubSub.Channel()
	go n.consume()

	return nil
}

// Stop stops the Node gracefully
func (n *Node) Stop(ctx context.Context) error {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, n.shutdownTimeout)
	defer cancelFn()

	// set the logger
	logger := n.logger

	// add some logging information
	logger.Infof("Stopping GoAkt cluster Node=(%s)....🤔", n.name)

	// close the events listener
	if err := n.pubSub.Close(); err != nil {
		logger.Error(errors.Wrap(err, "failed to shutdown the cluster events listener.💥"))
		return err
	}

	// close the Node client
	if err := n.client.Close(ctx); err != nil {
		logger.Error(errors.Wrapf(err, "failed to shutdown the cluster Node=(%s).💥", n.name))
		return err
	}

	// let us stop the server
	if err := n.server.Shutdown(ctx); err != nil {
		logger.Error(errors.Wrapf(err, "failed to Shutdown GoAkt cluster Node=(%s)....💥", n.name))
		return err
	}

	// close the events queue
	close(n.events)

	// signal we are stopping listening to events
	n.messagesReaderChan <- types.Unit{}

	logger.Infof("GoAkt cluster Node=(%s) successfully stopped.🎉", n.name)
	return nil
}

// NodeHost returns the Node Host
func (n *Node) NodeHost() string {
	return n.host.Host
}

// NodeRemotingPort returns the Node remoting port
func (n *Node) NodeRemotingPort() int {
	return n.host.RemotingPort
}

// AdvertisedAddress returns the cluster node cluster address that is known by the
// peers in the cluster
func (n *Node) AdvertisedAddress() string {
	return n.host.ClusterAddress()
}

// PutActor replicates onto the Node the metadata of an actor
func (n *Node) PutActor(ctx context.Context, actor *internalpb.WireActor) error {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, n.writeTimeout)
	defer cancelFn()

	// set the logger
	logger := n.logger

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
	err = n.kvStore.Put(ctx, actor.GetActorName(), data)
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to replicate actor=%s record.💥", actor.GetActorName()))
		return err
	}

	logger.Infof("actor (%s) successfully replicated.🎉", actor.GetActorName())
	return nil
}

// GetActor fetches an actor from the Node
func (n *Node) GetActor(ctx context.Context, actorName string) (*internalpb.WireActor, error) {
	ctx, cancelFn := context.WithTimeout(ctx, n.readTimeout)
	defer cancelFn()

	logger := n.logger

	logger.Infof("retrieving actor (%s) from the Node.🤔", actorName)

	resp, err := n.kvStore.Get(ctx, actorName)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("actor=%s is not found in the Node", actorName)
			return nil, ErrActorNotFound
		}
		logger.Error(errors.Wrapf(err, "failed to get actor=%s record.💥", actorName))
		return nil, err
	}

	base64ActorStr, err := resp.String()
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to read the record at:{%s}.💥", actorName))
		return nil, err
	}

	actor, err := decode(base64ActorStr)
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to decode actor=%s record.💥", actorName))
		return nil, err
	}

	logger.Infof("actor (%s) successfully retrieved from the cluster.🎉", actor.GetActorName())
	return actor, nil
}

// RemoveActor removes a given actor from the cluster.
// An actor is removed from the cluster when this actor has been passivated.
func (n *Node) RemoveActor(ctx context.Context, actorName string) error {
	logger := n.logger

	logger.Infof("removing actor (%s).🤔", actorName)

	_, err := n.kvStore.Delete(ctx, actorName)
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to remove actor=%s record.💥", actorName))
		return err
	}

	logger.Infof("actor (%s) successfully removed from the cluster.🎉", actorName)
	return nil
}

// SetKey sets a given key to the cluster
func (n *Node) SetKey(ctx context.Context, key string) error {
	ctx, cancelFn := context.WithTimeout(ctx, n.writeTimeout)
	defer cancelFn()

	logger := n.logger

	logger.Infof("replicating key (%s).🤔", key)

	if err := n.kvStore.Put(ctx, key, true); err != nil {
		logger.Error(errors.Wrapf(err, "failed to replicate key=%s record.💥", key))
		return err
	}

	logger.Infof("key (%s) successfully replicated.🎉", key)
	return nil
}

// KeyExists checks the existence of a given key
func (n *Node) KeyExists(ctx context.Context, key string) (bool, error) {
	ctx, cancelFn := context.WithTimeout(ctx, n.readTimeout)
	defer cancelFn()

	logger := n.logger

	logger.Infof("checking key (%s) existence in the cluster.🤔", key)

	resp, err := n.kvStore.Get(ctx, key)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("key=%s is not found in the Node", key)
			return false, nil
		}

		logger.Error(errors.Wrapf(err, "failed to check key=%s existence.💥", key))
		return false, err
	}
	return resp.Bool()
}

// GetPartition returns the partition where a given actor is stored
func (n *Node) GetPartition(actorName string) int {
	key := []byte(actorName)
	hkey := n.hasher.HashCode(key)
	partition := int(hkey % n.partitionsCount)
	n.logger.Debugf("partition of actor (%s) is (%d)", actorName, partition)
	return partition
}

// Events returns a channel where cluster events are published
func (n *Node) Events() <-chan *Event {
	return n.events
}

// consume reads to the underlying cluster events
// and emit the event
func (n *Node) consume() {
	for {
		select {
		case <-n.messagesReaderChan:
			return
		case message, ok := <-n.messagesChan:
			if !ok {
				return
			}
			payload := message.Payload
			var event map[string]any
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				n.logger.Error(errors.Wrap(err, "failed to decode cluster event"))
				// TODO: should we continue or not
				continue
			}

			kind := event["kind"]
			n.logger.Debugf("%s received (%s) cluster event", n.name, kind)

			switch kind {
			case events.KindNodeJoinEvent:
				nodeJoined := new(events.NodeJoinEvent)
				if err := json.Unmarshal([]byte(payload), &nodeJoined); err != nil {
					n.logger.Error(errors.Wrap(err, "failed to decode NodeJoined cluster event"))
					// TODO: should we continue or not
					continue
				}

				if n.AdvertisedAddress() == nodeJoined.NodeJoin {
					n.logger.Debug("skipping self")
					continue
				}

				// TODO: need to cross check this calculation
				timeMilli := nodeJoined.Timestamp / int64(1e6)
				event := &goaktpb.NodeJoined{
					Address:   nodeJoined.NodeJoin,
					Timestamp: timestamppb.New(time.UnixMilli(timeMilli)),
				}

				eventType, _ := anypb.New(event)
				n.events <- &Event{eventType}

			case events.KindNodeLeftEvent:
				nodeLeft := new(events.NodeLeftEvent)
				if err := json.Unmarshal([]byte(payload), &nodeLeft); err != nil {
					n.logger.Error(errors.Wrap(err, "failed to decode NodeLeft cluster event"))
					// TODO: should we continue or not
					continue
				}

				// TODO: need to cross check this calculation
				timeMilli := nodeLeft.Timestamp / int64(1e6)

				event := &goaktpb.NodeLeft{
					Address:   nodeLeft.NodeLeft,
					Timestamp: timestamppb.New(time.UnixMilli(timeMilli)),
				}

				eventType, _ := anypb.New(event)
				n.events <- &Event{eventType}
			default:
				// skip
			}
		}
	}
}

// buildConfig builds the Node configuration
func (n *Node) buildConfig() *config.Config {
	// define the log level
	logLevel := "INFO"
	switch n.logger.LogLevel() {
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
		BindAddr:                   n.host.Host,
		BindPort:                   n.host.ClusterPort,
		ReadRepair:                 false,
		ReplicaCount:               config.MinimumReplicaCount,
		WriteQuorum:                config.DefaultWriteQuorum,
		ReadQuorum:                 config.DefaultReadQuorum,
		MemberCountQuorum:          config.DefaultMemberCountQuorum,
		Peers:                      []string{},
		DMaps:                      &olriconfig.DMaps{},
		KeepAlivePeriod:            config.DefaultKeepAlivePeriod,
		PartitionCount:             n.partitionsCount,
		BootstrapTimeout:           config.DefaultBootstrapTimeout,
		ReplicationMode:            olriconfig.SyncReplicationMode,
		RoutingTablePushInterval:   config.DefaultRoutingTablePushInterval,
		JoinRetryInterval:          config.DefaultJoinRetryInterval,
		MaxJoinAttempts:            config.DefaultMaxJoinAttempts,
		LogLevel:                   logLevel,
		LogOutput:                  newLogWriter(n.logger),
		EnableClusterEventsChannel: true,
		Hasher:                     hasher.NewDefaultHasher(),
	}

	// set verbosity when debug is enabled
	if n.logger.LogLevel() == log.DebugLevel {
		conf.LogVerbosity = config.DefaultLogVerbosity
	}

	return conf
}
