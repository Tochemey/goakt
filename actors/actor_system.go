package actors

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/cluster"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/eventstream"
	internalpb "github.com/tochemey/goakt/internal/v1"
	"github.com/tochemey/goakt/internal/v1/internalpbconnect"
	"github.com/tochemey/goakt/log"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	eventspb "github.com/tochemey/goakt/pb/events/v1"
	"github.com/tochemey/goakt/telemetry"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/atomic"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// ActorSystem defines the contract of an actor system
type ActorSystem interface {
	// Name returns the actor system name
	Name() string
	// Actors returns the list of Actors that are alive in the actor system
	Actors() []PID
	// Start starts the actor system
	Start(ctx context.Context) error
	// Stop stops the actor system
	Stop(ctx context.Context) error
	// Spawn creates an actor in the system and starts it
	Spawn(ctx context.Context, name string, actor Actor) (PID, error)
	// Kill stops a given actor in the system
	Kill(ctx context.Context, name string) error
	// ReSpawn recreates a given actor in the system
	ReSpawn(ctx context.Context, name string) (PID, error)
	// NumActors returns the total number of active actors in the system
	NumActors() uint64
	// LocalActor returns the reference of a local actor.
	// A local actor is an actor that reside on the same node where the given actor system is running
	LocalActor(actorName string) (PID, error)
	// RemoteActor returns the address of a remote actor when cluster is enabled
	// When the cluster mode is not enabled an actor not found error will be returned
	// One can always check whether cluster is enabled before calling this method or just use the ActorOf method.
	RemoteActor(ctx context.Context, actorName string) (addr *addresspb.Address, err error)
	// ActorOf returns an existing actor in the local system or in the cluster when clustering is enabled
	// When cluster mode is activated, the PID will be nil.
	// When remoting is enabled this method will return and error
	// An actor not found error is return when the actor is not found.
	ActorOf(ctx context.Context, actorName string) (addr *addresspb.Address, pid PID, err error)
	// InCluster states whether the actor system is running within a cluster of nodes
	InCluster() bool
	// GetPartition returns the partition where a given actor is located
	GetPartition(actorName string) uint64
	// Subscribe creates an event subscriber.
	Subscribe(event eventspb.Event) (eventstream.Subscriber, error)
	// Unsubscribe unsubscribes a subscriber.
	Unsubscribe(event eventspb.Event, subscriber eventstream.Subscriber) error

	// handleRemoteAsk handles a synchronous message to another actor and expect a response.
	// This block until a response is received or timed out.
	handleRemoteAsk(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error)
	// handleRemoteTell handles an asynchronous message to an actor
	handleRemoteTell(ctx context.Context, to PID, message proto.Message) error
}

// ActorSystem represent a collection of actors on a given node
// Only a single instance of the ActorSystem can be created on a given node
type actorSystem struct {
	internalpbconnect.UnimplementedRemotingServiceHandler

	// map of actors in the system
	actors *pidMap

	// states whether the actor system has started or not
	hasStarted atomic.Bool

	// Specifies the actor system name
	name string
	// Specifies the logger to use in the system
	logger log.Logger
	// Specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 5s
	expireActorAfter time.Duration
	// Specifies how long the sender of a receiveContext should wait to receive a reply
	// when using SendReply. The default value is 5s
	replyTimeout time.Duration
	// Specifies the shutdown timeout. The default value is 30s
	shutdownTimeout time.Duration
	// Specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	actorInitMaxRetries int
	// Specifies the supervisor strategy
	supervisorStrategy StrategyDirective
	// Specifies the telemetry config
	telemetry *telemetry.Telemetry
	// Specifies whether remoting is enabled.
	// This allows to handle remote messaging
	remotingEnabled atomic.Bool
	// Specifies the remoting port
	remotingPort int32
	// Specifies the remoting host
	remotingHost string
	// Specifies the remoting server
	remotingServer *http.Server

	// convenient field to check cluster setup
	clusterEnabled atomic.Bool
	// cluster discovery method
	serviceDiscovery *discovery.ServiceDiscovery
	// define the number of partitions to shard the actors in the cluster
	partitionsCount uint64
	// cluster mode
	cluster     cluster.Interface
	clusterChan chan *internalpb.WireActor

	// help protect some the fields to set
	sem sync.Mutex
	// specifies actors mailbox size
	mailboxSize uint64
	// specifies the mailbox to use for the actors
	mailbox Mailbox
	// specifies the stash buffer
	stashBuffer        uint64
	housekeeperStopSig chan Unit

	eventsStream *eventstream.EventsStream
}

// enforce compilation error when all methods of the ActorSystem interface are not implemented
// by the struct actorSystem
var _ ActorSystem = (*actorSystem)(nil)

// NewActorSystem creates an instance of ActorSystem
func NewActorSystem(name string, opts ...Option) (ActorSystem, error) {
	// check whether the name is set or not
	if name == "" {
		return nil, ErrNameRequired
	}
	// make sure the actor system name is valid
	if match, _ := regexp.MatchString("^[a-zA-Z0-9][a-zA-Z0-9-_]*$", name); !match {
		return nil, ErrInvalidActorSystemName
	}

	// create the instance of the actor system with the default settings
	system := &actorSystem{
		actors:              newPIDMap(10),
		clusterChan:         make(chan *internalpb.WireActor, 10),
		name:                name,
		logger:              log.DefaultLogger,
		expireActorAfter:    DefaultPassivationTimeout,
		replyTimeout:        DefaultReplyTimeout,
		actorInitMaxRetries: DefaultInitMaxRetries,
		supervisorStrategy:  DefaultSupervisoryStrategy,
		telemetry:           telemetry.New(),
		sem:                 sync.Mutex{},
		shutdownTimeout:     DefaultShutdownTimeout,
		mailboxSize:         defaultMailboxSize,
		housekeeperStopSig:  make(chan Unit, 1),
		eventsStream:        eventstream.New(),
	}
	// set the atomic settings
	system.hasStarted.Store(false)
	system.remotingEnabled.Store(false)
	system.clusterEnabled.Store(false)

	// apply the various options
	for _, opt := range opts {
		opt.Apply(system)
	}

	return system, nil
}

// Subscribe help receive dead letters whenever there are available
func (x *actorSystem) Subscribe(event eventspb.Event) (eventstream.Subscriber, error) {
	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		return nil, ErrActorSystemNotStarted
	}
	// create the consumer
	subscriber := x.eventsStream.AddSubscriber()
	// based upon the event we will subscribe to the various topic
	switch event {
	case eventspb.Event_DEAD_LETTER:
		// subscribe the consumer to the deadletter topic
		x.eventsStream.Subscribe(subscriber, deadlettersTopic)
	}
	return subscriber, nil
}

// Unsubscribe unsubscribes a subscriber.
func (x *actorSystem) Unsubscribe(event eventspb.Event, subscriber eventstream.Subscriber) error {
	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		return ErrActorSystemNotStarted
	}

	// based upon the event we will unsubscribe to the various topic
	switch event {
	case eventspb.Event_DEAD_LETTER:
		// subscribe the consumer to the deadletter topic
		x.eventsStream.Unsubscribe(subscriber, deadlettersTopic)
		// remove the subscriber from the events stream
		x.eventsStream.RemoveSubscriber(subscriber)
	}
	return nil
}

// GetPartition returns the partition where a given actor is located
func (x *actorSystem) GetPartition(actorName string) uint64 {
	// return zero when the actor system is not in cluster mode
	if !x.InCluster() {
		// TODO: maybe add a partitioner function
		return 0
	}

	// fetch the actor name partition from the cluster
	return uint64(x.cluster.GetPartition(actorName))
}

// InCluster states whether the actor system is running within a cluster of nodes
func (x *actorSystem) InCluster() bool {
	return x.clusterEnabled.Load() && x.cluster != nil
}

// NumActors returns the total number of active actors in the system
func (x *actorSystem) NumActors() uint64 {
	return uint64(x.actors.Len())
}

// Spawn creates or returns the instance of a given actor in the system
func (x *actorSystem) Spawn(ctx context.Context, name string, actor Actor) (PID, error) {
	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		return nil, ErrActorSystemNotStarted
	}
	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(x.name, "", -1))
	// set the actor path when the remoting is enabled
	if x.remotingEnabled.Load() {
		// get the path of the given actor
		actorPath = NewPath(name, NewAddress(x.name, x.remotingHost, int(x.remotingPort)))
	}
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath)
	// actor already exist no need recreate it.
	if exist {
		// check whether the given actor heart beat
		if pid.IsRunning() {
			// return the existing instance
			return pid, nil
		}
	}

	// create an instance of the actor ref
	pid, err := newPID(ctx,
		actorPath,
		actor,
		withInitMaxRetries(x.actorInitMaxRetries),
		withPassivationAfter(x.expireActorAfter),
		withSendReplyTimeout(x.replyTimeout),
		withCustomLogger(x.logger),
		withActorSystem(x),
		withSupervisorStrategy(x.supervisorStrategy),
		withMailboxSize(x.mailboxSize),
		withMailbox(x.mailbox),
		withStash(x.stashBuffer),
		withEventsStream(x.eventsStream),
		withTelemetry(x.telemetry))

	// handle the error
	if err != nil {
		return nil, err
	}

	// add the given actor to the actor map
	x.actors.Set(pid)

	// when cluster is enabled replicate the actor metadata across the cluster
	if x.clusterEnabled.Load() {
		// send it to the cluster channel a wire actor
		x.clusterChan <- &internalpb.WireActor{
			ActorName:    name,
			ActorAddress: actorPath.RemoteAddress(),
			ActorPath:    actorPath.String(),
		}
	}

	// return the actor ref
	return pid, nil
}

// Kill stops a given actor in the system
func (x *actorSystem) Kill(ctx context.Context, name string) error {
	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		return ErrActorSystemNotStarted
	}
	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(x.name, "", -1))
	// set the actor path with the remoting is enabled
	if x.remotingEnabled.Load() {
		// get the path of the given actor
		actorPath = NewPath(name, NewAddress(x.name, x.remotingHost, int(x.remotingPort)))
	}
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath)
	// actor is found.
	if exist {
		// stop the given actor
		return pid.Shutdown(ctx)
	}
	return ErrActorNotFound(actorPath.String())
}

// ReSpawn recreates a given actor in the system
func (x *actorSystem) ReSpawn(ctx context.Context, name string) (PID, error) {
	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		return nil, ErrActorSystemNotStarted
	}
	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(x.name, "", -1))
	// set the actor path with the remoting is enabled
	if x.remotingEnabled.Load() {
		// get the path of the given actor
		actorPath = NewPath(name, NewAddress(x.name, x.remotingHost, int(x.remotingPort)))
	}
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath)
	// actor is found.
	if exist {
		// restart the given actor
		if err := pid.Restart(ctx); err != nil {
			// return the error in case the restart failed
			return nil, errors.Wrapf(err, "failed to restart actor=%s", actorPath.String())
		}
		// let us re-add the actor to the actor system list when it has successfully restarted
		// add the given actor to the actor map
		x.actors.Set(pid)
		return pid, nil
	}
	return nil, ErrActorNotFound(actorPath.String())
}

// Name returns the actor system name
func (x *actorSystem) Name() string {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	return x.name
}

// Actors returns the list of Actors that are alive in the actor system
func (x *actorSystem) Actors() []PID {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()

	// get the actors from the actor map
	return x.actors.List()
}

// ActorOf returns an existing actor in the local system or in the cluster when clustering is enabled
// When cluster mode is activated, the PID will be nil.
// When remoting is enabled this method will return and error
// An actor not found error is return when the actor is not found.
func (x *actorSystem) ActorOf(ctx context.Context, actorName string) (addr *addresspb.Address, pid PID, err error) {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()

	// make sure the actor system has started
	if !x.hasStarted.Load() {
		return nil, nil, ErrActorSystemNotStarted
	}

	// try to locate the actor in the cluster when cluster is enabled
	if x.cluster != nil || x.clusterEnabled.Load() {
		// let us locate the actor in the cluster
		wireActor, err := x.cluster.GetActor(ctx, actorName)
		// handle the eventual error
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				x.logger.Infof("actor=%s not found", actorName)
				return nil, nil, ErrActorNotFound(actorName)
			}
			return nil, nil, errors.Wrapf(err, "failed to fetch remote actor=%s", actorName)
		}

		// return the address of the remote actor
		return wireActor.GetActorAddress(), nil, nil
	}

	// method call is not allowed
	if x.remotingEnabled.Load() {
		return nil, nil, ErrMethodCallNotAllowed
	}

	// try to locate the actor when cluster is not enabled
	// first let us do a local lookup
	items := x.actors.List()
	// iterate the local actors storage
	for _, actorRef := range items {
		if actorRef.ActorPath().Name() == actorName {
			return actorRef.ActorPath().RemoteAddress(), actorRef, nil
		}
	}
	// add a logger
	x.logger.Infof("actor=%s not found", actorName)
	return nil, nil, ErrActorNotFound(actorName)
}

// LocalActor returns the reference of a local actor.
// A local actor is an actor that reside on the same node where the given actor system is running
func (x *actorSystem) LocalActor(actorName string) (PID, error) {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()

	// make sure the actor system has started
	if !x.hasStarted.Load() {
		return nil, ErrActorSystemNotStarted
	}

	// first let us do a local lookup
	items := x.actors.List()
	// iterate the local actors storage
	for _, actorRef := range items {
		if actorRef.ActorPath().Name() == actorName {
			return actorRef, nil
		}
	}
	// add a logger
	x.logger.Infof("actor=%s not found", actorName)
	return nil, ErrActorNotFound(actorName)
}

// RemoteActor returns the address of a remote actor when cluster is enabled
// When the cluster mode is not enabled an actor not found error will be returned
// One can always check whether cluster is enabled before calling this method or just use the ActorOf method.
func (x *actorSystem) RemoteActor(ctx context.Context, actorName string) (addr *addresspb.Address, err error) {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()

	// make sure the actor system has started
	if !x.hasStarted.Load() {
		return nil, ErrActorSystemNotStarted
	}

	// check whether cluster is enabled or not
	if x.cluster == nil {
		return nil, ErrClusterDisabled
	}

	// let us locate the actor in the cluster
	wireActor, err := x.cluster.GetActor(ctx, actorName)
	// handle the eventual error
	if err != nil {
		if errors.Is(err, cluster.ErrActorNotFound) {
			x.logger.Infof("actor=%s not found", actorName)
			return nil, ErrActorNotFound(actorName)
		}
		return nil, errors.Wrapf(err, "failed to fetch remote actor=%s", actorName)
	}

	// return the address of the remote actor
	return wireActor.GetActorAddress(), nil
}

// Start starts the actor system
func (x *actorSystem) Start(ctx context.Context) error {
	// set the has started to true
	x.hasStarted.Store(true)

	// enable clustering when it is enabled
	if x.clusterEnabled.Load() {
		x.enableClustering(ctx)
	}

	// start remoting when remoting is enabled
	if x.remotingEnabled.Load() {
		x.enableRemoting(ctx)
	}

	// start the housekeeper
	go x.housekeeper()

	x.logger.Infof("%s started..:)", x.name)
	return nil
}

// Stop stops the actor system
func (x *actorSystem) Stop(ctx context.Context) error {
	// make sure the actor system has started
	if !x.hasStarted.Load() {
		return ErrActorSystemNotStarted
	}

	// create a cancellation context to gracefully shutdown
	ctx, cancel := context.WithTimeout(ctx, x.shutdownTimeout)
	defer cancel()

	// stop the housekeeper
	x.housekeeperStopSig <- Unit{}
	x.logger.Infof("%s is shutting down..:)", x.name)

	// set started to false
	x.hasStarted.Store(false)

	// shutdown the events stream
	if x.eventsStream != nil {
		x.eventsStream.Shutdown()
	}

	// stop the remoting server
	if x.remotingEnabled.Load() {
		// stop the server
		if err := x.remotingServer.Shutdown(ctx); err != nil {
			return err
		}

		// unset the remoting settings
		x.remotingEnabled.Store(false)
		x.remotingServer = nil
	}

	// stop the cluster service
	if x.clusterEnabled.Load() {
		// stop the cluster service
		if err := x.cluster.Stop(ctx); err != nil {
			return err
		}
		// stop broadcasting cluster messages
		close(x.clusterChan)

		// unset the remoting settings
		x.clusterEnabled.Store(false)
		x.cluster = nil
	}

	// short-circuit the shutdown process when there are no online actors
	if len(x.Actors()) == 0 {
		x.logger.Info("No online actors to shutdown. Shutting down successfully done")
		return nil
	}

	// stop all the actors
	for _, actor := range x.Actors() {
		// remove the actor the map and shut it down
		x.actors.Delete(actor.ActorPath())
		// only shutdown live actors
		if err := actor.Shutdown(ctx); err != nil {
			// reset the actor system
			x.reset()
			// return the error
			return err
		}
	}

	// reset the actor system
	x.reset()

	return nil
}

// RemoteLookup for an actor on a remote host.
func (x *actorSystem) RemoteLookup(_ context.Context, request *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	// get a context log
	logger := x.logger

	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	// set the actor path with the remoting is enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	// get the remoting server address
	nodeAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)

	// let us validate the host and port
	hostAndPort := fmt.Sprintf("%s:%d", reqCopy.GetHost(), reqCopy.GetPort())
	if hostAndPort != nodeAddr {
		// log the error
		logger.Error(ErrInvalidNode.Message())
		// here message is sent to the wrong actor system node
		return nil, ErrInvalidNode
	}

	// construct the actor address
	name := reqCopy.GetName()
	actorPath := NewPath(name, NewAddress(x.Name(), reqCopy.GetHost(), int(reqCopy.GetPort())))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath)
	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	// let us construct the address
	addr := pid.ActorPath().RemoteAddress()

	return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: addr}), nil
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately. With this type of message the receiver cannot communicate back to Sender
// except reply the message with a response. This one-way communication
func (x *actorSystem) RemoteAsk(ctx context.Context, request *connect.Request[internalpb.RemoteAskRequest]) (*connect.Response[internalpb.RemoteAskResponse], error) {
	// get a context log
	logger := x.logger
	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	// set the actor path with the remoting is enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	// get the remoting server address
	nodeAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)

	// let us validate the host and port
	hostAndPort := fmt.Sprintf("%s:%d", reqCopy.GetRemoteMessage().GetReceiver().GetHost(), reqCopy.GetRemoteMessage().GetReceiver().GetPort())
	if hostAndPort != nodeAddr {
		// log the error
		logger.Error(ErrInvalidNode.Message())
		// here message is sent to the wrong actor system node
		return nil, ErrInvalidNode
	}

	// construct the actor address
	name := reqCopy.GetRemoteMessage().GetReceiver().GetName()
	actorPath := NewPath(name, NewAddress(x.name, x.remotingHost, int(x.remotingPort)))

	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath)
	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	// send the message to actor
	reply, err := x.handleRemoteAsk(ctx, pid, reqCopy.GetRemoteMessage())
	// handle the error
	if err != nil {
		logger.Error(ErrRemoteSendFailure(err).Error())
		return nil, ErrRemoteSendFailure(err)
	}
	// let us marshal the reply
	marshaled, _ := anypb.New(reply)
	return connect.NewResponse(&internalpb.RemoteAskResponse{Message: marshaled}), nil
}

// RemoteTell is used to send a message to an actor remotely by another actor
// This is the only way remote actors can interact with each other. The actor on the
// other line can reply to the sender by using the Sender in the message
func (x *actorSystem) RemoteTell(ctx context.Context, request *connect.Request[internalpb.RemoteTellRequest]) (*connect.Response[internalpb.RemoteTellResponse], error) {
	// get a context log
	logger := x.logger
	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	receiver := reqCopy.GetRemoteMessage().GetReceiver()

	// set the actor path with the remoting is enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	// get the remoting server address
	nodeAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)

	// let us validate the host and port
	hostAndPort := fmt.Sprintf("%s:%d", receiver.GetHost(), receiver.GetPort())
	if hostAndPort != nodeAddr {
		// log the error
		logger.Error(ErrInvalidNode.Message())
		// here message is sent to the wrong actor system node
		return nil, ErrInvalidNode
	}

	// construct the actor address
	actorPath := NewPath(
		receiver.GetName(),
		NewAddress(
			x.Name(),
			receiver.GetHost(),
			int(receiver.GetPort())))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath)
	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	// send the message to actor
	if err := x.handleRemoteTell(ctx, pid, reqCopy.GetRemoteMessage()); err != nil {
		logger.Error(ErrRemoteSendFailure(err))
		return nil, ErrRemoteSendFailure(err)
	}
	return connect.NewResponse(new(internalpb.RemoteTellResponse)), nil
}

// handleRemoteAsk handles a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (x *actorSystem) handleRemoteAsk(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
	return Ask(ctx, to, message, x.replyTimeout)
}

// handleRemoteTell handles an asynchronous message to an actor
func (x *actorSystem) handleRemoteTell(ctx context.Context, to PID, message proto.Message) error {
	return Tell(ctx, to, message)
}

// enableClustering enables clustering. When clustering is enabled remoting is also enabled to facilitate remote
// communication
func (x *actorSystem) enableClustering(ctx context.Context) {
	// add some logging information
	x.logger.Info("enabling clustering...")

	// create an instance of the cluster service and start it
	cluster, err := cluster.New(x.Name(),
		x.serviceDiscovery,
		cluster.WithLogger(x.logger),
		cluster.WithPartitionsCount(x.partitionsCount),
	)
	// handle the error
	if err != nil {
		x.logger.Panic(errors.Wrap(err, "failed to initialize cluster engine"))
	}

	// add some logging information
	x.logger.Info("starting cluster engine...")
	// start the cluster service
	if err := cluster.Start(ctx); err != nil {
		x.logger.Panic(errors.Wrap(err, "failed to start cluster engine"))
	}

	// create the bootstrap channel
	bootstrapChan := make(chan struct{}, 1)
	// let us wait for some time for the cluster to be properly started
	timer := time.AfterFunc(time.Second, func() {
		bootstrapChan <- struct{}{}
	})
	<-bootstrapChan
	timer.Stop()

	// add some logging information
	x.logger.Info("cluster engine successfully started...")
	// acquire the lock
	x.sem.Lock()
	// set the cluster field
	x.cluster = cluster
	// set the remoting host and port
	x.remotingHost = cluster.NodeHost()
	x.remotingPort = int32(cluster.NodeRemotingPort())
	// release the lock
	x.sem.Unlock()
	// start broadcasting cluster message
	go x.broadcast(ctx)
	// add some logging
	x.logger.Info("clustering enabled...:)")
}

// enableRemoting enables the remoting service to handle remote messaging
func (x *actorSystem) enableRemoting(ctx context.Context) {
	// add some logging information
	x.logger.Info("enabling remoting...")
	// create a function to handle the observability
	interceptor := func(tp trace.TracerProvider, mp metric.MeterProvider) connect.Interceptor {
		return otelconnect.NewInterceptor(
			otelconnect.WithTracerProvider(tp),
			otelconnect.WithMeterProvider(mp),
		)
	}

	// create a http service mux
	mux := http.NewServeMux()
	// create the resource and handler
	path, handler := internalpbconnect.NewRemotingServiceHandler(
		x,
		connect.WithInterceptors(interceptor(x.telemetry.TracerProvider, x.telemetry.MeterProvider)),
	)
	mux.Handle(path, handler)
	// create the address
	serverAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)

	// create a http service instance
	// TODO revisit the timeouts
	// reference: https://adam-p.ca/blog/2022/01/golang-http-server-timeouts/
	server := &http.Server{
		Addr: serverAddr,
		// The maximum duration for reading the entire request, including the body.
		// It’s implemented in net/http by calling SetReadDeadline immediately after Accept
		// ReadTimeout := handler_timeout + ReadHeaderTimeout + wiggle_room
		ReadTimeout: 3 * time.Second,
		// ReadHeaderTimeout is the amount of time allowed to read request headers
		ReadHeaderTimeout: time.Second,
		// WriteTimeout is the maximum duration before timing out writes of the response.
		// It is reset whenever a new request’s header is read.
		// This effectively covers the lifetime of the ServeHTTP handler stack
		WriteTimeout: time.Second,
		// IdleTimeout is the maximum amount of time to wait for the next request when keep-alive are enabled.
		// If IdleTimeout is zero, the value of ReadTimeout is used. Not relevant to request timeouts
		IdleTimeout: 1200 * time.Second,
		// For gRPC clients, it's convenient to support HTTP/2 without TLS. You can
		// avoid x/net/http2 by using http.ListenAndServeTLS.
		Handler: h2c.NewHandler(mux, &http2.Server{
			IdleTimeout: 1200 * time.Second,
		}),
		// Set the base context to incoming context of the system
		BaseContext: func(listener net.Listener) context.Context {
			return ctx
		},
	}

	// listen and service requests
	go func() {
		if err := server.ListenAndServe(); err != nil {
			// check the error type
			if !errors.Is(err, http.ErrServerClosed) {
				x.logger.Panic(errors.Wrap(err, "failed to start remoting service"))
			}
		}
	}()

	// acquire the lock
	x.sem.Lock()
	// set the server
	x.remotingServer = server
	// release the lock
	x.sem.Unlock()

	// add some logging information
	x.logger.Info("remoting enabled...:)")
}

// reset the actor system
func (x *actorSystem) reset() {
	x.telemetry = nil
	x.actors = newPIDMap(10)
	x.name = ""
}

// broadcast publishes newly created actor into the cluster when cluster is enabled
func (x *actorSystem) broadcast(ctx context.Context) {
	// iterate over the channel
	for wireActor := range x.clusterChan {
		// making sure the cluster is still enabled
		if x.cluster != nil {
			// broadcast the message on the cluster
			if err := x.cluster.PutActor(ctx, wireActor); err != nil {
				x.logger.Error(err.Error())
				// TODO: stop or continue
				return
			}
		}
	}
}

// housekeeper time to time removes dead actors from the system
// that helps free non-utilized resources
func (x *actorSystem) housekeeper() {
	// add some logging
	x.logger.Info("Housekeeping has started...")
	// create the ticker
	ticker := time.NewTicker(30 * time.Millisecond)
	// create the stop ticker signal
	tickerStopSig := make(chan Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				// loop over the actors in the system and remove the dead one
				for _, actor := range x.Actors() {
					if !actor.IsRunning() {
						x.logger.Infof("Removing actor=%s from system", actor.ActorPath().String())
						x.actors.Delete(actor.ActorPath())
					}
				}
			case <-x.housekeeperStopSig:
				// set the done channel to stop the ticker
				tickerStopSig <- Unit{}
				return
			}
		}
	}()
	// wait for the stop signal to stop the ticker
	<-tickerStopSig
	// stop the ticker
	ticker.Stop()
	// add some logging
	x.logger.Info("Housekeeping has stopped...")
}
