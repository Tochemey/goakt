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

package actor

import (
	"context"
	"errors"
	"fmt"
	"net"
	stdhttp "net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/google/uuid"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/hash"
	"github.com/tochemey/goakt/v2/internal/cluster"
	"github.com/tochemey/goakt/v2/internal/eventstream"
	"github.com/tochemey/goakt/v2/internal/http"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v2/internal/metric"
	"github.com/tochemey/goakt/v2/internal/tcp"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/telemetry"
)

// System defines the contract of an actor system
type System interface {
	// Name returns the actor system name
	Name() string
	// Actors returns the list of Actors that are alive in the actor system
	Actors() []*PID
	// Start starts the actor system
	Start(ctx context.Context) error
	// Stop stops the actor system
	Stop(ctx context.Context) error
	// Spawn creates an actor in the system and starts it
	Spawn(ctx context.Context, name string, actor Actor) (*PID, error)
	// SpawnFromFunc creates an actor with the given receive function. One can set the PreStart and PostStop lifecycle hooks
	// in the given optional options
	SpawnFromFunc(ctx context.Context, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error)
	// SpawnNamedFromFunc creates an actor with the given receive function and provided name. One can set the PreStart and PostStop lifecycle hooks
	// in the given optional options
	SpawnNamedFromFunc(ctx context.Context, name string, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error)
	// SpawnRouter creates a new router. One can additionally set the router options.
	// A router is a special type of actor that helps distribute messages of the same type over a set of actors, so that messages can be processed in parallel.
	// A single actor will only process one message at a time.
	SpawnRouter(ctx context.Context, poolSize int, routeesKind Actor, opts ...RouterOption) (*PID, error)
	// Kill stops a given actor in the system
	Kill(ctx context.Context, name string) error
	// ReSpawn recreates a given actor in the system
	ReSpawn(ctx context.Context, name string) (*PID, error)
	// ActorsCount returns the total number of active actors in the system
	ActorsCount() uint64
	// InCluster states whether the actor system is running within a cluster of nodes
	InCluster() bool
	// GetPartition returns the partition where a given actor is located
	GetPartition(actorName string) uint64
	// Subscribe creates an event subscriber.
	Subscribe() (eventstream.Subscriber, error)
	// Unsubscribe unsubscribes a subscriber.
	Unsubscribe(subscriber eventstream.Subscriber) error
	// ScheduleOnce schedules a message that will be delivered to the receiver actor
	// This will send the given message to the actor after the given interval specified.
	// The message will be sent once
	ScheduleOnce(ctx context.Context, message proto.Message, actorName string, interval time.Duration) error
	// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
	ScheduleWithCron(ctx context.Context, message proto.Message, actorName string, cronExpression string) error
	// PeerAddress returns the actor system address known in the cluster. That address is used by other nodes to communicate with the actor system.
	// This address is empty when cluster mode is not activated
	PeerAddress() string
	// Register registers an actor for future use. This is necessary when creating an actor remotely
	Register(ctx context.Context, actor Actor) error
	// Deregister removes a registered actor from the registry
	Deregister(ctx context.Context, actor Actor) error
	// Logger returns the logger sets when creating the actor system
	Logger() log.Logger
	// handleRemoteAsk handles a synchronous message to another actor and expect a response.
	// This block until a response is received or timed out.
	handleRemoteAsk(ctx context.Context, to *PID, message proto.Message, timeout time.Duration) (response proto.Message, err error)
	// handleRemoteTell handles an asynchronous message to an actor
	handleRemoteTell(ctx context.Context, to *PID, message proto.Message) error
	// setActor sets actor in the actor system actors registry
	setActor(actor *PID)
	// supervisor return the system supervisor
	getSupervisor() *PID
	// actorOf returns an existing actor in the local system or in the cluster when clustering is enabled
	// When cluster mode is activated, the PID will be nil.
	// When remoting is enabled this method will return and error
	// An actor not found error is return when the actor is not found.
	actorOf(ctx context.Context, actorName string) (addr *goaktpb.Address, pid *PID, err error)
}

// System represent a collection of actors on a given node
// Only a single instance of the System can be created on a given node
type system struct {
	internalpbconnect.UnimplementedRemotingServiceHandler
	internalpbconnect.UnimplementedClusterServiceHandler

	// map of actors in the system
	actors *pidMap

	// states whether the actor system has started or not
	started atomic.Bool

	// Specifies the actor system name
	name string
	// Specifies the logger to use in the system
	logger log.Logger
	// Specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 5s
	expireActorAfter time.Duration
	// Specifies how long the sender of a message should wait to receive a reply
	// when using SendReply. The default value is 5s
	askTimeout time.Duration
	// Specifies the shutdown timeout. The default value is 30s
	shutdownTimeout time.Duration
	// Specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	actorInitMaxRetries int
	// Specifies the actors initialization timeout
	// The default value is 1s
	actorInitTimeout time.Duration
	// Specifies the supervisor strategy
	supervisorDirective SupervisorDirective
	// Specifies the telemetry config
	telemetry *telemetry.Telemetry
	// Specifies the remoting host
	host string
	// Specifies the remoting port
	remotingPort int32
	// Specifies the remoting server
	remotingServer *stdhttp.Server

	// cluster settings
	clusterEnabled atomic.Bool
	// cluster mode
	cluster            cluster.Interface
	actorsChan         chan *internalpb.WireActor
	clusterEventsChan  <-chan *cluster.Event
	clusterSyncStopSig chan types.Unit
	partitionHasher    hash.Hasher

	// help protect some the fields to set
	locker sync.Mutex
	// specifies the stash capacity
	stashEnabled bool

	stopGC     chan types.Unit
	gcInterval time.Duration

	// specifies the events stream
	eventsStream *eventstream.EventsStream

	// specifies the message scheduler
	scheduler *scheduler

	// specifies whether metrics is enabled
	metricEnabled atomic.Bool

	registry   types.Registry
	reflection *reflection

	peersStateLoopInterval time.Duration
	peersCacheMu           *sync.RWMutex
	peersCache             map[string][]byte
	clusterConfig          *ClusterConfig
	redistributionChan     chan *cluster.Event

	supervisor *PID
}

// enforce compilation error when all methods of the System interface are not implemented
// by the struct system
var _ System = (*system)(nil)

// NewSystem creates an instance of System
func NewSystem(name string, opts ...Option) (System, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	if match, _ := regexp.MatchString("^[a-zA-Z0-9][a-zA-Z0-9-_]*$", name); !match {
		return nil, ErrInvalidActorSystemName
	}

	system := &system{
		actors:                 newPIDMap(1_000),
		actorsChan:             make(chan *internalpb.WireActor, 10),
		name:                   name,
		logger:                 log.DefaultLogger,
		expireActorAfter:       DefaultPassivationTimeout,
		askTimeout:             DefaultAskTimeout,
		actorInitMaxRetries:    DefaultInitMaxRetries,
		supervisorDirective:    DefaultSupervisoryStrategy,
		telemetry:              telemetry.New(),
		locker:                 sync.Mutex{},
		shutdownTimeout:        DefaultShutdownTimeout,
		stashEnabled:           false,
		stopGC:                 make(chan types.Unit, 1),
		gcInterval:             DefaultGCInterval,
		eventsStream:           eventstream.New(),
		partitionHasher:        hash.DefaultHasher(),
		actorInitTimeout:       DefaultInitTimeout,
		clusterEventsChan:      make(chan *cluster.Event, 1),
		registry:               types.NewRegistry(),
		clusterSyncStopSig:     make(chan types.Unit, 1),
		peersCacheMu:           &sync.RWMutex{},
		peersCache:             make(map[string][]byte),
		peersStateLoopInterval: DefaultPeerStateLoopInterval,
		host:                   "",
		remotingPort:           -1,
	}

	system.started.Store(false)
	system.clusterEnabled.Store(false)
	system.metricEnabled.Store(false)

	system.reflection = newReflection(system.registry)

	// apply the various options
	for _, opt := range opts {
		opt.Apply(system)
	}

	// we need to make sure the cluster kinds are defined
	if system.clusterEnabled.Load() {
		if err := system.clusterConfig.Validate(); err != nil {
			return nil, err
		}
	}

	system.scheduler = newScheduler(system.logger, system.shutdownTimeout, withSchedulerCluster(system.cluster))
	if system.metricEnabled.Load() {
		if err := system.registerMetrics(); err != nil {
			return nil, err
		}
	}

	return system, nil
}

// Logger returns the logger sets when creating the actor system
func (sys *system) Logger() log.Logger {
	sys.locker.Lock()
	logger := sys.logger
	sys.locker.Unlock()
	return logger
}

// Deregister removes a registered actor from the registry
func (sys *system) Deregister(_ context.Context, actor Actor) error {
	sys.locker.Lock()
	defer sys.locker.Unlock()

	if !sys.started.Load() {
		return ErrActorSystemNotStarted
	}

	sys.registry.Deregister(actor)
	return nil
}

// Register registers an actor for future use. This is necessary when creating an actor remotely
func (sys *system) Register(_ context.Context, actor Actor) error {
	sys.locker.Lock()
	defer sys.locker.Unlock()

	if !sys.started.Load() {
		return ErrActorSystemNotStarted
	}

	sys.registry.Register(actor)
	return nil
}

// ScheduleOnce schedules a message that will be delivered to the receiver actor
// This will send the given message to the actor after the given interval specified.
// The message will be sent once
func (sys *system) ScheduleOnce(ctx context.Context, message proto.Message, actorName string, interval time.Duration) error {
	addr, pid, err := sys.actorOf(ctx, actorName)
	if err != nil {
		return err
	}

	if pid != nil {
		return sys.scheduler.scheduleOnce(ctx, message, pid, interval)
	}

	if addr != nil {
		return sys.scheduler.remoteScheduleOnce(ctx, message, addr, interval)
	}

	return ErrActorNotFound(actorName)
}

// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (sys *system) ScheduleWithCron(ctx context.Context, message proto.Message, actorName string, cronExpression string) error {
	addr, pid, err := sys.actorOf(ctx, actorName)
	if err != nil {
		return err
	}

	if pid != nil {
		return sys.scheduler.scheduleWithCron(ctx, message, pid, cronExpression)
	}

	if addr != nil {
		return sys.scheduler.remoteScheduleWithCron(ctx, message, addr, cronExpression)
	}

	return ErrActorNotFound(actorName)
}

// Subscribe help receive dead letters whenever there are available
func (sys *system) Subscribe() (eventstream.Subscriber, error) {
	if !sys.started.Load() {
		return nil, ErrActorSystemNotStarted
	}
	subscriber := sys.eventsStream.AddSubscriber()
	sys.eventsStream.Subscribe(subscriber, eventsTopic)
	return subscriber, nil
}

// Unsubscribe unsubscribes a subscriber.
func (sys *system) Unsubscribe(subscriber eventstream.Subscriber) error {
	if !sys.started.Load() {
		return ErrActorSystemNotStarted
	}
	sys.eventsStream.Unsubscribe(subscriber, eventsTopic)
	sys.eventsStream.RemoveSubscriber(subscriber)
	return nil
}

// GetPartition returns the partition where a given actor is located
func (sys *system) GetPartition(actorName string) uint64 {
	if !sys.InCluster() {
		// TODO: maybe add a partitioner function
		return 0
	}
	return uint64(sys.cluster.GetPartition(actorName))
}

// InCluster states whether the actor system is running within a cluster of nodes
func (sys *system) InCluster() bool {
	return sys.clusterEnabled.Load() && sys.cluster != nil
}

// ActorsCount returns the total number of active actors in the system
func (sys *system) ActorsCount() uint64 {
	return uint64(len(sys.Actors()))
}

// Spawn creates or returns the instance of a given actor in the system
func (sys *system) Spawn(ctx context.Context, name string, actor Actor) (*PID, error) {
	if !sys.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	pid, exist := sys.actors.get(sys.buildActorPath(name))
	if exist {
		if pid.IsRunning() {
			// return the existing instance
			return pid, nil
		}
	}

	pid, err := sys.configPID(ctx, name, actor)
	if err != nil {
		return nil, err
	}

	sys.supervisor.Watch(pid)
	sys.setActor(pid)
	return pid, nil
}

// SpawnNamedFromFunc creates an actor with the given receive function and provided name. One can set the PreStart and PostStop lifecycle hooks
// in the given optional options
func (sys *system) SpawnNamedFromFunc(ctx context.Context, name string, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error) {
	if !sys.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	actor := newFuncActor(name, receiveFunc, opts...)
	pid, err := sys.configPID(ctx, name, actor)
	if err != nil {
		return nil, err
	}

	sys.supervisor.Watch(pid)
	sys.setActor(pid)
	return pid, nil
}

// SpawnFromFunc creates an actor with the given receive function.
func (sys *system) SpawnFromFunc(ctx context.Context, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error) {
	return sys.SpawnNamedFromFunc(ctx, uuid.NewString(), receiveFunc, opts...)
}

// SpawnRouter creates a new router. One can additionally set the router options.
// A router is a special type of actor that helps distribute messages of the same type over a set of actors, so that messages can be processed in parallel.
// A single actor will only process one message at a time.
func (sys *system) SpawnRouter(ctx context.Context, poolSize int, routeesKind Actor, opts ...RouterOption) (*PID, error) {
	router := newRouter(poolSize, routeesKind, sys.logger, opts...)
	routerName := sys.getSystemActorName(routerType)
	return sys.Spawn(ctx, routerName, router)
}

// Kill stops a given actor in the system
func (sys *system) Kill(ctx context.Context, name string) error {
	if !sys.started.Load() {
		return ErrActorSystemNotStarted
	}

	actorPath := sys.buildActorPath(name)
	pid, exist := sys.actors.get(actorPath)
	if exist {
		// stop the given actor. No need to record error in the span context
		// because the shutdown method is taking care of that
		return pid.Shutdown(ctx)
	}

	return ErrActorNotFound(actorPath.String())
}

// ReSpawn recreates a given actor in the system
func (sys *system) ReSpawn(ctx context.Context, name string) (*PID, error) {
	if !sys.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	actorPath := sys.buildActorPath(name)
	pid, exist := sys.actors.get(actorPath)
	if exist {
		if err := pid.Restart(ctx); err != nil {
			return nil, fmt.Errorf("failed to restart actor=%s: %w", actorPath.String(), err)
		}

		sys.actors.set(pid)
		sys.supervisor.Watch(pid)
		return pid, nil
	}

	return nil, ErrActorNotFound(actorPath.String())
}

// Name returns the actor system name
func (sys *system) Name() string {
	sys.locker.Lock()
	defer sys.locker.Unlock()
	return sys.name
}

// Actors returns the list of Actors that are alive in the actor system
func (sys *system) Actors() []*PID {
	sys.locker.Lock()
	pids := sys.actors.pids()
	sys.locker.Unlock()
	actors := make([]*PID, 0, len(pids))
	for _, pid := range pids {
		if !isSystemName(pid.Name()) {
			actors = append(actors, pid)
		}
	}
	return actors
}

// PeerAddress returns the actor system address known in the cluster. That address is used by other nodes to communicate with the actor system.
// This address is empty when cluster mode is not activated
func (sys *system) PeerAddress() string {
	sys.locker.Lock()
	defer sys.locker.Unlock()
	if sys.clusterEnabled.Load() {
		return sys.cluster.AdvertisedAddress()
	}
	return ""
}

// Start starts the actor system
func (sys *system) Start(ctx context.Context) error {
	sys.started.Store(true)

	if sys.clusterEnabled.Load() {
		sys.enableRemoting(ctx)
		if err := sys.enableClustering(ctx); err != nil {
			return err
		}
	}

	sys.scheduler.start(ctx)
	actorName := sys.getSystemActorName(supervisorType)
	pid, err := sys.configPID(ctx, actorName, newRootActor(sys.logger))
	if err != nil {
		return fmt.Errorf("actor=%s failed to start system supervisor: %w", actorName, err)
	}

	sys.supervisor = pid
	sys.setActor(pid)

	go sys.gc()

	sys.logger.Infof("%s started..:)", sys.name)
	return nil
}

// Stop stops the actor system
func (sys *system) Stop(ctx context.Context) error {
	sys.logger.Infof("%s shutting down...", sys.name)

	// make sure the actor system has started
	if !sys.started.Load() {
		return ErrActorSystemNotStarted
	}

	sys.stopGC <- types.Unit{}
	sys.logger.Infof("%s is shutting down..:)", sys.name)

	sys.started.Store(false)
	sys.scheduler.stop(ctx)

	if sys.eventsStream != nil {
		sys.eventsStream.Shutdown()
	}

	ctx, cancel := context.WithTimeout(ctx, sys.shutdownTimeout)
	defer cancel()

	if sys.clusterEnabled.Load() {
		if err := sys.remotingServer.Shutdown(ctx); err != nil {
			return err
		}
		sys.remotingServer = nil
	}

	if sys.clusterEnabled.Load() {
		if err := sys.cluster.Stop(ctx); err != nil {
			return err
		}
		close(sys.actorsChan)
		sys.clusterSyncStopSig <- types.Unit{}
		sys.clusterEnabled.Store(false)
		close(sys.redistributionChan)
	}

	// stop the supervisor actor
	if err := sys.getSupervisor().Shutdown(ctx); err != nil {
		sys.reset()
		return err
	}
	// remove the supervisor from the actors list
	sys.actors.delete(sys.supervisor.ActorPath())

	for _, actor := range sys.Actors() {
		sys.actors.delete(actor.ActorPath())
		if err := actor.Shutdown(ctx); err != nil {
			sys.reset()
			return err
		}
	}

	sys.reset()
	sys.logger.Infof("%s shuts down successfully", sys.name)
	return nil
}

// RemoteLookup for an actor on a remote host.
func (sys *system) RemoteLookup(ctx context.Context, request *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	logger := sys.logger
	msg := request.Msg

	if !sys.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", sys.host, sys.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	if sys.clusterEnabled.Load() {
		actorName := msg.GetName()
		actor, err := sys.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				logger.Error(ErrAddressNotFound(actorName).Error())
				return nil, ErrAddressNotFound(actorName)
			}

			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: actor.GetActorAddress()}), nil
	}

	actorPath := NewPath(msg.GetName(), NewAddress(sys.Name(), msg.GetHost(), int(msg.GetPort())))
	pid, exist := sys.actors.get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: pid.ActorPath().RemoteAddress()}), nil
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately. With this type of message the receiver cannot communicate back to Sender
// except reply the message with a response. This one-way communication
func (sys *system) RemoteAsk(ctx context.Context, stream *connect.BidiStream[internalpb.RemoteAskRequest, internalpb.RemoteAskResponse]) error {
	logger := sys.logger

	if !sys.clusterEnabled.Load() {
		return connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	for {
		switch err := ctx.Err(); {
		case errors.Is(err, context.Canceled):
			return connect.NewError(connect.CodeCanceled, err)
		case errors.Is(err, context.DeadlineExceeded):
			return connect.NewError(connect.CodeDeadlineExceeded, err)
		}

		request, err := stream.Receive()
		if eof(err) {
			return nil
		}

		if err != nil {
			logger.Error(err)
			return connect.NewError(connect.CodeUnknown, err)
		}

		message := request.GetRemoteMessage()
		receiver := message.GetReceiver()
		name := receiver.GetName()

		remoteAddr := fmt.Sprintf("%s:%d", sys.host, sys.remotingPort)
		if remoteAddr != net.JoinHostPort(receiver.GetHost(), strconv.Itoa(int(receiver.GetPort()))) {
			return connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
		}

		actorPath := NewPath(name, NewAddress(sys.name, sys.host, int(sys.remotingPort)))
		pid, exist := sys.actors.get(actorPath)
		if !exist {
			logger.Error(ErrAddressNotFound(actorPath.String()).Error())
			return ErrAddressNotFound(actorPath.String())
		}

		timeout := sys.askTimeout
		if request.GetTimeout() != nil {
			timeout = request.GetTimeout().AsDuration()
		}

		reply, err := sys.handleRemoteAsk(ctx, pid, message, timeout)
		if err != nil {
			logger.Error(ErrRemoteSendFailure(err).Error())
			return ErrRemoteSendFailure(err)
		}

		marshaled, _ := anypb.New(reply)
		response := &internalpb.RemoteAskResponse{Message: marshaled}
		if err := stream.Send(response); err != nil {
			return connect.NewError(connect.CodeUnknown, err)
		}
	}
}

// RemoteTell is used to send a message to an actor remotely by another actor
func (sys *system) RemoteTell(ctx context.Context, stream *connect.ClientStream[internalpb.RemoteTellRequest]) (*connect.Response[internalpb.RemoteTellResponse], error) {
	logger := sys.logger

	if !sys.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	requestc := make(chan *internalpb.RemoteTellRequest, 1)
	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(2)

	eg.Go(func() error {
		defer close(requestc)
		for stream.Receive() {
			select {
			case requestc <- stream.Msg():
			case <-ctx.Done():
				logger.Error(ctx.Err())
				return connect.NewError(connect.CodeCanceled, ctx.Err())
			}
		}

		if err := stream.Err(); err != nil {
			logger.Error(err)
			return connect.NewError(connect.CodeUnknown, err)
		}

		return nil
	})

	eg.Go(func() error {
		for request := range requestc {
			receiver := request.GetRemoteMessage().GetReceiver()
			actorPath := NewPath(
				receiver.GetName(),
				NewAddress(
					sys.Name(),
					receiver.GetHost(),
					int(receiver.GetPort())))

			pid, exist := sys.actors.get(actorPath)
			if !exist {
				logger.Error(ErrAddressNotFound(actorPath.String()).Error())
				return ErrAddressNotFound(actorPath.String())
			}

			if err := sys.handleRemoteTell(ctx, pid, request.GetRemoteMessage()); err != nil {
				logger.Error(ErrRemoteSendFailure(err))
				return ErrRemoteSendFailure(err)
			}
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return connect.NewResponse(new(internalpb.RemoteTellResponse)), nil
}

// RemoteReSpawn is used the handle the re-creation of an actor from a remote host or from an api call
func (sys *system) RemoteReSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteReSpawnRequest]) (*connect.Response[internalpb.RemoteReSpawnResponse], error) {
	logger := sys.logger

	msg := request.Msg

	if !sys.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", sys.host, sys.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorPath := NewPath(msg.GetName(), NewAddress(sys.Name(), msg.GetHost(), int(msg.GetPort())))
	pid, exist := sys.actors.get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	if err := pid.Restart(ctx); err != nil {
		return nil, fmt.Errorf("failed to restart actor=%s: %w", actorPath.String(), err)
	}

	sys.actors.set(pid)
	return connect.NewResponse(new(internalpb.RemoteReSpawnResponse)), nil
}

// RemoteStop stops an actor on a remote machine
func (sys *system) RemoteStop(ctx context.Context, request *connect.Request[internalpb.RemoteStopRequest]) (*connect.Response[internalpb.RemoteStopResponse], error) {
	logger := sys.logger

	msg := request.Msg

	if !sys.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", sys.host, sys.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorPath := NewPath(msg.GetName(), NewAddress(sys.Name(), msg.GetHost(), int(msg.GetPort())))
	pid, exist := sys.actors.get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	if err := pid.Shutdown(ctx); err != nil {
		return nil, fmt.Errorf("failed to stop actor=%s: %w", actorPath.String(), err)
	}

	sys.actors.delete(actorPath)
	return connect.NewResponse(new(internalpb.RemoteStopResponse)), nil
}

// RemoteSpawn handles the remoteSpawn call
func (sys *system) RemoteSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteSpawnRequest]) (*connect.Response[internalpb.RemoteSpawnResponse], error) {
	logger := sys.logger

	msg := request.Msg
	if !sys.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", sys.host, sys.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actor, err := sys.reflection.ActorFrom(msg.GetActorType())
	if err != nil {
		logger.Errorf("failed to create actor=[(%s) of type (%s)] on [host=%s, port=%d]: reason: (%v)",
			msg.GetActorName(), msg.GetActorType(), msg.GetHost(), msg.GetPort(), err)

		if errors.Is(err, ErrTypeNotRegistered) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, ErrTypeNotRegistered)
		}

		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if _, err = sys.Spawn(ctx, msg.GetActorName(), actor); err != nil {
		logger.Errorf("failed to create actor=(%s) on [host=%s, port=%d]: reason: (%v)", msg.GetActorName(), msg.GetHost(), msg.GetPort(), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	logger.Infof("actor=(%s) successfully created on [host=%s, port=%d]", msg.GetActorName(), msg.GetHost(), msg.GetPort())
	return connect.NewResponse(new(internalpb.RemoteSpawnResponse)), nil
}

// GetNodeMetric handles the GetNodeMetric request send the given node
func (sys *system) GetNodeMetric(_ context.Context, request *connect.Request[internalpb.GetNodeMetricRequest]) (*connect.Response[internalpb.GetNodeMetricResponse], error) {
	if !sys.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrClusterDisabled)
	}

	req := request.Msg

	remoteAddr := fmt.Sprintf("%s:%d", sys.host, sys.remotingPort)
	if remoteAddr != req.GetNodeAddress() {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorCount := sys.actors.len()
	return connect.NewResponse(&internalpb.GetNodeMetricResponse{
		NodeRemoteAddress: remoteAddr,
		ActorsCount:       uint64(actorCount),
	}), nil
}

// GetKinds returns the cluster kinds
func (sys *system) GetKinds(_ context.Context, request *connect.Request[internalpb.GetKindsRequest]) (*connect.Response[internalpb.GetKindsResponse], error) {
	if !sys.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrClusterDisabled)
	}

	req := request.Msg
	remoteAddr := fmt.Sprintf("%s:%d", sys.host, sys.remotingPort)

	// routine check
	if remoteAddr != req.GetNodeAddress() {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	kinds := make([]string, len(sys.clusterConfig.Kinds()))
	for i, kind := range sys.clusterConfig.Kinds() {
		kinds[i] = types.TypeName(kind)
	}

	return connect.NewResponse(&internalpb.GetKindsResponse{Kinds: kinds}), nil
}

// handleRemoteAsk handles a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (sys *system) handleRemoteAsk(ctx context.Context, to *PID, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	return Ask(ctx, to, message, timeout)
}

// handleRemoteTell handles an asynchronous message to an actor
func (sys *system) handleRemoteTell(ctx context.Context, to *PID, message proto.Message) error {
	return Tell(ctx, to, message)
}

// getSupervisor return the system supervisor
func (sys *system) getSupervisor() *PID {
	sys.locker.Lock()
	supervisor := sys.supervisor
	sys.locker.Unlock()
	return supervisor
}

// setActor implements System.
func (sys *system) setActor(actor *PID) {
	sys.actors.set(actor)
	if sys.clusterEnabled.Load() {
		sys.actorsChan <- &internalpb.WireActor{
			ActorName:    actor.Name(),
			ActorAddress: actor.ActorPath().RemoteAddress(),
			ActorPath:    actor.ActorPath().String(),
			ActorType:    types.TypeName(actor.Actor()),
		}
	}
}

// enableClustering enables clustering. When clustering is enabled remoting is also enabled to facilitate remote
// communication
func (sys *system) enableClustering(ctx context.Context) error {
	sys.logger.Info("enabling clustering...")

	node := &discovery.Node{
		Name:         sys.Name(),
		Host:         sys.host,
		GossipPort:   sys.clusterConfig.GossipPort(),
		PeersPort:    sys.clusterConfig.PeersPort(),
		RemotingPort: sys.clusterConfig.RemotingPort(),
	}

	clusterEngine, err := cluster.NewEngine(
		sys.Name(),
		sys.clusterConfig.Discovery(),
		node,
		cluster.WithLogger(sys.logger),
		cluster.WithPartitionsCount(sys.clusterConfig.PartitionCount()),
		cluster.WithHasher(sys.partitionHasher),
		cluster.WithMinimumPeersQuorum(sys.clusterConfig.MinimumPeersQuorum()),
		cluster.WithReplicaCount(sys.clusterConfig.ReplicaCount()),
	)
	if err != nil {
		sys.logger.Errorf("failed to initialize cluster engine: %v", err)
		return err
	}

	sys.logger.Info("starting cluster engine...")
	if err := clusterEngine.Start(ctx); err != nil {
		sys.logger.Errorf("failed to start cluster engine: %v", err)
		return err
	}

	bootstrapChan := make(chan struct{}, 1)
	timer := time.AfterFunc(time.Second, func() {
		bootstrapChan <- struct{}{}
	})
	<-bootstrapChan
	timer.Stop()

	sys.logger.Info("cluster engine successfully started...")

	sys.locker.Lock()
	sys.cluster = clusterEngine
	sys.clusterEventsChan = clusterEngine.Events()
	sys.redistributionChan = make(chan *cluster.Event, 1)
	for _, kind := range sys.clusterConfig.Kinds() {
		sys.registry.Register(kind)
		sys.logger.Infof("cluster kind=(%s) registered", types.TypeName(kind))
	}
	sys.locker.Unlock()

	go sys.clusterEventsLoop()
	go sys.replicationLoop()
	go sys.peersStateLoop()
	go sys.redistributionLoop()

	sys.logger.Info("clustering enabled...:)")
	return nil
}

// enableRemoting enables the remoting service to handle remote messaging
func (sys *system) enableRemoting(ctx context.Context) {
	sys.logger.Info("enabling remoting...")

	var interceptor *otelconnect.Interceptor
	var err error
	if sys.metricEnabled.Load() {
		interceptor, err = otelconnect.NewInterceptor(
			otelconnect.WithMeterProvider(sys.telemetry.MeterProvider),
		)
		if err != nil {
			sys.logger.Panic(fmt.Errorf("failed to initialize observability feature: %w", err))
		}
	}

	var opts []connect.HandlerOption
	if interceptor != nil {
		opts = append(opts, connect.WithInterceptors(interceptor))
	}

	if sys.host == "" {
		sys.host, _ = os.Hostname()
	}

	remotingHost, remotingPort, err := tcp.GetHostPort(fmt.Sprintf("%s:%d", sys.host, sys.remotingPort))
	if err != nil {
		sys.logger.Panic(fmt.Errorf("failed to resolve remoting TCP address: %w", err))
	}

	sys.host = remotingHost
	sys.remotingPort = int32(remotingPort)

	remotingServicePath, remotingServiceHandler := internalpbconnect.NewRemotingServiceHandler(sys, opts...)
	clusterServicePath, clusterServiceHandler := internalpbconnect.NewClusterServiceHandler(sys, opts...)

	mux := stdhttp.NewServeMux()
	mux.Handle(remotingServicePath, remotingServiceHandler)
	mux.Handle(clusterServicePath, clusterServiceHandler)
	server := http.NewServer(ctx, sys.host, remotingPort, mux)

	go func() {
		if err := server.ListenAndServe(); err != nil {
			if !errors.Is(err, stdhttp.ErrServerClosed) {
				sys.logger.Panic(fmt.Errorf("failed to start remoting service: %w", err))
			}
		}
	}()

	sys.locker.Lock()
	sys.remotingServer = server
	sys.locker.Unlock()

	sys.logger.Info("remoting enabled...:)")
}

// reset the actor system
func (sys *system) reset() {
	sys.telemetry = nil
	sys.actors.reset()
	sys.name = ""
	sys.cluster = nil
}

// gc time to time removes dead actors from the system
// that helps free non-utilized resources
func (sys *system) gc() {
	sys.logger.Info("garbage collector has started...")
	ticker := time.NewTicker(sys.gcInterval)
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				for _, actor := range sys.Actors() {
					if !actor.IsRunning() {
						sys.logger.Infof("removing actor=%s from system", actor.ActorPath().Name())
						sys.actors.delete(actor.ActorPath())
						if sys.InCluster() {
							if err := sys.cluster.RemoveActor(context.Background(), actor.ActorPath().Name()); err != nil {
								sys.logger.Error(err.Error())
								// TODO: stop or continue
							}
						}
					}
				}
			case <-sys.stopGC:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()
	sys.logger.Info("garbage collector has stopped...")
}

// registerMetrics register the PID metrics with OTel instrumentation.
func (sys *system) registerMetrics() error {
	meter := sys.telemetry.Meter
	metrics, err := metric.NewActorSystemMetric(meter)
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		observer.ObserveInt64(metrics.ActorsCount(), int64(sys.ActorsCount()))
		return nil
	}, metrics.ActorsCount())

	return err
}

// replicationLoop publishes newly created actor into the cluster when cluster is enabled
func (sys *system) replicationLoop() {
	for actor := range sys.actorsChan {
		// never replicate system actors because there are specific to the
		// running node
		if isSystemName(actor.GetActorName()) {
			continue
		}
		if sys.InCluster() {
			ctx := context.Background()
			if err := sys.cluster.PutActor(ctx, actor); err != nil {
				sys.logger.Panic(err.Error())
			}
		}
	}
}

// clusterEventsLoop listens to cluster events and send them to the event streams
func (sys *system) clusterEventsLoop() {
	for event := range sys.clusterEventsChan {
		if sys.InCluster() {
			if event != nil && event.Payload != nil {
				// push the event to the event stream
				message, _ := event.Payload.UnmarshalNew()
				if sys.eventsStream != nil {
					sys.logger.Debugf("node=(%s) publishing cluster event=(%s)....", sys.name, event.Type)
					sys.eventsStream.Publish(eventsTopic, message)
					sys.logger.Debugf("cluster event=(%s) successfully published by node=(%s)", event.Type, sys.name)
				}
				sys.redistributionChan <- event
			}
		}
	}
}

// peersStateLoop fetches the cluster peers' PeerState and update the node peersCache
func (sys *system) peersStateLoop() {
	sys.logger.Info("peers state synchronization has started...")
	ticker := time.NewTicker(sys.peersStateLoopInterval)
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				eg, ctx := errgroup.WithContext(context.Background())

				peersChan := make(chan *cluster.Peer)

				eg.Go(func() error {
					defer close(peersChan)
					peers, err := sys.cluster.Peers(ctx)
					if err != nil {
						return err
					}

					for _, peer := range peers {
						select {
						case peersChan <- peer:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					return nil
				})

				eg.Go(func() error {
					for peer := range peersChan {
						if err := sys.processPeerState(ctx, peer); err != nil {
							return err
						}
						select {
						case <-ctx.Done():
							return ctx.Err()
						default:
							// pass
						}
					}
					return nil
				})

				if err := eg.Wait(); err != nil {
					sys.logger.Error(err)
					// TODO: stop or panic
				}

			case <-sys.clusterSyncStopSig:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()
	sys.logger.Info("peers state synchronization has stopped...")
}

func (sys *system) redistributionLoop() {
	for event := range sys.redistributionChan {
		if sys.InCluster() {
			// check for cluster rebalancing
			if err := sys.redistribute(context.Background(), event); err != nil {
				sys.logger.Errorf("cluster rebalancing failed: %v", err)
				// TODO: panic or retry or shutdown actor system
			}
		}
	}
}

// processPeerState processes a given peer synchronization record.
func (sys *system) processPeerState(ctx context.Context, peer *cluster.Peer) error {
	sys.peersCacheMu.Lock()
	defer sys.peersCacheMu.Unlock()

	peerAddress := net.JoinHostPort(peer.Host, strconv.Itoa(peer.Port))

	sys.logger.Infof("processing peer sync:(%s)", peerAddress)
	peerState, err := sys.cluster.GetState(ctx, peerAddress)
	if err != nil {
		if errors.Is(err, cluster.ErrPeerSyncNotFound) {
			return nil
		}
		sys.logger.Error(err)
		return err
	}

	sys.logger.Debugf("peer (%s) actors count (%d)", peerAddress, len(peerState.GetActors()))

	// no need to handle the error
	bytea, err := proto.Marshal(peerState)
	if err != nil {
		sys.logger.Error(err)
		return err
	}

	sys.peersCache[peerAddress] = bytea
	sys.logger.Infof("peer sync(%s) successfully processed", peerAddress)
	return nil
}

// configPID constructs a PID provided the actor name and the actor kind
// this is a utility function used when spawning actors
func (sys *system) configPID(ctx context.Context, name string, actor Actor) (*PID, error) {
	actorPath := NewPath(name, NewAddress(sys.name, "", -1))
	if sys.clusterEnabled.Load() {
		actorPath = NewPath(name, NewAddress(sys.name, sys.host, int(sys.remotingPort)))
	}

	// define the pid options
	// pid inherit the actor system settings defined during instantiation
	pidOpts := []pidOption{
		withInitMaxRetries(sys.actorInitMaxRetries),
		withAskTimeout(sys.askTimeout),
		withCustomLogger(sys.logger),
		withActorSystem(sys),
		withSupervisorDirective(sys.supervisorDirective),
		withEventsStream(sys.eventsStream),
		withInitTimeout(sys.actorInitTimeout),
		withTelemetry(sys.telemetry),
	}

	// enable stash
	if sys.stashEnabled {
		pidOpts = append(pidOpts, withStash())
	}

	// disable passivation for system supervisor
	if isSystemName(name) {
		pidOpts = append(pidOpts, withPassivationDisabled())
	} else {
		pidOpts = append(pidOpts, withPassivationAfter(sys.expireActorAfter))
	}

	if sys.metricEnabled.Load() {
		pidOpts = append(pidOpts, withMetric())
	}

	pid, err := newPID(ctx,
		actorPath,
		actor,
		pidOpts...)

	if err != nil {
		return nil, err
	}
	return pid, nil
}

// getSystemActorName returns the system supervisor name
func (sys *system) getSystemActorName(nameType nameType) string {
	if sys.clusterEnabled.Load() {
		return fmt.Sprintf("%s%s%s-%d-%d",
			systemNames[nameType],
			strings.ToTitle(sys.name),
			sys.host,
			sys.remotingPort,
			time.Now().UnixNano())
	}
	return fmt.Sprintf("%s%s-%d",
		systemNames[nameType],
		strings.ToTitle(sys.name),
		time.Now().UnixNano())
}

func isSystemName(name string) bool {
	return strings.HasPrefix(name, systemNamePrefix)
}

// ActorOf returns an existing actor in the local system or in the cluster when clustering is enabled
// When cluster mode is activated, the PID will be nil.
// An actor not found error is return when the actor is not found.
func (sys *system) actorOf(ctx context.Context, actorName string) (addr *goaktpb.Address, pid *PID, err error) {
	sys.locker.Lock()

	if !sys.started.Load() {
		sys.locker.Unlock()
		return nil, nil, ErrActorSystemNotStarted
	}

	actorPath := sys.buildActorPath(actorName)
	if pid, ok := sys.actors.get(actorPath); ok {
		sys.locker.Unlock()
		return pid.ActorPath().RemoteAddress(), pid, nil
	}

	// check in the cluster
	if sys.clusterEnabled.Load() {
		actor, err := sys.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				sys.logger.Infof("actor=%s not found", actorName)
				e := ErrActorNotFound(actorName)
				sys.locker.Unlock()
				return nil, nil, e
			}

			sys.locker.Unlock()
			return nil, nil, fmt.Errorf("failed to fetch remote actor=%s: %w", actorName, err)
		}

		sys.locker.Unlock()
		return actor.GetActorAddress(), nil, nil
	}

	sys.logger.Infof("actor=%s not found", actorName)
	sys.locker.Unlock()
	return nil, nil, ErrActorNotFound(actorName)
}

// buildActorPath returns the actor path provided the actor name
func (sys *system) buildActorPath(name string) *Path {
	return NewPath(name, NewAddress(sys.name, sys.host, int(sys.remotingPort)))
}
