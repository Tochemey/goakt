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
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/buraksezer/olric"
	"github.com/buraksezer/olric/config"
	"github.com/buraksezer/olric/events"
	"github.com/buraksezer/olric/hasher"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/hash"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
)

type EventType int

const (
	NodeJoined EventType = iota
	NodeLeft
)

func (x EventType) String() string {
	switch x {
	case NodeJoined:
		return "NodJoined"
	case NodeLeft:
		return "NodeLeft"
	default:
		return fmt.Sprintf("%d", int(x))
	}
}

// Event defines the cluster event
type Event struct {
	Payload *anypb.Any
	Type    EventType
}

// Interface defines the Node interface
type Interface interface {
	// Start starts the cluster engine
	Start(ctx context.Context) error
	// Stop stops the cluster engine
	Stop(ctx context.Context) error
	// NodeHost returns the cluster startNode host address
	NodeHost() string
	// NodeRemotingPort returns the cluster remoting port
	NodeRemotingPort() int
	// GetActor fetches an actor from the Node
	GetActor(ctx context.Context, actorName string) (*internalpb.WireActor, error)
	// GetPartition returns the partition where a given actor is stored
	GetPartition(actorName string) int
	// SetKey sets a given key to the cluster
	SetKey(ctx context.Context, key string) error
	// KeyExists checks the existence of a given key
	KeyExists(ctx context.Context, key string) (bool, error)
	// UnsetKey unsets the already set given key in the cluster
	UnsetKey(ctx context.Context, key string) error
	// RemoveActor removes a given actor from the cluster.
	// An actor is removed from the cluster when this actor has been passivated.
	RemoveActor(ctx context.Context, actorName string) error
	// Events returns a channel where cluster events are published
	Events() <-chan *Event
	// AdvertisedAddress returns the cluster node cluster address that is known by the
	// peers in the cluster
	AdvertisedAddress() string
	// Peers returns a channel containing the list of peers at a given time
	Peers(ctx context.Context) ([]*Peer, error)
	// PutPeerSync pushes to the cluster the peer sync request
	PutPeerSync(ctx context.Context, data *internalpb.PeersSync) error
	// GetPeerSync fetches a given peer synchronization record
	GetPeerSync(ctx context.Context, peerAddress string) (*internalpb.PeersSync, error)
}

// Engine represents the Engine
type Engine struct {
	// specifies the total number of partitions
	// the default values is 20
	partitionsCount uint64

	// specifies the minimum number of cluster members
	// the default values is 1
	minimumPeersQuorum uint16

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

	writeTimeout    time.Duration
	readTimeout     time.Duration
	shutdownTimeout time.Duration

	events             chan *Event
	pubSub             *redis.PubSub
	messagesChan       <-chan *redis.Message
	messagesReaderChan chan types.Unit
}

// enforce compilation error
var _ Interface = (*Engine)(nil)

// NewEngine creates an instance of cluster Engine
func NewEngine(name string, disco discovery.Provider, host *discovery.Node, opts ...Option) (*Engine, error) {
	// create an instance of the Node
	engine := &Engine{
		partitionsCount:    20,
		logger:             log.DefaultLogger,
		name:               name,
		discoveryProvider:  disco,
		writeTimeout:       time.Second,
		readTimeout:        time.Second,
		shutdownTimeout:    3 * time.Second,
		hasher:             hash.DefaultHasher(),
		pubSub:             nil,
		events:             make(chan *Event, 20),
		messagesReaderChan: make(chan types.Unit, 1),
		messagesChan:       make(chan *redis.Message, 1),
		minimumPeersQuorum: 1,
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(engine)
	}

	// set the host startNode
	engine.host = host

	return engine, nil
}

// Start starts the Engine.
func (n *Engine) Start(ctx context.Context) error {
	logger := n.logger

	logger.Infof("Starting GoAkt cluster Engine service on host=(%s)....🤔", n.host.PeersAddress())

	conf := n.buildConfig()
	conf.Hasher = &hasherWrapper{n.hasher}

	m, err := config.NewMemberlistConfig("lan")
	if err != nil {
		logger.Error(errors.Wrap(err, "failed to configure the cluster Engine members list.💥"))
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
		"plugin": discoveryWrapper,
		"id":     n.discoveryProvider.ID(),
	}

	// let us start the Node
	startCtx, cancel := context.WithCancel(ctx)
	// cancel the context the server has started
	conf.Started = func() {
		defer cancel()
	}

	eng, err := olric.New(conf)
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to start the cluster Engine on host=(%s)", n.name))
		return err
	}

	// set the server
	n.server = eng
	go func() {
		if err = n.server.Start(); err != nil {
			if e := n.server.Shutdown(ctx); e != nil {
				logger.Panic(e)
			}
			// the expectation is to exit the application
			logger.Fatal(errors.Wrapf(err, "failed to start the cluster Engine on host=(%s)", n.name))
		}
	}()

	<-startCtx.Done()
	logger.Info("cluster engine successfully started. 🎉")

	// set the client
	n.client = n.server.NewEmbeddedClient()
	dmp, err := n.client.NewDMap(n.name)
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to start the cluster Engine on host=(%s)", n.name))
		return n.server.Shutdown(ctx)
	}

	n.kvStore = dmp

	// create a subscriber to consume to cluster events
	ps, err := n.client.NewPubSub()
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to start the cluster Engine on host=(%s)", n.name))
		return n.server.Shutdown(ctx)
	}

	n.pubSub = ps.Subscribe(ctx, events.ClusterEventsChannel)
	n.messagesChan = n.pubSub.Channel()
	go n.consume()

	logger.Infof("GoAkt cluster Engine=(%s) successfully started.", n.name)
	return nil
}

// Stop stops the Engine gracefully
func (n *Engine) Stop(ctx context.Context) error {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, n.shutdownTimeout)
	defer cancelFn()

	// set the logger
	logger := n.logger

	// add some logging information
	logger.Infof("Stopping GoAkt cluster Node=(%s)....🤔", n.name)

	// close the events listener
	if err := n.pubSub.Close(); err != nil {
		logger.Error(errors.Wrap(err, "failed to shutdown the cluster events listener"))
		return err
	}

	// close the Node client
	if err := n.client.Close(ctx); err != nil {
		logger.Error(errors.Wrapf(err, "failed to shutdown the cluster engine=(%s)", n.name))
		return err
	}

	// let us stop the server
	if err := n.server.Shutdown(ctx); err != nil {
		logger.Error(errors.Wrapf(err, "failed to shutdown the cluster engine=(%s)", n.name))
		return err
	}

	// close the events queue
	close(n.events)

	// signal we are stopping listening to events
	n.messagesReaderChan <- types.Unit{}

	logger.Infof("GoAkt cluster Node=(%s) successfully stopped.", n.name)
	return nil
}

// NodeHost returns the Node Host
func (n *Engine) NodeHost() string {
	return n.host.Host
}

// NodeRemotingPort returns the Node remoting port
func (n *Engine) NodeRemotingPort() int {
	return n.host.RemotingPort
}

// AdvertisedAddress returns the cluster node cluster address that is known by the
// peers in the cluster
func (n *Engine) AdvertisedAddress() string {
	return n.host.PeersAddress()
}

// PutPeerSync pushes to the cluster the peer sync request
func (n *Engine) PutPeerSync(ctx context.Context, syncRequest *internalpb.PeersSync) error {
	ctx, cancelFn := context.WithTimeout(ctx, n.writeTimeout)
	defer cancelFn()

	logger := n.logger

	peerAddress := net.JoinHostPort(syncRequest.GetHost(), strconv.Itoa(int(syncRequest.GetPeersPort())))
	logger.Infof("synchronization peer (%s)", peerAddress)

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(2)

	eg.Go(func() error {
		encoded, _ := encode(syncRequest.GetActor())
		if err := n.kvStore.Put(ctx, syncRequest.GetActor().GetActorName(), encoded); err != nil {
			return fmt.Errorf("failed to sync actor=(%s) of peer=(%s): %v", syncRequest.GetActor().GetActorName(), peerAddress, err)
		}

		return nil
	})

	eg.Go(func() error {
		encoded, _ := proto.Marshal(syncRequest)
		if err := n.kvStore.Put(ctx, peerAddress, encoded); err != nil {
			return fmt.Errorf("failed to sync peer=(%s) request: %v", peerAddress, err)
		}

		return nil
	})

	if err := eg.Wait(); err != nil {
		logger.Errorf("failed to synchronize peer=(%s): %v", peerAddress, err)
		return err
	}

	logger.Infof("peer (%s) successfully synchronized in the cluster", peerAddress)
	return nil
}

// GetPeerSync fetches a given peer synchronization record
func (n *Engine) GetPeerSync(ctx context.Context, peerAddress string) (*internalpb.PeersSync, error) {
	ctx, cancelFn := context.WithTimeout(ctx, n.readTimeout)
	defer cancelFn()

	logger := n.logger

	logger.Infof("retrieving peer (%s) sync record", peerAddress)
	resp, err := n.kvStore.Get(ctx, peerAddress)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("peer=(%s) sync record is not found", peerAddress)
			return nil, ErrPeerSyncNotFound
		}

		logger.Errorf("failed to find peer=(%s) sync record: %v", peerAddress, err)
		return nil, err
	}

	bytea, err := resp.Byte()
	if err != nil {
		logger.Errorf("failed to read peer=(%s) sync record: %v", err)
		return nil, err
	}

	peerSync := new(internalpb.PeersSync)
	if err := proto.Unmarshal(bytea, peerSync); err != nil {
		logger.Errorf("failed to decode peer=(%s) sync record: %v", err)
		return nil, err
	}

	logger.Infof("peer (%s) sync record successfully retrieved.🎉", peerAddress)
	return peerSync, nil
}

// GetActor fetches an actor from the Node
func (n *Engine) GetActor(ctx context.Context, actorName string) (*internalpb.WireActor, error) {
	ctx, cancelFn := context.WithTimeout(ctx, n.readTimeout)
	defer cancelFn()

	logger := n.logger

	logger.Infof("retrieving actor (%s) from the cluster", actorName)

	resp, err := n.kvStore.Get(ctx, actorName)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("actor=%s is not found in the cluster", actorName)
			return nil, ErrActorNotFound
		}
		logger.Error(errors.Wrapf(err, "failed to get actor=%s record", actorName))
		return nil, err
	}

	bytea, err := resp.Byte()
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to read the record at:{%s}", actorName))
		return nil, err
	}

	actor, err := decode(bytea)
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to decode actor=%s record", actorName))
		return nil, err
	}

	logger.Infof("actor (%s) successfully retrieved from the cluster", actor.GetActorName())
	return actor, nil
}

// RemoveActor removes a given actor from the cluster.
// An actor is removed from the cluster when this actor has been passivated.
func (n *Engine) RemoveActor(ctx context.Context, actorName string) error {
	logger := n.logger

	logger.Infof("removing actor (%s)", actorName)

	_, err := n.kvStore.Delete(ctx, actorName)
	if err != nil {
		logger.Error(errors.Wrapf(err, "failed to remove actor=%s record", actorName))
		return err
	}

	logger.Infof("actor (%s) successfully removed from the cluster", actorName)
	return nil
}

// SetKey sets a given key to the cluster
func (n *Engine) SetKey(ctx context.Context, key string) error {
	ctx, cancelFn := context.WithTimeout(ctx, n.writeTimeout)
	defer cancelFn()

	logger := n.logger

	logger.Infof("replicating key (%s)", key)

	if err := n.kvStore.Put(ctx, key, true); err != nil {
		logger.Error(errors.Wrapf(err, "failed to replicate key=%s record.💥", key))
		return err
	}

	logger.Infof("key (%s) successfully replicated", key)
	return nil
}

// KeyExists checks the existence of a given key
func (n *Engine) KeyExists(ctx context.Context, key string) (bool, error) {
	ctx, cancelFn := context.WithTimeout(ctx, n.readTimeout)
	defer cancelFn()

	logger := n.logger

	logger.Infof("checking key (%s) existence in the cluster", key)

	resp, err := n.kvStore.Get(ctx, key)
	if err != nil {
		if errors.Is(err, olric.ErrKeyNotFound) {
			logger.Warnf("key=%s is not found in the Node", key)
			return false, nil
		}

		logger.Error(errors.Wrapf(err, "failed to check key=%s existence", key))
		return false, err
	}
	return resp.Bool()
}

// UnsetKey unsets the already set given key in the cluster
func (n *Engine) UnsetKey(ctx context.Context, key string) error {
	logger := n.logger

	logger.Infof("unsetting key (%s)", key)

	if _, err := n.kvStore.Delete(ctx, key); err != nil {
		logger.Error(errors.Wrapf(err, "failed to unset key=%s record.💥", key))
		return err
	}

	logger.Infof("key (%s) successfully unset", key)
	return nil
}

// GetPartition returns the partition where a given actor is stored
func (n *Engine) GetPartition(actorName string) int {
	key := []byte(actorName)
	hkey := n.hasher.HashCode(key)
	partition := int(hkey % n.partitionsCount)
	n.logger.Debugf("partition of actor (%s) is (%d)", actorName, partition)
	return partition
}

// Events returns a channel where cluster events are published
func (n *Engine) Events() <-chan *Event {
	return n.events
}

// Peers returns a channel containing the list of peers at a given time
func (n *Engine) Peers(ctx context.Context) ([]*Peer, error) {
	members, err := n.client.Members(ctx)
	if err != nil {
		n.logger.Error(errors.Wrap(err, "failed to read cluster peers"))
		return nil, err
	}

	peers := make([]*Peer, 0, len(members))
	for _, member := range members {
		if member.Name != n.AdvertisedAddress() {
			n.logger.Debugf("node=(%s) has found peer=(%s)", n.AdvertisedAddress(), member.Name)
			peerHost, port, _ := net.SplitHostPort(member.Name)
			peerPort, _ := strconv.Atoi(port)
			peers = append(peers, &Peer{Host: peerHost, Port: peerPort, Leader: member.Coordinator})
		}
	}
	return peers, nil
}

// consume reads to the underlying cluster events
// and emit the event
func (n *Engine) consume() {
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

				n.logger.Debugf("%s received (%s) cluster event:[addr=(%s)]", n.name, kind, event.GetAddress())

				payload, _ := anypb.New(event)
				n.events <- &Event{payload, NodeJoined}

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

				n.logger.Debugf("%s received (%s) cluster event:[addr=(%s)]", n.name, kind, event.GetAddress())

				payload, _ := anypb.New(event)
				n.events <- &Event{payload, NodeLeft}
			default:
				// skip
			}
		}
	}
}

// buildConfig builds the Node configuration
func (n *Engine) buildConfig() *config.Config {
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
		BindPort:                   n.host.PeersPort,
		ReadRepair:                 true,
		ReplicaCount:               int(n.minimumPeersQuorum),
		WriteQuorum:                int(n.minimumPeersQuorum),
		ReadQuorum:                 int(n.minimumPeersQuorum),
		MemberCountQuorum:          int32(n.minimumPeersQuorum),
		Peers:                      []string{},
		DMaps:                      &config.DMaps{},
		KeepAlivePeriod:            config.DefaultKeepAlivePeriod,
		PartitionCount:             n.partitionsCount,
		BootstrapTimeout:           config.DefaultBootstrapTimeout,
		ReplicationMode:            config.SyncReplicationMode,
		RoutingTablePushInterval:   config.DefaultRoutingTablePushInterval,
		JoinRetryInterval:          config.DefaultJoinRetryInterval,
		MaxJoinAttempts:            config.DefaultMaxJoinAttempts,
		LogLevel:                   logLevel,
		LogOutput:                  newLogWriter(n.logger),
		EnableClusterEventsChannel: true,
		Hasher:                     hasher.NewDefaultHasher(),
		TriggerBalancerInterval:    config.DefaultTriggerBalancerInterval,
	}

	// set verbosity when debug is enabled
	if n.logger.LogLevel() == log.DebugLevel {
		conf.LogVerbosity = config.DefaultLogVerbosity
	}

	return conf
}
