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
	"errors"
	"fmt"
	stdhttp "net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/flowchartsman/retry"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v2/future"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/eventstream"
	"github.com/tochemey/goakt/v2/internal/http"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v2/internal/metric"
	"github.com/tochemey/goakt/v2/internal/queue"
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

// taskCompletion is used to track completions' taskCompletion
// to pipe the result to the appropriate PID
type taskCompletion struct {
	Receiver PID
	Task     future.Task
}

// PID defines the various actions one can perform on a given actor
type PID interface {
	// ID is a convenient method that returns the actor unique identifier
	// An actor unique identifier is its address in the actor system.
	ID() string
	// Name returns the actor given name
	Name() string
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
	// Children returns the list of all the direct children of the given actor
	// Only alive actors are included in the list or an empty list is returned
	Children() []PID
	// Parents returns the list of all direct parents of a given actor.
	// Only alive actors are included in the list or an empty list is returned
	Parents() []PID
	// Child returns the named child actor if it is alive
	Child(name string) (PID, error)
	// Stop forces the child Actor under the given name to terminate after it finishes processing its current message.
	// Nothing happens if child is already stopped.
	Stop(ctx context.Context, pid PID) error
	// StashSize returns the stash buffer size
	StashSize() uint64
	// Equals is a convenient method to compare two PIDs
	Equals(to PID) bool
	// PipeTo processes a long-running task and pipes the result to the provided actor.
	// The successful result of the task will be put onto the provided actor mailbox.
	// This is useful when interacting with external services.
	// It’s common that you would like to use the value of the response in the actor when the long-running task is completed
	PipeTo(ctx context.Context, to PID, task future.Task) error
	// push a message to the actor's receiveContextBuffer
	doReceive(ctx ReceiveContext)
	// watchers returns the list of actors watching this actor
	watchers() *slices.Slice[*watcher]
	// watchees returns the list of actors watched by this actor
	watchees() *pidMap
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

	getLastProcessingTime() time.Time
	setLastProcessingDuration(d time.Duration)
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
	askTimeout atomic.Duration

	// specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	initMaxRetries atomic.Int32

	// specifies the init timeout.
	// the default initialization timeout is 1s
	initTimeout atomic.Duration

	// shutdownTimeout specifies the graceful shutdown timeout
	// the default value is 5 seconds
	shutdownTimeout atomic.Duration

	// specifies the actor mailbox
	mailbox *queue.MpscQueue[ReceiveContext]

	haltPassivationLnr chan types.Unit

	// hold the watchersList watching the given actor
	watchersList *slices.Slice[*watcher]

	// hold the list of the children
	children *pidMap

	// hold the list of watched actors
	watchedList *pidMap

	// the actor system
	system ActorSystem

	// specifies the logger to use
	logger log.Logger

	// specifies the last processing message duration
	lastReceivedDuration atomic.Duration

	// fieldsLocker that helps synchronize the pid in a concurrent environment
	// this helps protect the pid fields accessibility
	fieldsLocker *sync.RWMutex

	stopLocker           *sync.Mutex
	processingTimeLocker *sync.Mutex

	// testSupervisor strategy
	supervisorDirective SupervisorDirective

	// observability settings
	telemetry *telemetry.Telemetry

	// http client
	httpClient *stdhttp.Client

	// specifies the actor behavior stack
	behaviorStack *behaviorStack

	// stash settings
	stashBuffer *queue.MpscQueue[ReceiveContext]
	stashLocker *sync.Mutex

	// define an events stream
	eventsStream *eventstream.EventsStream

	// set the metrics settings
	restartCount   *atomic.Int64
	childrenCount  *atomic.Int64
	processedCount *atomic.Int64
	metricEnabled  atomic.Bool
	metrics        *metric.ActorMetric
	errChan        chan error
}

// enforce compilation error
var _ PID = (*pid)(nil)

// newPID creates a new pid
func newPID(ctx context.Context, actorPath *Path, actor Actor, opts ...pidOption) (*pid, error) {
	// actor path is required
	if actorPath == nil {
		return nil, errors.New("actorPath is required")
	}

	// validate actor path
	if err := actorPath.Validate(); err != nil {
		return nil, err
	}

	p := &pid{
		Actor:                actor,
		lastProcessingTime:   atomic.Time{},
		haltPassivationLnr:   make(chan types.Unit, 1),
		logger:               log.DefaultLogger,
		children:             newPIDMap(10),
		supervisorDirective:  DefaultSupervisoryStrategy,
		watchersList:         slices.New[*watcher](),
		watchedList:          newPIDMap(10),
		telemetry:            telemetry.New(),
		actorPath:            actorPath,
		fieldsLocker:         &sync.RWMutex{},
		stopLocker:           &sync.Mutex{},
		httpClient:           http.NewClient(),
		mailbox:              queue.NewMpscQueue[ReceiveContext](),
		stashBuffer:          nil,
		stashLocker:          &sync.Mutex{},
		eventsStream:         nil,
		restartCount:         atomic.NewInt64(0),
		childrenCount:        atomic.NewInt64(0),
		processedCount:       atomic.NewInt64(0),
		processingTimeLocker: new(sync.Mutex),
	}

	p.initMaxRetries.Store(DefaultInitMaxRetries)
	p.shutdownTimeout.Store(DefaultShutdownTimeout)
	p.lastReceivedDuration.Store(0)
	p.isRunning.Store(false)
	p.passivateAfter.Store(DefaultPassivationTimeout)
	p.askTimeout.Store(DefaultAskTimeout)
	p.initTimeout.Store(DefaultInitTimeout)
	p.metricEnabled.Store(false)

	for _, opt := range opts {
		opt(p)
	}

	behaviorStack := newBehaviorStack()
	behaviorStack.Push(p.Receive)
	p.behaviorStack = behaviorStack

	if err := p.init(ctx); err != nil {
		return nil, err
	}

	go p.errorLoop()
	go p.receive()

	if p.passivateAfter.Load() > 0 {
		go p.passivationLoop()
	}

	if p.metricEnabled.Load() {
		metrics, err := metric.NewActorMetric(p.telemetry.Meter)
		if err != nil {
			return nil, err
		}

		p.metrics = metrics
		if err := p.registerMetrics(); err != nil {
			return nil, fmt.Errorf("failed to register actor=%s metrics: %w", p.ID(), err)
		}
	}

	p.doReceive(newReceiveContext(ctx, NoSender, p, new(goaktpb.PostStart), true))

	return p, nil
}

// ID is a convenient method that returns the actor unique identifier
// An actor unique identifier is its address in the actor system.
func (x *pid) ID() string {
	return x.ActorPath().String()
}

// Name returns the actor given name
func (x *pid) Name() string {
	return x.ActorPath().Name()
}

// Equals is a convenient method to compare two PIDs
func (x *pid) Equals(to PID) bool {
	return strings.EqualFold(x.ID(), to.ID())
}

// ActorHandle returns the underlying Actor
func (x *pid) ActorHandle() Actor {
	return x.Actor
}

// Child returns the named child actor if it is alive
func (x *pid) Child(name string) (PID, error) {
	if !x.IsRunning() {
		return nil, ErrDead
	}
	childActorPath := NewPath(name, x.ActorPath().Address()).WithParent(x.ActorPath())
	if cid, ok := x.children.get(childActorPath); ok {
		x.childrenCount.Inc()
		return cid, nil
	}
	return nil, ErrActorNotFound(childActorPath.String())
}

// Parents returns the list of all direct parents of a given actor.
// Only alive actors are included in the list or an empty list is returned
func (x *pid) Parents() []PID {
	x.fieldsLocker.Lock()
	watchers := x.watchersList
	x.fieldsLocker.Unlock()
	var parents []PID
	if watchers.Len() > 0 {
		for item := range watchers.Iter() {
			watcher := item.Value
			if watcher != nil {
				pid := watcher.WatcherID
				if pid.IsRunning() {
					parents = append(parents, pid)
				}
			}
		}
	}
	return parents
}

// Children returns the list of all the direct children of the given actor
// Only alive actors are included in the list or an empty list is returned
func (x *pid) Children() []PID {
	x.fieldsLocker.RLock()
	children := x.children.pids()
	x.fieldsLocker.RUnlock()

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
func (x *pid) Stop(ctx context.Context, cid PID) error {
	if !x.IsRunning() {
		return ErrDead
	}

	if cid == nil || cid == NoSender {
		return ErrUndefinedActor
	}

	x.fieldsLocker.RLock()
	children := x.children
	x.fieldsLocker.RUnlock()

	if cid, ok := children.get(cid.ActorPath()); ok {
		if err := cid.Shutdown(ctx); err != nil {
			return err
		}

		return nil
	}

	return ErrActorNotFound(cid.ActorPath().String())
}

// IsRunning returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (x *pid) IsRunning() bool {
	return x != nil && x != NoSender && x.isRunning.Load()
}

// ActorSystem returns the actor system
func (x *pid) ActorSystem() ActorSystem {
	x.fieldsLocker.RLock()
	sys := x.system
	x.fieldsLocker.RUnlock()
	return sys
}

// ActorPath returns the path of the actor
func (x *pid) ActorPath() *Path {
	x.fieldsLocker.RLock()
	path := x.actorPath
	x.fieldsLocker.RUnlock()
	return path
}

// Restart restarts the actor.
// During restart all messages that are in the mailbox and not yet processed will be ignored
func (x *pid) Restart(ctx context.Context) error {
	if x == nil || x.ActorPath() == nil {
		return ErrUndefinedActor
	}

	x.logger.Debugf("restarting actor=(%s)", x.actorPath.String())

	if x.IsRunning() {
		if err := x.Shutdown(ctx); err != nil {
			return err
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		tickerStopSig := make(chan types.Unit, 1)
		go func() {
			for range ticker.C {
				if !x.IsRunning() {
					tickerStopSig <- types.Unit{}
					return
				}
			}
		}()
		<-tickerStopSig
		ticker.Stop()
	}

	x.resetBehavior()
	if err := x.init(ctx); err != nil {
		return err
	}
	go x.errorLoop()
	go x.receive()
	if x.passivateAfter.Load() > 0 {
		go x.passivationLoop()
	}

	x.restartCount.Inc()

	if x.eventsStream != nil {
		x.eventsStream.Publish(eventsTopic, &goaktpb.ActorRestarted{
			Address:     x.ActorPath().RemoteAddress(),
			RestartedAt: timestamppb.Now(),
		})
	}

	return nil
}

// SpawnChild creates a child actor and start watching it for error
// When the given child actor already exists its PID will only be returned
func (x *pid) SpawnChild(ctx context.Context, name string, actor Actor) (PID, error) {
	if !x.IsRunning() {
		return nil, ErrDead
	}

	childActorPath := NewPath(name, x.ActorPath().Address()).WithParent(x.ActorPath())

	x.fieldsLocker.RLock()
	children := x.children
	x.fieldsLocker.RUnlock()

	if cid, ok := children.get(childActorPath); ok {
		return cid, nil
	}

	x.fieldsLocker.RLock()

	// create the child actor options child inherit parent's options
	opts := []pidOption{
		withInitMaxRetries(int(x.initMaxRetries.Load())),
		withPassivationAfter(x.passivateAfter.Load()),
		withAskTimeout(x.askTimeout.Load()),
		withCustomLogger(x.logger),
		withActorSystem(x.system),
		withSupervisorDirective(x.supervisorDirective),
		withEventsStream(x.eventsStream),
		withInitTimeout(x.initTimeout.Load()),
		withShutdownTimeout(x.shutdownTimeout.Load()),
	}

	if x.metricEnabled.Load() {
		opts = append(opts, withMetric())
	}

	cid, err := newPID(ctx,
		childActorPath,
		actor,
		opts...,
	)

	if err != nil {
		x.fieldsLocker.RUnlock()
		return nil, err
	}

	x.children.set(cid)
	eventsStream := x.eventsStream

	x.fieldsLocker.RUnlock()

	x.Watch(cid)

	if eventsStream != nil {
		eventsStream.Publish(eventsTopic, &goaktpb.ActorChildCreated{
			Address:   cid.ActorPath().RemoteAddress(),
			CreatedAt: timestamppb.Now(),
			Parent:    x.ActorPath().RemoteAddress(),
		})
	}

	// set the actor in the given actor system registry
	if x.ActorSystem() != nil {
		x.ActorSystem().setActor(cid)
	}

	return cid, nil
}

// StashSize returns the stash buffer size
func (x *pid) StashSize() uint64 {
	if x.stashBuffer == nil {
		return 0
	}
	return uint64(x.stashBuffer.Len())
}

// PipeTo processes a long-running task and pipes the result to the provided actor.
// The successful result of the task will be put onto the provided actor mailbox.
// This is useful when interacting with external services.
// It’s common that you would like to use the value of the response in the actor when the long-running task is completed
func (x *pid) PipeTo(ctx context.Context, to PID, task future.Task) error {
	if task == nil {
		return ErrUndefinedTask
	}

	if !to.IsRunning() {
		return ErrDead
	}

	go x.handleCompletion(ctx, &taskCompletion{
		Receiver: to,
		Task:     task,
	})

	return nil
}

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (x *pid) Ask(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
	if !to.IsRunning() {
		return nil, ErrDead
	}

	messageContext := newReceiveContext(ctx, x, to, message, false)
	to.doReceive(messageContext)

	for await := time.After(x.askTimeout.Load()); ; {
		select {
		case response = <-messageContext.response:
			x.recordLastReceivedDurationMetric(ctx)
			return
		case <-await:
			x.recordLastReceivedDurationMetric(ctx)
			err = ErrRequestTimeout
			x.handleError(messageContext, err)
			return nil, err
		}
	}
}

// Tell sends an asynchronous message to another PID
func (x *pid) Tell(ctx context.Context, to PID, message proto.Message) error {
	if !to.IsRunning() {
		return ErrDead
	}

	messageContext := newReceiveContext(ctx, x, to, message, true)
	to.doReceive(messageContext)
	x.recordLastReceivedDurationMetric(ctx)
	return nil
}

// BatchTell sends an asynchronous bunch of messages to the given PID
// The messages will be processed one after the other in the order they are sent.
// This is a design choice to follow the simple principle of one message at a time processing by actors.
// When BatchTell encounter a single message it will fall back to a Tell call.
func (x *pid) BatchTell(ctx context.Context, to PID, messages ...proto.Message) error {
	if !to.IsRunning() {
		return ErrDead
	}

	if len(messages) == 1 {
		return x.Tell(ctx, to, messages[0])
	}

	for i := 0; i < len(messages); i++ {
		message := messages[i]
		messageContext := newReceiveContext(ctx, x, to, message, true)
		to.doReceive(messageContext)
	}

	x.recordLastReceivedDurationMetric(ctx)
	return nil
}

// BatchAsk sends a synchronous bunch of messages to the given PID and expect responses in the same order as the messages.
// The messages will be processed one after the other in the order they are sent.
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func (x *pid) BatchAsk(ctx context.Context, to PID, messages ...proto.Message) (responses chan proto.Message, err error) {
	if !to.IsRunning() {
		return nil, ErrDead
	}

	responses = make(chan proto.Message, len(messages))
	defer close(responses)

	for i := 0; i < len(messages); i++ {
		message := messages[i]
		messageContext := newReceiveContext(ctx, x, to, message, false)
		to.doReceive(messageContext)

		// await patiently to receive the response from the actor
	timerLoop:
		for await := time.After(x.askTimeout.Load()); ; {
			select {
			case resp := <-messageContext.response:
				responses <- resp
				x.recordLastReceivedDurationMetric(ctx)
				break timerLoop
			case <-await:
				err = ErrRequestTimeout
				x.recordLastReceivedDurationMetric(ctx)
				x.handleError(messageContext, err)
				return nil, err
			}
		}
	}
	return
}

// RemoteLookup look for an actor address on a remote node.
func (x *pid) RemoteLookup(ctx context.Context, host string, port int, name string) (addr *goaktpb.Address, err error) {
	remoteClient, err := x.remotingClient(host, port)
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
func (x *pid) RemoteTell(ctx context.Context, to *goaktpb.Address, message proto.Message) error {
	marshaled, err := anypb.New(message)
	if err != nil {
		return err
	}

	remoteService, err := x.remotingClient(to.GetHost(), int(to.GetPort()))
	if err != nil {
		return err
	}

	sender := &goaktpb.Address{
		Host: x.ActorPath().Address().Host(),
		Port: int32(x.ActorPath().Address().Port()),
		Name: x.ActorPath().Name(),
		Id:   x.ActorPath().ID().String(),
	}

	request := &internalpb.RemoteTellRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   sender,
			Receiver: to,
			Message:  marshaled,
		},
	}

	x.logger.Debugf("sending a message to remote=(%s:%d)", to.GetHost(), to.GetPort())
	stream := remoteService.RemoteTell(ctx)
	if err := stream.Send(request); err != nil {
		if IsEOF(err) {
			if _, err := stream.CloseAndReceive(); err != nil {
				return err
			}
			return nil
		}
		fmtErr := fmt.Errorf("failed to send message to remote=(%s:%d): %w", to.GetHost(), to.GetPort(), err)
		x.logger.Error(fmtErr)
		return fmtErr
	}

	if _, err := stream.CloseAndReceive(); err != nil {
		fmtErr := fmt.Errorf("failed to send message to remote=(%s:%d): %w", to.GetHost(), to.GetPort(), err)
		x.logger.Error(fmtErr)
		return fmtErr
	}

	x.logger.Debugf("message successfully sent to remote=(%s:%d)", to.GetHost(), to.GetPort())
	return nil
}

// RemoteAsk sends a synchronous message to another actor remotely and expect a response.
func (x *pid) RemoteAsk(ctx context.Context, to *goaktpb.Address, message proto.Message) (response *anypb.Any, err error) {
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, err
	}

	remoteService, err := x.remotingClient(to.GetHost(), int(to.GetPort()))
	if err != nil {
		return nil, err
	}

	senderPath := x.ActorPath()
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
func (x *pid) RemoteBatchTell(ctx context.Context, to *goaktpb.Address, messages ...proto.Message) error {
	if len(messages) == 1 {
		return x.RemoteTell(ctx, to, messages[0])
	}

	senderPath := x.ActorPath()
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

	remoteService, err := x.remotingClient(to.GetHost(), int(to.GetPort()))
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
func (x *pid) RemoteBatchAsk(ctx context.Context, to *goaktpb.Address, messages ...proto.Message) (responses []*anypb.Any, err error) {
	senderPath := x.ActorPath()
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

	remoteService, err := x.remotingClient(to.GetHost(), int(to.GetPort()))
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
func (x *pid) RemoteStop(ctx context.Context, host string, port int, name string) error {
	remoteService, err := x.remotingClient(host, port)
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
func (x *pid) RemoteSpawn(ctx context.Context, host string, port int, name, actorType string) error {
	remoteService, err := x.remotingClient(host, port)
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
func (x *pid) RemoteReSpawn(ctx context.Context, host string, port int, name string) error {
	remoteService, err := x.remotingClient(host, port)
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
func (x *pid) Shutdown(ctx context.Context) error {
	x.stopLocker.Lock()
	defer x.stopLocker.Unlock()

	x.logger.Info("Shutdown process has started...")

	if !x.isRunning.Load() {
		x.logger.Infof("Actor=%s is offline. Maybe it has been passivated or stopped already", x.ActorPath().String())
		return nil
	}

	if x.passivateAfter.Load() > 0 {
		x.logger.Debug("sending a signal to stop passivation listener....")
		x.haltPassivationLnr <- types.Unit{}
	}

	if err := x.doStop(ctx); err != nil {
		x.logger.Errorf("failed to cleanly stop actor=(%s)", x.ID())
		return err
	}

	if x.eventsStream != nil {
		x.eventsStream.Publish(eventsTopic, &goaktpb.ActorStopped{
			Address:   x.ActorPath().RemoteAddress(),
			StoppedAt: timestamppb.Now(),
		})
	}

	x.logger.Infof("Actor=%s successfully shutdown", x.ID())
	return nil
}

// Watch a pid for errors, and send on the returned channel if an error occurred
func (x *pid) Watch(cid PID) {
	w := &watcher{
		WatcherID: x,
		ErrChan:   make(chan error, 1),
		Done:      make(chan types.Unit, 1),
	}
	cid.watchers().Append(w)
	x.watchees().set(cid)
	go x.supervise(cid, w)
}

// UnWatch stops watching a given actor
func (x *pid) UnWatch(pid PID) {
	for item := range pid.watchers().Iter() {
		w := item.Value
		if w.WatcherID.Equals(x) {
			w.Done <- types.Unit{}
			x.watchees().delete(pid.ActorPath())
			pid.watchers().Delete(item.Index)
			break
		}
	}
}

// watchers return the list of watchersList
func (x *pid) watchers() *slices.Slice[*watcher] {
	return x.watchersList
}

// watchees returns the list of actors watched by this actor
func (x *pid) watchees() *pidMap {
	return x.watchedList
}

// doReceive pushes a given message to the actor receiveContextBuffer
func (x *pid) doReceive(ctx ReceiveContext) {
	x.lastProcessingTime.Store(time.Now())
	x.mailbox.Push(ctx)
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor will not be started
func (x *pid) init(ctx context.Context) error {
	x.logger.Info("Initialization process has started...")

	cancelCtx, cancel := context.WithTimeout(ctx, x.initTimeout.Load())
	defer cancel()

	// create a new retrier that will try a maximum of `initMaxRetries` times, with
	// an initial delay of 100 ms and a maximum delay of 1 second
	retrier := retry.NewRetrier(int(x.initMaxRetries.Load()), 100*time.Millisecond, time.Second)
	if err := retrier.RunContext(cancelCtx, x.Actor.PreStart); err != nil {
		e := ErrInitFailure(err)
		return e
	}

	x.fieldsLocker.Lock()
	x.isRunning.Store(true)
	x.errChan = make(chan error, 1)
	x.fieldsLocker.Unlock()

	x.logger.Info("Initialization process successfully completed.")

	if x.eventsStream != nil {
		x.eventsStream.Publish(eventsTopic, &goaktpb.ActorStarted{
			Address:   x.ActorPath().RemoteAddress(),
			StartedAt: timestamppb.Now(),
		})
	}

	return nil
}

// reset re-initializes the actor PID
func (x *pid) reset() {
	x.lastProcessingTime.Store(time.Time{})
	x.passivateAfter.Store(DefaultPassivationTimeout)
	x.askTimeout.Store(DefaultAskTimeout)
	x.shutdownTimeout.Store(DefaultShutdownTimeout)
	x.initMaxRetries.Store(DefaultInitMaxRetries)
	x.lastReceivedDuration.Store(0)
	x.initTimeout.Store(DefaultInitTimeout)
	x.children.close()
	x.watchersList.Reset()
	x.resetBehavior()
	if x.metricEnabled.Load() {
		if err := x.registerMetrics(); err != nil {
			fmtErr := fmt.Errorf("failed to register actor=%s metrics: %w", x.ID(), err)
			x.logger.Error(fmtErr)
		}
	}
	x.processedCount.Store(0)
}

func (x *pid) freeWatchers(ctx context.Context) {
	x.logger.Debug("freeing all watcher actors...")
	watchers := x.watchers()
	if watchers.Len() > 0 {
		for item := range watchers.Iter() {
			watcher := item.Value
			terminated := &goaktpb.Terminated{
				ActorId: x.ID(),
			}
			if watcher.WatcherID.IsRunning() {
				x.logger.Debugf("watcher=(%s) releasing watched=(%s)", watcher.WatcherID.ID(), x.ID())
				// TODO: handle error and push to some system dead-letters queue
				_ = x.Tell(ctx, watcher.WatcherID, terminated)
				watcher.WatcherID.UnWatch(x)
				watcher.WatcherID.removeChild(x)
				x.logger.Debugf("watcher=(%s) released watched=(%s)", watcher.WatcherID.ID(), x.ID())
			}
		}
	}
}

func (x *pid) freeWatchees(ctx context.Context) {
	x.logger.Debug("freeing all watched actors...")
	for _, watched := range x.watchedList.pids() {
		x.logger.Debugf("watcher=(%s) unwatching actor=(%s)", x.ID(), watched.ID())
		x.UnWatch(watched)
		if err := watched.Shutdown(ctx); err != nil {
			x.logger.Panic(
				fmt.Errorf("watcher=(%s) failed to unwatch actor=(%s): %w",
					x.ID(), watched.ID(), err))
		}
		x.logger.Debugf("watcher=(%s) successfully unwatch actor=(%s)", x.ID(), watched.ID())
	}
}

func (x *pid) freeChildren(ctx context.Context) {
	x.logger.Debug("freeing all child actors...")
	for _, child := range x.Children() {
		x.logger.Debugf("parent=(%s) disowning child=(%s)", x.ID(), child.ID())
		x.UnWatch(child)
		x.children.delete(child.ActorPath())
		if err := child.Shutdown(ctx); err != nil {
			x.logger.Panic(
				fmt.Errorf(
					"parent=(%s) failed to disown child=(%s): %w", x.ID(), child.ID(),
					err))
		}
		x.logger.Debugf("parent=(%s) successfully disown child=(%s)", x.ID(), child.ID())
	}
}

// receive extracts every message from the actor mailbox
func (x *pid) receive() {
	for {
		if !x.isRunning.Load() {
			return
		}

		// fetch the data and continue the loop when there are no records yet
		received, ok := x.mailbox.Pop()
		if !ok {
			continue
		}

		switch received.Message().(type) {
		case *goaktpb.PoisonPill:
			// stop the actor
			_ = x.Shutdown(received.Context())
		default:
			x.handleReceived(received)
		}
	}
}

func (x *pid) handleReceived(received ReceiveContext) {
	// handle panic when the processing of the message fails
	defer func() {
		if r := recover(); r != nil {
			x.errChan <- fmt.Errorf("%s", r)
		}
	}()

	behaviorStack := x.behaviorStack
	// send the message to the current actor behavior
	if behavior, ok := behaviorStack.Peek(); ok {
		// set the total messages processed
		x.processedCount.Inc()
		// handle the message
		behavior(received)
	}
}

// errorLoop is used to process error during message processing
func (x *pid) errorLoop() {
	for {
		if !x.isRunning.Load() {
			return
		}
		for err := range x.errChan {
			for item := range x.watchersList.Iter() {
				item.Value.ErrChan <- err
			}
		}
	}
}

// passivationLoop checks whether the actor is processing public or not.
// when the actor is idle, it automatically shuts down to free resources
func (x *pid) passivationLoop() {
	x.logger.Info("start the passivation listener...")
	x.logger.Infof("passivation timeout is (%s)", x.passivateAfter.Load().String())
	ticker := time.NewTicker(x.passivateAfter.Load())
	tickerStopSig := make(chan types.Unit, 1)

	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				idleTime := time.Since(x.lastProcessingTime.Load())
				if idleTime >= x.passivateAfter.Load() {
					tickerStopSig <- types.Unit{}
					return
				}
			case <-x.haltPassivationLnr:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()

	if !x.IsRunning() {
		x.logger.Infof("Actor=%s is offline. No need to passivate", x.ActorPath().String())
		return
	}

	x.logger.Infof("Passivation mode has been triggered for actor=%s...", x.ActorPath().String())

	ctx := context.Background()
	if err := x.doStop(ctx); err != nil {
		// TODO: rethink properly about PostStop error handling
		x.logger.Errorf("failed to passivate actor=(%s): reason=(%v)", x.ActorPath().String(), err)
		return
	}

	if x.eventsStream != nil {
		event := &goaktpb.ActorPassivated{
			Address:      x.ActorPath().RemoteAddress(),
			PassivatedAt: timestamppb.Now(),
		}
		x.eventsStream.Publish(eventsTopic, event)
	}

	x.logger.Infof("Actor=%s successfully passivated", x.ActorPath().String())
}

// setBehavior is a utility function that helps set the actor behavior
func (x *pid) setBehavior(behavior Behavior) {
	x.fieldsLocker.Lock()
	x.behaviorStack.Clear()
	x.behaviorStack.Push(behavior)
	x.fieldsLocker.Unlock()
}

// resetBehavior is a utility function resets the actor behavior
func (x *pid) resetBehavior() {
	x.fieldsLocker.Lock()
	x.behaviorStack.Push(x.Receive)
	x.fieldsLocker.Unlock()
}

// setBehaviorStacked adds a behavior to the actor's behaviorStack
func (x *pid) setBehaviorStacked(behavior Behavior) {
	x.fieldsLocker.Lock()
	x.behaviorStack.Push(behavior)
	x.fieldsLocker.Unlock()
}

// unsetBehaviorStacked sets the actor's behavior to the previous behavior
// prior to setBehaviorStacked is called
func (x *pid) unsetBehaviorStacked() {
	x.fieldsLocker.Lock()
	x.behaviorStack.Pop()
	x.fieldsLocker.Unlock()
}

// doStop stops the actor
func (x *pid) doStop(ctx context.Context) error {
	x.isRunning.Store(false)
	close(x.errChan)

	// TODO: just signal stash processing done and ignore the messages or process them
	if x.stashBuffer != nil {
		if err := x.unstashAll(); err != nil {
			x.logger.Errorf("actor=(%s) failed to unstash messages", x.ActorPath().String())
			return err
		}
	}

	// wait for all messages in the mailbox to be processed
	// init a ticker that run every 10 ms to make sure we process all messages in the
	// mailbox.
	ticker := time.NewTicker(10 * time.Millisecond)
	timer := time.After(x.shutdownTimeout.Load())
	tickerStopSig := make(chan types.Unit)
	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				if x.mailbox.IsEmpty() {
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
	//x.shutdownSignal <- types.Unit{}
	x.httpClient.CloseIdleConnections()

	x.freeWatchees(ctx)
	x.freeChildren(ctx)
	x.freeWatchers(ctx)

	x.logger.Infof("Shutdown process is on going for actor=%s...", x.ActorPath().String())
	x.reset()
	return x.Actor.PostStop(ctx)
}

// removeChild helps remove child actor
func (x *pid) removeChild(pid PID) {
	if !x.IsRunning() {
		return
	}

	if pid == nil || pid == NoSender {
		return
	}

	path := pid.ActorPath()
	if cid, ok := x.children.get(path); ok {
		if cid.IsRunning() {
			return
		}
		x.children.delete(path)
	}
}

// handleError handles the error during a message handling
func (x *pid) handleError(receiveCtx ReceiveContext, err error) {
	if x.eventsStream == nil {
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

	receiver := x.actorPath.RemoteAddress()
	deadletter := &goaktpb.Deadletter{
		Sender:   senderAddr,
		Receiver: receiver,
		Message:  msg,
		SendTime: timestamppb.Now(),
		Reason:   err.Error(),
	}

	x.eventsStream.Publish(eventsTopic, deadletter)
}

// registerMetrics register the PID metrics with OTel instrumentation.
func (x *pid) registerMetrics() error {
	meter := x.telemetry.Meter
	metrics := x.metrics
	_, err := meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		observer.ObserveInt64(metrics.ChildrenCount(), x.childrenCount.Load())
		observer.ObserveInt64(metrics.StashCount(), int64(x.StashSize()))
		observer.ObserveInt64(metrics.RestartCount(), x.restartCount.Load())
		observer.ObserveInt64(metrics.ProcessedCount(), x.processedCount.Load())
		return nil
	}, metrics.ChildrenCount(),
		metrics.StashCount(),
		metrics.ProcessedCount(),
		metrics.RestartCount())

	return err
}

// clientOptions returns the gRPC client connections options
func (x *pid) clientOptions() ([]connect.ClientOption, error) {
	var interceptor *otelconnect.Interceptor
	var err error
	if x.metricEnabled.Load() {
		interceptor, err = otelconnect.NewInterceptor(
			otelconnect.WithTracerProvider(x.telemetry.TracerProvider),
			otelconnect.WithMeterProvider(x.telemetry.MeterProvider))
		if err != nil {
			return nil, fmt.Errorf("failed to initialize observability feature: %w", err)
		}
	}

	var clientOptions []connect.ClientOption
	if interceptor != nil {
		clientOptions = append(clientOptions, connect.WithInterceptors(interceptor))
	}
	return clientOptions, err
}

// remotingClient returns an instance of the Remote Service client
func (x *pid) remotingClient(host string, port int) (internalpbconnect.RemotingServiceClient, error) {
	clientConnectionOptions, err := x.clientOptions()
	if err != nil {
		return nil, err
	}

	return internalpbconnect.NewRemotingServiceClient(
		x.httpClient,
		http.URL(host, port),
		clientConnectionOptions...,
	), nil
}

// getLastProcessingTime returns the last processing time
func (x *pid) getLastProcessingTime() time.Time {
	x.processingTimeLocker.Lock()
	processingTime := x.lastProcessingTime.Load()
	x.processingTimeLocker.Unlock()
	return processingTime
}

// setLastProcessingDuration sets the last processing duration
func (x *pid) setLastProcessingDuration(d time.Duration) {
	x.processingTimeLocker.Lock()
	x.lastReceivedDuration.Store(d)
	x.processingTimeLocker.Unlock()
}

// handleCompletion processes a long-running task and pipe the result to
// the completion receiver
func (x *pid) handleCompletion(ctx context.Context, completion *taskCompletion) {
	// defensive programming
	if completion == nil ||
		completion.Receiver == nil ||
		completion.Receiver == NoSender ||
		completion.Task == nil {
		x.logger.Error(ErrUndefinedTask)
		return
	}

	// wrap the provided completion task into a future that can help run the task
	f := future.NewWithContext(ctx, completion.Task)
	var wg sync.WaitGroup
	var result *future.Result

	// wait for a potential result or timeout
	wg.Add(1)
	go func() {
		result = f.Result()
		wg.Done()
	}()
	wg.Wait()

	// logger the error when the task returns an error
	if err := result.Failure(); err != nil {
		x.logger.Error(err)
		return
	}

	// make sure that the receiver is still alive
	to := completion.Receiver
	if !to.IsRunning() {
		x.logger.Errorf("unable to pipe message to actor=(%s): not running", to.ActorPath().String())
		return
	}

	messageContext := newReceiveContext(ctx, x, to, result.Success(), true)
	to.doReceive(messageContext)
}

// supervise watches for child actor's failure and act based upon the supervisory strategy
func (x *pid) supervise(cid PID, watcher *watcher) {
	for {
		select {
		case <-watcher.Done:
			x.logger.Debugf("stop watching cid=(%s)", cid.ID())
			return
		case err := <-watcher.ErrChan:
			x.logger.Errorf("child actor=(%s) is failing: Err=%v", cid.ID(), err)
			switch directive := x.supervisorDirective.(type) {
			case *StopDirective:
				x.handleStopDirective(cid)
			case *RestartDirective:
				x.handleRestartDirective(cid, directive.MaxNumRetries(), directive.Timeout())
			case *ResumeDirective:
				// pass
			default:
				x.handleStopDirective(cid)
			}
		}
	}
}

// handleStopDirective handles the testSupervisor stop directive
func (x *pid) handleStopDirective(cid PID) {
	x.UnWatch(cid)
	x.children.delete(cid.ActorPath())
	if err := cid.Shutdown(context.Background()); err != nil {
		// this can enter into some infinite loop if we panic
		// since we are just shutting down the actor we can just log the error
		// TODO: rethink properly about PostStop error handling
		x.logger.Error(err)
	}
}

// handleRestartDirective handles the testSupervisor restart directive
func (x *pid) handleRestartDirective(cid PID, maxRetries uint32, timeout time.Duration) {
	x.UnWatch(cid)
	ctx := context.Background()
	var err error
	if maxRetries == 0 || timeout <= 0 {
		err = cid.Restart(ctx)
	} else {
		// TODO: handle the initial delay
		retrier := retry.NewRetrier(int(maxRetries), 100*time.Millisecond, timeout)
		err = retrier.RunContext(ctx, cid.Restart)
	}

	if err != nil {
		x.logger.Error(err)
		// remove the actor and stop it
		x.children.delete(cid.ActorPath())
		if err := cid.Shutdown(ctx); err != nil {
			x.logger.Error(err)
		}
		return
	}
	x.Watch(cid)
}

func (x *pid) recordLastReceivedDurationMetric(ctx context.Context) {
	x.lastReceivedDuration.Store(time.Since(x.lastProcessingTime.Load()))
	if x.metricEnabled.Load() {
		x.metrics.LastReceivedDuration().Record(ctx, x.lastReceivedDuration.Load().Milliseconds())
	}
}
