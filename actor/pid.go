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
	stdhttp "net/http"
	"os"
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
	"github.com/tochemey/goakt/v2/internal/slice"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/telemetry"
)

// watcher is used to handle parent child relationship.
// This helps handle error propagation from a child actor using any of supervisory strategies
type watcher struct {
	WatcherID *PID            // the WatcherID of the actor watching
	ErrChan   chan error      // ErrChan the channel where to pass error message
	Done      chan types.Unit // Done when watching is completed
}

// taskCompletion is used to track completions' taskCompletion
// to pipe the result to the appropriate actor
type taskCompletion struct {
	Receiver string
	Task     future.Task
}

// PID specifies an actor unique process
// With the PID one can send a ReceiveContext to the actor
type PID struct {
	// specifies the message processor
	actor Actor

	// specifies the actor path
	actorPath *Path

	// helps determine whether the actor should handle public or not.
	running atomic.Bool

	latestReceiveTime     atomic.Time
	latestReceiveDuration atomic.Duration

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

	// specifies the doPreStart timeout.
	// the default initialization timeout is 1s
	initTimeout atomic.Duration

	// shutdownTimeout specifies the graceful shutdown timeout
	// the default value is 5 seconds
	shutdownTimeout atomic.Duration

	// specifies the actor mailbox
	mailbox *mailbox

	haltPassivationLnr chan types.Unit

	// hold the watchersList watching the given actor
	watchersList *slice.Slice[*watcher]

	// hold the list of the children
	children *pidMap

	// hold the list of watched actors
	watchedList *pidMap

	// the actor system
	system System

	// specifies the logger to use
	logger log.Logger

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
	stashBuffer *mailbox
	stashLocker *sync.Mutex

	// define an events stream
	eventsStream *eventstream.EventsStream

	// set the metrics settings
	restartCount   *atomic.Int64
	childrenCount  *atomic.Int64
	processedCount *atomic.Int64
	metricEnabled  atomic.Bool
	metrics        *metric.ActorMetric

	watcherNotificationChan        chan error
	watchersNotificationStopSignal chan types.Unit
	receiveSignal                  chan types.Unit
	receiveStopSignal              chan types.Unit
}

// newPID creates a new pid
func newPID(ctx context.Context, actorPath *Path, actor Actor, opts ...pidOption) (*PID, error) {
	// actor path is required
	if actorPath == nil {
		return nil, errors.New("actorPath is required")
	}

	// validate actor path
	if err := actorPath.Validate(); err != nil {
		return nil, err
	}

	p := &PID{
		actor:                          actor,
		latestReceiveTime:              atomic.Time{},
		haltPassivationLnr:             make(chan types.Unit, 1),
		logger:                         log.New(log.InfoLevel, os.Stderr),
		children:                       newPIDMap(10),
		supervisorDirective:            DefaultSupervisoryStrategy,
		watchersList:                   slice.New[*watcher](),
		watchedList:                    newPIDMap(10),
		telemetry:                      telemetry.New(),
		actorPath:                      actorPath,
		fieldsLocker:                   new(sync.RWMutex),
		stopLocker:                     new(sync.Mutex),
		httpClient:                     http.NewClient(),
		mailbox:                        newMailbox(),
		stashBuffer:                    nil,
		stashLocker:                    &sync.Mutex{},
		eventsStream:                   nil,
		restartCount:                   atomic.NewInt64(0),
		childrenCount:                  atomic.NewInt64(0),
		processedCount:                 atomic.NewInt64(0),
		processingTimeLocker:           new(sync.Mutex),
		watcherNotificationChan:        make(chan error, 1),
		watchersNotificationStopSignal: make(chan types.Unit, 1),
		receiveSignal:                  make(chan types.Unit, 1),
		receiveStopSignal:              make(chan types.Unit, 1),
	}

	p.initMaxRetries.Store(DefaultInitMaxRetries)
	p.shutdownTimeout.Store(DefaultShutdownTimeout)
	p.latestReceiveDuration.Store(0)
	p.running.Store(false)
	p.passivateAfter.Store(DefaultPassivationTimeout)
	p.askTimeout.Store(DefaultAskTimeout)
	p.initTimeout.Store(DefaultInitTimeout)
	p.metricEnabled.Store(false)

	for _, opt := range opts {
		opt(p)
	}

	behaviorStack := newBehaviorStack()
	behaviorStack.Push(p.actor.Receive)
	p.behaviorStack = behaviorStack

	if err := p.doPreStart(ctx); err != nil {
		return nil, err
	}

	go p.receive()
	go p.notifyWatchers()
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

	p.doReceive(newReceiveContext(ctx, NoSender, p, new(goaktpb.PostStart)))

	return p, nil
}

// ID is a convenient method that returns the actor unique identifier
// An actor unique identifier is its address in the actor system.
func (x *PID) ID() string {
	return x.ActorPath().String()
}

// Name returns the actor given name
func (x *PID) Name() string {
	return x.ActorPath().Name()
}

// Equals is a convenient method to compare two PIDs
func (x *PID) Equals(to *PID) bool {
	return strings.EqualFold(x.ID(), to.ID())
}

// Actor returns the underlying Actor
func (x *PID) Actor() Actor {
	return x.actor
}

// Child returns the named child actor if it is alive
func (x *PID) Child(name string) (*PID, error) {
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
func (x *PID) Parents() []*PID {
	x.fieldsLocker.Lock()
	watchers := x.watchersList
	x.fieldsLocker.Unlock()
	var parents []*PID
	if watchers.Len() > 0 {
		for _, item := range watchers.Items() {
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
func (x *PID) Children() []*PID {
	x.fieldsLocker.RLock()
	children := x.children.pids()
	x.fieldsLocker.RUnlock()

	cids := make([]*PID, 0, len(children))
	for _, child := range children {
		if child.IsRunning() {
			cids = append(cids, child)
		}
	}

	return cids
}

// Stop forces the child Actor under the given name to terminate after it finishes processing its current message.
// Nothing happens if child is already stopped.
func (x *PID) Stop(ctx context.Context, cid *PID) error {
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
func (x *PID) IsRunning() bool {
	return x != nil && x != NoSender && x.running.Load()
}

// ActorSystem returns the actor system
func (x *PID) ActorSystem() System {
	x.fieldsLocker.RLock()
	sys := x.system
	x.fieldsLocker.RUnlock()
	return sys
}

// ActorPath returns the path of the actor
func (x *PID) ActorPath() *Path {
	x.fieldsLocker.RLock()
	path := x.actorPath
	x.fieldsLocker.RUnlock()
	return path
}

// Restart restarts the actor.
// During restart all messages that are in the mailbox and not yet processed will be ignored
func (x *PID) Restart(ctx context.Context) error {
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
	if err := x.doPreStart(ctx); err != nil {
		return err
	}
	go x.receive()
	go x.notifyWatchers()
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
func (x *PID) SpawnChild(ctx context.Context, name string, actor Actor) (*PID, error) {
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
func (x *PID) StashSize() uint64 {
	if x.stashBuffer == nil {
		return 0
	}
	return uint64(x.stashBuffer.Len())
}

// PipeTo processes a long-running task and pipes the result to the provided actor.
// The successful result of the task will be put onto the provided actor mailbox.
// This is useful when interacting with external services.
// It’s common that you would like to use the value of the response in the actor when the long-running task is completed
func (x *PID) PipeTo(ctx context.Context, actorName string, task future.Task) error {
	if task == nil {
		return ErrUndefinedTask
	}

	go x.handleCompletion(ctx, &taskCompletion{
		Receiver: actorName,
		Task:     task,
	})

	return nil
}

// Tell sends an asynchronous message to a given actor. The location of the given actor
// is transparent to the caller.
func (x *PID) Tell(ctx context.Context, actorName string, message proto.Message) error {
	if !x.IsRunning() {
		return ErrDead
	}

	addr, pid, err := x.ActorSystem().actorOf(ctx, actorName)
	if err != nil {
		return err
	}

	if pid != nil {
		return x.doTell(ctx, pid, message)
	}

	if addr != nil {
		return x.doRemoteTell(ctx, addr, message)
	}

	return ErrActorNotFound(actorName)
}

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
// The location of the given actor is transparent to the caller.
func (x *PID) Ask(ctx context.Context, actorName string, message proto.Message) (response proto.Message, err error) {
	if !x.IsRunning() {
		return nil, ErrDead
	}

	addr, pid, err := x.ActorSystem().actorOf(ctx, actorName)
	if err != nil {
		return nil, err
	}

	if pid != nil {
		return x.doAsk(ctx, pid, message)
	}

	if addr != nil {
		reply, err := x.doRemoteAsk(ctx, addr, message)
		if err != nil {
			return nil, err
		}
		return reply.UnmarshalNew()
	}

	return nil, ErrActorNotFound(actorName)
}

// BatchTell sends an asynchronous bunch of messages to the given PID
// The messages will be processed one after the other in the order they are sent.
// This is a design choice to follow the simple principle of one message at a time processing by actors.
// When BatchTell encounter a single message it will fall back to a Tell call.
func (x *PID) BatchTell(ctx context.Context, actorName string, messages ...proto.Message) error {
	for _, message := range messages {
		if err := x.Tell(ctx, actorName, message); err != nil {
			return err
		}
	}
	return nil
}

// BatchAsk sends a synchronous bunch of messages to the given PID and expect responses in the same order as the messages.
// The messages will be processed one after the other in the order they are sent.
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func (x *PID) BatchAsk(ctx context.Context, actorName string, messages ...proto.Message) (responses chan proto.Message, err error) {
	responses = make(chan proto.Message, len(messages))
	defer close(responses)

	for i := 0; i < len(messages); i++ {
		response, err := x.Ask(ctx, actorName, messages[i])
		if err != nil {
			return nil, err
		}
		responses <- response
	}
	return
}

// RemoteStop stops an actor on a remote node
func (x *PID) RemoteStop(ctx context.Context, host string, port int, name string) error {
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

// RemoteSpawn creates an actor on a remote node. The given actor needs to be registered on the remote node using the Register method of System
func (x *PID) RemoteSpawn(ctx context.Context, host string, port int, name, actorType string) error {
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
func (x *PID) RemoteReSpawn(ctx context.Context, host string, port int, name string) error {
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
func (x *PID) Shutdown(ctx context.Context) error {
	x.stopLocker.Lock()
	defer x.stopLocker.Unlock()

	x.logger.Info("Shutdown process has started...")

	if !x.running.Load() {
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
func (x *PID) Watch(cid *PID) {
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
func (x *PID) UnWatch(pid *PID) {
	for _, item := range pid.watchers().Items() {
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
func (x *PID) watchers() *slice.Slice[*watcher] {
	return x.watchersList
}

// watchees returns the list of actors watched by this actor
func (x *PID) watchees() *pidMap {
	return x.watchedList
}

// doReceive pushes a given message to the actor receiveContextBuffer
func (x *PID) doReceive(receiveCtx *ReceiveContext) {
	x.latestReceiveTime.Store(time.Now())
	x.mailbox.Push(receiveCtx)
	x.receiveSignal <- types.Unit{}
}

// receive extracts every message from the actor mailbox
func (x *PID) receive() {
	for {
		select {
		case <-x.receiveStopSignal:
			return
		case <-x.receiveSignal:
			received := x.mailbox.Pop()
			if received == nil {
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
}

// handleReceived picks the right behavior and processes the message
func (x *PID) handleReceived(received *ReceiveContext) {
	defer x.recovery(received)

	if behavior := x.behaviorStack.Peek(); behavior != nil {
		x.processedCount.Inc()
		behavior(received)
	}
}

// doPreStart initializes the given actor and doPreStart processing messages
// when the initialization failed the actor will not be started
func (x *PID) doPreStart(ctx context.Context) error {
	x.logger.Info("Initialization process has started...")

	cancelCtx, cancel := context.WithTimeout(ctx, x.initTimeout.Load())
	defer cancel()

	// create a new retrier that will try a maximum of `initMaxRetries` times, with
	// an initial delay of 100 ms and a maximum delay of 1 second
	retrier := retry.NewRetrier(int(x.initMaxRetries.Load()), 100*time.Millisecond, time.Second)
	if err := retrier.RunContext(cancelCtx, x.actor.PreStart); err != nil {
		e := ErrInitFailure(err)
		return e
	}

	x.fieldsLocker.Lock()
	x.running.Store(true)
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
func (x *PID) reset() {
	x.latestReceiveTime.Store(time.Time{})
	x.passivateAfter.Store(DefaultPassivationTimeout)
	x.askTimeout.Store(DefaultAskTimeout)
	x.shutdownTimeout.Store(DefaultShutdownTimeout)
	x.initMaxRetries.Store(DefaultInitMaxRetries)
	x.latestReceiveDuration.Store(0)
	x.initTimeout.Store(DefaultInitTimeout)
	x.children.reset()
	x.watchersList.Reset()
	x.telemetry = telemetry.New()
	x.behaviorStack.Reset()
	if x.metricEnabled.Load() {
		if err := x.registerMetrics(); err != nil {
			fmtErr := fmt.Errorf("failed to register actor=%s metrics: %w", x.ID(), err)
			x.logger.Error(fmtErr)
		}
	}
	x.processedCount.Store(0)
}

func (x *PID) freeWatchers(ctx context.Context) {
	x.logger.Debug("freeing all watcher actors...")
	watchers := x.watchers()
	if watchers.Len() > 0 {
		for _, item := range watchers.Items() {
			watcher := item.Value
			terminated := &goaktpb.Terminated{
				ActorId: x.ID(),
			}
			if watcher.WatcherID.IsRunning() {
				x.logger.Debugf("watcher=(%s) releasing watched=(%s)", watcher.WatcherID.ID(), x.ID())
				// TODO: handle error and push to some system dead-letters queue
				_ = x.doTell(ctx, watcher.WatcherID, terminated)
				watcher.WatcherID.UnWatch(x)
				watcher.WatcherID.removeChild(x)
				x.logger.Debugf("watcher=(%s) released watched=(%s)", watcher.WatcherID.ID(), x.ID())
			}
		}
	}
}

func (x *PID) freeWatchees(ctx context.Context) {
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

func (x *PID) freeChildren(ctx context.Context) {
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

// recovery is called upon after message is processed
func (x *PID) recovery(received *ReceiveContext) {
	if r := recover(); r != nil {
		x.watcherNotificationChan <- fmt.Errorf("%s", r)
		return
	}
	// no panic or recommended way to handle error
	x.watcherNotificationChan <- received.getError()
}

// passivationLoop checks whether the actor is processing public or not.
// when the actor is idle, it automatically shuts down to free resources
func (x *PID) passivationLoop() {
	x.logger.Info("start the passivation listener...")
	x.logger.Infof("passivation timeout is (%s)", x.passivateAfter.Load().String())
	ticker := time.NewTicker(x.passivateAfter.Load())
	tickerStopSig := make(chan types.Unit, 1)

	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				idleTime := time.Since(x.latestReceiveTime.Load())
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
func (x *PID) setBehavior(behavior Behavior) {
	x.fieldsLocker.Lock()
	x.behaviorStack.Reset()
	x.behaviorStack.Push(behavior)
	x.fieldsLocker.Unlock()
}

// resetBehavior is a utility function resets the actor behavior
func (x *PID) resetBehavior() {
	x.fieldsLocker.Lock()
	x.behaviorStack.Push(x.actor.Receive)
	x.fieldsLocker.Unlock()
}

// setBehaviorStacked adds a behavior to the actor's behaviorStack
func (x *PID) setBehaviorStacked(behavior Behavior) {
	x.fieldsLocker.Lock()
	x.behaviorStack.Push(behavior)
	x.fieldsLocker.Unlock()
}

// unsetBehaviorStacked sets the actor's behavior to the previous behavior
// prior to setBehaviorStacked is called
func (x *PID) unsetBehaviorStacked() {
	x.fieldsLocker.Lock()
	x.behaviorStack.Pop()
	x.fieldsLocker.Unlock()
}

// doStop stops the actor
func (x *PID) doStop(ctx context.Context) error {
	x.running.Store(false)

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
	x.httpClient.CloseIdleConnections()
	x.watchersNotificationStopSignal <- types.Unit{}
	x.receiveStopSignal <- types.Unit{}
	x.freeWatchees(ctx)
	x.freeChildren(ctx)
	x.freeWatchers(ctx)

	x.logger.Infof("Shutdown process is on going for actor=%s...", x.ActorPath().String())
	x.reset()
	return x.actor.PostStop(ctx)
}

// removeChild helps remove child actor
func (x *PID) removeChild(pid *PID) {
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

// notifyWatchers send error to watchers
func (x *PID) notifyWatchers() {
	for {
		select {
		case err := <-x.watcherNotificationChan:
			if err != nil {
				for _, item := range x.watchers().Items() {
					item.Value.ErrChan <- err
				}
			}
		case <-x.watchersNotificationStopSignal:
			return
		}
	}
}

// toDeadletterQueue sends message to deadletter queue
func (x *PID) toDeadletterQueue(receiveCtx *ReceiveContext, err error) {
	// the message is lost
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

	x.eventsStream.Publish(eventsTopic, &goaktpb.Deadletter{
		Sender:   senderAddr,
		Receiver: x.actorPath.RemoteAddress(),
		Message:  msg,
		SendTime: timestamppb.Now(),
		Reason:   err.Error(),
	})
}

// registerMetrics register the PID metrics with OTel instrumentation.
func (x *PID) registerMetrics() error {
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
func (x *PID) clientOptions() ([]connect.ClientOption, error) {
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
func (x *PID) remotingClient(host string, port int) (internalpbconnect.RemotingServiceClient, error) {
	clientOptions, err := x.clientOptions()
	if err != nil {
		return nil, err
	}

	return internalpbconnect.NewRemotingServiceClient(
		x.httpClient,
		http.URL(host, port),
		clientOptions...,
	), nil
}

// handleCompletion processes a long-running task and pipe the result to
// the completion receiver
func (x *PID) handleCompletion(ctx context.Context, completion *taskCompletion) {
	// defensive programming
	if completion == nil ||
		completion.Receiver == "" ||
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

	if err := x.Tell(ctx, completion.Receiver, result.Success()); err != nil {
		x.logger.Errorf("unable to pipe message to actor=(%s): %v", completion.Receiver, err)
		return
	}
}

// supervise watches for child actor's failure and act based upon the supervisory strategy
func (x *PID) supervise(cid *PID, watcher *watcher) {
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
func (x *PID) handleStopDirective(cid *PID) {
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
func (x *PID) handleRestartDirective(cid *PID, maxRetries uint32, timeout time.Duration) {
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

func (x *PID) recordLatestReceiveDurationMetric(ctx context.Context) {
	// record processing time
	x.latestReceiveDuration.Store(time.Since(x.latestReceiveTime.Load()))
	if x.metricEnabled.Load() {
		x.metrics.LastReceivedDuration().Record(ctx, x.latestReceiveDuration.Load().Milliseconds())
	}
}

// doAsk sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (x *PID) doAsk(ctx context.Context, to *PID, message proto.Message) (response proto.Message, err error) {
	if !to.IsRunning() {
		return nil, ErrDead
	}

	receiveContext := newReceiveContext(ctx, x, to, message)
	to.doReceive(receiveContext)
	timeout := x.askTimeout.Load()

	select {
	case result := <-receiveContext.response:
		x.recordLatestReceiveDurationMetric(ctx)
		return result, nil
	case <-time.After(timeout):
		x.recordLatestReceiveDurationMetric(ctx)
		err = ErrRequestTimeout
		x.toDeadletterQueue(receiveContext, err)
		return nil, err
	}
}

// doTell sends an asynchronous message to another PID
func (x *PID) doTell(ctx context.Context, to *PID, message proto.Message) error {
	if !to.IsRunning() {
		return ErrDead
	}

	to.doReceive(newReceiveContext(ctx, x, to, message))
	x.recordLatestReceiveDurationMetric(ctx)
	return nil
}

// doRemoteTell sends a message to an actor remotely without expecting any reply
func (x *PID) doRemoteTell(ctx context.Context, to *goaktpb.Address, message proto.Message) error {
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
		if eof(err) {
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

// doRemoteAsk sends a synchronous message to another actor remotely and expect a response.
func (x *PID) doRemoteAsk(ctx context.Context, to *goaktpb.Address, message proto.Message) (response *anypb.Any, err error) {
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
	if eof(err) {
		return response, nil
	}

	if err != nil {
		return nil, err
	}

	return
}
