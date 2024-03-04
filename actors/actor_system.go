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
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	addresspb "github.com/tochemey/goakt/goaktpb"
	"github.com/tochemey/goakt/hash"
	"github.com/tochemey/goakt/internal/cluster"
	"github.com/tochemey/goakt/internal/eventstream"
	internalpb "github.com/tochemey/goakt/internal/internalpb"
	"github.com/tochemey/goakt/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/internal/metric"
	"github.com/tochemey/goakt/internal/types"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/telemetry"
	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
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
	RemoteScheduleOnce(ctx context.Context, message proto.Message, address *addresspb.Address, interval time.Duration) error
	// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
	ScheduleWithCron(ctx context.Context, message proto.Message, pid PID, cronExpression string) error
	// RemoteScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
	RemoteScheduleWithCron(ctx context.Context, message proto.Message, address *addresspb.Address, cronExpression string) error
	// PeerAddress returns the actor system address known in the cluster. That address is used by other nodes to communicate with the actor system.
	// This address is empty when cluster mode is not activated
	PeerAddress() string
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
	serviceDiscovery *discovery.ServiceDiscovery
	// define the number of partitions to shard the actors in the cluster
	partitionsCount uint64
	// cluster mode
	cluster         cluster.Interface
	clusterChan     chan *internalpb.WireActor
	partitionHasher hash.Hasher

	// help protect some the fields to set
	sem sync.Mutex
	// specifies actors mailbox size
	mailboxSize uint64
	// specifies the mailbox to use for the actors
	mailbox Mailbox
	// specifies the stash buffer
	stashBuffer        uint64
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
	eventsChan    <-chan *cluster.Event

	typesRegistry Registry
	reflection    Reflection
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
		housekeeperStopSig:  make(chan types.Unit, 1),
		eventsStream:        eventstream.New(),
		partitionHasher:     hash.DefaultHasher(),
		actorInitTimeout:    DefaultInitTimeout,
		tracer:              noop.NewTracerProvider().Tracer(name),
		eventsChan:          make(chan *cluster.Event, 1),
		typesRegistry:       NewRegistry(),
	}
	// set the atomic settings
	system.hasStarted.Store(false)
	system.remotingEnabled.Store(false)
	system.clusterEnabled.Store(false)
	system.traceEnabled.Store(false)
	system.metricEnabled.Store(false)

	// set the reflection
	system.reflection = NewReflection(system.typesRegistry)

	// apply the various options
	for _, opt := range opts {
		opt.Apply(system)
	}

	// set the tracer when tracing is enabled
	if system.traceEnabled.Load() {
		system.tracer = system.telemetry.Tracer
	}

	// set the message scheduler
	system.scheduler = newScheduler(system.logger, system.shutdownTimeout, withSchedulerCluster(system.cluster))

	// set metric and return the registration error in case there is one
	// re-register metric when metric is enabled
	if system.metricEnabled.Load() {
		// log the error but don't panic
		if err := system.registerMetrics(); err != nil {
			return nil, err
		}
	}

	return system, nil
}

// ScheduleOnce schedules a message that will be delivered to the receiver actor
// This will send the given message to the actor after the given interval specified.
// The message will be sent once
func (x *actorSystem) ScheduleOnce(ctx context.Context, message proto.Message, pid PID, interval time.Duration) error {
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "ScheduleOnce")
	// defer the closing of the span
	defer span.End()
	// schedule message
	return x.scheduler.ScheduleOnce(spanCtx, message, pid, interval)
}

// RemoteScheduleOnce schedules a message to be sent to a remote actor in the future.
// This requires remoting to be enabled on the actor system.
// This will send the given message to the actor after the given interval specified
// The message will be sent once
func (x *actorSystem) RemoteScheduleOnce(ctx context.Context, message proto.Message, address *addresspb.Address, interval time.Duration) error {
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "RemoteScheduleOnce")
	// defer the closing of the span
	defer span.End()
	// schedule the message
	return x.scheduler.RemoteScheduleOnce(spanCtx, message, address, interval)
}

// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (x *actorSystem) ScheduleWithCron(ctx context.Context, message proto.Message, pid PID, cronExpression string) error {
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "ScheduleWithCron")
	// defer the closing of the span
	defer span.End()
	// schedule message
	return x.scheduler.ScheduleWithCron(spanCtx, message, pid, cronExpression)
}

// RemoteScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (x *actorSystem) RemoteScheduleWithCron(ctx context.Context, message proto.Message, address *addresspb.Address, cronExpression string) error {
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "RemoteScheduleWithCron")
	// defer the closing of the span
	defer span.End()
	// schedule message
	return x.scheduler.RemoteScheduleWithCron(spanCtx, message, address, cronExpression)
}

// Subscribe help receive dead letters whenever there are available
func (x *actorSystem) Subscribe() (eventstream.Subscriber, error) {
	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		return nil, ErrActorSystemNotStarted
	}
	// create the consumer
	subscriber := x.eventsStream.AddSubscriber()
	// subscribe the consumer to the deadletter topic
	x.eventsStream.Subscribe(subscriber, eventsTopic)
	return subscriber, nil
}

// Unsubscribe unsubscribes a subscriber.
func (x *actorSystem) Unsubscribe(subscriber eventstream.Subscriber) error {
	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		return ErrActorSystemNotStarted
	}
	// subscribe the consumer to the deadletter topic
	x.eventsStream.Unsubscribe(subscriber, eventsTopic)
	// remove the subscriber from the events stream
	x.eventsStream.RemoveSubscriber(subscriber)
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
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "Spawn")
	// defer the closing of the span
	defer span.End()

	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		// let us record the error in the span
		span.SetStatus(codes.Error, "Spawn")
		span.RecordError(ErrDead)
		// return the error
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
		withStash(x.stashBuffer),
		withEventsStream(x.eventsStream),
		withInitTimeout(x.actorInitTimeout),
		withTelemetry(x.telemetry),
	}

	// set the pid tracing option
	if x.traceEnabled.Load() {
		opts = append(opts, withTracing())
	}

	// set the pid metric option
	if x.metricEnabled.Load() {
		opts = append(opts, withMetric())
	}

	// create an instance of the actor ref
	pid, err := newPID(spanCtx,
		actorPath,
		actor,
		opts...)

	// handle the error
	if err != nil {
		// let us record the error in the span
		span.SetStatus(codes.Error, "Spawn")
		span.RecordError(err)
		// return the error
		return nil, err
	}

	// add the given actor to the actor map
	x.actors.Set(pid)

	// when cluster is enabled replicate the actor metadata across the cluster
	if x.clusterEnabled.Load() {
		// get the binary version of the given actor
		actorBinary, err := actor.MarshalBinary()
		// handle the error
		if err != nil {
			// let us record the error in the span
			span.SetStatus(codes.Error, "Spawn")
			span.RecordError(err)
			// return the error
			return nil, err
		}
		// send it to the cluster channel a wire actor
		x.clusterChan <- &internalpb.WireActor{
			ActorName:    name,
			ActorAddress: actorPath.RemoteAddress(),
			ActorPath:    actorPath.String(),
			ActorBinary:  actorBinary,
		}
	}

	// return the actor ref
	return pid, nil
}

// Kill stops a given actor in the system
func (x *actorSystem) Kill(ctx context.Context, name string) error {
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "Kill")
	// defer the closing of the span
	defer span.End()

	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		// let us record the error in the span
		span.SetStatus(codes.Error, "Kill")
		span.RecordError(ErrActorSystemNotStarted)
		// return the error
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
		// stop the given actor. No need to record error in the span context
		// because the shutdown method is taking care of that
		return pid.Shutdown(spanCtx)
	}
	// create the error
	err := ErrActorNotFound(actorPath.String())
	// let us record the error in the span
	span.SetStatus(codes.Error, "Kill")
	span.RecordError(err)
	return err
}

// ReSpawn recreates a given actor in the system
func (x *actorSystem) ReSpawn(ctx context.Context, name string) (PID, error) {
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "ReSpawn")
	// defer the closing of the span
	defer span.End()

	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		// let us record the error in the span
		span.SetStatus(codes.Error, "ReSpawn")
		span.RecordError(ErrActorSystemNotStarted)
		// return the error
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
		// pid.Restart handle error propagation in span
		if err := pid.Restart(spanCtx); err != nil {
			// return the error in case the restart failed
			return nil, errors.Wrapf(err, "failed to restart actor=%s", actorPath.String())
		}
		// let us re-add the actor to the actor system list when it has successfully restarted
		// add the given actor to the actor map
		x.actors.Set(pid)
		return pid, nil
	}
	// create the error
	err := ErrActorNotFound(actorPath.String())
	// let us record the error in the span
	span.SetStatus(codes.Error, "ReSpawn")
	span.RecordError(err)
	return nil, err
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

// PeerAddress returns the actor system address known in the cluster. That address is used by other nodes to communicate with the actor system.
// This address is empty when cluster mode is not activated
func (x *actorSystem) PeerAddress() string {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	// check whether cluster mode is enabled
	if x.clusterEnabled.Load() {
		// return the cluster node advertised address
		return x.cluster.AdvertisedAddress()
	}
	return ""
}

// ActorOf returns an existing actor in the local system or in the cluster when clustering is enabled
// When cluster mode is activated, the PID will be nil.
// When remoting is enabled this method will return and error
// An actor not found error is return when the actor is not found.
func (x *actorSystem) ActorOf(ctx context.Context, actorName string) (addr *addresspb.Address, pid PID, err error) {
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "ActorOf")
	// defer the closing of the span
	defer span.End()

	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()

	// make sure the actor system has started
	if !x.hasStarted.Load() {
		// let us record the error in the span
		span.SetStatus(codes.Error, "ActorOf")
		span.RecordError(ErrActorSystemNotStarted)
		// return the error
		return nil, nil, ErrActorSystemNotStarted
	}

	// try to locate the actor in the cluster when cluster is enabled
	if x.cluster != nil || x.clusterEnabled.Load() {
		// let us locate the actor in the cluster
		wireActor, err := x.cluster.GetActor(spanCtx, actorName)
		// handle the eventual error
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				x.logger.Infof("actor=%s not found", actorName)
				// create the error
				e := ErrActorNotFound(actorName)
				// let us record the error in the span
				span.SetStatus(codes.Error, "ActorOf")
				span.RecordError(e)
				// return the error
				return nil, nil, e
			}
			// create the error
			e := errors.Wrapf(err, "failed to fetch remote actor=%s", actorName)
			// let us record the error in the span
			span.SetStatus(codes.Error, "ActorOf")
			span.RecordError(e)
			return nil, nil, e
		}

		// return the address of the remote actor
		return wireActor.GetActorAddress(), nil, nil
	}

	// method call is not allowed
	if x.remotingEnabled.Load() {
		// let us record the error in the span
		span.SetStatus(codes.Error, "ActorOf")
		span.RecordError(ErrMethodCallNotAllowed)
		// return the error
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
	// create the error
	e := ErrActorNotFound(actorName)
	// let us record the error in the span
	span.SetStatus(codes.Error, "ActorOf")
	span.RecordError(e)
	return nil, nil, e
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
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "RemoteActor")
	// defer the closing of the span
	defer span.End()

	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()

	// make sure the actor system has started
	if !x.hasStarted.Load() {
		// set the error
		e := ErrActorSystemNotStarted
		// let us record the error in the span
		span.SetStatus(codes.Error, "RemoteActor")
		span.RecordError(e)
		// return the error
		return nil, e
	}

	// check whether cluster is enabled or not
	if x.cluster == nil {
		// set the error
		e := ErrClusterDisabled
		// let us record the error in the span
		span.SetStatus(codes.Error, "RemoteActor")
		span.RecordError(e)
		// return the error
		return nil, e
	}

	// let us locate the actor in the cluster
	wireActor, err := x.cluster.GetActor(spanCtx, actorName)
	// handle the eventual error
	if err != nil {
		if errors.Is(err, cluster.ErrActorNotFound) {
			x.logger.Infof("actor=%s not found", actorName)
			// set the error
			e := ErrActorNotFound(actorName)
			// let us record the error in the span
			span.SetStatus(codes.Error, "RemoteActor")
			span.RecordError(e)
			// return the error
			return nil, e
		}
		// set the error
		e := errors.Wrapf(err, "failed to fetch remote actor=%s", actorName)
		// let us record the error in the span
		span.SetStatus(codes.Error, "RemoteActor")
		span.RecordError(e)
		return nil, e
	}

	// return the address of the remote actor
	return wireActor.GetActorAddress(), nil
}

// Start starts the actor system
func (x *actorSystem) Start(ctx context.Context) error {
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "Start")
	// defer the closing of the span
	defer span.End()

	// set the has started to true
	x.hasStarted.Store(true)

	// enable clustering when it is enabled
	if x.clusterEnabled.Load() {
		x.enableClustering(spanCtx)
	}

	// start remoting when remoting is enabled
	if x.remotingEnabled.Load() {
		x.enableRemoting(spanCtx)
	}

	// start the message scheduler
	x.scheduler.Start(spanCtx)

	// start the housekeeper
	go x.housekeeper()

	x.logger.Infof("%s started..:)", x.name)
	return nil
}

// Stop stops the actor system
func (x *actorSystem) Stop(ctx context.Context) error {
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "Stop")
	// defer the closing of the span
	defer span.End()

	// make sure the actor system has started
	if !x.hasStarted.Load() {
		// set the error
		e := ErrActorSystemNotStarted
		// let us record the error in the span
		span.SetStatus(codes.Error, "Stop")
		span.RecordError(e)
		return e
	}

	// stop the housekeeper
	x.housekeeperStopSig <- types.Unit{}
	x.logger.Infof("%s is shutting down..:)", x.name)

	// set started to false
	x.hasStarted.Store(false)

	// stop the messages scheduler
	x.scheduler.Stop(spanCtx)

	// shutdown the events stream
	if x.eventsStream != nil {
		x.eventsStream.Shutdown()
	}

	// create a cancellation context to gracefully shutdown
	ctx, cancel := context.WithTimeout(spanCtx, x.shutdownTimeout)
	defer cancel()

	// stop the remoting server
	if x.remotingEnabled.Load() {
		// stop the server
		if err := x.remotingServer.Shutdown(spanCtx); err != nil {
			// let us record the error in the span
			span.SetStatus(codes.Error, "Stop")
			span.RecordError(err)
			// return the error
			return err
		}

		// unset the remoting settings
		x.remotingEnabled.Store(false)
		x.remotingServer = nil
	}

	// stop the cluster service
	if x.clusterEnabled.Load() {
		// stop the cluster service
		if err := x.cluster.Stop(spanCtx); err != nil {
			// let us record the error in the span
			span.SetStatus(codes.Error, "Stop")
			span.RecordError(err)
			// return the error
			return err
		}
		// stop broadcasting cluster messages
		close(x.clusterChan)
		// unset the remoting settings
		x.clusterEnabled.Store(false)
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
		// no need to record span error
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

// RemoteBatchTell is used to send a bulk of messages to an actor remotely by another actor.
func (x *actorSystem) RemoteBatchTell(ctx context.Context, request *connect.Request[internalpb.RemoteBatchTellRequest]) (*connect.Response[internalpb.RemoteBatchTellResponse], error) {
	// get a context log
	logger := x.logger
	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	// get the receiver
	receiver := reqCopy.GetReceiver()

	// set the actor path with the remoting is enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
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

	// send the messages to the receiver mailbox
	var messages []proto.Message
	// let us unpack each message
	for _, message := range reqCopy.GetMessages() {
		// unmarshal the message
		actual, err := message.UnmarshalNew()
		// handle the error
		if err != nil {
			logger.Error(err)
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		// add to the list of messages
		messages = append(messages, actual)
	}

	// push the messages to the actor mailbox
	if err := BatchTell(ctx, pid, messages...); err != nil {
		// log the error
		logger.Error(ErrRemoteSendFailure(err))
		return nil, ErrRemoteSendFailure(err)
	}

	// return the rpc response
	return connect.NewResponse(new(internalpb.RemoteBatchTellResponse)), nil
}

// RemoteBatchAsk is used to send a bulk messages to a remote actor with replies.
// The replies are sent in the same order as the messages
func (x *actorSystem) RemoteBatchAsk(ctx context.Context, request *connect.Request[internalpb.RemoteBatchAskRequest]) (*connect.Response[internalpb.RemoteBatchAskResponse], error) {
	// get a context log
	logger := x.logger
	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	// set the actor path with the remoting is enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	// construct the actor address
	name := reqCopy.GetReceiver().GetName()
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

	// send the messages to the receiver mailbox
	var messages []proto.Message
	// let us unpack each message
	for _, message := range reqCopy.GetMessages() {
		// unmarshal the message
		actual, err := message.UnmarshalNew()
		// handle the error
		if err != nil {
			logger.Error(err)
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		// add to the list of messages
		messages = append(messages, actual)
	}

	// push the messages to the actor mailbox and await replies
	replies, err := BatchAsk(ctx, pid, x.replyTimeout, messages...)
	// handle the error
	if err != nil {
		// log the error
		logger.Error(ErrRemoteSendFailure(err))
		return nil, ErrRemoteSendFailure(err)
	}

	// define a variable to hold responses as they trickle in
	var responses []*anypb.Any
	for reply := range replies {
		// let us unpack the responses
		actual, err := anypb.New(reply)
		// handle the error
		if err != nil {
			// log the error
			logger.Error(err)
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		responses = append(responses, actual)
	}

	// return the rpc response
	return connect.NewResponse(&internalpb.RemoteBatchAskResponse{Messages: responses}), nil
}

// RemoteReSpawn is used the handle the re-creation of an actor from a remote host or from an api call
func (x *actorSystem) RemoteReSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteReSpawnRequest]) (*connect.Response[internalpb.RemoteReSpawnResponse], error) {
	// get a context log
	logger := x.logger

	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	// set the actor path with the remoting is enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	// construct the actor address
	actorPath := NewPath(reqCopy.GetName(), NewAddress(x.Name(), reqCopy.GetHost(), int(reqCopy.GetPort())))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath)

	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrAddressNotFound(actorPath.String()).Error())
		return nil, ErrAddressNotFound(actorPath.String())
	}

	// restart the given actor
	if err := pid.Restart(ctx); err != nil {
		// return the error in case the restart failed
		return nil, errors.Wrapf(err, "failed to restart actor=%s", actorPath.String())
	}

	// let us re-add the actor to the actor system list when it has successfully restarted
	// add the given actor to the actor map
	x.actors.Set(pid)

	// return the response
	return connect.NewResponse(new(internalpb.RemoteReSpawnResponse)), nil
}

// handleRemoteAsk handles a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (x *actorSystem) handleRemoteAsk(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "HandleRemoteAsk")
	// defer the closing of the span
	defer span.End()
	return Ask(spanCtx, to, message, x.replyTimeout)
}

// handleRemoteTell handles an asynchronous message to an actor
func (x *actorSystem) handleRemoteTell(ctx context.Context, to PID, message proto.Message) error {
	// start a tracing span
	spanCtx, span := x.tracer.Start(ctx, "HandleRemoteTell")
	// defer the closing of the span
	defer span.End()
	return Tell(spanCtx, to, message)
}

// enableClustering enables clustering. When clustering is enabled remoting is also enabled to facilitate remote
// communication
func (x *actorSystem) enableClustering(ctx context.Context) {
	// add some logging information
	x.logger.Info("enabling clustering...")

	// create an instance of the cluster service and start it
	cluster, err := cluster.NewNode(x.Name(),
		x.serviceDiscovery,
		cluster.WithLogger(x.logger),
		cluster.WithPartitionsCount(x.partitionsCount),
		cluster.WithHasher(x.partitionHasher),
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
	// set the cluster events channel
	x.eventsChan = cluster.Events()
	// set the remoting host and port
	x.remotingHost = cluster.NodeHost()
	x.remotingPort = int32(cluster.NodeRemotingPort())
	// release the lock
	x.sem.Unlock()
	// start listening to cluster events
	go x.broadcastClusterEvents()
	// start broadcasting cluster message
	go x.broadcast(ctx)
	// add some logging
	x.logger.Info("clustering enabled...:)")
}

// enableRemoting enables the remoting service to handle remote messaging
func (x *actorSystem) enableRemoting(ctx context.Context) {
	// add some logging information
	x.logger.Info("enabling remoting...")

	// define a variable to hold the interceptor
	var interceptor *otelconnect.Interceptor
	var err error
	// only set the observability when metric or trace is enabled
	if x.metricEnabled.Load() || x.traceEnabled.Load() {
		// create an interceptor and panic
		interceptor, err = otelconnect.NewInterceptor(
			otelconnect.WithTracerProvider(x.telemetry.TracerProvider),
			otelconnect.WithMeterProvider(x.telemetry.MeterProvider),
		)
		// panic when there is an error
		if err != nil {
			x.logger.Panic(errors.Wrap(err, "failed to initialize observability feature"))
		}
	}

	// create the handler option
	var opts []connect.HandlerOption
	// set handler options when interceptor is defined
	if interceptor != nil {
		opts = append(opts, connect.WithInterceptors(interceptor))
	}

	// create a http service mux
	mux := http.NewServeMux()
	// create the resource and handler
	path, handler := internalpbconnect.NewRemotingServiceHandler(x, opts...)
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
	x.cluster = nil
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
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				// loop over the actors in the system and remove the dead one
				for _, actor := range x.Actors() {
					if !actor.IsRunning() {
						x.logger.Infof("Removing actor=%s from system", actor.ActorPath().Name())
						// delete the actor from the map
						x.actors.Delete(actor.ActorPath())
						// when cluster is enabled
						if x.InCluster() {
							// remove the actor
							if err := x.cluster.RemoveActor(context.Background(), actor.ActorPath().Name()); err != nil {
								// log the error
								x.logger.Error(err.Error())
								// TODO: stop or continue
							}
						}
					}
				}
			case <-x.housekeeperStopSig:
				// set the done channel to stop the ticker
				tickerStopSig <- types.Unit{}
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

// registerMetrics register the PID metrics with OTel instrumentation.
func (x *actorSystem) registerMetrics() error {
	// grab the OTel meter
	meter := x.telemetry.Meter
	// create an instance of the ActorMetrics
	metrics, err := metric.NewActorSystemMetric(meter)
	// handle the error
	if err != nil {
		return err
	}

	// register the metrics
	_, err = meter.RegisterCallback(func(ctx context.Context, observer otelmetric.Observer) error {
		observer.ObserveInt64(metrics.ActorsCount(), int64(x.NumActors()))
		return nil
	}, metrics.ActorsCount())

	return err
}

// broadcastClusterEvents listens to cluster events
func (x *actorSystem) broadcastClusterEvents() {
	// read from the channel
	for event := range x.eventsChan {
		// when cluster is enabled
		if x.clusterEnabled.Load() {
			// only push cluster event when defined
			if event != nil && event.Type != nil {
				// unpack the event
				message, _ := event.Type.UnmarshalNew()
				// push the cluster event when event stream is set
				if x.eventsStream != nil {
					// add some debug log
					x.logger.Debugf("node=(%s) publishing cluster event=(%s)....", x.name, event.Type.GetTypeUrl())
					// send the event to the event streams
					x.eventsStream.Publish(eventsTopic, message)
					// add some debug log
					x.logger.Debugf("cluster event=(%s) successfully published by node=(%s)", event.Type.GetTypeUrl(), x.name)
				}
			}
		}
	}
}
