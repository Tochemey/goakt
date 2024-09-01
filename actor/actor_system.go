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

// ActorSystem defines the contract of an actor actorSystem
type ActorSystem interface {
	// Name returns the actor actorSystem name
	Name() string
	// Actors returns the list of Actors that are alive in the actor actorSystem
	Actors() []*PID
	// Start starts the actor actorSystem
	Start(ctx context.Context) error
	// Stop stops the actor actorSystem
	Stop(ctx context.Context) error
	// Spawn creates an actor in the actorSystem and starts it
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
	// Kill stops a given actor in the actorSystem
	Kill(ctx context.Context, name string) error
	// ReSpawn recreates a given actor in the actorSystem
	ReSpawn(ctx context.Context, name string) (*PID, error)
	// ActorsCount returns the total number of active actors in the actorSystem
	ActorsCount() uint64
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
	// PeerAddress returns the actor actorSystem address known in the cluster. That address is used by other nodes to communicate with the actor actorSystem.
	// This address is empty when cluster mode is not activated
	PeerAddress() string
	// Register registers an actor for future use. This is necessary when creating an actor remotely
	Register(ctx context.Context, actor Actor) error
	// Deregister removes a registered actor from the registry
	Deregister(ctx context.Context, actor Actor) error
	// Logger returns the logger sets when creating the actor actorSystem
	Logger() log.Logger
	// handleRemoteAsk handles a synchronous message to another actor and expect a response.
	// This block until a response is received or timed out.
	handleRemoteAsk(ctx context.Context, to *PID, message proto.Message, timeout time.Duration) (response proto.Message, err error)
	// handleRemoteTell handles an asynchronous message to an actor
	handleRemoteTell(ctx context.Context, to *PID, message proto.Message) error
	// setActor sets actor in the actor actorSystem actors registry
	setActor(actor *PID)
	// supervisor return the actorSystem supervisor
	getSupervisor() *PID
	// actorOf returns an existing actor in the local actorSystem or in the cluster when clustering is enabled
	// When cluster mode is activated, the PID will be nil.
	// When remoting is enabled this method will return and error
	// An actor not found error is return when the actor is not found.
	actorOf(ctx context.Context, actorName string) (addr *goaktpb.Address, pid *PID, err error)
	// inCluster states whether the actor actorSystem is running within a cluster of nodes
	inCluster() bool
}

// ActorSystem represent a collection of actors on a given node
// Only a single instance of the ActorSystem can be created on a given node
type actorSystem struct {
	internalpbconnect.UnimplementedRemotingServiceHandler
	internalpbconnect.UnimplementedClusterServiceHandler

	// map of actors in the actorSystem
	actors *pidMap

	// states whether the actor actorSystem has started or not
	started atomic.Bool

	// Specifies the actor actorSystem name
	name string
	// Specifies the logger to use in the actorSystem
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

// enforce compilation error when all methods of the ActorSystem interface are not implemented
// by the struct actorSystem
var _ ActorSystem = (*actorSystem)(nil)

// NewActorSystem creates an instance of ActorSystem
func NewActorSystem(name string, opts ...Option) (ActorSystem, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	if match, _ := regexp.MatchString("^[a-zA-Z0-9][a-zA-Z0-9-_]*$", name); !match {
		return nil, ErrInvalidActorSystemName
	}

	system := &actorSystem{
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

// Logger returns the logger sets when creating the actor actorSystem
func (system *actorSystem) Logger() log.Logger {
	system.locker.Lock()
	logger := system.logger
	system.locker.Unlock()
	return logger
}

// Deregister removes a registered actor from the registry
func (system *actorSystem) Deregister(_ context.Context, actor Actor) error {
	system.locker.Lock()
	defer system.locker.Unlock()

	if !system.started.Load() {
		return ErrActorSystemNotStarted
	}

	system.registry.Deregister(actor)
	return nil
}

// Register registers an actor for future use. This is necessary when creating an actor remotely
func (system *actorSystem) Register(_ context.Context, actor Actor) error {
	system.locker.Lock()
	defer system.locker.Unlock()

	if !system.started.Load() {
		return ErrActorSystemNotStarted
	}

	system.registry.Register(actor)
	return nil
}

// ScheduleOnce schedules a message that will be delivered to the receiver actor
// This will send the given message to the actor after the given interval specified.
// The message will be sent once
func (system *actorSystem) ScheduleOnce(ctx context.Context, message proto.Message, actorName string, interval time.Duration) error {
	addr, pid, err := system.actorOf(ctx, actorName)
	if err != nil {
		return err
	}

	if pid != nil {
		return system.scheduler.scheduleOnce(ctx, message, pid, interval)
	}

	if addr != nil {
		return system.scheduler.remoteScheduleOnce(ctx, message, addr, interval)
	}

	return ErrActorNotFound(actorName)
}

// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (system *actorSystem) ScheduleWithCron(ctx context.Context, message proto.Message, actorName string, cronExpression string) error {
	addr, pid, err := system.actorOf(ctx, actorName)
	if err != nil {
		return err
	}

	if pid != nil {
		return system.scheduler.scheduleWithCron(ctx, message, pid, cronExpression)
	}

	if addr != nil {
		return system.scheduler.remoteScheduleWithCron(ctx, message, addr, cronExpression)
	}

	return ErrActorNotFound(actorName)
}

// Subscribe help receive dead letters whenever there are available
func (system *actorSystem) Subscribe() (eventstream.Subscriber, error) {
	if !system.started.Load() {
		return nil, ErrActorSystemNotStarted
	}
	subscriber := system.eventsStream.AddSubscriber()
	system.eventsStream.Subscribe(subscriber, eventsTopic)
	return subscriber, nil
}

// Unsubscribe unsubscribes a subscriber.
func (system *actorSystem) Unsubscribe(subscriber eventstream.Subscriber) error {
	if !system.started.Load() {
		return ErrActorSystemNotStarted
	}
	system.eventsStream.Unsubscribe(subscriber, eventsTopic)
	system.eventsStream.RemoveSubscriber(subscriber)
	return nil
}

// GetPartition returns the partition where a given actor is located
func (system *actorSystem) GetPartition(actorName string) uint64 {
	if !system.inCluster() {
		// TODO: maybe add a partitioner function
		return 0
	}
	return uint64(system.cluster.GetPartition(actorName))
}

// ActorsCount returns the total number of active actors in the actorSystem
func (system *actorSystem) ActorsCount() uint64 {
	return uint64(len(system.Actors()))
}

// Spawn creates or returns the instance of a given actor in the actorSystem
func (system *actorSystem) Spawn(ctx context.Context, name string, actor Actor) (*PID, error) {
	if !system.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	pid, exist := system.actors.get(system.actorPath(name))
	if exist {
		if pid.IsRunning() {
			// return the existing instance
			return pid, nil
		}
	}

	pid, err := system.configPID(ctx, name, actor)
	if err != nil {
		return nil, err
	}

	system.supervisor.Watch(pid)
	system.setActor(pid)
	return pid, nil
}

// SpawnNamedFromFunc creates an actor with the given receive function and provided name. One can set the PreStart and PostStop lifecycle hooks
// in the given optional options
func (system *actorSystem) SpawnNamedFromFunc(ctx context.Context, name string, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error) {
	if !system.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	actor := newFuncActor(name, receiveFunc, opts...)
	pid, err := system.configPID(ctx, name, actor)
	if err != nil {
		return nil, err
	}

	system.supervisor.Watch(pid)
	system.setActor(pid)
	return pid, nil
}

// SpawnFromFunc creates an actor with the given receive function.
func (system *actorSystem) SpawnFromFunc(ctx context.Context, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error) {
	return system.SpawnNamedFromFunc(ctx, uuid.NewString(), receiveFunc, opts...)
}

// SpawnRouter creates a new router. One can additionally set the router options.
// A router is a special type of actor that helps distribute messages of the same type over a set of actors, so that messages can be processed in parallel.
// A single actor will only process one message at a time.
func (system *actorSystem) SpawnRouter(ctx context.Context, poolSize int, routeesKind Actor, opts ...RouterOption) (*PID, error) {
	router := newRouter(poolSize, routeesKind, system.logger, opts...)
	routerName := system.getSystemActorName(routerType)
	return system.Spawn(ctx, routerName, router)
}

// Kill stops a given actor in the actorSystem
func (system *actorSystem) Kill(ctx context.Context, name string) error {
	if !system.started.Load() {
		return ErrActorSystemNotStarted
	}

	actorPath := system.actorPath(name)
	pid, exist := system.actors.get(actorPath)
	if exist {
		// stop the given actor. No need to record error in the span context
		// because the shutdown method is taking care of that
		return pid.Shutdown(ctx)
	}

	return ErrActorNotFound(actorPath.String())
}

// ReSpawn recreates a given actor in the actorSystem
func (system *actorSystem) ReSpawn(ctx context.Context, name string) (*PID, error) {
	if !system.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	actorPath := system.actorPath(name)
	pid, exist := system.actors.get(actorPath)
	if exist {
		if err := pid.Restart(ctx); err != nil {
			return nil, fmt.Errorf("failed to restart actor=%s: %w", actorPath.String(), err)
		}

		system.actors.set(pid)
		system.supervisor.Watch(pid)
		return pid, nil
	}

	return nil, ErrActorNotFound(actorPath.String())
}

// Name returns the actor actorSystem name
func (system *actorSystem) Name() string {
	system.locker.Lock()
	defer system.locker.Unlock()
	return system.name
}

// Actors returns the list of Actors that are alive in the actor actorSystem
func (system *actorSystem) Actors() []*PID {
	system.locker.Lock()
	pids := system.actors.pids()
	system.locker.Unlock()
	actors := make([]*PID, 0, len(pids))
	for _, pid := range pids {
		if !isSystemName(pid.Name()) {
			actors = append(actors, pid)
		}
	}
	return actors
}

// PeerAddress returns the actor actorSystem address known in the cluster. That address is used by other nodes to communicate with the actor actorSystem.
// This address is empty when cluster mode is not activated
func (system *actorSystem) PeerAddress() string {
	system.locker.Lock()
	defer system.locker.Unlock()
	if system.clusterEnabled.Load() {
		return system.cluster.AdvertisedAddress()
	}
	return ""
}

// Start starts the actor actorSystem
func (system *actorSystem) Start(ctx context.Context) error {
	system.started.Store(true)

	if system.clusterEnabled.Load() {
		system.enableRemoting(ctx)
		if err := system.enableClustering(ctx); err != nil {
			return err
		}
	}

	system.scheduler.start(ctx)
	actorName := system.getSystemActorName(supervisorType)
	pid, err := system.configPID(ctx, actorName, newRootActor(system.logger))
	if err != nil {
		return fmt.Errorf("actor=%s failed to start actorSystem supervisor: %w", actorName, err)
	}

	system.supervisor = pid
	system.setActor(pid)

	go system.janitor()

	system.logger.Infof("%s started..:)", system.name)
	return nil
}

// Stop stops the actor actorSystem
func (system *actorSystem) Stop(ctx context.Context) error {
	system.logger.Infof("%s shutting down...", system.name)

	// make sure the actor actorSystem has started
	if !system.started.Load() {
		return ErrActorSystemNotStarted
	}

	system.stopGC <- types.Unit{}
	system.logger.Infof("%s is shutting down..:)", system.name)

	system.scheduler.stop(ctx)

	if system.eventsStream != nil {
		system.eventsStream.Shutdown()
	}

	ctx, cancel := context.WithTimeout(ctx, system.shutdownTimeout)
	defer cancel()

	if system.clusterEnabled.Load() {
		if err := system.remotingServer.Shutdown(ctx); err != nil {
			return err
		}
		system.remotingServer = nil

		if err := system.cluster.Stop(ctx); err != nil {
			return err
		}
		close(system.actorsChan)
		system.clusterSyncStopSig <- types.Unit{}
		system.clusterEnabled.Store(false)
		close(system.redistributionChan)
	}

	// stop the supervisor actor
	if err := system.getSupervisor().Shutdown(ctx); err != nil {
		system.reset()
		return err
	}

	// remove the supervisor from the actors list
	system.actors.delete(system.supervisor.ActorPath())

	for _, actor := range system.Actors() {
		system.actors.delete(actor.ActorPath())
		if err := actor.Shutdown(ctx); err != nil {
			system.reset()
			return err
		}
	}

	system.started.Store(false)
	system.reset()
	system.logger.Infof("%s shuts down successfully", system.name)
	return nil
}

// RemoteLookup for an actor on a remote host.
func (system *actorSystem) RemoteLookup(ctx context.Context, request *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	logger := system.logger
	msg := request.Msg

	if !system.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", system.host, system.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	if system.clusterEnabled.Load() {
		actorName := msg.GetName()
		actor, err := system.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				logger.Error(ErrAddressNotFound(actorName).Error())
				return nil, ErrAddressNotFound(actorName)
			}

			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: actor.GetActorAddress()}), nil
	}

	actorPath := NewPath(msg.GetName(), NewAddress(system.Name(), msg.GetHost(), int(msg.GetPort())))
	pid, exist := system.actors.get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: pid.ActorPath().RemoteAddress()}), nil
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately. With this type of message the receiver cannot communicate back to Sender
// except reply the message with a response. This one-way communication
func (system *actorSystem) RemoteAsk(ctx context.Context, stream *connect.BidiStream[internalpb.RemoteAskRequest, internalpb.RemoteAskResponse]) error {
	logger := system.logger

	if !system.clusterEnabled.Load() {
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

		remoteAddr := fmt.Sprintf("%s:%d", system.host, system.remotingPort)
		if remoteAddr != net.JoinHostPort(receiver.GetHost(), strconv.Itoa(int(receiver.GetPort()))) {
			return connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
		}

		actorPath := NewPath(name, NewAddress(system.name, system.host, int(system.remotingPort)))
		pid, exist := system.actors.get(actorPath)
		if !exist {
			logger.Error(ErrAddressNotFound(actorPath.String()).Error())
			return ErrAddressNotFound(actorPath.String())
		}

		timeout := system.askTimeout
		if request.GetTimeout() != nil {
			timeout = request.GetTimeout().AsDuration()
		}

		reply, err := system.handleRemoteAsk(ctx, pid, message, timeout)
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
func (system *actorSystem) RemoteTell(ctx context.Context, stream *connect.ClientStream[internalpb.RemoteTellRequest]) (*connect.Response[internalpb.RemoteTellResponse], error) {
	logger := system.logger

	if !system.clusterEnabled.Load() {
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
					system.Name(),
					receiver.GetHost(),
					int(receiver.GetPort())))

			pid, exist := system.actors.get(actorPath)
			if !exist {
				logger.Error(ErrAddressNotFound(actorPath.String()).Error())
				return ErrAddressNotFound(actorPath.String())
			}

			if err := system.handleRemoteTell(ctx, pid, request.GetRemoteMessage()); err != nil {
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
func (system *actorSystem) RemoteReSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteReSpawnRequest]) (*connect.Response[internalpb.RemoteReSpawnResponse], error) {
	logger := system.logger

	msg := request.Msg

	if !system.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", system.host, system.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorPath := NewPath(msg.GetName(), NewAddress(system.Name(), msg.GetHost(), int(msg.GetPort())))
	pid, exist := system.actors.get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	if err := pid.Restart(ctx); err != nil {
		return nil, fmt.Errorf("failed to restart actor=%s: %w", actorPath.String(), err)
	}

	system.actors.set(pid)
	return connect.NewResponse(new(internalpb.RemoteReSpawnResponse)), nil
}

// RemoteStop stops an actor on a remote machine
func (system *actorSystem) RemoteStop(ctx context.Context, request *connect.Request[internalpb.RemoteStopRequest]) (*connect.Response[internalpb.RemoteStopResponse], error) {
	logger := system.logger

	msg := request.Msg

	if !system.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", system.host, system.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorPath := NewPath(msg.GetName(), NewAddress(system.Name(), msg.GetHost(), int(msg.GetPort())))
	pid, exist := system.actors.get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	if err := pid.Shutdown(ctx); err != nil {
		return nil, fmt.Errorf("failed to stop actor=%s: %w", actorPath.String(), err)
	}

	system.actors.delete(actorPath)
	return connect.NewResponse(new(internalpb.RemoteStopResponse)), nil
}

// RemoteSpawn handles the remoteSpawn call
func (system *actorSystem) RemoteSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteSpawnRequest]) (*connect.Response[internalpb.RemoteSpawnResponse], error) {
	logger := system.logger

	msg := request.Msg
	if !system.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", system.host, system.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actor, err := system.reflection.ActorFrom(msg.GetActorType())
	if err != nil {
		logger.Errorf("failed to create actor=[(%s) of type (%s)] on [host=%s, port=%d]: reason: (%v)",
			msg.GetActorName(), msg.GetActorType(), msg.GetHost(), msg.GetPort(), err)

		if errors.Is(err, ErrTypeNotRegistered) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, ErrTypeNotRegistered)
		}

		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if _, err = system.Spawn(ctx, msg.GetActorName(), actor); err != nil {
		logger.Errorf("failed to create actor=(%s) on [host=%s, port=%d]: reason: (%v)", msg.GetActorName(), msg.GetHost(), msg.GetPort(), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	logger.Infof("actor=(%s) successfully created on [host=%s, port=%d]", msg.GetActorName(), msg.GetHost(), msg.GetPort())
	return connect.NewResponse(new(internalpb.RemoteSpawnResponse)), nil
}

// GetNodeMetric handles the GetNodeMetric request send the given node
func (system *actorSystem) GetNodeMetric(_ context.Context, request *connect.Request[internalpb.GetNodeMetricRequest]) (*connect.Response[internalpb.GetNodeMetricResponse], error) {
	if !system.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrClusterDisabled)
	}

	req := request.Msg

	remoteAddr := fmt.Sprintf("%s:%d", system.host, system.remotingPort)
	if remoteAddr != req.GetNodeAddress() {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorCount := system.actors.len()
	return connect.NewResponse(&internalpb.GetNodeMetricResponse{
		NodeRemoteAddress: remoteAddr,
		ActorsCount:       uint64(actorCount),
	}), nil
}

// GetKinds returns the cluster kinds
func (system *actorSystem) GetKinds(_ context.Context, request *connect.Request[internalpb.GetKindsRequest]) (*connect.Response[internalpb.GetKindsResponse], error) {
	if !system.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrClusterDisabled)
	}

	req := request.Msg
	remoteAddr := fmt.Sprintf("%s:%d", system.host, system.remotingPort)

	// routine check
	if remoteAddr != req.GetNodeAddress() {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	kinds := make([]string, len(system.clusterConfig.Kinds()))
	for i, kind := range system.clusterConfig.Kinds() {
		kinds[i] = types.TypeName(kind)
	}

	return connect.NewResponse(&internalpb.GetKindsResponse{Kinds: kinds}), nil
}

// handleRemoteAsk handles a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (system *actorSystem) handleRemoteAsk(ctx context.Context, to *PID, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	return Ask(ctx, to, message, timeout)
}

// handleRemoteTell handles an asynchronous message to an actor
func (system *actorSystem) handleRemoteTell(ctx context.Context, to *PID, message proto.Message) error {
	return Tell(ctx, to, message)
}

// getSupervisor return the actorSystem supervisor
func (system *actorSystem) getSupervisor() *PID {
	system.locker.Lock()
	supervisor := system.supervisor
	system.locker.Unlock()
	return supervisor
}

// setActor implements ActorSystem.
func (system *actorSystem) setActor(actor *PID) {
	system.actors.set(actor)
	if system.clusterEnabled.Load() {
		system.actorsChan <- &internalpb.WireActor{
			ActorName:    actor.Name(),
			ActorAddress: actor.ActorPath().RemoteAddress(),
			ActorPath:    actor.ActorPath().String(),
			ActorType:    types.TypeName(actor.Actor()),
		}
	}
}

// enableClustering enables clustering. When clustering is enabled remoting is also enabled to facilitate remote
// communication
func (system *actorSystem) enableClustering(ctx context.Context) error {
	system.logger.Info("enabling clustering...")

	node := &discovery.Node{
		Name:         system.Name(),
		Host:         system.host,
		GossipPort:   system.clusterConfig.GossipPort(),
		PeersPort:    system.clusterConfig.PeersPort(),
		RemotingPort: system.clusterConfig.RemotingPort(),
	}

	clusterEngine, err := cluster.NewEngine(
		system.Name(),
		system.clusterConfig.Discovery(),
		node,
		cluster.WithLogger(system.logger),
		cluster.WithPartitionsCount(system.clusterConfig.PartitionCount()),
		cluster.WithHasher(system.partitionHasher),
		cluster.WithMinimumPeersQuorum(system.clusterConfig.MinimumPeersQuorum()),
		cluster.WithReplicaCount(system.clusterConfig.ReplicaCount()),
	)
	if err != nil {
		system.logger.Errorf("failed to initialize cluster engine: %v", err)
		return err
	}

	system.logger.Info("starting cluster engine...")
	if err := clusterEngine.Start(ctx); err != nil {
		system.logger.Errorf("failed to start cluster engine: %v", err)
		return err
	}

	bootstrapChan := make(chan struct{}, 1)
	timer := time.AfterFunc(time.Second, func() {
		bootstrapChan <- struct{}{}
	})
	<-bootstrapChan
	timer.Stop()

	system.logger.Info("cluster engine successfully started...")

	system.locker.Lock()
	system.cluster = clusterEngine
	system.clusterEventsChan = clusterEngine.Events()
	system.redistributionChan = make(chan *cluster.Event, 1)
	for _, kind := range system.clusterConfig.Kinds() {
		system.registry.Register(kind)
		system.logger.Infof("cluster kind=(%s) registered", types.TypeName(kind))
	}
	system.locker.Unlock()

	go system.clusterEventsLoop()
	go system.replicationLoop()
	go system.peersStateLoop()
	go system.redistributionLoop()

	system.logger.Info("clustering enabled...:)")
	return nil
}

// enableRemoting enables the remoting service to handle remote messaging
func (system *actorSystem) enableRemoting(ctx context.Context) {
	system.logger.Info("enabling remoting...")

	var interceptor *otelconnect.Interceptor
	var err error
	if system.metricEnabled.Load() {
		interceptor, err = otelconnect.NewInterceptor(
			otelconnect.WithMeterProvider(system.telemetry.MeterProvider),
		)
		if err != nil {
			system.logger.Panic(fmt.Errorf("failed to initialize observability feature: %w", err))
		}
	}

	var opts []connect.HandlerOption
	if interceptor != nil {
		opts = append(opts, connect.WithInterceptors(interceptor))
	}

	if system.host == "" {
		system.host, _ = os.Hostname()
	}

	remotingHost, remotingPort, err := tcp.GetHostPort(fmt.Sprintf("%s:%d", system.host, system.remotingPort))
	if err != nil {
		system.logger.Panic(fmt.Errorf("failed to resolve remoting TCP address: %w", err))
	}

	system.host = remotingHost
	system.remotingPort = int32(remotingPort)

	remotingServicePath, remotingServiceHandler := internalpbconnect.NewRemotingServiceHandler(system, opts...)
	clusterServicePath, clusterServiceHandler := internalpbconnect.NewClusterServiceHandler(system, opts...)

	mux := stdhttp.NewServeMux()
	mux.Handle(remotingServicePath, remotingServiceHandler)
	mux.Handle(clusterServicePath, clusterServiceHandler)
	server := http.NewServer(ctx, system.host, remotingPort, mux)

	go func() {
		if err := server.ListenAndServe(); err != nil {
			if !errors.Is(err, stdhttp.ErrServerClosed) {
				system.logger.Panic(fmt.Errorf("failed to start remoting service: %w", err))
			}
		}
	}()

	system.locker.Lock()
	system.remotingServer = server
	system.locker.Unlock()

	system.logger.Info("remoting enabled...:)")
}

// reset the actor actorSystem
func (system *actorSystem) reset() {
	system.telemetry = nil
	system.actors.reset()
	system.name = ""
	system.cluster = nil
}

// janitor time to time removes dead actors from the actorSystem
// that helps free non-utilized resources
func (system *actorSystem) janitor() {
	system.logger.Info("janitor has started...")
	ticker := time.NewTicker(system.gcInterval)
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				for _, actor := range system.Actors() {
					if !actor.IsRunning() {
						system.logger.Infof("removing actor=%s from actorSystem", actor.ActorPath().Name())
						system.actors.delete(actor.ActorPath())
						if system.inCluster() {
							if err := system.cluster.RemoveActor(context.Background(), actor.ActorPath().Name()); err != nil {
								system.logger.Error(err.Error())
								// TODO: stop or continue
							}
						}
					}
				}
			case <-system.stopGC:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()
	system.logger.Info("janitor has stopped...")
}

// registerMetrics register the PID metrics with OTel instrumentation.
func (system *actorSystem) registerMetrics() error {
	meter := system.telemetry.Meter
	metrics, err := metric.NewActorSystemMetric(meter)
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		observer.ObserveInt64(metrics.ActorsCount(), int64(system.ActorsCount()))
		return nil
	}, metrics.ActorsCount())

	return err
}

// replicationLoop publishes newly created actor into the cluster when cluster is enabled
func (system *actorSystem) replicationLoop() {
	for actor := range system.actorsChan {
		// never replicate actorSystem actors because there are specific to the
		// running node
		if isSystemName(actor.GetActorName()) {
			continue
		}
		if system.inCluster() {
			ctx := context.Background()
			if err := system.cluster.PutActor(ctx, actor); err != nil {
				system.logger.Panic(err.Error())
			}
		}
	}
}

// clusterEventsLoop listens to cluster events and send them to the event streams
func (system *actorSystem) clusterEventsLoop() {
	for event := range system.clusterEventsChan {
		if system.inCluster() {
			if event != nil && event.Payload != nil {
				// push the event to the event stream
				message, _ := event.Payload.UnmarshalNew()
				if system.eventsStream != nil {
					system.logger.Debugf("node=(%s) publishing cluster event=(%s)....", system.name, event.Type)
					system.eventsStream.Publish(eventsTopic, message)
					system.logger.Debugf("cluster event=(%s) successfully published by node=(%s)", event.Type, system.name)
				}
				system.redistributionChan <- event
			}
		}
	}
}

// peersStateLoop fetches the cluster peers' PeerState and update the node peersCache
func (system *actorSystem) peersStateLoop() {
	system.logger.Info("peers state synchronization has started...")
	ticker := time.NewTicker(system.peersStateLoopInterval)
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				eg, ctx := errgroup.WithContext(context.Background())

				peersChan := make(chan *cluster.Peer)

				eg.Go(func() error {
					defer close(peersChan)
					peers, err := system.cluster.Peers(ctx)
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
						if err := system.processPeerState(ctx, peer); err != nil {
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
					system.logger.Error(err)
					// TODO: stop or panic
				}

			case <-system.clusterSyncStopSig:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()
	system.logger.Info("peers state synchronization has stopped...")
}

func (system *actorSystem) redistributionLoop() {
	for event := range system.redistributionChan {
		if system.inCluster() {
			// check for cluster rebalancing
			if err := system.redistribute(context.Background(), event); err != nil {
				system.logger.Errorf("cluster rebalancing failed: %v", err)
				// TODO: panic or retry or shutdown actor actorSystem
			}
		}
	}
}

// processPeerState processes a given peer synchronization record.
func (system *actorSystem) processPeerState(ctx context.Context, peer *cluster.Peer) error {
	system.peersCacheMu.Lock()
	defer system.peersCacheMu.Unlock()

	peerAddress := net.JoinHostPort(peer.Host, strconv.Itoa(peer.Port))

	system.logger.Infof("processing peer sync:(%s)", peerAddress)
	peerState, err := system.cluster.GetState(ctx, peerAddress)
	if err != nil {
		if errors.Is(err, cluster.ErrPeerSyncNotFound) {
			return nil
		}
		system.logger.Error(err)
		return err
	}

	system.logger.Debugf("peer (%s) actors count (%d)", peerAddress, len(peerState.GetActors()))

	// no need to handle the error
	bytea, err := proto.Marshal(peerState)
	if err != nil {
		system.logger.Error(err)
		return err
	}

	system.peersCache[peerAddress] = bytea
	system.logger.Infof("peer sync(%s) successfully processed", peerAddress)
	return nil
}

// configPID constructs a PID provided the actor name and the actor kind
// this is a utility function used when spawning actors
func (system *actorSystem) configPID(ctx context.Context, name string, actor Actor) (*PID, error) {
	actorPath := NewPath(name, NewAddress(system.name, "", -1))
	if system.clusterEnabled.Load() {
		actorPath = NewPath(name, NewAddress(system.name, system.host, int(system.remotingPort)))
	}

	// define the pid options
	// pid inherit the actor actorSystem settings defined during instantiation
	pidOpts := []pidOption{
		withInitMaxRetries(system.actorInitMaxRetries),
		withAskTimeout(system.askTimeout),
		withCustomLogger(system.logger),
		withActorSystem(system),
		withSupervisorDirective(system.supervisorDirective),
		withEventsStream(system.eventsStream),
		withInitTimeout(system.actorInitTimeout),
		withTelemetry(system.telemetry),
	}

	// enable stash
	if system.stashEnabled {
		pidOpts = append(pidOpts, withStash())
	}

	// disable passivation for actorSystem supervisor
	if isSystemName(name) {
		pidOpts = append(pidOpts, withPassivationDisabled())
	} else {
		pidOpts = append(pidOpts, withPassivationAfter(system.expireActorAfter))
	}

	if system.metricEnabled.Load() {
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

// getSystemActorName returns the actorSystem supervisor name
func (system *actorSystem) getSystemActorName(nameType nameType) string {
	if system.clusterEnabled.Load() {
		return fmt.Sprintf("%s%s%s-%d-%d",
			systemNames[nameType],
			strings.ToTitle(system.name),
			system.host,
			system.remotingPort,
			time.Now().UnixNano())
	}
	return fmt.Sprintf("%s%s-%d",
		systemNames[nameType],
		strings.ToTitle(system.name),
		time.Now().UnixNano())
}

func isSystemName(name string) bool {
	return strings.HasPrefix(name, systemNamePrefix)
}

// ActorOf returns an existing actor in the local actorSystem or in the cluster when clustering is enabled
// When cluster mode is activated, the PID will be nil.
// An actor not found error is return when the actor is not found.
func (system *actorSystem) actorOf(ctx context.Context, actorName string) (addr *goaktpb.Address, pid *PID, err error) {
	system.locker.Lock()

	if !system.started.Load() {
		system.locker.Unlock()
		return nil, nil, ErrActorSystemNotStarted
	}

	actorPath := system.actorPath(actorName)
	if pid, ok := system.actors.get(actorPath); ok {
		system.locker.Unlock()
		return pid.ActorPath().RemoteAddress(), pid, nil
	}

	// check in the cluster
	if system.clusterEnabled.Load() {
		actor, err := system.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				system.logger.Infof("actor=%s not found", actorName)
				e := ErrActorNotFound(actorName)
				system.locker.Unlock()
				return nil, nil, e
			}

			system.locker.Unlock()
			return nil, nil, fmt.Errorf("failed to fetch remote actor=%s: %w", actorName, err)
		}

		system.locker.Unlock()
		return actor.GetActorAddress(), nil, nil
	}

	system.logger.Infof("actor=%s not found", actorName)
	system.locker.Unlock()
	return nil, nil, ErrActorNotFound(actorName)
}

// actorPath returns the actor path provided the actor name
func (system *actorSystem) actorPath(name string) *Path {
	return NewPath(name, NewAddress(system.name, system.host, int(system.remotingPort)))
}

// inCluster states whether the actor actorSystem is running within a cluster of nodes
func (system *actorSystem) inCluster() bool {
	return system.clusterEnabled.Load() && system.cluster != nil
}
