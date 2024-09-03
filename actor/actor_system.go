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
	"github.com/tochemey/goakt/v2/internal/errorchain"
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

// ActorSystem represent a collection of actors on a given node
// Only a single instance of the ActorSystem can be created on a given node
type ActorSystem struct {
	internalpbconnect.UnimplementedRemotingServiceHandler
	internalpbconnect.UnimplementedClusterServiceHandler

	// map of actors in the ActorSystem
	actors *pidMap

	// states whether the actor ActorSystem has started or not
	started atomic.Bool

	// Specifies the actor ActorSystem name
	name string
	// Specifies the logger to use in the ActorSystem
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

	peersStateSyncInterval time.Duration
	peersCacheMu           *sync.RWMutex
	peersCache             map[string][]byte
	clusterConfig          *ClusterConfig
	redistributionChan     chan *cluster.Event

	supervisor *PID
}

// NewActorSystem creates an instance of ActorSystem
func NewActorSystem(name string, opts ...Option) (*ActorSystem, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	if match, _ := regexp.MatchString("^[a-zA-Z0-9][a-zA-Z0-9-_]*$", name); !match {
		return nil, ErrInvalidActorSystemName
	}

	actorSystem := &ActorSystem{
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
		peersStateSyncInterval: DefaultPeerStateSyncInterval,
		host:                   "",
		remotingPort:           -1,
	}

	actorSystem.started.Store(false)
	actorSystem.clusterEnabled.Store(false)
	actorSystem.metricEnabled.Store(false)

	actorSystem.reflection = newReflection(actorSystem.registry)

	// apply the various options
	for _, opt := range opts {
		opt.Apply(actorSystem)
	}

	if actorSystem.clusterEnabled.Load() {
		if err := actorSystem.clusterConfig.Validate(); err != nil {
			return nil, err
		}

		if err := actorSystem.setRemotingHostAndPort(); err != nil {
			return nil, err
		}
	}

	actorSystem.scheduler = newScheduler(actorSystem.logger, actorSystem.shutdownTimeout, withSchedulerCluster(actorSystem.cluster))
	if actorSystem.metricEnabled.Load() {
		if err := actorSystem.registerMetrics(); err != nil {
			return nil, err
		}
	}

	return actorSystem, nil
}

// Logger returns the logger sets when creating the actor ActorSystem
func (actorSystem *ActorSystem) Logger() log.Logger {
	actorSystem.locker.Lock()
	logger := actorSystem.logger
	actorSystem.locker.Unlock()
	return logger
}

// Deregister removes a registered actor from the registry
func (actorSystem *ActorSystem) Deregister(_ context.Context, actor Actor) error {
	actorSystem.locker.Lock()
	defer actorSystem.locker.Unlock()

	if !actorSystem.started.Load() {
		return ErrActorSystemNotStarted
	}

	actorSystem.registry.Deregister(actor)
	return nil
}

// Register registers an actor for future use. This is necessary when creating an actor remotely
func (actorSystem *ActorSystem) Register(_ context.Context, actor Actor) error {
	actorSystem.locker.Lock()
	defer actorSystem.locker.Unlock()

	if !actorSystem.started.Load() {
		return ErrActorSystemNotStarted
	}

	actorSystem.registry.Register(actor)
	return nil
}

// ScheduleOnce schedules a message that will be delivered to the receiver actor
// This will send the given message to the actor after the given interval specified.
// The message will be sent once
func (actorSystem *ActorSystem) ScheduleOnce(ctx context.Context, message proto.Message, actorName string, interval time.Duration) error {
	addr, pid, err := actorSystem.actorOf(ctx, actorName)
	if err != nil {
		return err
	}

	if pid != nil {
		return actorSystem.scheduler.scheduleOnce(ctx, message, pid, interval)
	}

	if addr != nil {
		return actorSystem.scheduler.remoteScheduleOnce(ctx, message, addr, interval)
	}

	return ErrActorNotFound(actorName)
}

// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (actorSystem *ActorSystem) ScheduleWithCron(ctx context.Context, message proto.Message, actorName string, cronExpression string) error {
	addr, pid, err := actorSystem.actorOf(ctx, actorName)
	if err != nil {
		return err
	}

	if pid != nil {
		return actorSystem.scheduler.scheduleWithCron(ctx, message, pid, cronExpression)
	}

	if addr != nil {
		return actorSystem.scheduler.remoteScheduleWithCron(ctx, message, addr, cronExpression)
	}

	return ErrActorNotFound(actorName)
}

// Subscribe help receive dead letters whenever there are available
func (actorSystem *ActorSystem) Subscribe() (eventstream.Subscriber, error) {
	if !actorSystem.started.Load() {
		return nil, ErrActorSystemNotStarted
	}
	subscriber := actorSystem.eventsStream.AddSubscriber()
	actorSystem.eventsStream.Subscribe(subscriber, eventsTopic)
	return subscriber, nil
}

// Unsubscribe unsubscribes a subscriber.
func (actorSystem *ActorSystem) Unsubscribe(subscriber eventstream.Subscriber) error {
	if !actorSystem.started.Load() {
		return ErrActorSystemNotStarted
	}
	actorSystem.eventsStream.Unsubscribe(subscriber, eventsTopic)
	actorSystem.eventsStream.RemoveSubscriber(subscriber)
	return nil
}

// GetPartition returns the partition where a given actor is located
func (actorSystem *ActorSystem) GetPartition(actorName string) uint64 {
	if !actorSystem.inCluster() {
		// TODO: maybe add a partitioner function
		return 0
	}
	return uint64(actorSystem.cluster.GetPartition(actorName))
}

// ActorsCount returns the total number of active actors in the ActorSystem
func (actorSystem *ActorSystem) ActorsCount() uint64 {
	return uint64(len(actorSystem.Actors()))
}

// Spawn creates or returns the instance of a given actor in the ActorSystem
func (actorSystem *ActorSystem) Spawn(ctx context.Context, name string, actor Actor) (*PID, error) {
	if !actorSystem.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	pid, exist := actorSystem.actors.get(actorSystem.actorPath(name))
	if exist {
		if pid.IsRunning() {
			// return the existing instance
			return pid, nil
		}
	}

	pid, err := actorSystem.configPID(ctx, name, actor)
	if err != nil {
		return nil, err
	}

	actorSystem.supervisor.Watch(pid)
	actorSystem.setActor(pid)
	return pid, nil
}

// SpawnNamedFromFunc creates an actor with the given receive function and provided name. One can set the PreStart and PostStop lifecycle hooks
// in the given optional options
func (actorSystem *ActorSystem) SpawnNamedFromFunc(ctx context.Context, name string, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error) {
	if !actorSystem.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	actor := newFuncActor(name, receiveFunc, opts...)
	pid, err := actorSystem.configPID(ctx, name, actor)
	if err != nil {
		return nil, err
	}

	actorSystem.supervisor.Watch(pid)
	actorSystem.setActor(pid)
	return pid, nil
}

// SpawnFromFunc creates an actor with the given receive function.
func (actorSystem *ActorSystem) SpawnFromFunc(ctx context.Context, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error) {
	return actorSystem.SpawnNamedFromFunc(ctx, uuid.NewString(), receiveFunc, opts...)
}

// SpawnRouter creates a new router. One can additionally set the router options.
// A router is a special type of actor that helps distribute messages of the same type over a set of actors, so that messages can be processed in parallel.
// A single actor will only process one message at a time.
func (actorSystem *ActorSystem) SpawnRouter(ctx context.Context, poolSize int, routeesKind Actor, opts ...RouterOption) (*PID, error) {
	router := newRouter(poolSize, routeesKind, actorSystem.logger, opts...)
	routerName := actorSystem.getSystemActorName(routerType)
	return actorSystem.Spawn(ctx, routerName, router)
}

// Kill stops a given actor in the ActorSystem
func (actorSystem *ActorSystem) Kill(ctx context.Context, name string) error {
	if !actorSystem.started.Load() {
		return ErrActorSystemNotStarted
	}

	actorPath := actorSystem.actorPath(name)
	pid, exist := actorSystem.actors.get(actorPath)
	if exist {
		// stop the given actor. No need to record error in the span context
		// because the shutdown method is taking care of that
		return pid.Shutdown(ctx)
	}

	return ErrActorNotFound(actorPath.String())
}

// ReSpawn recreates a given actor in the ActorSystem
func (actorSystem *ActorSystem) ReSpawn(ctx context.Context, name string) (*PID, error) {
	if !actorSystem.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	actorPath := actorSystem.actorPath(name)
	pid, exist := actorSystem.actors.get(actorPath)
	if exist {
		if err := pid.Restart(ctx); err != nil {
			return nil, fmt.Errorf("failed to restart actor=%s: %w", actorPath.String(), err)
		}

		actorSystem.actors.set(pid)
		actorSystem.supervisor.Watch(pid)
		return pid, nil
	}

	return nil, ErrActorNotFound(actorPath.String())
}

// Name returns the actor ActorSystem name
func (actorSystem *ActorSystem) Name() string {
	actorSystem.locker.Lock()
	defer actorSystem.locker.Unlock()
	return actorSystem.name
}

// Actors returns the list of Actors that are alive in the actor ActorSystem
func (actorSystem *ActorSystem) Actors() []*PID {
	actorSystem.locker.Lock()
	pids := actorSystem.actors.pids()
	actorSystem.locker.Unlock()
	actors := make([]*PID, 0, len(pids))
	for _, pid := range pids {
		if !isSystemName(pid.Name()) {
			actors = append(actors, pid)
		}
	}
	return actors
}

// PeerAddress returns the actor ActorSystem address known in the cluster. That address is used by other nodes to communicate with the actor ActorSystem.
// This address is empty when cluster mode is not activated
func (actorSystem *ActorSystem) PeerAddress() string {
	actorSystem.locker.Lock()
	defer actorSystem.locker.Unlock()
	if actorSystem.clusterEnabled.Load() {
		return actorSystem.cluster.AdvertisedAddress()
	}
	return ""
}

// Start starts the actor ActorSystem
func (actorSystem *ActorSystem) Start(ctx context.Context) error {
	actorSystem.started.Store(true)

	if actorSystem.clusterEnabled.Load() {
		actorSystem.enableRemoting(ctx)
		if err := actorSystem.enableClustering(ctx); err != nil {
			return err
		}
	}

	actorSystem.scheduler.start(ctx)
	actorName := actorSystem.getSystemActorName(supervisorType)
	pid, err := actorSystem.configPID(ctx, actorName, newRootActor(actorSystem.logger))
	if err != nil {
		return fmt.Errorf("actor=%s failed to start ActorSystem supervisor: %w", actorName, err)
	}

	actorSystem.supervisor = pid
	actorSystem.setActor(pid)

	go actorSystem.janitor()

	actorSystem.logger.Infof("%s started..:)", actorSystem.name)
	return nil
}

// Stop stops the actor ActorSystem
func (actorSystem *ActorSystem) Stop(ctx context.Context) error {
	actorSystem.logger.Infof("%s shutting down...", actorSystem.name)

	// make sure the actor ActorSystem has started
	if !actorSystem.started.Load() {
		return ErrActorSystemNotStarted
	}

	actorSystem.stopGC <- types.Unit{}
	actorSystem.logger.Infof("%s is shutting down..:)", actorSystem.name)

	actorSystem.scheduler.stop(ctx)

	if actorSystem.eventsStream != nil {
		actorSystem.eventsStream.Shutdown()
	}

	ctx, cancel := context.WithTimeout(ctx, actorSystem.shutdownTimeout)
	defer cancel()

	if actorSystem.clusterEnabled.Load() {
		if err := errorchain.New(errorchain.ReturnFirst()).
			AddError(actorSystem.remotingServer.Shutdown(ctx)).
			AddError(actorSystem.cluster.Stop(ctx)).Error(); err != nil {
			actorSystem.remotingServer = nil
			return err
		}

		close(actorSystem.actorsChan)
		actorSystem.clusterSyncStopSig <- types.Unit{}
		actorSystem.clusterEnabled.Store(false)
		close(actorSystem.redistributionChan)
	}

	// stop the supervisor actor
	if err := actorSystem.getSupervisor().Shutdown(ctx); err != nil {
		actorSystem.reset()
		return err
	}

	// remove the supervisor from the actors list
	actorSystem.actors.delete(actorSystem.supervisor.ActorPath())
	for _, actor := range actorSystem.Actors() {
		actorSystem.actors.delete(actor.ActorPath())
		if err := actor.Shutdown(ctx); err != nil {
			actorSystem.reset()
			return err
		}
	}

	actorSystem.started.Store(false)
	actorSystem.reset()
	actorSystem.logger.Infof("%s shuts down successfully", actorSystem.name)
	return nil
}

// RemoteLookup for an actor on a remote host.
func (actorSystem *ActorSystem) RemoteLookup(ctx context.Context, request *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	logger := actorSystem.logger
	msg := request.Msg

	if !actorSystem.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", actorSystem.host, actorSystem.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	if actorSystem.clusterEnabled.Load() {
		actorName := msg.GetName()
		actor, err := actorSystem.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				logger.Error(ErrAddressNotFound(actorName).Error())
				return nil, ErrAddressNotFound(actorName)
			}

			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: actor.GetActorAddress()}), nil
	}

	actorPath := NewPath(msg.GetName(), NewAddress(actorSystem.Name(), msg.GetHost(), int(msg.GetPort())))
	pid, exist := actorSystem.actors.get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: pid.ActorPath().RemoteAddress()}), nil
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately. With this type of message the receiver cannot communicate back to Sender
// except reply the message with a response. This one-way communication
func (actorSystem *ActorSystem) RemoteAsk(ctx context.Context, stream *connect.BidiStream[internalpb.RemoteAskRequest, internalpb.RemoteAskResponse]) error {
	logger := actorSystem.logger

	if !actorSystem.clusterEnabled.Load() {
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

		remoteAddr := fmt.Sprintf("%s:%d", actorSystem.host, actorSystem.remotingPort)
		if remoteAddr != net.JoinHostPort(receiver.GetHost(), strconv.Itoa(int(receiver.GetPort()))) {
			return connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
		}

		actorPath := NewPath(name, NewAddress(actorSystem.name, actorSystem.host, int(actorSystem.remotingPort)))
		pid, exist := actorSystem.actors.get(actorPath)
		if !exist {
			logger.Error(ErrAddressNotFound(actorPath.String()).Error())
			return ErrAddressNotFound(actorPath.String())
		}

		timeout := actorSystem.askTimeout
		if request.GetTimeout() != nil {
			timeout = request.GetTimeout().AsDuration()
		}

		reply, err := actorSystem.handleRemoteAsk(ctx, pid, message, timeout)
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
func (actorSystem *ActorSystem) RemoteTell(ctx context.Context, stream *connect.ClientStream[internalpb.RemoteTellRequest]) (*connect.Response[internalpb.RemoteTellResponse], error) {
	logger := actorSystem.logger

	if !actorSystem.clusterEnabled.Load() {
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
					actorSystem.Name(),
					receiver.GetHost(),
					int(receiver.GetPort())))

			pid, exist := actorSystem.actors.get(actorPath)
			if !exist {
				logger.Error(ErrAddressNotFound(actorPath.String()).Error())
				return ErrAddressNotFound(actorPath.String())
			}

			if err := actorSystem.handleRemoteTell(ctx, pid, request.GetRemoteMessage()); err != nil {
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
func (actorSystem *ActorSystem) RemoteReSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteReSpawnRequest]) (*connect.Response[internalpb.RemoteReSpawnResponse], error) {
	logger := actorSystem.logger

	msg := request.Msg

	if !actorSystem.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", actorSystem.host, actorSystem.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorPath := NewPath(msg.GetName(), NewAddress(actorSystem.Name(), msg.GetHost(), int(msg.GetPort())))
	pid, exist := actorSystem.actors.get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	if err := pid.Restart(ctx); err != nil {
		return nil, fmt.Errorf("failed to restart actor=%s: %w", actorPath.String(), err)
	}

	actorSystem.actors.set(pid)
	return connect.NewResponse(new(internalpb.RemoteReSpawnResponse)), nil
}

// RemoteStop stops an actor on a remote machine
func (actorSystem *ActorSystem) RemoteStop(ctx context.Context, request *connect.Request[internalpb.RemoteStopRequest]) (*connect.Response[internalpb.RemoteStopResponse], error) {
	logger := actorSystem.logger

	msg := request.Msg

	if !actorSystem.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", actorSystem.host, actorSystem.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorPath := NewPath(msg.GetName(), NewAddress(actorSystem.Name(), msg.GetHost(), int(msg.GetPort())))
	pid, exist := actorSystem.actors.get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	if err := pid.Shutdown(ctx); err != nil {
		return nil, fmt.Errorf("failed to stop actor=%s: %w", actorPath.String(), err)
	}

	actorSystem.actors.delete(actorPath)
	return connect.NewResponse(new(internalpb.RemoteStopResponse)), nil
}

// RemoteSpawn handles the remoteSpawn call
func (actorSystem *ActorSystem) RemoteSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteSpawnRequest]) (*connect.Response[internalpb.RemoteSpawnResponse], error) {
	logger := actorSystem.logger

	msg := request.Msg
	if !actorSystem.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", actorSystem.host, actorSystem.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actor, err := actorSystem.reflection.ActorFrom(msg.GetActorType())
	if err != nil {
		logger.Errorf("failed to create actor=[(%s) of type (%s)] on [host=%s, port=%d]: reason: (%v)",
			msg.GetActorName(), msg.GetActorType(), msg.GetHost(), msg.GetPort(), err)

		if errors.Is(err, ErrTypeNotRegistered) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, ErrTypeNotRegistered)
		}

		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if _, err = actorSystem.Spawn(ctx, msg.GetActorName(), actor); err != nil {
		logger.Errorf("failed to create actor=(%s) on [host=%s, port=%d]: reason: (%v)", msg.GetActorName(), msg.GetHost(), msg.GetPort(), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	logger.Infof("actor=(%s) successfully created on [host=%s, port=%d]", msg.GetActorName(), msg.GetHost(), msg.GetPort())
	return connect.NewResponse(new(internalpb.RemoteSpawnResponse)), nil
}

// GetNodeMetric handles the GetNodeMetric request send the given node
func (actorSystem *ActorSystem) GetNodeMetric(_ context.Context, request *connect.Request[internalpb.GetNodeMetricRequest]) (*connect.Response[internalpb.GetNodeMetricResponse], error) {
	if !actorSystem.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrClusterDisabled)
	}

	req := request.Msg

	remoteAddr := fmt.Sprintf("%s:%d", actorSystem.host, actorSystem.remotingPort)
	if remoteAddr != req.GetNodeAddress() {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorCount := actorSystem.actors.len()
	return connect.NewResponse(&internalpb.GetNodeMetricResponse{
		NodeRemoteAddress: remoteAddr,
		ActorsCount:       uint64(actorCount),
	}), nil
}

// GetKinds returns the cluster kinds
func (actorSystem *ActorSystem) GetKinds(_ context.Context, request *connect.Request[internalpb.GetKindsRequest]) (*connect.Response[internalpb.GetKindsResponse], error) {
	if !actorSystem.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrClusterDisabled)
	}

	req := request.Msg
	remoteAddr := fmt.Sprintf("%s:%d", actorSystem.host, actorSystem.remotingPort)

	// routine check
	if remoteAddr != req.GetNodeAddress() {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	kinds := make([]string, len(actorSystem.clusterConfig.Kinds()))
	for i, kind := range actorSystem.clusterConfig.Kinds() {
		kinds[i] = types.TypeName(kind)
	}

	return connect.NewResponse(&internalpb.GetKindsResponse{Kinds: kinds}), nil
}

// handleRemoteAsk handles a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (actorSystem *ActorSystem) handleRemoteAsk(ctx context.Context, to *PID, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	return Ask(ctx, to, message, timeout)
}

// handleRemoteTell handles an asynchronous message to an actor
func (actorSystem *ActorSystem) handleRemoteTell(ctx context.Context, to *PID, message proto.Message) error {
	return Tell(ctx, to, message)
}

// getSupervisor return the ActorSystem supervisor
func (actorSystem *ActorSystem) getSupervisor() *PID {
	actorSystem.locker.Lock()
	supervisor := actorSystem.supervisor
	actorSystem.locker.Unlock()
	return supervisor
}

// setActor implements ActorSystem.
func (actorSystem *ActorSystem) setActor(actor *PID) {
	actorSystem.actors.set(actor)
	if actorSystem.clusterEnabled.Load() {
		actorSystem.actorsChan <- &internalpb.WireActor{
			ActorName:    actor.Name(),
			ActorAddress: actor.ActorPath().RemoteAddress(),
			ActorPath:    actor.ActorPath().String(),
			ActorType:    types.TypeName(actor.Actor()),
		}
	}
}

// enableClustering enables clustering. When clustering is enabled remoting is also enabled to facilitate remote
// communication
func (actorSystem *ActorSystem) enableClustering(ctx context.Context) error {
	actorSystem.logger.Info("enabling clustering...")

	node := &discovery.Node{
		Name:         actorSystem.Name(),
		Host:         actorSystem.host,
		GossipPort:   actorSystem.clusterConfig.GossipPort(),
		PeersPort:    actorSystem.clusterConfig.PeersPort(),
		RemotingPort: actorSystem.clusterConfig.RemotingPort(),
	}

	clusterEngine, err := cluster.NewEngine(
		actorSystem.Name(),
		actorSystem.clusterConfig.Discovery(),
		node,
		cluster.WithLogger(actorSystem.logger),
		cluster.WithPartitionsCount(actorSystem.clusterConfig.PartitionCount()),
		cluster.WithHasher(actorSystem.partitionHasher),
		cluster.WithMinimumPeersQuorum(actorSystem.clusterConfig.MinimumPeersQuorum()),
		cluster.WithReplicaCount(actorSystem.clusterConfig.ReplicaCount()),
	)
	if err != nil {
		actorSystem.logger.Errorf("failed to initialize cluster engine: %v", err)
		return err
	}

	actorSystem.logger.Info("starting cluster engine...")
	if err := clusterEngine.Start(ctx); err != nil {
		actorSystem.logger.Errorf("failed to start cluster engine: %v", err)
		return err
	}

	bootstrapChan := make(chan types.Unit, 1)
	timer := time.AfterFunc(time.Second, func() {
		bootstrapChan <- types.Unit{}
	})
	<-bootstrapChan
	timer.Stop()

	actorSystem.logger.Info("cluster engine successfully started...")

	actorSystem.locker.Lock()
	actorSystem.cluster = clusterEngine
	actorSystem.clusterEventsChan = clusterEngine.Events()
	actorSystem.redistributionChan = make(chan *cluster.Event, 1)
	for _, kind := range actorSystem.clusterConfig.Kinds() {
		actorSystem.registry.Register(kind)
		actorSystem.logger.Infof("cluster kind=(%s) registered", types.TypeName(kind))
	}
	actorSystem.locker.Unlock()

	go actorSystem.clusterEventsLoop()
	go actorSystem.replicationLoop()
	go actorSystem.peersStateLoop()
	go actorSystem.redistributionLoop()

	actorSystem.logger.Info("clustering enabled...:)")
	return nil
}

// enableRemoting enables the remoting service to handle remote messaging
func (actorSystem *ActorSystem) enableRemoting(ctx context.Context) {
	actorSystem.logger.Info("enabling remoting...")

	var interceptor *otelconnect.Interceptor
	var err error
	if actorSystem.metricEnabled.Load() {
		interceptor, err = otelconnect.NewInterceptor(
			otelconnect.WithMeterProvider(actorSystem.telemetry.MeterProvider),
		)
		if err != nil {
			actorSystem.logger.Panic(fmt.Errorf("failed to initialize observability feature: %w", err))
		}
	}

	var opts []connect.HandlerOption
	if interceptor != nil {
		opts = append(opts, connect.WithInterceptors(interceptor))
	}

	remotingServicePath, remotingServiceHandler := internalpbconnect.NewRemotingServiceHandler(actorSystem, opts...)
	clusterServicePath, clusterServiceHandler := internalpbconnect.NewClusterServiceHandler(actorSystem, opts...)

	mux := stdhttp.NewServeMux()
	mux.Handle(remotingServicePath, remotingServiceHandler)
	mux.Handle(clusterServicePath, clusterServiceHandler)
	server := http.NewServer(ctx, actorSystem.host, int(actorSystem.remotingPort), mux)

	go func() {
		if err := server.ListenAndServe(); err != nil {
			if !errors.Is(err, stdhttp.ErrServerClosed) {
				actorSystem.logger.Panic(fmt.Errorf("failed to start remoting service: %w", err))
			}
		}
	}()

	actorSystem.locker.Lock()
	actorSystem.remotingServer = server
	actorSystem.locker.Unlock()

	actorSystem.logger.Info("remoting enabled...:)")
}

// reset the actor ActorSystem
func (actorSystem *ActorSystem) reset() {
	actorSystem.telemetry = nil
	actorSystem.actors.reset()
	actorSystem.name = ""
	actorSystem.cluster = nil
}

// janitor time to time removes dead actors from the ActorSystem
// that helps free non-utilized resources
func (actorSystem *ActorSystem) janitor() {
	actorSystem.logger.Info("janitor has started...")
	ticker := time.NewTicker(actorSystem.gcInterval)
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				for _, actor := range actorSystem.Actors() {
					if !actor.IsRunning() {
						actorSystem.logger.Infof("removing actor=%s from ActorSystem", actor.ActorPath().Name())
						actorSystem.actors.delete(actor.ActorPath())
						if actorSystem.inCluster() {
							if err := actorSystem.cluster.RemoveActor(context.Background(), actor.ActorPath().Name()); err != nil {
								actorSystem.logger.Error(err.Error())
								// TODO: stop or continue
							}
						}
					}
				}
			case <-actorSystem.stopGC:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()
	actorSystem.logger.Info("janitor has stopped...")
}

// registerMetrics register the PID metrics with OTel instrumentation.
func (actorSystem *ActorSystem) registerMetrics() error {
	meter := actorSystem.telemetry.Meter
	metrics, err := metric.NewActorSystemMetric(meter)
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		observer.ObserveInt64(metrics.ActorsCount(), int64(actorSystem.ActorsCount()))
		return nil
	}, metrics.ActorsCount())

	return err
}

// replicationLoop publishes newly created actor into the cluster when cluster is enabled
func (actorSystem *ActorSystem) replicationLoop() {
	for actor := range actorSystem.actorsChan {
		// never replicate ActorSystem actors because there are specific to the
		// running node
		if isSystemName(actor.GetActorName()) {
			continue
		}
		if actorSystem.inCluster() {
			ctx := context.Background()
			if err := actorSystem.cluster.PutActor(ctx, actor); err != nil {
				actorSystem.logger.Panic(err.Error())
			}
		}
	}
}

// clusterEventsLoop listens to cluster events and send them to the event streams
func (actorSystem *ActorSystem) clusterEventsLoop() {
	for event := range actorSystem.clusterEventsChan {
		if actorSystem.inCluster() {
			if event != nil && event.Payload != nil {
				// push the event to the event stream
				message, _ := event.Payload.UnmarshalNew()
				if actorSystem.eventsStream != nil {
					actorSystem.logger.Debugf("node=(%s) publishing cluster event=(%s)....", actorSystem.name, event.Type)
					actorSystem.eventsStream.Publish(eventsTopic, message)
					actorSystem.logger.Debugf("cluster event=(%s) successfully published by node=(%s)", event.Type, actorSystem.name)
				}
				actorSystem.redistributionChan <- event
			}
		}
	}
}

// peersStateLoop fetches the cluster peers' PeerState and update the node peersCache
func (actorSystem *ActorSystem) peersStateLoop() {
	actorSystem.logger.Info("peers state synchronization has started...")
	ticker := time.NewTicker(actorSystem.peersStateSyncInterval)
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				eg, ctx := errgroup.WithContext(context.Background())
				peersChan := make(chan *cluster.Peer)
				eg.Go(func() error {
					defer close(peersChan)
					peers, err := actorSystem.cluster.Peers(ctx)
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
						if err := actorSystem.processPeerState(ctx, peer); err != nil {
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
					actorSystem.logger.Error(err)
					// TODO: stop or panic
				}

			case <-actorSystem.clusterSyncStopSig:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()
	actorSystem.logger.Info("peers state synchronization has stopped...")
}

func (actorSystem *ActorSystem) redistributionLoop() {
	for event := range actorSystem.redistributionChan {
		if actorSystem.inCluster() {
			// check for cluster rebalancing
			if err := actorSystem.redistribute(context.Background(), event); err != nil {
				actorSystem.logger.Errorf("cluster rebalancing failed: %v", err)
				// TODO: panic or retry or shutdown actor ActorSystem
			}
		}
	}
}

// processPeerState processes a given peer synchronization record.
func (actorSystem *ActorSystem) processPeerState(ctx context.Context, peer *cluster.Peer) error {
	actorSystem.peersCacheMu.Lock()
	defer actorSystem.peersCacheMu.Unlock()

	peerAddress := net.JoinHostPort(peer.Host, strconv.Itoa(peer.Port))

	actorSystem.logger.Infof("processing peer sync:(%s)", peerAddress)
	peerState, err := actorSystem.cluster.GetState(ctx, peerAddress)
	if err != nil {
		if errors.Is(err, cluster.ErrPeerSyncNotFound) {
			return nil
		}
		actorSystem.logger.Error(err)
		return err
	}

	actorSystem.logger.Debugf("peer (%s) actors count (%d)", peerAddress, len(peerState.GetActors()))

	// no need to handle the error
	bytea, err := proto.Marshal(peerState)
	if err != nil {
		actorSystem.logger.Error(err)
		return err
	}

	actorSystem.peersCache[peerAddress] = bytea
	actorSystem.logger.Infof("peer sync(%s) successfully processed", peerAddress)
	return nil
}

// configPID constructs a PID provided the actor name and the actor kind
// this is a utility function used when spawning actors
func (actorSystem *ActorSystem) configPID(ctx context.Context, name string, actor Actor) (*PID, error) {
	actorPath := NewPath(name, NewAddress(actorSystem.name, "", -1))
	if actorSystem.clusterEnabled.Load() {
		actorPath = NewPath(name, NewAddress(actorSystem.name, actorSystem.host, int(actorSystem.remotingPort)))
	}

	// define the pid options
	// pid inherit the actor ActorSystem settings defined during instantiation
	pidOpts := []pidOption{
		withInitMaxRetries(actorSystem.actorInitMaxRetries),
		withAskTimeout(actorSystem.askTimeout),
		withCustomLogger(actorSystem.logger),
		withActorSystem(actorSystem),
		withSupervisorDirective(actorSystem.supervisorDirective),
		withEventsStream(actorSystem.eventsStream),
		withInitTimeout(actorSystem.actorInitTimeout),
		withTelemetry(actorSystem.telemetry),
	}

	// enable stash
	if actorSystem.stashEnabled {
		pidOpts = append(pidOpts, withStash())
	}

	// disable passivation for ActorSystem supervisor
	if isSystemName(name) {
		pidOpts = append(pidOpts, withPassivationDisabled())
	} else {
		pidOpts = append(pidOpts, withPassivationAfter(actorSystem.expireActorAfter))
	}

	if actorSystem.metricEnabled.Load() {
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

// getSystemActorName returns the ActorSystem supervisor name
func (actorSystem *ActorSystem) getSystemActorName(nameType nameType) string {
	if actorSystem.clusterEnabled.Load() {
		return fmt.Sprintf("%s%s%s-%d-%d",
			systemNames[nameType],
			strings.ToTitle(actorSystem.name),
			actorSystem.host,
			actorSystem.remotingPort,
			time.Now().UnixNano())
	}
	return fmt.Sprintf("%s%s-%d",
		systemNames[nameType],
		strings.ToTitle(actorSystem.name),
		time.Now().UnixNano())
}

func isSystemName(name string) bool {
	return strings.HasPrefix(name, systemNamePrefix)
}

// ActorOf returns an existing actor in the local ActorSystem or in the cluster when clustering is enabled
// When cluster mode is activated, the PID will be nil.
// An actor not found error is return when the actor is not found.
func (actorSystem *ActorSystem) actorOf(ctx context.Context, actorName string) (addr *goaktpb.Address, pid *PID, err error) {
	actorSystem.locker.Lock()

	if !actorSystem.started.Load() {
		actorSystem.locker.Unlock()
		return nil, nil, ErrActorSystemNotStarted
	}

	// first check whether the actor exists locally
	actorPath := actorSystem.actorPath(actorName)
	if pid, ok := actorSystem.actors.get(actorPath); ok {
		actorSystem.locker.Unlock()
		return pid.ActorPath().RemoteAddress(), pid, nil
	}

	// check in the cluster
	if actorSystem.clusterEnabled.Load() {
		actor, err := actorSystem.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				actorSystem.logger.Infof("actor=%s not found", actorName)
				actorSystem.locker.Unlock()
				return nil, nil, ErrActorNotFound(actorName)
			}

			actorSystem.locker.Unlock()
			return nil, nil, fmt.Errorf("failed to fetch remote actor=%s: %w", actorName, err)
		}

		actorSystem.locker.Unlock()
		return actor.GetActorAddress(), nil, nil
	}

	actorSystem.logger.Infof("actor=%s not found", actorName)
	actorSystem.locker.Unlock()
	return nil, nil, ErrActorNotFound(actorName)
}

// actorPath returns the actor path provided the actor name
func (actorSystem *ActorSystem) actorPath(name string) *Path {
	return NewPath(name, NewAddress(actorSystem.name, actorSystem.host, int(actorSystem.remotingPort)))
}

// inCluster states whether the actor ActorSystem is running within a cluster of nodes
func (actorSystem *ActorSystem) inCluster() bool {
	return actorSystem.clusterEnabled.Load() && actorSystem.cluster != nil
}

// setRemotingHostAndPort sets the appropriate remoting host and port configuration
func (actorSystem *ActorSystem) setRemotingHostAndPort() error {
	if actorSystem.clusterEnabled.Load() {
		if err := actorSystem.clusterConfig.Validate(); err != nil {
			return err
		}

		actorSystem.remotingPort = int32(actorSystem.clusterConfig.RemotingPort())
		if actorSystem.host == "" {
			actorSystem.host, _ = os.Hostname()
		}

		remotingHost, remotingPort, err := tcp.GetHostPort(fmt.Sprintf("%s:%d", actorSystem.host, actorSystem.remotingPort))
		if err != nil {
			actorSystem.logger.Panic(fmt.Errorf("failed to resolve remoting TCP address: %w", err))
		}

		actorSystem.host = remotingHost
		actorSystem.remotingPort = int32(remotingPort)
	}
	return nil
}
