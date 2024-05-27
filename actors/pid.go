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
	stdhttp "net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/flowchartsman/retry"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/eventstream"
	"github.com/tochemey/goakt/v2/internal/http"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v2/internal/metric"
	"github.com/tochemey/goakt/v2/internal/slices"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/telemetry"
)

// watcher is used to handle parent child relationship.
// This helps handle error propagation from a child actor using any of supervisory strategies
type watcher struct {
	WatcherID PID             // the WatcherID of the actor watching
	ErrChan   chan error      // ErrChan the channel where to pass error message
	Done      chan types.Unit // Done when watching is completed
}

// PID defines the various actions one can perform on a given actor
type PID interface {
	// ID is a convenient method that returns the actor unique identifier
	// An actor unique identifier is its address in the actor system.
	ID() string
	// Shutdown gracefully shuts down the given actor
	// All current messages in the mailbox will be processed before the actor shutdown after a period of time
	// that can be configured. All child actors will be gracefully shutdown.
	Shutdown(ctx context.Context) error
	// IsRunning returns true when the actor is running ready to process public and false
	// when the actor is stopped or not started at all
	IsRunning() bool
	// SpawnChild creates a child actor
	// When the given child actor already exists its PID will only be returned
	SpawnChild(ctx context.Context, name string, actor Actor) (PID, error)
	// Restart restarts the actor
	Restart(ctx context.Context) error
	// Watch an actor
	Watch(pid PID)
	// UnWatch stops watching a given actor
	UnWatch(pid PID)
	// ActorSystem returns the underlying actor system
	ActorSystem() ActorSystem
	// ActorPath returns the path of the actor
	ActorPath() *Path
	// ActorHandle returns the underlying actor
	ActorHandle() Actor
	// Tell sends an asynchronous message to another PID
	Tell(ctx context.Context, to PID, message proto.Message) error
	// BatchTell sends an asynchronous bunch of messages to the given PID
	// The messages will be processed one after the other in the order they are sent
	// This is a design choice to follow the simple principle of one message at a time processing by actors.
	// When BatchTell encounter a single message it will fall back to a Tell call.
	BatchTell(ctx context.Context, to PID, messages ...proto.Message) error
	// Ask sends a synchronous message to another actor and expect a response.
	Ask(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error)
	// BatchAsk sends a synchronous bunch of messages to the given PID and expect responses in the same order as the messages.
	// The messages will be processed one after the other in the order they are sent
	// This is a design choice to follow the simple principle of one message at a time processing by actors.
	// This can hinder performance if it is not properly used.
	BatchAsk(ctx context.Context, to PID, messages ...proto.Message) (responses chan proto.Message, err error)
	// RemoteTell sends a message to an actor remotely without expecting any reply
	RemoteTell(ctx context.Context, to *goaktpb.Address, message proto.Message) error
	// RemoteBatchTell sends a batch of messages to a remote actor in a way fire-and-forget manner
	// Messages are processed one after the other in the order they are sent.
	RemoteBatchTell(ctx context.Context, to *goaktpb.Address, messages ...proto.Message) error
	// RemoteAsk is used to send a message to an actor remotely and expect a response
	// immediately. With this type of message the receiver cannot communicate back to Sender
	// except reply the message with a response. This one-way communication.
	RemoteAsk(ctx context.Context, to *goaktpb.Address, message proto.Message) (response *anypb.Any, err error)
	// RemoteBatchAsk sends a synchronous bunch of messages to a remote actor and expect responses in the same order as the messages.
	// Messages are processed one after the other in the order they are sent.
	// This can hinder performance if it is not properly used.
	RemoteBatchAsk(ctx context.Context, to *goaktpb.Address, messages ...proto.Message) (responses []*anypb.Any, err error)
	// RemoteLookup look for an actor address on a remote node.
	RemoteLookup(ctx context.Context, host string, port int, name string) (addr *goaktpb.Address, err error)
	// RemoteReSpawn restarts an actor on a remote node.
	RemoteReSpawn(ctx context.Context, host string, port int, name string) error
	// RemoteStop stops an actor on a remote node
	RemoteStop(ctx context.Context, host string, port int, name string) error
	// RemoteSpawn creates an actor on a remote node. The given actor needs to be registered on the remote node using the Register method of ActorSystem
	RemoteSpawn(ctx context.Context, host string, port int, name, actorType string) error
	// Children returns the list of all the children of the given actor that are still alive
	// or an empty list.
	Children() []PID
	// Child returns the named child actor if it is alive
	Child(name string) (PID, error)
	// Stop forces the child Actor under the given name to terminate after it finishes processing its current message.
	// Nothing happens if child is already stopped.
	Stop(ctx context.Context, pid PID) error
	// StashSize returns the stash buffer size
	StashSize() uint64
	// Equals is a convenient method to compare two PIDs
	Equals(to PID) bool
	// push a message to the actor's receiveContextBuffer
	doReceive(ctx ReceiveContext)
	// watchers returns the list of watchMen
	watchers() *slices.ConcurrentSlice[*watcher]
	// setBehavior is a utility function that helps set the actor behavior
	setBehavior(behavior Behavior)
	// setBehaviorStacked adds a behavior to the actor's behaviors
	setBehaviorStacked(behavior Behavior)
	// unsetBehaviorStacked sets the actor's behavior to the previous behavior
	// prior to setBehaviorStacked is called
	unsetBehaviorStacked()
	// resetBehavior is a utility function resets the actor behavior
	resetBehavior()
	// stash adds the current message to the stash buffer
	stash(ctx ReceiveContext) error
	// unstashAll unstashes all messages from the stash buffer and prepends in the mailbox
	// (it keeps the messages in the same order as received, unstashing older messages before newer)
	unstashAll() error
	// unstash unstashes the oldest message in the stash and prepends to the mailbox
	unstash() error
	// toDeadletters add the given message to the deadletters queue
	handleError(receiveCtx ReceiveContext, err error)
	// removeChild is a utility function to remove child actor
	removeChild(pid PID)
}

// pid specifies an actor unique process
// With the pid one can send a receiveContext to the actor
type pid struct {
	Actor

	// specifies the actor path
	actorPath *Path

	// helps determine whether the actor should handle public or not.
	isRunning atomic.Bool
	// is captured whenever a mail is sent to the actor
	lastProcessingTime atomic.Time

	// specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 120 seconds
	passivateAfter atomic.Duration

	// specifies how long the sender of a mail should wait to receive a reply
	// when using Ask. The default value is 5s
	replyTimeout atomic.Duration

	// specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	initMaxRetries atomic.Int32

	// specifies the init timeout. the default initialization timeout is
	// 1s
	initTimeout atomic.Duration

	// shutdownTimeout specifies the graceful shutdown timeout
	// the default value is 5 seconds
	shutdownTimeout atomic.Duration

	// specifies the actor mailbox
	mailbox     Mailbox
	mailboxSize uint64

	// receives a shutdown signal. Once the signal is received
	// the actor is shut down gracefully.
	shutdownSignal     chan types.Unit
	haltPassivationLnr chan types.Unit

	// set of watchersList watching the given actor
	watchersList *slices.ConcurrentSlice[*watcher]

	// hold the list of the children
	children *pidMap

	// the actor system
	system ActorSystem

	// specifies the logger to use
	logger log.Logger

	// specifies the last processing message duration
	lastProcessingDuration atomic.Duration

	// rwMutex that helps synchronize the pid in a concurrent environment
	// this helps protect the pid fields accessibility
	rwMutex   *sync.RWMutex
	stopMutex *sync.Mutex

	// supervisor strategy
	supervisorStrategy StrategyDirective

	// observability settings
	telemetry *telemetry.Telemetry

	// http client
	httpClient *stdhttp.Client

	// specifies the current actor behavior
	behaviorStack *behaviorStack

	// stash settings
	stashBuffer   Mailbox
	stashCapacity atomic.Uint64
	stashMutex    *sync.Mutex

	// define an events stream
	eventsStream *eventstream.EventsStream

	// define tracing
	traceEnabled atomic.Bool
	tracer       trace.Tracer

	// set the metric settings
	restartCount  *atomic.Int64
	childrenCount *atomic.Int64
	metricEnabled atomic.Bool
}

// enforce compilation error
var _ PID = (*pid)(nil)

// newPID creates a new pid
func newPID(ctx context.Context, actorPath *Path, actor Actor, opts ...pidOption) (*pid, error) {
	p := &pid{
		Actor:              actor,
		lastProcessingTime: atomic.Time{},
		shutdownSignal:     make(chan types.Unit, 1),
		haltPassivationLnr: make(chan types.Unit, 1),
		logger:             log.DefaultLogger,
		mailboxSize:        defaultMailboxSize,
		children:           newPIDMap(10),
		supervisorStrategy: DefaultSupervisoryStrategy,
		watchersList:       slices.NewConcurrentSlice[*watcher](),
		telemetry:          telemetry.New(),
		actorPath:          actorPath,
		rwMutex:            &sync.RWMutex{},
		stopMutex:          &sync.Mutex{},
		httpClient:         http.NewClient(),
		mailbox:            nil,
		stashBuffer:        nil,
		stashMutex:         &sync.Mutex{},
		eventsStream:       nil,
		tracer:             noop.NewTracerProvider().Tracer("PID"),
		restartCount:       atomic.NewInt64(0),
		childrenCount:      atomic.NewInt64(0),
	}

	p.initMaxRetries.Store(DefaultInitMaxRetries)
	p.shutdownTimeout.Store(DefaultShutdownTimeout)
	p.lastProcessingDuration.Store(0)
	p.stashCapacity.Store(0)
	p.isRunning.Store(false)
	p.passivateAfter.Store(DefaultPassivationTimeout)
	p.replyTimeout.Store(DefaultReplyTimeout)
	p.initTimeout.Store(DefaultInitTimeout)
	p.traceEnabled.Store(false)
	p.metricEnabled.Store(false)

	for _, opt := range opts {
		opt(p)
	}

	if p.mailbox == nil {
		p.mailbox = newReceiveContextBuffer(p.mailboxSize)
	}

	if p.stashCapacity.Load() > 0 {
		p.stashBuffer = newReceiveContextBuffer(p.stashCapacity.Load())
	}

	behaviorStack := newBehaviorStack()
	behaviorStack.Push(p.Receive)
	p.behaviorStack = behaviorStack

	if p.traceEnabled.Load() {
		p.tracer = p.telemetry.Tracer
	}

	if err := p.init(ctx); err != nil {
		return nil, err
	}

	go p.receive()

	if p.passivateAfter.Load() > 0 {
		go p.passivationListener()
	}

	if p.metricEnabled.Load() {
		if err := p.registerMetrics(); err != nil {
			return nil, errors.Wrapf(err, "failed to register actor=%s metrics", p.ActorPath().String())
		}
	}

	p.doReceive(newReceiveContext(ctx, NoSender, p, new(goaktpb.PostStart), true))

	return p, nil
}

// ID is a convenient method that returns the actor unique identifier
// An actor unique identifier is its address in the actor system.
func (p *pid) ID() string {
	return p.ActorPath().String()
}

// Equals is a convenient method to compare two PIDs
func (p *pid) Equals(to PID) bool {
	return strings.EqualFold(p.ID(), to.ID())
}

// ActorHandle returns the underlying Actor
func (p *pid) ActorHandle() Actor {
	return p.Actor
}

// Child returns the named child actor if it is alive
func (p *pid) Child(name string) (PID, error) {
	if !p.IsRunning() {
		return nil, ErrDead
	}
	childActorPath := NewPath(name, p.ActorPath().Address()).WithParent(p.ActorPath())
	if cid, ok := p.children.get(childActorPath); ok {
		p.childrenCount.Inc()
		return cid, nil
	}
	return nil, ErrActorNotFound(childActorPath.String())
}

// Children returns the list of all the children of the given actor that are still alive or an empty list
func (p *pid) Children() []PID {
	p.rwMutex.RLock()
	children := p.children.pids()
	p.rwMutex.RUnlock()

	cids := make([]PID, 0, len(children))
	for _, child := range children {
		if child.IsRunning() {
			cids = append(cids, child)
		}
	}

	return cids
}

// Stop forces the child Actor under the given name to terminate after it finishes processing its current message.
// Nothing happens if child is already stopped.
func (p *pid) Stop(ctx context.Context, cid PID) error {
	spanCtx, span := p.tracer.Start(ctx, "Stop")
	defer span.End()

	if !p.IsRunning() {
		span.SetStatus(codes.Error, "Stop")
		span.RecordError(ErrDead)
		return ErrDead
	}

	if cid == nil || cid == NoSender {
		span.SetStatus(codes.Error, "Stop")
		span.RecordError(ErrUndefinedActor)
		return ErrUndefinedActor
	}

	p.rwMutex.RLock()
	children := p.children
	p.rwMutex.RUnlock()

	if cid, ok := children.get(cid.ActorPath()); ok {
		desc := fmt.Sprintf("child.[%s] Shutdown", cid.ActorPath())
		if err := cid.Shutdown(spanCtx); err != nil {
			span.SetStatus(codes.Error, desc)
			span.RecordError(err)
			return err
		}

		span.SetStatus(codes.Ok, desc)
		return nil
	}

	err := ErrActorNotFound(cid.ActorPath().String())
	span.SetStatus(codes.Error, "Stop")
	span.RecordError(err)
	return err
}

// IsRunning returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (p *pid) IsRunning() bool {
	return p != nil && p != NoSender && p.isRunning.Load()
}

// ActorSystem returns the actor system
func (p *pid) ActorSystem() ActorSystem {
	p.rwMutex.RLock()
	sys := p.system
	p.rwMutex.RUnlock()
	return sys
}

// ActorPath returns the path of the actor
func (p *pid) ActorPath() *Path {
	p.rwMutex.RLock()
	path := p.actorPath
	p.rwMutex.RUnlock()
	return path
}

// Restart restarts the actor.
// During restart all messages that are in the mailbox and not yet processed will be ignored
func (p *pid) Restart(ctx context.Context) error {
	spanCtx, span := p.tracer.Start(ctx, "Restart")
	defer span.End()

	if p == nil || p.ActorPath() == nil {
		span.SetStatus(codes.Error, "Restart")
		span.RecordError(ErrUndefinedActor)
		return ErrUndefinedActor
	}

	p.logger.Debugf("restarting actor=(%s)", p.actorPath.String())

	if p.IsRunning() {
		if err := p.Shutdown(spanCtx); err != nil {
			return err
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		tickerStopSig := make(chan types.Unit, 1)
		go func() {
			for range ticker.C {
				if !p.IsRunning() {
					tickerStopSig <- types.Unit{}
					return
				}
			}
		}()
		<-tickerStopSig
		ticker.Stop()
	}

	p.mailbox.Reset()
	p.resetBehavior()
	if err := p.init(spanCtx); err != nil {
		return err
	}
	go p.receive()
	if p.passivateAfter.Load() > 0 {
		go p.passivationListener()
	}

	span.SetStatus(codes.Ok, "Restart")
	p.restartCount.Inc()

	if p.eventsStream != nil {
		p.eventsStream.Publish(eventsTopic, &goaktpb.ActorRestarted{
			Address:     p.ActorPath().RemoteAddress(),
			RestartedAt: timestamppb.Now(),
		})
	}

	return nil
}

// SpawnChild creates a child actor and start watching it for error
// When the given child actor already exists its PID will only be returned
func (p *pid) SpawnChild(ctx context.Context, name string, actor Actor) (PID, error) {
	spanCtx, span := p.tracer.Start(ctx, "SpawnChild")
	defer span.End()

	if !p.IsRunning() {
		span.SetStatus(codes.Error, "SpawnChild")
		span.RecordError(ErrDead)
		return nil, ErrDead
	}

	childActorPath := NewPath(name, p.ActorPath().Address()).WithParent(p.ActorPath())

	p.rwMutex.RLock()
	children := p.children
	p.rwMutex.RUnlock()

	if cid, ok := children.get(childActorPath); ok {
		return cid, nil
	}

	p.rwMutex.Lock()
	defer p.rwMutex.Unlock()

	// create the child actor options child inherit parent's options
	opts := []pidOption{
		withInitMaxRetries(int(p.initMaxRetries.Load())),
		withPassivationAfter(p.passivateAfter.Load()),
		withSendReplyTimeout(p.replyTimeout.Load()),
		withCustomLogger(p.logger),
		withActorSystem(p.system),
		withSupervisorStrategy(p.supervisorStrategy),
		withMailboxSize(p.mailboxSize),
		withStash(p.stashCapacity.Load()),
		withMailbox(p.mailbox.Clone()),
		withEventsStream(p.eventsStream),
		withInitTimeout(p.initTimeout.Load()),
		withShutdownTimeout(p.shutdownTimeout.Load()),
	}

	if p.traceEnabled.Load() {
		opts = append(opts, withTracing())
	}

	if p.metricEnabled.Load() {
		opts = append(opts, withMetric())
	}

	cid, err := newPID(spanCtx,
		childActorPath,
		actor,
		opts...,
	)

	if err != nil {
		span.SetStatus(codes.Error, "SpawnChild")
		span.RecordError(err)
		return nil, err
	}

	p.children.set(cid)
	p.Watch(cid)

	span.SetStatus(codes.Ok, "SpawnChild")

	if p.eventsStream != nil {
		p.eventsStream.Publish(eventsTopic, &goaktpb.ActorChildCreated{
			Address:   cid.ActorPath().RemoteAddress(),
			CreatedAt: timestamppb.Now(),
			Parent:    p.ActorPath().RemoteAddress(),
		})
	}

	return cid, nil
}

// StashSize returns the stash buffer size
func (p *pid) StashSize() uint64 {
	if p.stashBuffer == nil {
		return 0
	}
	return p.stashBuffer.Size()
}

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (p *pid) Ask(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
	spanCtx, span := p.tracer.Start(ctx, "Ask")
	defer span.End()

	if !to.IsRunning() {
		span.SetStatus(codes.Error, "Ask")
		span.RecordError(ErrDead)
		return nil, ErrDead
	}

	messageContext := newReceiveContext(spanCtx, p, to, message, false)
	to.doReceive(messageContext)

	for await := time.After(p.replyTimeout.Load()); ; {
		select {
		case response = <-messageContext.response:
			span.SetStatus(codes.Ok, "Ask")
			return
		case <-await:
			err = ErrRequestTimeout
			span.SetStatus(codes.Error, "Ask")
			span.RecordError(err)
			p.handleError(messageContext, err)
			return nil, err
		}
	}
}

// Tell sends an asynchronous message to another PID
func (p *pid) Tell(ctx context.Context, to PID, message proto.Message) error {
	spanCtx, span := p.tracer.Start(ctx, "Tell")
	defer span.End()

	if !to.IsRunning() {
		span.SetStatus(codes.Error, "Tell")
		span.RecordError(ErrDead)
		return ErrDead
	}

	messageContext := newReceiveContext(spanCtx, p, to, message, true)
	to.doReceive(messageContext)

	span.SetStatus(codes.Ok, "Tell")
	return nil
}

// BatchTell sends an asynchronous bunch of messages to the given PID
// The messages will be processed one after the other in the order they are sent.
// This is a design choice to follow the simple principle of one message at a time processing by actors.
// When BatchTell encounter a single message it will fall back to a Tell call.
func (p *pid) BatchTell(ctx context.Context, to PID, messages ...proto.Message) error {
	spanCtx, span := p.tracer.Start(ctx, "BatchTell")
	defer span.End()

	if !to.IsRunning() {
		span.SetStatus(codes.Error, "BatchTell")
		span.RecordError(ErrDead)
		return ErrDead
	}

	if len(messages) == 1 {
		// no need to record span error here because Tell handles it
		return p.Tell(spanCtx, to, messages[0])
	}

	for i := 0; i < len(messages); i++ {
		message := messages[i]
		messageContext := newReceiveContext(ctx, p, to, message, true)
		to.doReceive(messageContext)
	}

	span.SetStatus(codes.Ok, "BatchTell")
	return nil
}

// BatchAsk sends a synchronous bunch of messages to the given PID and expect responses in the same order as the messages.
// The messages will be processed one after the other in the order they are sent.
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func (p *pid) BatchAsk(ctx context.Context, to PID, messages ...proto.Message) (responses chan proto.Message, err error) {
	spanCtx, span := p.tracer.Start(ctx, "BatchAsk")
	defer span.End()

	if !to.IsRunning() {
		span.SetStatus(codes.Error, "BatchAsk")
		span.RecordError(ErrDead)
		return nil, ErrDead
	}

	responses = make(chan proto.Message, len(messages))
	defer close(responses)

	for i := 0; i < len(messages); i++ {
		message := messages[i]
		messageContext := newReceiveContext(spanCtx, p, to, message, false)
		to.doReceive(messageContext)

		// await patiently to receive the response from the actor
	timerLoop:
		for await := time.After(p.replyTimeout.Load()); ; {
			select {
			case resp := <-messageContext.response:
				responses <- resp
				span.SetStatus(codes.Ok, "BatchAsk")
				break timerLoop
			case <-await:
				err = ErrRequestTimeout
				span.SetStatus(codes.Error, "BatchAsk")
				span.RecordError(err)
				p.handleError(messageContext, err)
				return nil, err
			}
		}
	}
	return
}

// RemoteLookup look for an actor address on a remote node.
func (p *pid) RemoteLookup(ctx context.Context, host string, port int, name string) (addr *goaktpb.Address, err error) {
	remoteClient, err := p.getRemoteServiceClient(host, port)
	if err != nil {
		return nil, err
	}

	request := connect.NewRequest(&internalpb.RemoteLookupRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})

	response, err := remoteClient.RemoteLookup(ctx, request)
	if err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil, nil
		}
		return nil, err
	}

	return response.Msg.GetAddress(), nil
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (p *pid) RemoteTell(ctx context.Context, to *goaktpb.Address, message proto.Message) error {
	marshaled, err := anypb.New(message)
	if err != nil {
		return err
	}

	remoteService, err := p.getRemoteServiceClient(to.GetHost(), int(to.GetPort()))
	if err != nil {
		return err
	}

	sender := &goaktpb.Address{
		Host: p.ActorPath().Address().Host(),
		Port: int32(p.ActorPath().Address().Port()),
		Name: p.ActorPath().Name(),
		Id:   p.ActorPath().ID().String(),
	}

	request := &internalpb.RemoteTellRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   sender,
			Receiver: to,
			Message:  marshaled,
		},
	}

	p.logger.Debugf("sending a message to remote=(%s:%d)", to.GetHost(), to.GetPort())
	stream := remoteService.RemoteTell(ctx)
	if err := stream.Send(request); err != nil {
		if IsEOF(err) {
			if _, err := stream.CloseAndReceive(); err != nil {
				return err
			}
			return nil
		}
		p.logger.Error(errors.Wrapf(err, "failed to send message to remote=(%s:%d)", to.GetHost(), to.GetPort()))
		return err
	}

	if _, err := stream.CloseAndReceive(); err != nil {
		p.logger.Error(errors.Wrapf(err, "failed to send message to remote=(%s:%d)", to.GetHost(), to.GetPort()))
		return err
	}

	p.logger.Debugf("message successfully sent to remote=(%s:%d)", to.GetHost(), to.GetPort())
	return nil
}

// RemoteAsk sends a synchronous message to another actor remotely and expect a response.
func (p *pid) RemoteAsk(ctx context.Context, to *goaktpb.Address, message proto.Message) (response *anypb.Any, err error) {
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, err
	}

	remoteService, err := p.getRemoteServiceClient(to.GetHost(), int(to.GetPort()))
	if err != nil {
		return nil, err
	}

	senderPath := p.ActorPath()
	senderAddress := senderPath.Address()

	sender := &goaktpb.Address{
		Host: senderAddress.Host(),
		Port: int32(senderAddress.Port()),
		Name: senderPath.Name(),
		Id:   senderPath.ID().String(),
	}

	request := &internalpb.RemoteAskRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   sender,
			Receiver: to,
			Message:  marshaled,
		},
	}

	stream := remoteService.RemoteAsk(ctx)
	errc := make(chan error, 1)

	go func() {
		defer close(errc)
		for {
			resp, err := stream.Receive()
			if err != nil {
				errc <- err
				return
			}

			response = resp.GetMessage()
		}
	}()

	err = stream.Send(request)
	if err != nil {
		return nil, err
	}

	if err := stream.CloseRequest(); err != nil {
		return nil, err
	}

	err = <-errc
	if IsEOF(err) {
		return response, nil
	}

	if err != nil {
		return nil, err
	}

	return
}

// RemoteBatchTell sends a batch of messages to a remote actor in a way fire-and-forget manner
// Messages are processed one after the other in the order they are sent.
func (p *pid) RemoteBatchTell(ctx context.Context, to *goaktpb.Address, messages ...proto.Message) error {
	if len(messages) == 1 {
		return p.RemoteTell(ctx, to, messages[0])
	}

	senderPath := p.ActorPath()
	senderAddress := senderPath.Address()

	sender := &goaktpb.Address{
		Host: senderAddress.Host(),
		Port: int32(senderAddress.Port()),
		Name: senderPath.Name(),
		Id:   senderPath.ID().String(),
	}

	var requests []*internalpb.RemoteTellRequest
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return ErrInvalidRemoteMessage(err)
		}

		requests = append(requests, &internalpb.RemoteTellRequest{
			RemoteMessage: &internalpb.RemoteMessage{
				Sender:   sender,
				Receiver: to,
				Message:  packed,
			},
		})
	}

	remoteService, err := p.getRemoteServiceClient(to.GetHost(), int(to.GetPort()))
	if err != nil {
		return err
	}

	stream := remoteService.RemoteTell(ctx)
	for _, request := range requests {
		if err := stream.Send(request); err != nil {
			if IsEOF(err) {
				if _, err := stream.CloseAndReceive(); err != nil {
					return err
				}
				return nil
			}
			return err
		}
	}

	// close the connection
	if _, err := stream.CloseAndReceive(); err != nil {
		return err
	}

	return nil
}

// RemoteBatchAsk sends a synchronous bunch of messages to a remote actor and expect responses in the same order as the messages.
// Messages are processed one after the other in the order they are sent.
// This can hinder performance if it is not properly used.
func (p *pid) RemoteBatchAsk(ctx context.Context, to *goaktpb.Address, messages ...proto.Message) (responses []*anypb.Any, err error) {
	senderPath := p.ActorPath()
	senderAddress := senderPath.Address()

	sender := &goaktpb.Address{
		Host: senderAddress.Host(),
		Port: int32(senderAddress.Port()),
		Name: senderPath.Name(),
		Id:   senderPath.ID().String(),
	}

	var requests []*internalpb.RemoteAskRequest
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return nil, ErrInvalidRemoteMessage(err)
		}

		requests = append(requests, &internalpb.RemoteAskRequest{
			RemoteMessage: &internalpb.RemoteMessage{
				Sender:   sender,
				Receiver: to,
				Message:  packed,
			},
		})
	}

	remoteService, err := p.getRemoteServiceClient(to.GetHost(), int(to.GetPort()))
	if err != nil {
		return nil, err
	}

	stream := remoteService.RemoteAsk(ctx)
	errc := make(chan error, 1)

	go func() {
		defer close(errc)
		for {
			resp, err := stream.Receive()
			if err != nil {
				errc <- err
				return
			}

			responses = append(responses, resp.GetMessage())
		}
	}()

	for _, request := range requests {
		err := stream.Send(request)
		if err != nil {
			return nil, err
		}
	}

	if err := stream.CloseRequest(); err != nil {
		return nil, err
	}

	err = <-errc
	if IsEOF(err) {
		return responses, nil
	}

	if err != nil {
		return nil, err
	}

	return
}

// RemoteStop stops an actor on a remote node
func (p *pid) RemoteStop(ctx context.Context, host string, port int, name string) error {
	remoteService, err := p.getRemoteServiceClient(host, port)
	if err != nil {
		return err
	}

	request := connect.NewRequest(&internalpb.RemoteStopRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})

	if _, err = remoteService.RemoteStop(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil
		}
		return err
	}

	return nil
}

// RemoteSpawn creates an actor on a remote node. The given actor needs to be registered on the remote node using the Register method of ActorSystem
func (p *pid) RemoteSpawn(ctx context.Context, host string, port int, name, actorType string) error {
	remoteService, err := p.getRemoteServiceClient(host, port)
	if err != nil {
		return err
	}

	request := connect.NewRequest(&internalpb.RemoteSpawnRequest{
		Host:      host,
		Port:      int32(port),
		ActorName: name,
		ActorType: actorType,
	})

	if _, err := remoteService.RemoteSpawn(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeFailedPrecondition {
			connectErr := err.(*connect.Error)
			e := connectErr.Unwrap()
			// TODO: find a better way to use errors.Is with connect.Error
			if strings.Contains(e.Error(), ErrTypeNotRegistered.Error()) {
				return ErrTypeNotRegistered
			}
		}
		return err
	}

	return nil
}

// RemoteReSpawn restarts an actor on a remote node.
func (p *pid) RemoteReSpawn(ctx context.Context, host string, port int, name string) error {
	remoteService, err := p.getRemoteServiceClient(host, port)
	if err != nil {
		return err
	}

	request := connect.NewRequest(&internalpb.RemoteReSpawnRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})

	if _, err = remoteService.RemoteReSpawn(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil
		}
		return err
	}

	return nil
}

// Shutdown gracefully shuts down the given actor
// All current messages in the mailbox will be processed before the actor shutdown after a period of time
// that can be configured. All child actors will be gracefully shutdown.
func (p *pid) Shutdown(ctx context.Context) error {
	spanCtx, span := p.tracer.Start(ctx, "Shutdown")
	defer span.End()

	p.stopMutex.Lock()
	defer p.stopMutex.Unlock()

	p.logger.Info("Shutdown process has started...")

	if !p.isRunning.Load() {
		p.logger.Infof("Actor=%s is offline. Maybe it has been passivated or stopped already", p.ActorPath().String())
		span.SetStatus(codes.Ok, "Shutdown")
		return nil
	}

	if p.passivateAfter.Load() > 0 {
		p.logger.Debug("sending a signal to stop passivation listener....")
		p.haltPassivationLnr <- types.Unit{}
	}

	if err := p.doStop(spanCtx); err != nil {
		p.logger.Errorf("failed to stop actor=(%s)", p.ActorPath().String())
		return err
	}

	if p.eventsStream != nil {
		p.eventsStream.Publish(eventsTopic, &goaktpb.ActorStopped{
			Address:   p.ActorPath().RemoteAddress(),
			StoppedAt: timestamppb.Now(),
		})
	}

	p.logger.Infof("Actor=%s successfully shutdown", p.ActorPath().String())
	span.SetStatus(codes.Ok, "Shutdown")
	return nil
}

// Watch a pid for errors, and send on the returned channel if an error occurred
func (p *pid) Watch(cid PID) {
	w := &watcher{
		WatcherID: p,
		ErrChan:   make(chan error, 1),
		Done:      make(chan types.Unit, 1),
	}
	cid.watchers().Append(w)
	go p.supervise(cid, w)
}

// UnWatch stops watching a given actor
func (p *pid) UnWatch(pid PID) {
	for item := range pid.watchers().Iter() {
		w := item.Value
		if w.WatcherID.ActorPath().Equals(p.ActorPath()) {
			w.Done <- types.Unit{}
			pid.watchers().Delete(item.Index)
			break
		}
	}
}

// Watchers return the list of watchersList
func (p *pid) watchers() *slices.ConcurrentSlice[*watcher] {
	return p.watchersList
}

// doReceive pushes a given message to the actor receiveContextBuffer
func (p *pid) doReceive(ctx ReceiveContext) {
	p.rwMutex.Lock()
	defer p.rwMutex.Unlock()

	p.lastProcessingTime.Store(time.Now())

	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		p.lastProcessingDuration.Store(duration)
	}()

	if err := p.mailbox.Push(ctx); err != nil {
		p.logger.Warn(err)
		p.handleError(ctx, err)
		return
	}
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor will not be started
func (p *pid) init(ctx context.Context) error {
	spanCtx, span := p.tracer.Start(ctx, "Init")
	defer span.End()

	p.logger.Info("Initialization process has started...")

	cancelCtx, cancel := context.WithTimeout(spanCtx, p.initTimeout.Load())
	defer cancel()

	// create a new retrier that will try a maximum of `initMaxRetries` times, with
	// an initial delay of 100 ms and a maximum delay of 1 second
	retrier := retry.NewRetrier(int(p.initMaxRetries.Load()), 100*time.Millisecond, time.Second)
	if err := retrier.RunContext(cancelCtx, p.Actor.PreStart); err != nil {
		e := ErrInitFailure(err)
		span.SetStatus(codes.Error, "Init")
		span.RecordError(e)
		return e
	}

	p.rwMutex.Lock()
	p.isRunning.Store(true)
	p.rwMutex.Unlock()

	p.logger.Info("Initialization process successfully completed.")
	span.SetStatus(codes.Ok, "Init")

	if p.eventsStream != nil {
		p.eventsStream.Publish(eventsTopic, &goaktpb.ActorStarted{
			Address:   p.ActorPath().RemoteAddress(),
			StartedAt: timestamppb.Now(),
		})
	}

	return nil
}

// reset re-initializes the actor PID
func (p *pid) reset() {
	p.lastProcessingTime = atomic.Time{}
	p.passivateAfter.Store(DefaultPassivationTimeout)
	p.replyTimeout.Store(DefaultReplyTimeout)
	p.shutdownTimeout.Store(DefaultShutdownTimeout)
	p.initMaxRetries.Store(DefaultInitMaxRetries)
	p.lastProcessingDuration.Store(0)
	p.initTimeout.Store(DefaultInitTimeout)
	p.children = newPIDMap(10)
	p.watchersList = slices.NewConcurrentSlice[*watcher]()
	p.telemetry = telemetry.New()
	p.mailbox.Reset()
	p.resetBehavior()
	if p.metricEnabled.Load() {
		if err := p.registerMetrics(); err != nil {
			p.logger.Error(errors.Wrapf(err, "failed to register actor=%s metrics", p.ActorPath().String()))
		}
	}
}

func (p *pid) freeWatchers(ctx context.Context) {
	p.logger.Debug("freeing all watcher actors...")

	watchers := p.watchers()
	if watchers.Len() > 0 {
		for item := range watchers.Iter() {
			watcher := item.Value
			terminated := &goaktpb.Terminated{}
			if watcher.WatcherID.IsRunning() {
				// TODO: handle error and push to some system dead-letters queue
				_ = p.Tell(ctx, watcher.WatcherID, terminated)
				watcher.WatcherID.UnWatch(p)
				watcher.WatcherID.removeChild(p)
			}
		}
	}
}

func (p *pid) freeChildren(ctx context.Context) {
	p.logger.Debug("freeing all child actors...")

	for _, child := range p.Children() {
		p.UnWatch(child)
		p.children.delete(child.ActorPath())
		if err := child.Shutdown(ctx); err != nil {
			p.logger.Panic(err)
		}
	}
}

// receive handles every mail in the actor receiveContextBuffer
func (p *pid) receive() {
	for {
		select {
		case <-p.shutdownSignal:
			return
		case received, ok := <-p.mailbox.Iterator():
			// break out of the loop when the channel is closed
			if !ok {
				return
			}

			switch received.Message().(type) {
			case *goaktpb.PoisonPill:
				// stop the actor
				_ = p.Shutdown(received.Context())
			default:
				p.handleReceived(received)
			}
		}
	}
}

func (p *pid) handleReceived(received ReceiveContext) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("%s", r)
			for item := range p.watchersList.Iter() {
				item.Value.ErrChan <- err
			}
		}
	}()
	// send the message to the current actor behavior
	if behavior, ok := p.behaviorStack.Peek(); ok {
		behavior(received)
	}
}

// supervise watches for child actor's failure and act based upon the supervisory strategy
func (p *pid) supervise(cid PID, watcher *watcher) {
	for {
		select {
		case <-watcher.Done:
			p.logger.Debugf("stop watching cid=(%s)", cid.ActorPath().String())
			return
		case err := <-watcher.ErrChan:
			p.logger.Errorf("child actor=(%s) is failing: Err=%v", cid.ActorPath().String(), err)
			switch p.supervisorStrategy {
			case StopDirective:
				p.UnWatch(cid)
				p.children.delete(cid.ActorPath())
				if err := cid.Shutdown(context.Background()); err != nil {
					// this can enter into some infinite loop if we panic
					// since we are just shutting down the actor we can just log the error
					// TODO: rethink properly about PostStop error handling
					p.logger.Error(err)
				}
			case RestartDirective:
				p.UnWatch(cid)
				if err := cid.Restart(context.Background()); err != nil {
					p.logger.Panic(err)
				}
				p.Watch(cid)
			default:
				p.UnWatch(cid)
				p.children.delete(cid.ActorPath())
				if err := cid.Shutdown(context.Background()); err != nil {
					// this can enter into some infinite loop if we panic
					// since we are just shutting down the actor we can just log the error
					// TODO: rethink properly about PostStop error handling
					p.logger.Error(err)
				}
			}
		}
	}
}

// passivationListener checks whether the actor is processing public or not.
// when the actor is idle, it automatically shuts down to free resources
func (p *pid) passivationListener() {
	p.logger.Info("start the passivation listener...")
	p.logger.Infof("passivation timeout is (%s)", p.passivateAfter.Load().String())
	ticker := time.NewTicker(p.passivateAfter.Load())
	tickerStopSig := make(chan types.Unit, 1)

	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				idleTime := time.Since(p.lastProcessingTime.Load())
				if idleTime >= p.passivateAfter.Load() {
					tickerStopSig <- types.Unit{}
					return
				}
			case <-p.haltPassivationLnr:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()

	if !p.IsRunning() {
		p.logger.Infof("Actor=%s is offline. No need to passivate", p.ActorPath().String())
		return
	}

	p.stopMutex.Lock()
	defer p.stopMutex.Unlock()

	p.logger.Infof("Passivation mode has been triggered for actor=%s...", p.ActorPath().String())

	ctx := context.Background()

	if err := p.doStop(ctx); err != nil {
		// TODO: rethink properly about PostStop error handling
		p.logger.Errorf("failed to passivate actor=(%s): reason=(%v)", p.ActorPath().String(), err)
		return
	}

	if p.eventsStream != nil {
		event := &goaktpb.ActorPassivated{
			Address:      p.ActorPath().RemoteAddress(),
			PassivatedAt: timestamppb.Now(),
		}
		p.eventsStream.Publish(eventsTopic, event)
	}

	p.logger.Infof("Actor=%s successfully passivated", p.ActorPath().String())
}

// setBehavior is a utility function that helps set the actor behavior
func (p *pid) setBehavior(behavior Behavior) {
	p.rwMutex.Lock()
	p.behaviorStack.Clear()
	p.behaviorStack.Push(behavior)
	p.rwMutex.Unlock()
}

// resetBehavior is a utility function resets the actor behavior
func (p *pid) resetBehavior() {
	p.rwMutex.Lock()
	p.behaviorStack.Clear()
	p.behaviorStack.Push(p.Receive)
	p.rwMutex.Unlock()
}

// setBehaviorStacked adds a behavior to the actor's behaviorStack
func (p *pid) setBehaviorStacked(behavior Behavior) {
	p.rwMutex.Lock()
	p.behaviorStack.Push(behavior)
	p.rwMutex.Unlock()
}

// unsetBehaviorStacked sets the actor's behavior to the previous behavior
// prior to setBehaviorStacked is called
func (p *pid) unsetBehaviorStacked() {
	p.rwMutex.Lock()
	p.behaviorStack.Pop()
	p.rwMutex.Unlock()
}

// doStop stops the actor
func (p *pid) doStop(ctx context.Context) error {
	spanCtx, span := p.tracer.Start(ctx, "doStop")
	defer span.End()

	p.isRunning.Store(false)

	// TODO: just signal stash processing done and ignore the messages or process them
	for p.stashBuffer != nil && !p.stashBuffer.IsEmpty() {
		if err := p.unstashAll(); err != nil {
			span.SetStatus(codes.Error, "doStop")
			span.RecordError(err)
			return err
		}
	}

	// wait for all messages in the mailbox to be processed
	// init a ticker that run every 10 ms to make sure we process all messages in the
	// mailbox.
	ticker := time.NewTicker(10 * time.Millisecond)
	timer := time.After(p.shutdownTimeout.Load())
	tickerStopSig := make(chan types.Unit)
	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				if p.mailbox.IsEmpty() {
					close(tickerStopSig)
					return
				}
			case <-timer:
				close(tickerStopSig)
				return
			}
		}
	}()

	<-tickerStopSig
	p.shutdownSignal <- types.Unit{}
	p.httpClient.CloseIdleConnections()
	p.freeChildren(spanCtx)
	p.freeWatchers(spanCtx)

	p.logger.Infof("Shutdown process is on going for actor=%s...", p.ActorPath().String())
	p.reset()
	return p.Actor.PostStop(spanCtx)
}

// removeChild helps remove child actor
func (p *pid) removeChild(pid PID) {
	if !p.IsRunning() {
		return
	}

	if pid == nil || pid == NoSender {
		return
	}

	path := pid.ActorPath()
	if cid, ok := p.children.get(path); ok {
		if cid.IsRunning() {
			return
		}
		p.children.delete(path)
	}
}

// handleError handles the error during a message handling
func (p *pid) handleError(receiveCtx ReceiveContext, err error) {
	if p.eventsStream == nil {
		return
	}

	// skip system messages
	switch receiveCtx.Message().(type) {
	case *goaktpb.PostStart:
		return
	default:
		// pass through
	}

	msg, _ := anypb.New(receiveCtx.Message())
	var senderAddr *goaktpb.Address
	if receiveCtx.Sender() != nil || receiveCtx.Sender() != NoSender {
		senderAddr = receiveCtx.Sender().ActorPath().RemoteAddress()
	}

	receiver := p.actorPath.RemoteAddress()
	deadletter := &goaktpb.Deadletter{
		Sender:   senderAddr,
		Receiver: receiver,
		Message:  msg,
		SendTime: timestamppb.Now(),
		Reason:   err.Error(),
	}

	p.eventsStream.Publish(eventsTopic, deadletter)
}

// registerMetrics register the PID metrics with OTel instrumentation.
func (p *pid) registerMetrics() error {
	meter := p.telemetry.Meter
	metrics, err := metric.NewActorMetric(meter)
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(func(ctx context.Context, observer otelmetric.Observer) error {
		observer.ObserveInt64(metrics.ChildrenCount(), p.childrenCount.Load())
		observer.ObserveInt64(metrics.StashCount(), int64(p.StashSize()))
		observer.ObserveInt64(metrics.RestartCount(), p.restartCount.Load())
		return nil
	}, metrics.ChildrenCount(),
		metrics.StashCount(),
		metrics.RestartCount())

	return err
}

// getConnectionOptions returns the gRPC client connections options
func (p *pid) getConnectionOptions() ([]connect.ClientOption, error) {
	var interceptor *otelconnect.Interceptor
	var err error
	if p.metricEnabled.Load() || p.traceEnabled.Load() {
		interceptor, err = otelconnect.NewInterceptor(
			otelconnect.WithTracerProvider(p.telemetry.TracerProvider),
			otelconnect.WithMeterProvider(p.telemetry.MeterProvider))
		if err != nil {
			return nil, errors.Wrap(err, "failed to initialize observability feature")
		}
	}

	var clientConnectionOptions []connect.ClientOption
	if interceptor != nil {
		clientConnectionOptions = append(clientConnectionOptions, connect.WithInterceptors(interceptor))
	}
	return clientConnectionOptions, err
}

// getRemoteServiceClient returns an instance of the Remote Service client
func (p *pid) getRemoteServiceClient(host string, port int) (internalpbconnect.RemotingServiceClient, error) {
	clientConnectionOptions, err := p.getConnectionOptions()
	if err != nil {
		return nil, err
	}

	return internalpbconnect.NewRemotingServiceClient(
		p.httpClient,
		http.URL(host, port),
		clientConnectionOptions...,
	), nil
}
