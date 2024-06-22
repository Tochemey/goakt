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
	"regexp"
	"strconv"
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

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/hash"
	"github.com/tochemey/goakt/v2/internal/cluster"
	"github.com/tochemey/goakt/v2/internal/eventstream"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v2/internal/metric"
	"github.com/tochemey/goakt/v2/internal/tcp"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/telemetry"
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
	// Register registers an actor for future use. This is necessary when creating an actor remotely
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
	// specifies actors mailbox size
	mailboxSize uint64
	// specifies the mailbox to use for the actors
	mailbox Mailbox
	// specifies the stash capacity
	stashCapacity uint64

	housekeeperStopSig chan types.Unit

	// specifies the events stream
	eventsStream *eventstream.EventsStream

	// specifies the message scheduler
	scheduler *scheduler
	// specifies whether tracing is enabled
	traceEnabled atomic.Bool
	tracer       trace.Tracer

	// specifies whether metric is enabled
	metricEnabled atomic.Bool

	registry   types.Registry
	reflection reflection

	peersStateLoopInterval time.Duration
	peersCacheMu           *sync.RWMutex
	peersCache             map[string][]byte
	clusterConfig          *ClusterConfig
	redistributionChan     chan *cluster.Event
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
		actors:                 newPIDMap(1_000), // TODO need to check with memory footprint here since we change the map engine
		actorsChan:             make(chan *internalpb.WireActor, 10),
		name:                   name,
		logger:                 log.DefaultLogger,
		expireActorAfter:       DefaultPassivationTimeout,
		replyTimeout:           DefaultReplyTimeout,
		actorInitMaxRetries:    DefaultInitMaxRetries,
		supervisorStrategy:     DefaultSupervisoryStrategy,
		telemetry:              telemetry.New(),
		locker:                 sync.Mutex{},
		shutdownTimeout:        DefaultShutdownTimeout,
		mailboxSize:            DefaultMailboxSize,
		housekeeperStopSig:     make(chan types.Unit, 1),
		eventsStream:           eventstream.New(),
		partitionHasher:        hash.DefaultHasher(),
		actorInitTimeout:       DefaultInitTimeout,
		tracer:                 noop.NewTracerProvider().Tracer(name),
		clusterEventsChan:      make(chan *cluster.Event, 1),
		registry:               types.NewRegistry(),
		clusterSyncStopSig:     make(chan types.Unit, 1),
		peersCacheMu:           &sync.RWMutex{},
		peersCache:             make(map[string][]byte),
		peersStateLoopInterval: DefaultPeerStateLoopInterval,
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

	// we need to make sure the cluster kinds are defined
	if system.clusterEnabled.Load() {
		if err := system.clusterConfig.Validate(); err != nil {
			return nil, err
		}
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

	x.locker.Lock()
	defer x.locker.Unlock()

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

	x.locker.Lock()
	defer x.locker.Unlock()

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
	return uint64(x.actors.len())
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

	pid, exist := x.actors.get(actorPath)
	if exist {
		if pid.IsRunning() {
			// return the existing instance
			return pid, nil
		}
	}

	var mailbox Mailbox
	if x.mailbox != nil {
		// always create a fresh copy of provided mailbox for every new PID
		mailbox = x.mailbox.Clone()
	}

	// define the pid options
	// pid inherit the actor system settings defined during
	opts := []pidOption{
		withInitMaxRetries(x.actorInitMaxRetries),
		withPassivationAfter(x.expireActorAfter),
		withReplyTimeout(x.replyTimeout),
		withCustomLogger(x.logger),
		withActorSystem(x),
		withSupervisorStrategy(x.supervisorStrategy),
		withMailboxSize(x.mailboxSize),
		withMailbox(mailbox), // nil mailbox is taken care during initiliazation by the newPID
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

	x.actors.set(pid)
	if x.clusterEnabled.Load() {
		x.actorsChan <- &internalpb.WireActor{
			ActorName:    name,
			ActorAddress: actorPath.RemoteAddress(),
			ActorPath:    actorPath.String(),
			ActorType:    types.NameOf(actor),
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

	var mailbox Mailbox
	if x.mailbox != nil {
		// always create a fresh copy of provided mailbox for every new PID
		mailbox = x.mailbox.Clone()
	}

	// define the pid options
	// pid inherit the actor system settings defined during instantiation
	pidOpts := []pidOption{
		withInitMaxRetries(x.actorInitMaxRetries),
		withPassivationAfter(x.expireActorAfter),
		withReplyTimeout(x.replyTimeout),
		withCustomLogger(x.logger),
		withActorSystem(x),
		withSupervisorStrategy(x.supervisorStrategy),
		withMailboxSize(x.mailboxSize),
		withMailbox(mailbox), // nil mailbox is taken care during initiliazation by the newPID
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

	x.actors.set(pid)
	if x.clusterEnabled.Load() {
		x.actorsChan <- &internalpb.WireActor{
			ActorName:    actorID,
			ActorAddress: actorPath.RemoteAddress(),
			ActorPath:    actorPath.String(),
			ActorType:    types.NameOf(actor),
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

	pid, exist := x.actors.get(actorPath)
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

	pid, exist := x.actors.get(actorPath)
	if exist {
		if err := pid.Restart(spanCtx); err != nil {
			return nil, errors.Wrapf(err, "failed to restart actor=%s", actorPath.String())
		}

		x.actors.set(pid)
		return pid, nil
	}

	err := ErrActorNotFound(actorPath.String())
	span.SetStatus(codes.Error, "ReSpawn")
	span.RecordError(err)
	return nil, err
}

// Name returns the actor system name
func (x *actorSystem) Name() string {
	x.locker.Lock()
	defer x.locker.Unlock()
	return x.name
}

// Actors returns the list of Actors that are alive in the actor system
func (x *actorSystem) Actors() []PID {
	x.locker.Lock()
	defer x.locker.Unlock()
	return x.actors.pids()
}

// PeerAddress returns the actor system address known in the cluster. That address is used by other nodes to communicate with the actor system.
// This address is empty when cluster mode is not activated
func (x *actorSystem) PeerAddress() string {
	x.locker.Lock()
	defer x.locker.Unlock()
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

	x.locker.Lock()
	defer x.locker.Unlock()

	if !x.started.Load() {
		span.SetStatus(codes.Error, "ActorOf")
		span.RecordError(ErrActorSystemNotStarted)
		return nil, nil, ErrActorSystemNotStarted
	}

	// first check whether the actor exist locally
	pids := x.actors.pids()
	for _, actorRef := range pids {
		if actorRef.ActorPath().Name() == actorName {
			return actorRef.ActorPath().RemoteAddress(), actorRef, nil
		}
	}

	// check in the cluster
	if x.clusterEnabled.Load() {
		actor, err := x.cluster.GetActor(spanCtx, actorName)
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

		return actor.GetActorAddress(), nil, nil
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
	x.locker.Lock()
	defer x.locker.Unlock()

	if !x.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	pids := x.actors.pids()
	for _, pid := range pids {
		if pid.ActorPath().Name() == actorName {
			return pid, nil
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

	x.locker.Lock()
	defer x.locker.Unlock()

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

	actor, err := x.cluster.GetActor(spanCtx, actorName)
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

	return actor.GetActorAddress(), nil
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
		close(x.actorsChan)
		x.clusterSyncStopSig <- types.Unit{}
		x.clusterEnabled.Store(false)
		close(x.redistributionChan)
	}

	if len(x.Actors()) == 0 {
		x.logger.Info("No online actors to shutdown. Shutting down successfully done")
		return nil
	}

	for _, actor := range x.Actors() {
		x.actors.delete(actor.ActorPath())
		if err := actor.Shutdown(ctx); err != nil {
			x.reset()
			return err
		}
	}

	x.actors.close()
	x.reset()
	return nil
}

// RemoteLookup for an actor on a remote host.
func (x *actorSystem) RemoteLookup(ctx context.Context, request *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	logger := x.logger
	msg := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	if x.clusterEnabled.Load() {
		actorName := msg.GetName()
		actor, err := x.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				logger.Error(ErrAddressNotFound(actorName).Error())
				return nil, ErrAddressNotFound(actorName)
			}

			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: actor.GetActorAddress()}), nil
	}

	actorPath := NewPath(msg.GetName(), NewAddress(x.Name(), msg.GetHost(), int(msg.GetPort())))
	pid, exist := x.actors.get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: pid.ActorPath().RemoteAddress()}), nil
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
		switch err := ctx.Err(); {
		case errors.Is(err, context.Canceled):
			return connect.NewError(connect.CodeCanceled, err)
		case errors.Is(err, context.DeadlineExceeded):
			return connect.NewError(connect.CodeDeadlineExceeded, err)
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
		receiver := message.GetReceiver()
		name := receiver.GetName()

		remoteAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)
		if remoteAddr != net.JoinHostPort(receiver.GetHost(), strconv.Itoa(int(receiver.GetPort()))) {
			return connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
		}

		actorPath := NewPath(name, NewAddress(x.name, x.remotingHost, int(x.remotingPort)))
		pid, exist := x.actors.get(actorPath)
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
					x.Name(),
					receiver.GetHost(),
					int(receiver.GetPort())))

			pid, exist := x.actors.get(actorPath)
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

	msg := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorPath := NewPath(msg.GetName(), NewAddress(x.Name(), msg.GetHost(), int(msg.GetPort())))
	pid, exist := x.actors.get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	if err := pid.Restart(ctx); err != nil {
		return nil, errors.Wrapf(err, "failed to restart actor=%s", actorPath.String())
	}

	x.actors.set(pid)
	return connect.NewResponse(new(internalpb.RemoteReSpawnResponse)), nil
}

// RemoteStop stops an actor on a remote machine
func (x *actorSystem) RemoteStop(ctx context.Context, request *connect.Request[internalpb.RemoteStopRequest]) (*connect.Response[internalpb.RemoteStopResponse], error) {
	logger := x.logger

	msg := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorPath := NewPath(msg.GetName(), NewAddress(x.Name(), msg.GetHost(), int(msg.GetPort())))
	pid, exist := x.actors.get(actorPath)
	if !exist {
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	if err := pid.Shutdown(ctx); err != nil {
		return nil, errors.Wrapf(err, "failed to stop actor=%s", actorPath.String())
	}

	x.actors.delete(actorPath)
	return connect.NewResponse(new(internalpb.RemoteStopResponse)), nil
}

// RemoteSpawn handles the remoteSpawn call
func (x *actorSystem) RemoteSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteSpawnRequest]) (*connect.Response[internalpb.RemoteSpawnResponse], error) {
	logger := x.logger

	msg := request.Msg
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actor, err := x.reflection.ActorFrom(msg.GetActorType())
	if err != nil {
		logger.Errorf("failed to create actor=[(%s) of type (%s)] on [host=%s, port=%d]: reason: (%v)",
			msg.GetActorName(), msg.GetActorType(), msg.GetHost(), msg.GetPort(), err)

		if errors.Is(err, ErrTypeNotRegistered) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, ErrTypeNotRegistered)
		}

		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if _, err = x.Spawn(ctx, msg.GetActorName(), actor); err != nil {
		logger.Errorf("failed to create actor=(%s) on [host=%s, port=%d]: reason: (%v)", msg.GetActorName(), msg.GetHost(), msg.GetPort(), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	logger.Infof("actor=(%s) successfully created on [host=%s, port=%d]", msg.GetActorName(), msg.GetHost(), msg.GetPort())
	return connect.NewResponse(new(internalpb.RemoteSpawnResponse)), nil
}

// GetNodeMetric handles the GetNodeMetric request send the given node
func (x *actorSystem) GetNodeMetric(_ context.Context, request *connect.Request[internalpb.GetNodeMetricRequest]) (*connect.Response[internalpb.GetNodeMetricResponse], error) {
	if !x.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrClusterDisabled)
	}

	req := request.Msg

	remoteAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)
	if remoteAddr != req.GetNodeAddress() {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorCount := x.actors.len()
	return connect.NewResponse(&internalpb.GetNodeMetricResponse{
		NodeRemoteAddress: remoteAddr,
		ActorsCount:       uint64(actorCount),
	}), nil
}

// GetKinds returns the cluster kinds
func (x *actorSystem) GetKinds(_ context.Context, request *connect.Request[internalpb.GetKindsRequest]) (*connect.Response[internalpb.GetKindsResponse], error) {
	if !x.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrClusterDisabled)
	}

	req := request.Msg
	remoteAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)

	// routine check
	if remoteAddr != req.GetNodeAddress() {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	kinds := make([]string, len(x.clusterConfig.Kinds()))
	for i, kind := range x.clusterConfig.Kinds() {
		kinds[i] = types.NameOf(kind)
	}

	return connect.NewResponse(&internalpb.GetKindsResponse{Kinds: kinds}), nil
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

	node := &discovery.Node{
		Name:         x.Name(),
		Host:         x.remotingHost,
		GossipPort:   x.clusterConfig.GossipPort(),
		PeersPort:    x.clusterConfig.PeersPort(),
		RemotingPort: int(x.remotingPort),
	}

	clusterEngine, err := cluster.NewEngine(
		x.Name(),
		x.clusterConfig.Discovery(),
		node,
		cluster.WithLogger(x.logger),
		cluster.WithPartitionsCount(x.clusterConfig.PartitionCount()),
		cluster.WithHasher(x.partitionHasher),
		cluster.WithMinimumPeersQuorum(x.clusterConfig.MinimumPeersQuorum()),
		cluster.WithReplicaCount(x.clusterConfig.ReplicaCount()),
	)
	if err != nil {
		x.logger.Error(errors.Wrap(err, "failed to initialize cluster engine"))
		return err
	}

	x.logger.Info("starting cluster engine...")
	if err := clusterEngine.Start(ctx); err != nil {
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

	x.locker.Lock()
	x.cluster = clusterEngine
	x.clusterEventsChan = clusterEngine.Events()
	x.redistributionChan = make(chan *cluster.Event, 1)
	for _, kind := range x.clusterConfig.Kinds() {
		x.registry.Register(kind)
		x.logger.Infof("cluster kind=(%s) registered", types.NameOf(kind))
	}
	x.locker.Unlock()

	go x.clusterEventsLoop()
	go x.clusterReplicationLoop()
	go x.peersStateLoop()
	go x.redistributionLoop()

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

	remotingHost, remotingPort, err := tcp.GetHostPort(fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort))
	if err != nil {
		x.logger.Panic(errors.Wrap(err, "failed to resolve remoting TCP address"))
	}

	x.remotingHost = remotingHost
	x.remotingPort = int32(remotingPort)

	remotingServicePath, remotingServiceHandler := internalpbconnect.NewRemotingServiceHandler(x, opts...)
	clusterServicePath, clusterServiceHandler := internalpbconnect.NewClusterServiceHandler(x, opts...)

	mux := http.NewServeMux()
	mux.Handle(remotingServicePath, remotingServiceHandler)
	mux.Handle(clusterServicePath, clusterServiceHandler)
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
		BaseContext: func(_ net.Listener) context.Context {
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

	x.locker.Lock()
	x.remotingServer = server
	x.locker.Unlock()

	x.logger.Info("remoting enabled...:)")
}

// reset the actor system
func (x *actorSystem) reset() {
	x.telemetry = nil
	x.actors.close()
	x.name = ""
	x.cluster = nil
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
						x.actors.delete(actor.ActorPath())
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

	_, err = meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		observer.ObserveInt64(metrics.ActorsCount(), int64(x.NumActors()))
		return nil
	}, metrics.ActorsCount())

	return err
}

// clusterReplicationLoop publishes newly created actor into the cluster when cluster is enabled
func (x *actorSystem) clusterReplicationLoop() {
	for actor := range x.actorsChan {
		if x.InCluster() {
			ctx := context.Background()
			if err := x.cluster.PutActor(ctx, actor); err != nil {
				x.logger.Panic(err.Error())
			}
		}
	}
}

// clusterEventsLoop listens to cluster events and send them to the event streams
func (x *actorSystem) clusterEventsLoop() {
	for event := range x.clusterEventsChan {
		if x.InCluster() {
			if event != nil && event.Payload != nil {
				// push the event to the event stream
				message, _ := event.Payload.UnmarshalNew()
				if x.eventsStream != nil {
					x.logger.Debugf("node=(%s) publishing cluster event=(%s)....", x.name, event.Type)
					x.eventsStream.Publish(eventsTopic, message)
					x.logger.Debugf("cluster event=(%s) successfully published by node=(%s)", event.Type, x.name)
				}
				x.redistributionChan <- event
			}
		}
	}
}

// peersStateLoop fetches the cluster peers' PeerState and update the node peersCache
func (x *actorSystem) peersStateLoop() {
	x.logger.Info("peers state synchronization has started...")
	ticker := time.NewTicker(x.peersStateLoopInterval)
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				eg, ctx := errgroup.WithContext(context.Background())

				peersChan := make(chan *cluster.Peer)

				eg.Go(func() error {
					defer close(peersChan)
					peers, err := x.cluster.Peers(ctx)
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
						if err := x.processPeerState(ctx, peer); err != nil {
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
					x.logger.Error(err)
					// TODO: stop or panic
				}

			case <-x.clusterSyncStopSig:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()
	x.logger.Info("peers state synchronization has stopped...")
}

func (x *actorSystem) redistributionLoop() {
	for event := range x.redistributionChan {
		if x.InCluster() {
			// check for cluster rebalancing
			if err := x.redistribute(context.Background(), event); err != nil {
				x.logger.Errorf("cluster rebalancing failed: %v", err)
				// TODO: panic or retry or shutdown actor system
			}
		}
	}
}

// processPeerState processes a given peer synchronization record.
func (x *actorSystem) processPeerState(ctx context.Context, peer *cluster.Peer) error {
	x.peersCacheMu.Lock()
	defer x.peersCacheMu.Unlock()

	peerAddress := net.JoinHostPort(peer.Host, strconv.Itoa(peer.Port))

	x.logger.Infof("processing peer sync:(%s)", peerAddress)
	peerState, err := x.cluster.GetState(ctx, peerAddress)
	if err != nil {
		if errors.Is(err, cluster.ErrPeerSyncNotFound) {
			return nil
		}
		x.logger.Error(err)
		return err
	}

	x.logger.Debugf("peer (%s) actors count (%d)", peerAddress, len(peerState.GetActors()))

	// no need to handle the error
	bytea, err := proto.Marshal(peerState)
	if err != nil {
		x.logger.Error(err)
		return err
	}

	x.peersCache[peerAddress] = bytea
	x.logger.Infof("peer sync(%s) successfully processed", peerAddress)
	return nil
}
