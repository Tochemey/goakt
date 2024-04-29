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

package actors

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"regexp"
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/atomic"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/goaktpb"
	"github.com/tochemey/goakt/hash"
	"github.com/tochemey/goakt/internal/cluster"
	"github.com/tochemey/goakt/internal/eventstream"
	"github.com/tochemey/goakt/internal/internalpb"
	"github.com/tochemey/goakt/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/internal/metric"
	"github.com/tochemey/goakt/internal/types"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/telemetry"
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
	// SpawnFromFunc creates an actor with the given receive function. One can set the PreStart and PostStop lifecycle hooks
	// in the given optional options
	SpawnFromFunc(ctx context.Context, receiveFunc ReceiveFunc, opts ...FuncOption) (PID, error)
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
	RemoteActor(ctx context.Context, actorName string) (addr *goaktpb.Address, err error)
	// ActorOf returns an existing actor in the local system or in the cluster when clustering is enabled
	// When cluster mode is activated, the PID will be nil.
	// When remoting is enabled this method will return and error
	// An actor not found error is return when the actor is not found.
	ActorOf(ctx context.Context, actorName string) (addr *goaktpb.Address, pid PID, err error)
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
	ScheduleOnce(ctx context.Context, message proto.Message, pid PID, interval time.Duration) error
	// RemoteScheduleOnce schedules a message to be sent to a remote actor in the future.
	// This requires remoting to be enabled on the actor system.
	// This will send the given message to the actor after the given interval specified
	// The message will be sent once
	RemoteScheduleOnce(ctx context.Context, message proto.Message, address *goaktpb.Address, interval time.Duration) error
	// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
	ScheduleWithCron(ctx context.Context, message proto.Message, pid PID, cronExpression string) error
	// RemoteScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
	RemoteScheduleWithCron(ctx context.Context, message proto.Message, address *goaktpb.Address, cronExpression string) error
	// PeerAddress returns the actor system address known in the cluster. That address is used by other nodes to communicate with the actor system.
	// This address is empty when cluster mode is not activated
	PeerAddress() string
	// Register register an actor for future use. This is necessary when creating an actor remotely
	Register(ctx context.Context, actor Actor) error
	// Deregister removes a registered actor from the registry
	Deregister(ctx context.Context, actor Actor) error
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
	started atomic.Bool

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
	// Specifies the actors initialization timeout
	// The default value is 1s
	actorInitTimeout time.Duration
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
	discoveryProvider discovery.Provider
	// define the number of partitions to shard the actors in the cluster
	partitionsCount uint64
	// cluster mode
	cluster     cluster.Interface
	clusterChan chan *internalpb.WireActor
	clusterPort int
	gossipPort  int

	partitionHasher hash.Hasher

	// help protect some the fields to set
	mutex sync.Mutex
	// specifies actors mailbox size
	mailboxSize uint64
	// specifies the mailbox to use for the actors
	mailbox Mailbox
	// specifies the stash capacity
	stashCapacity uint64

	housekeeperStopSig chan types.Unit
	clusterSyncStopSig chan types.Unit

	// specifies the events stream
	eventsStream *eventstream.EventsStream

	// specifies the message scheduler
	scheduler *scheduler
	// specifies whether tracing is enabled
	traceEnabled atomic.Bool
	tracer       trace.Tracer

	// specifies whether metric is enabled
	metricEnabled atomic.Bool
	eventsChan    <-chan *cluster.Event

	registry   registry
	reflection reflection
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
		actors:              newPIDMap(10),
		clusterChan:         make(chan *internalpb.WireActor, 10),
		name:                name,
		logger:              log.DefaultLogger,
		expireActorAfter:    DefaultPassivationTimeout,
		replyTimeout:        DefaultReplyTimeout,
		actorInitMaxRetries: DefaultInitMaxRetries,
		supervisorStrategy:  DefaultSupervisoryStrategy,
		telemetry:           telemetry.New(),
		mutex:               sync.Mutex{},
		shutdownTimeout:     DefaultShutdownTimeout,
		mailboxSize:         defaultMailboxSize,
		housekeeperStopSig:  make(chan types.Unit, 1),
		eventsStream:        eventstream.New(),
		partitionHasher:     hash.DefaultHasher(),
		actorInitTimeout:    DefaultInitTimeout,
		tracer:              noop.NewTracerProvider().Tracer(name),
		eventsChan:          make(chan *cluster.Event, 1),
		registry:            newRegistry(),
		clusterSyncStopSig:  make(chan types.Unit, 1),
	}

	system.started.Store(false)
	system.remotingEnabled.Store(false)
	system.clusterEnabled.Store(false)
	system.traceEnabled.Store(false)
	system.metricEnabled.Store(false)

	system.reflection = newReflection(system.registry)

	// apply the various options
	for _, opt := range opts {
		opt.Apply(system)
	}

	if system.traceEnabled.Load() {
		system.tracer = system.telemetry.Tracer
	}

	system.scheduler = newScheduler(system.logger, system.shutdownTimeout, withSchedulerCluster(system.cluster))

	if system.metricEnabled.Load() {
		if err := system.registerMetrics(); err != nil {
			return nil, err
		}
	}

	return system, nil
}

// Deregister removes a registered actor from the registry
func (x *actorSystem) Deregister(ctx context.Context, actor Actor) error {
	_, span := x.tracer.Start(ctx, "Deregister")
	defer span.End()

	x.mutex.Lock()
	defer x.mutex.Unlock()

	if !x.started.Load() {
		return ErrActorSystemNotStarted
	}

	x.registry.Deregister(actor)
	return nil
}

// Register registers an actor for future use. This is necessary when creating an actor remotely
func (x *actorSystem) Register(ctx context.Context, actor Actor) error {
	_, span := x.tracer.Start(ctx, "Register")
	defer span.End()

	x.mutex.Lock()
	defer x.mutex.Unlock()

	if !x.started.Load() {
		return ErrActorSystemNotStarted
	}

	x.registry.Register(actor)
	return nil
}

// ScheduleOnce schedules a message that will be delivered to the receiver actor
// This will send the given message to the actor after the given interval specified.
// The message will be sent once
func (x *actorSystem) ScheduleOnce(ctx context.Context, message proto.Message, pid PID, interval time.Duration) error {
	spanCtx, span := x.tracer.Start(ctx, "ScheduleOnce")
	defer span.End()
	return x.scheduler.ScheduleOnce(spanCtx, message, pid, interval)
}

// RemoteScheduleOnce schedules a message to be sent to a remote actor in the future.
// This requires remoting to be enabled on the actor system.
// This will send the given message to the actor after the given interval specified
// The message will be sent once
func (x *actorSystem) RemoteScheduleOnce(ctx context.Context, message proto.Message, address *goaktpb.Address, interval time.Duration) error {
	spanCtx, span := x.tracer.Start(ctx, "RemoteScheduleOnce")
	defer span.End()
	return x.scheduler.RemoteScheduleOnce(spanCtx, message, address, interval)
}

// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (x *actorSystem) ScheduleWithCron(ctx context.Context, message proto.Message, pid PID, cronExpression string) error {
	spanCtx, span := x.tracer.Start(ctx, "ScheduleWithCron")
	defer span.End()
	return x.scheduler.ScheduleWithCron(spanCtx, message, pid, cronExpression)
}

// RemoteScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (x *actorSystem) RemoteScheduleWithCron(ctx context.Context, message proto.Message, address *goaktpb.Address, cronExpression string) error {
	spanCtx, span := x.tracer.Start(ctx, "RemoteScheduleWithCron")
	defer span.End()
	return x.scheduler.RemoteScheduleWithCron(spanCtx, message, address, cronExpression)
}

// Subscribe help receive dead letters whenever there are available
func (x *actorSystem) Subscribe() (eventstream.Subscriber, error) {
	if !x.started.Load() {
		return nil, ErrActorSystemNotStarted
	}
	subscriber := x.eventsStream.AddSubscriber()
	x.eventsStream.Subscribe(subscriber, eventsTopic)
	return subscriber, nil
}

// Unsubscribe unsubscribes a subscriber.
func (x *actorSystem) Unsubscribe(subscriber eventstream.Subscriber) error {
	if !x.started.Load() {
		return ErrActorSystemNotStarted
	}
	x.eventsStream.Unsubscribe(subscriber, eventsTopic)
	x.eventsStream.RemoveSubscriber(subscriber)
	return nil
}

// GetPartition returns the partition where a given actor is located
func (x *actorSystem) GetPartition(actorName string) uint64 {
	if !x.InCluster() {
		// TODO: maybe add a partitioner function
		return 0
	}
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
	spanCtx, span := x.tracer.Start(ctx, "Spawn")
	defer span.End()

	if !x.started.Load() {
		span.SetStatus(codes.Error, "Spawn")
		span.RecordError(ErrDead)
		return nil, ErrActorSystemNotStarted
	}

	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(x.name, "", -1))
	if x.remotingEnabled.Load() {
		actorPath = NewPath(name, NewAddress(x.name, x.remotingHost, int(x.remotingPort)))
	}

	pid, exist := x.actors.Get(actorPath)
	if exist {
		if pid.IsRunning() {
			// return the existing instance
			return pid, nil
		}
	}

	// define the pid options
	// pid inherit the actor system settings defined during instantiation
	opts := []pidOption{
		withInitMaxRetries(x.actorInitMaxRetries),
		withPassivationAfter(x.expireActorAfter),
		withSendReplyTimeout(x.replyTimeout),
		withCustomLogger(x.logger),
		withActorSystem(x),
		withSupervisorStrategy(x.supervisorStrategy),
		withMailboxSize(x.mailboxSize),
		withMailbox(x.mailbox),
		withStash(x.stashCapacity),
		withEventsStream(x.eventsStream),
		withInitTimeout(x.actorInitTimeout),
		withTelemetry(x.telemetry),
	}

	if x.traceEnabled.Load() {
		opts = append(opts, withTracing())
	}

	if x.metricEnabled.Load() {
		opts = append(opts, withMetric())
	}

	pid, err := newPID(spanCtx,
		actorPath,
		actor,
		opts...)

	if err != nil {
		span.SetStatus(codes.Error, "Spawn")
		span.RecordError(err)
		return nil, err
	}

	x.actors.Set(pid)
	x.registry.RegisterWithKey(name, actor)

	if x.clusterEnabled.Load() {
		actorType := reflect.TypeOf(actor).Elem().Name()
		x.clusterChan <- &internalpb.WireActor{
			ActorName:    name,
			ActorAddress: actorPath.RemoteAddress(),
			ActorPath:    actorPath.String(),
			ActorType:    actorType,
		}
	}

	return pid, nil
}

// SpawnFromFunc creates an actor with the given receive function.
func (x *actorSystem) SpawnFromFunc(ctx context.Context, receiveFunc ReceiveFunc, opts ...FuncOption) (PID, error) {
	spanCtx, span := x.tracer.Start(ctx, "SpawnFromFunc")
	defer span.End()

	if !x.started.Load() {
		span.SetStatus(codes.Error, "SpawnFromFunc")
		span.RecordError(ErrDead)
		return nil, ErrActorSystemNotStarted
	}

	actorID := uuid.NewString()
	actor := newFnActor(actorID, receiveFunc, opts...)

	actorPath := NewPath(actorID, NewAddress(x.name, "", -1))
	if x.remotingEnabled.Load() {
		actorPath = NewPath(actorID, NewAddress(x.name, x.remotingHost, int(x.remotingPort)))
	}

	// define the pid options
	// pid inherit the actor system settings defined during instantiation
	pidOpts := []pidOption{
		withInitMaxRetries(x.actorInitMaxRetries),
		withPassivationAfter(x.expireActorAfter),
		withSendReplyTimeout(x.replyTimeout),
		withCustomLogger(x.logger),
		withActorSystem(x),
		withSupervisorStrategy(x.supervisorStrategy),
		withMailboxSize(x.mailboxSize),
		withMailbox(x.mailbox),
		withStash(x.stashCapacity),
		withEventsStream(x.eventsStream),
		withInitTimeout(x.actorInitTimeout),
		withTelemetry(x.telemetry),
	}

	if x.traceEnabled.Load() {
		pidOpts = append(pidOpts, withTracing())
	}

	if x.metricEnabled.Load() {
		pidOpts = append(pidOpts, withMetric())
	}

	pid, err := newPID(spanCtx,
		actorPath,
		actor,
		pidOpts...)

	if err != nil {
		span.SetStatus(codes.Error, "Spawn")
		span.RecordError(err)
		return nil, err
	}

	x.actors.Set(pid)
	x.registry.RegisterWithKey(actorID, actor)

	if x.clusterEnabled.Load() {
		actorType := reflect.TypeOf(actor).Elem().Name()
		x.clusterChan <- &internalpb.WireActor{
			ActorName:    actorID,
			ActorAddress: actorPath.RemoteAddress(),
			ActorPath:    actorPath.String(),
			ActorType:    actorType,
		}
	}

	return pid, nil
}

// Kill stops a given actor in the system
func (x *actorSystem) Kill(ctx context.Context, name string) error {
	spanCtx, span := x.tracer.Start(ctx, "Kill")
	defer span.End()

	if !x.started.Load() {
		span.SetStatus(codes.Error, "Kill")
		span.RecordError(ErrActorSystemNotStarted)
		return ErrActorSystemNotStarted
	}

	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(x.name, "", -1))
	if x.remotingEnabled.Load() {
		actorPath = NewPath(name, NewAddress(x.name, x.remotingHost, int(x.remotingPort)))
	}

	pid, exist := x.actors.Get(actorPath)
	if exist {
		// stop the given actor. No need to record error in the span context
		// because the shutdown method is taking care of that
		return pid.Shutdown(spanCtx)
	}

	err := ErrActorNotFound(actorPath.String())
	span.SetStatus(codes.Error, "Kill")
	span.RecordError(err)
	return err
}

// ReSpawn recreates a given actor in the system
func (x *actorSystem) ReSpawn(ctx context.Context, name string) (PID, error) {
	spanCtx, span := x.tracer.Start(ctx, "ReSpawn")
	defer span.End()

	if !x.started.Load() {
		span.SetStatus(codes.Error, "ReSpawn")
		span.RecordError(ErrActorSystemNotStarted)
		return nil, ErrActorSystemNotStarted
	}

	actorPath := NewPath(name, NewAddress(x.name, "", -1))

	if x.remotingEnabled.Load() {
		actorPath = NewPath(name, NewAddress(x.name, x.remotingHost, int(x.remotingPort)))
	}

	pid, exist := x.actors.Get(actorPath)
	if exist {
		if err := pid.Restart(spanCtx); err != nil {
			return nil, errors.Wrapf(err, "failed to restart actor=%s", actorPath.String())
		}

		x.actors.Set(pid)
		return pid, nil
	}

	err := ErrActorNotFound(actorPath.String())
	span.SetStatus(codes.Error, "ReSpawn")
	span.RecordError(err)
	return nil, err
}

// Name returns the actor system name
func (x *actorSystem) Name() string {
	x.mutex.Lock()
	defer x.mutex.Unlock()
	return x.name
}

// Actors returns the list of Actors that are alive in the actor system
func (x *actorSystem) Actors() []PID {
	x.mutex.Lock()
	defer x.mutex.Unlock()
	return x.actors.List()
}

// PeerAddress returns the actor system address known in the cluster. That address is used by other nodes to communicate with the actor system.
// This address is empty when cluster mode is not activated
func (x *actorSystem) PeerAddress() string {
	x.mutex.Lock()
	defer x.mutex.Unlock()
	if x.clusterEnabled.Load() {
		return x.cluster.AdvertisedAddress()
	}
	return ""
}

// ActorOf returns an existing actor in the local system or in the cluster when clustering is enabled
// When cluster mode is activated, the PID will be nil.
// When remoting is enabled this method will return and error
// An actor not found error is return when the actor is not found.
func (x *actorSystem) ActorOf(ctx context.Context, actorName string) (addr *goaktpb.Address, pid PID, err error) {
	spanCtx, span := x.tracer.Start(ctx, "ActorOf")
	defer span.End()

	x.mutex.Lock()
	defer x.mutex.Unlock()

	if !x.started.Load() {
		span.SetStatus(codes.Error, "ActorOf")
		span.RecordError(ErrActorSystemNotStarted)
		return nil, nil, ErrActorSystemNotStarted
	}

	// first check whether the actor exist locally
	items := x.actors.List()
	for _, actorRef := range items {
		if actorRef.ActorPath().Name() == actorName {
			return actorRef.ActorPath().RemoteAddress(), actorRef, nil
		}
	}

	// check in the cluster
	if x.cluster != nil || x.clusterEnabled.Load() {
		wireActor, err := x.cluster.GetActor(spanCtx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				x.logger.Infof("actor=%s not found", actorName)
				e := ErrActorNotFound(actorName)
				span.SetStatus(codes.Error, "ActorOf")
				span.RecordError(e)
				return nil, nil, e
			}

			e := errors.Wrapf(err, "failed to fetch remote actor=%s", actorName)
			span.SetStatus(codes.Error, "ActorOf")
			span.RecordError(e)
			return nil, nil, e
		}

		return wireActor.GetActorAddress(), nil, nil
	}

	if x.remotingEnabled.Load() {
		span.SetStatus(codes.Error, "ActorOf")
		span.RecordError(ErrMethodCallNotAllowed)
		return nil, nil, ErrMethodCallNotAllowed
	}

	x.logger.Infof("actor=%s not found", actorName)
	e := ErrActorNotFound(actorName)
	span.SetStatus(codes.Error, "ActorOf")
	span.RecordError(e)
	return nil, nil, e
}

// LocalActor returns the reference of a local actor.
// A local actor is an actor that reside on the same node where the given actor system is running
func (x *actorSystem) LocalActor(actorName string) (PID, error) {
	x.mutex.Lock()
	defer x.mutex.Unlock()

	if !x.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	items := x.actors.List()
	for _, actorRef := range items {
		if actorRef.ActorPath().Name() == actorName {
			return actorRef, nil
		}
	}

	x.logger.Infof("actor=%s not found", actorName)
	return nil, ErrActorNotFound(actorName)
}

// RemoteActor returns the address of a remote actor when cluster is enabled
// When the cluster mode is not enabled an actor not found error will be returned
// One can always check whether cluster is enabled before calling this method or just use the ActorOf method.
func (x *actorSystem) RemoteActor(ctx context.Context, actorName string) (addr *goaktpb.Address, err error) {
	spanCtx, span := x.tracer.Start(ctx, "RemoteActor")
	defer span.End()

	x.mutex.Lock()
	defer x.mutex.Unlock()

	if !x.started.Load() {
		e := ErrActorSystemNotStarted
		span.SetStatus(codes.Error, "RemoteActor")
		span.RecordError(e)
		return nil, e
	}

	if x.cluster == nil {
		e := ErrClusterDisabled
		span.SetStatus(codes.Error, "RemoteActor")
		span.RecordError(e)
		return nil, e
	}

	wireActor, err := x.cluster.GetActor(spanCtx, actorName)
	if err != nil {
		if errors.Is(err, cluster.ErrActorNotFound) {
			x.logger.Infof("actor=%s not found", actorName)
			e := ErrActorNotFound(actorName)
			span.SetStatus(codes.Error, "RemoteActor")
			span.RecordError(e)
			return nil, e
		}

		e := errors.Wrapf(err, "failed to fetch remote actor=%s", actorName)
		span.SetStatus(codes.Error, "RemoteActor")
		span.RecordError(e)
		return nil, e
	}

	return wireActor.GetActorAddress(), nil
}

// Start starts the actor system
func (x *actorSystem) Start(ctx context.Context) error {
	spanCtx, span := x.tracer.Start(ctx, "Start")
	defer span.End()

	x.started.Store(true)

	if x.remotingEnabled.Load() {
		x.enableRemoting(spanCtx)
	}

	if x.clusterEnabled.Load() {
		if err := x.enableClustering(spanCtx); err != nil {
			return err
		}
		// start cluster synchronization
		// TODO: revisit this
		// go x.runClusterSync()
	}

	x.scheduler.Start(spanCtx)

	go x.housekeeper()

	x.logger.Infof("%s started..:)", x.name)
	return nil
}

// Stop stops the actor system
func (x *actorSystem) Stop(ctx context.Context) error {
	spanCtx, span := x.tracer.Start(ctx, "Stop")
	defer span.End()

	// make sure the actor system has started
	if !x.started.Load() {
		e := ErrActorSystemNotStarted
		span.SetStatus(codes.Error, "Stop")
		span.RecordError(e)
		return e
	}

	x.housekeeperStopSig <- types.Unit{}
	x.logger.Infof("%s is shutting down..:)", x.name)

	x.started.Store(false)
	x.scheduler.Stop(spanCtx)

	if x.eventsStream != nil {
		x.eventsStream.Shutdown()
	}

	ctx, cancel := context.WithTimeout(spanCtx, x.shutdownTimeout)
	defer cancel()

	if x.remotingEnabled.Load() {
		if err := x.remotingServer.Shutdown(spanCtx); err != nil {
			span.SetStatus(codes.Error, "Stop")
			span.RecordError(err)
			return err
		}

		x.remotingEnabled.Store(false)
		x.remotingServer = nil
	}

	if x.clusterEnabled.Load() {
		if err := x.cluster.Stop(spanCtx); err != nil {
			span.SetStatus(codes.Error, "Stop")
			span.RecordError(err)
			return err
		}
		close(x.clusterChan)
		x.clusterSyncStopSig <- types.Unit{}
		x.clusterEnabled.Store(false)
	}

	if len(x.Actors()) == 0 {
		x.logger.Info("No online actors to shutdown. Shutting down successfully done")
		return nil
	}

	for _, actor := range x.Actors() {
		x.actors.Delete(actor.ActorPath())
		if err := actor.Shutdown(ctx); err != nil {
			x.reset()
			return err
		}
	}

	x.reset()
	return nil
}

// RemoteLookup for an actor on a remote host.
func (x *actorSystem) RemoteLookup(_ context.Context, request *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	logger := x.logger

	reqCopy := request.Msg
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	actorPath := NewPath(reqCopy.GetName(), NewAddress(x.Name(), reqCopy.GetHost(), int(reqCopy.GetPort())))
	pid, exist := x.actors.Get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	addr := pid.ActorPath().RemoteAddress()

	return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: addr}), nil
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately. With this type of message the receiver cannot communicate back to Sender
// except reply the message with a response. This one-way communication
func (x *actorSystem) RemoteAsk(ctx context.Context, stream *connect.BidiStream[internalpb.RemoteAskRequest, internalpb.RemoteAskResponse]) error {
	logger := x.logger

	if !x.remotingEnabled.Load() {
		return connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	for {
		switch ctx.Err() {
		case context.Canceled:
			return connect.NewError(connect.CodeCanceled, ctx.Err())
		case context.DeadlineExceeded:
			return connect.NewError(connect.CodeDeadlineExceeded, ctx.Err())
		}

		request, err := stream.Receive()
		if IsEOF(err) {
			return nil
		}

		if err != nil {
			logger.Error(err)
			return connect.NewError(connect.CodeUnknown, err)
		}

		message := request.GetRemoteMessage()

		name := message.GetReceiver().GetName()
		actorPath := NewPath(name, NewAddress(x.name, x.remotingHost, int(x.remotingPort)))

		pid, exist := x.actors.Get(actorPath)
		if !exist {
			logger.Error(ErrAddressNotFound(actorPath.String()).Error())
			return ErrAddressNotFound(actorPath.String())
		}

		reply, err := x.handleRemoteAsk(ctx, pid, message)
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
func (x *actorSystem) RemoteTell(ctx context.Context, stream *connect.ClientStream[internalpb.RemoteTellRequest]) (*connect.Response[internalpb.RemoteTellResponse], error) {
	logger := x.logger

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	requestc := make(chan *internalpb.RemoteTellRequest, 1)
	eg, ctx := errgroup.WithContext(ctx)

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
					x.Name(),
					receiver.GetHost(),
					int(receiver.GetPort())))

			pid, exist := x.actors.Get(actorPath)
			if !exist {
				logger.Error(ErrAddressNotFound(actorPath.String()).Error())
				return ErrAddressNotFound(actorPath.String())
			}

			if err := x.handleRemoteTell(ctx, pid, request.GetRemoteMessage()); err != nil {
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
func (x *actorSystem) RemoteReSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteReSpawnRequest]) (*connect.Response[internalpb.RemoteReSpawnResponse], error) {
	logger := x.logger

	reqCopy := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	actorPath := NewPath(reqCopy.GetName(), NewAddress(x.Name(), reqCopy.GetHost(), int(reqCopy.GetPort())))
	pid, exist := x.actors.Get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	if err := pid.Restart(ctx); err != nil {
		return nil, errors.Wrapf(err, "failed to restart actor=%s", actorPath.String())
	}

	x.actors.Set(pid)
	return connect.NewResponse(new(internalpb.RemoteReSpawnResponse)), nil
}

// RemoteStop stops an actor on a remote machine
func (x *actorSystem) RemoteStop(ctx context.Context, request *connect.Request[internalpb.RemoteStopRequest]) (*connect.Response[internalpb.RemoteStopResponse], error) {
	logger := x.logger

	reqCopy := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	actorPath := NewPath(reqCopy.GetName(), NewAddress(x.Name(), reqCopy.GetHost(), int(reqCopy.GetPort())))
	pid, exist := x.actors.Get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	if err := pid.Shutdown(ctx); err != nil {
		return nil, errors.Wrapf(err, "failed to stop actor=%s", actorPath.String())
	}

	x.actors.Delete(actorPath)
	return connect.NewResponse(new(internalpb.RemoteStopResponse)), nil
}

// RemoteSpawn handles the remoteSpawn call
func (x *actorSystem) RemoteSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteSpawnRequest]) (*connect.Response[internalpb.RemoteSpawnResponse], error) {
	logger := x.logger

	req := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	actor, err := x.reflection.ActorFrom(req.GetActorType())
	if err != nil {
		logger.Errorf("failed to create actor=(%s) on [host=%s, port=%d]: reason: (%v)", req.GetActorName(), req.GetHost(), req.GetPort(), err)
		if errors.Is(err, ErrTypeNotRegistered) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, ErrTypeNotRegistered)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	_, err = x.Spawn(ctx, req.GetActorName(), actor)
	if err != nil {
		logger.Errorf("failed to create actor=(%s) on [host=%s, port=%d]: reason: (%v)", req.GetActorName(), req.GetHost(), req.GetPort(), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(new(internalpb.RemoteSpawnResponse)), nil
}

// handleRemoteAsk handles a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (x *actorSystem) handleRemoteAsk(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
	spanCtx, span := x.tracer.Start(ctx, "HandleRemoteAsk")
	defer span.End()
	return Ask(spanCtx, to, message, x.replyTimeout)
}

// handleRemoteTell handles an asynchronous message to an actor
func (x *actorSystem) handleRemoteTell(ctx context.Context, to PID, message proto.Message) error {
	spanCtx, span := x.tracer.Start(ctx, "HandleRemoteTell")
	defer span.End()
	return Tell(spanCtx, to, message)
}

// enableClustering enables clustering. When clustering is enabled remoting is also enabled to facilitate remote
// communication
func (x *actorSystem) enableClustering(ctx context.Context) error {
	x.logger.Info("enabling clustering...")

	if !x.remotingEnabled.Load() {
		x.logger.Error("clustering needs remoting to be enabled")
		return errors.New("clustering needs remoting to be enabled")
	}

	hostNode := discovery.Node{
		Name:         x.name,
		Host:         x.remotingHost,
		GossipPort:   x.gossipPort,
		ClusterPort:  x.clusterPort,
		RemotingPort: int(x.remotingPort),
	}

	cluster, err := cluster.NewNode(x.Name(),
		x.discoveryProvider,
		&hostNode,
		cluster.WithLogger(x.logger),
		cluster.WithPartitionsCount(x.partitionsCount),
		cluster.WithHasher(x.partitionHasher),
	)
	if err != nil {
		x.logger.Error(errors.Wrap(err, "failed to initialize cluster engine"))
		return err
	}

	x.logger.Info("starting cluster engine...")
	if err := cluster.Start(ctx); err != nil {
		x.logger.Error(errors.Wrap(err, "failed to start cluster engine"))
		return err
	}

	bootstrapChan := make(chan struct{}, 1)
	timer := time.AfterFunc(time.Second, func() {
		bootstrapChan <- struct{}{}
	})
	<-bootstrapChan
	timer.Stop()

	x.logger.Info("cluster engine successfully started...")

	x.mutex.Lock()
	x.cluster = cluster
	x.eventsChan = cluster.Events()
	x.mutex.Unlock()

	go x.broadcastClusterEvents()
	go x.broadcast(ctx)

	x.logger.Info("clustering enabled...:)")
	return nil
}

// enableRemoting enables the remoting service to handle remote messaging
func (x *actorSystem) enableRemoting(ctx context.Context) {
	x.logger.Info("enabling remoting...")

	var interceptor *otelconnect.Interceptor
	var err error
	if x.metricEnabled.Load() || x.traceEnabled.Load() {
		interceptor, err = otelconnect.NewInterceptor(
			otelconnect.WithTracerProvider(x.telemetry.TracerProvider),
			otelconnect.WithMeterProvider(x.telemetry.MeterProvider),
		)
		if err != nil {
			x.logger.Panic(errors.Wrap(err, "failed to initialize observability feature"))
		}
	}

	var opts []connect.HandlerOption
	if interceptor != nil {
		opts = append(opts, connect.WithInterceptors(interceptor))
	}

	mux := http.NewServeMux()
	path, handler := internalpbconnect.NewRemotingServiceHandler(x, opts...)
	mux.Handle(path, handler)
	serverAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)

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
		BaseContext: func(listener net.Listener) context.Context {
			return ctx
		},
	}

	go func() {
		if err := server.ListenAndServe(); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				x.logger.Panic(errors.Wrap(err, "failed to start remoting service"))
			}
		}
	}()

	x.mutex.Lock()
	x.remotingServer = server
	x.mutex.Unlock()

	x.logger.Info("remoting enabled...:)")
}

// reset the actor system
func (x *actorSystem) reset() {
	x.telemetry = nil
	x.actors = newPIDMap(10)
	x.name = ""
	x.cluster = nil
}

// broadcast publishes newly created actor into the cluster when cluster is enabled
func (x *actorSystem) broadcast(ctx context.Context) {
	for wireActor := range x.clusterChan {
		if x.cluster != nil {
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
	x.logger.Info("Housekeeping has started...")
	ticker := time.NewTicker(30 * time.Millisecond)
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				for _, actor := range x.Actors() {
					if !actor.IsRunning() {
						x.logger.Infof("Removing actor=%s from system", actor.ActorPath().Name())
						x.actors.Delete(actor.ActorPath())
						if x.InCluster() {
							if err := x.cluster.RemoveActor(context.Background(), actor.ActorPath().Name()); err != nil {
								x.logger.Error(err.Error())
								// TODO: stop or continue
							}
						}
					}
				}
			case <-x.housekeeperStopSig:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()
	x.logger.Info("Housekeeping has stopped...")
}

// registerMetrics register the PID metrics with OTel instrumentation.
func (x *actorSystem) registerMetrics() error {
	meter := x.telemetry.Meter
	metrics, err := metric.NewActorSystemMetric(meter)
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(func(ctx context.Context, observer otelmetric.Observer) error {
		observer.ObserveInt64(metrics.ActorsCount(), int64(x.NumActors()))
		return nil
	}, metrics.ActorsCount())

	return err
}

// broadcastClusterEvents listens to cluster events and send them to the event streams
func (x *actorSystem) broadcastClusterEvents() {
	for event := range x.eventsChan {
		if x.clusterEnabled.Load() {
			if event != nil && event.Payload != nil {
				// first need to resync actors map back to the cluster
				x.clusterSync()
				// push the event to the event stream
				message, _ := event.Payload.UnmarshalNew()
				if x.eventsStream != nil {
					x.logger.Debugf("node=(%s) publishing cluster event=(%s)....", x.name, event.Type)
					x.eventsStream.Publish(eventsTopic, message)
					x.logger.Debugf("cluster event=(%s) successfully published by node=(%s)", event.Type, x.name)
				}
			}
		}
	}
}

// clusterSync synchronizes the node' actors map to the cluster.
func (x *actorSystem) clusterSync() {
	typesMap := x.registry.List()
	if len(typesMap) != 0 {
		x.logger.Info("syncing node actors map to the cluster...")
		for actorID, actorType := range typesMap {
			actorPath := NewPath(actorID, NewAddress(x.name, "", -1))
			if x.remotingEnabled.Load() {
				actorPath = NewPath(actorID, NewAddress(x.name, x.remotingHost, int(x.remotingPort)))
			}

			if !x.clusterEnabled.Load() {
				return
			}

			x.clusterChan <- &internalpb.WireActor{
				ActorName:    actorID,
				ActorAddress: actorPath.RemoteAddress(),
				ActorPath:    actorPath.String(),
				ActorType:    actorType.Name(),
			}
		}
		x.logger.Info("node actors map successfully synced back to the cluster.")
	}
}

// runClusterSync runs time to time cluster synchronization
// by populating the given node actors map to the cluster for availability
func (x *actorSystem) runClusterSync() {
	x.logger.Info("cluster synchronization has started...")
	ticker := time.NewTicker(5 * time.Minute)
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				x.clusterSync()
			case <-x.clusterSyncStopSig:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()
	x.logger.Info("cluster synchronization has stopped...")
}
