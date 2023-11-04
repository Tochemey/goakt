/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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
	gothttp "net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/flowchartsman/retry"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/eventstream"
	internalpb "github.com/tochemey/goakt/internal/v1"
	"github.com/tochemey/goakt/internal/v1/internalpbconnect"
	"github.com/tochemey/goakt/log"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	eventspb "github.com/tochemey/goakt/pb/events/v1"
	messagespb "github.com/tochemey/goakt/pb/messages/v1"
	"github.com/tochemey/goakt/pkg/http"
	"github.com/tochemey/goakt/pkg/slices"
	"github.com/tochemey/goakt/pkg/types"
	"github.com/tochemey/goakt/telemetry"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// watchMan is used to handle parent child relationship.
// This helps handle error propagation from a child actor using any of supervisory strategies
type watchMan struct {
	ID      PID             // the ID of the actor watching
	ErrChan chan error      // ErrChan the channel where to pass error message
	Done    chan types.Unit // Done when watching is completed
}

// PID defines the various actions one can perform on a given actor
type PID interface {
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
	// Ask sends a synchronous message to another actor and expect a response.
	Ask(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error)
	// RemoteTell sends a message to an actor remotely without expecting any reply
	RemoteTell(ctx context.Context, to *addresspb.Address, message proto.Message) error
	// RemoteAsk is used to send a message to an actor remotely and expect a response
	// immediately. With this type of message the receiver cannot communicate back to Sender
	// except reply the message with a response. This one-way communication.
	RemoteAsk(ctx context.Context, to *addresspb.Address, message proto.Message) (response *anypb.Any, err error)
	// RemoteLookup look for an actor address on a remote node. If the actorSystem is nil then the lookup will be done
	// using the same actor system as the PID actor system
	RemoteLookup(ctx context.Context, host string, port int, name string) (addr *addresspb.Address, err error)
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
	// push a message to the actor's receiveContextBuffer
	doReceive(ctx ReceiveContext)
	// watchers returns the list of watchMen
	watchers() *slices.ConcurrentSlice[*watchMan]
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
	emitDeadletter(recvCtx ReceiveContext, err error)
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
	// when using SendReply. The default value is 5s
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

	// set of watchMen watching the given actor
	watchMen *slices.ConcurrentSlice[*watchMan]

	// hold the list of the children
	children *pidMap

	// the actor system
	system ActorSystem

	// specifies the logger to use
	logger log.Logger

	lastProcessingDuration atomic.Duration

	// semaphore that helps synchronize the pid in a concurrent environment
	// this helps protect the pid fields accessibility
	semaphore     sync.RWMutex
	stopSemaphore sync.Mutex

	// supervisor strategy
	supervisorStrategy StrategyDirective

	// observability settings
	telemetry *telemetry.Telemetry

	// http client
	httpClient *gothttp.Client

	// specifies the current actor behavior
	behaviorStack *behaviorStack

	// stash settings
	stashBuffer    Mailbox
	stashCapacity  atomic.Uint64
	stashSemaphore sync.Mutex

	// define an events stream
	eventsStream *eventstream.EventsStream
}

// enforce compilation error
var _ PID = (*pid)(nil)

// newPID creates a new pid
func newPID(ctx context.Context, actorPath *Path, actor Actor, opts ...pidOption) (*pid, error) {
	// create the actor PID
	pid := &pid{
		Actor:              actor,
		lastProcessingTime: atomic.Time{},
		shutdownSignal:     make(chan types.Unit, 1),
		haltPassivationLnr: make(chan types.Unit, 1),
		logger:             log.DefaultLogger,
		mailboxSize:        defaultMailboxSize,
		children:           newPIDMap(10),
		supervisorStrategy: DefaultSupervisoryStrategy,
		watchMen:           slices.NewConcurrentSlice[*watchMan](),
		telemetry:          telemetry.New(),
		actorPath:          actorPath,
		semaphore:          sync.RWMutex{},
		stopSemaphore:      sync.Mutex{},
		httpClient:         http.Client(),
		mailbox:            nil,
		stashBuffer:        nil,
		stashSemaphore:     sync.Mutex{},
		eventsStream:       nil,
	}

	// set some of the defaults values
	pid.initMaxRetries.Store(DefaultInitMaxRetries)
	pid.shutdownTimeout.Store(DefaultShutdownTimeout)
	pid.lastProcessingDuration.Store(0)
	pid.stashCapacity.Store(0)
	pid.isRunning.Store(false)
	pid.passivateAfter.Store(DefaultPassivationTimeout)
	pid.replyTimeout.Store(DefaultReplyTimeout)
	pid.initTimeout.Store(DefaultInitTimeout)

	// set the custom options to override the default values
	for _, opt := range opts {
		opt(pid)
	}
	// set the default mailbox if mailbox is not set
	if pid.mailbox == nil {
		pid.mailbox = newReceiveContextBuffer(pid.mailboxSize)
	}

	// set the stash buffer when capacity is set
	if pid.stashCapacity.Load() > 0 {
		pid.stashBuffer = newReceiveContextBuffer(pid.stashCapacity.Load())
	}

	// set the actor behavior stack
	behaviorStack := newBehaviorStack()
	behaviorStack.Push(pid.Receive)
	pid.behaviorStack = behaviorStack

	// initialize the actor and init processing public
	if err := pid.init(ctx); err != nil {
		return nil, err
	}
	// init processing public
	go pid.receive()
	// init the passivation listener loop iff passivation is set
	if pid.passivateAfter.Load() > 0 {
		go pid.passivationListener()
	}

	// return the actor reference
	return pid, nil
}

// ActorHandle returns the underlying Actor
func (p *pid) ActorHandle() Actor {
	return p.Actor
}

// Child returns the named child actor if it is alive
func (p *pid) Child(name string) (PID, error) {
	// first check whether the actor is ready to stop another actor
	if !p.IsRunning() {
		return nil, ErrDead
	}

	// create the child actor path
	childActorPath := NewPath(name, p.ActorPath().Address()).WithParent(p.ActorPath())

	// check whether the child actor already exist and just return the PID
	if cid, ok := p.children.Get(childActorPath); ok {
		return cid, nil
	}
	return nil, ErrActorNotFound(childActorPath.String())
}

// Children returns the list of all the children of the given actor that are still alive or an empty list
func (p *pid) Children() []PID {
	p.semaphore.RLock()
	kiddos := p.children.List()
	p.semaphore.RUnlock()

	// create the list of alive children
	pids := make([]PID, 0, len(kiddos))
	for _, child := range kiddos {
		if child.IsRunning() {
			pids = append(pids, child)
		}
	}

	return pids
}

// Stop forces the child Actor under the given name to terminate after it finishes processing its current message.
// Nothing happens if child is already stopped.
func (p *pid) Stop(ctx context.Context, pid PID) error {
	// first check whether the actor is ready to stop another actor
	if !p.IsRunning() {
		return ErrDead
	}

	// check the pid is not nil
	if pid == nil || pid == NoSender {
		return ErrUndefinedActor
	}

	// grab the actor path
	path := pid.ActorPath()

	// grab the children thread-safely
	p.semaphore.RLock()
	kiddos := p.children
	p.semaphore.RUnlock()

	if cid, ok := kiddos.Get(path); ok {
		// stop the actor
		return cid.Shutdown(ctx)
	}
	return ErrActorNotFound(path.String())
}

// IsRunning returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (p *pid) IsRunning() bool {
	return p != nil && p != NoSender && p.isRunning.Load()
}

// ActorSystem returns the actor system
func (p *pid) ActorSystem() ActorSystem {
	p.semaphore.RLock()
	sys := p.system
	p.semaphore.RUnlock()
	return sys
}

// ActorPath returns the path of the actor
func (p *pid) ActorPath() *Path {
	p.semaphore.RLock()
	path := p.actorPath
	p.semaphore.RUnlock()
	return path
}

// Restart restarts the actor.
// During restart all messages that are in the mailbox and not yet processed will be ignored
func (p *pid) Restart(ctx context.Context) error {
	// first check whether we have an empty PID
	if p == nil || p.ActorPath() == nil {
		return ErrUndefinedActor
	}

	// add some debug logging
	p.logger.Debugf("restarting actor=(%s)", p.actorPath.String())

	// only restart the actor when the actor is running
	if p.IsRunning() {
		// shutdown the actor
		if err := p.Shutdown(ctx); err != nil {
			return err
		}
		// wait a while for the shutdown process to complete
		ticker := time.NewTicker(10 * time.Millisecond)
		// create the ticker stop signal
		tickerStopSig := make(chan types.Unit, 1)
		go func() {
			for range ticker.C {
				// stop ticking once the actor is offline
				if !p.IsRunning() {
					tickerStopSig <- types.Unit{}
					return
				}
			}
		}()
		<-tickerStopSig
		ticker.Stop()
	}

	// set the default mailbox if mailbox is not set
	p.mailbox.Reset()
	// reset the behavior
	p.resetBehavior()
	// initialize the actor
	if err := p.init(ctx); err != nil {
		return err
	}
	// init processing public
	go p.receive()
	// init the passivation listener loop iff passivation is set
	if p.passivateAfter.Load() > 0 {
		go p.passivationListener()
	}

	// successful restart
	return nil
}

// SpawnChild creates a child actor and start watching it for error
// When the given child actor already exists its PID will only be returned
func (p *pid) SpawnChild(ctx context.Context, name string, actor Actor) (PID, error) {
	// first check whether the actor is ready to start another actor
	if !p.IsRunning() {
		return nil, ErrDead
	}

	// create the child actor path
	childActorPath := NewPath(name, p.ActorPath().Address()).WithParent(p.ActorPath())

	// grab the children thread-safely
	p.semaphore.RLock()
	kiddos := p.children
	p.semaphore.RUnlock()

	// check whether the child actor already exist and just return the PID
	// whenever a child actor exists it means it is live
	if cid, ok := kiddos.Get(childActorPath); ok {
		return cid, nil
	}

	// acquire the lock
	p.semaphore.Lock()
	// release the lock
	defer p.semaphore.Unlock()

	// create the child pid
	cid, err := newPID(ctx,
		childActorPath,
		actor,
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
		withShutdownTimeout(p.shutdownTimeout.Load()))

	// handle the error
	if err != nil {
		return nil, err
	}

	// add the pid to the map
	p.children.Set(cid)

	// let us start watching it
	p.Watch(cid)

	return cid, nil
}

// StashSize returns the stash buffer size
func (p *pid) StashSize() uint64 {
	return p.stashBuffer.Size()
}

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (p *pid) Ask(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
	// make sure the actor is live
	if !to.IsRunning() {
		return nil, ErrDead
	}

	// acquire a lock to set the message context
	p.semaphore.Lock()
	// create a receiver context
	context := new(receiveContext)

	// set the needed properties of the message context
	context.ctx = ctx
	context.sender = p
	context.recipient = to
	context.message = message
	context.isAsyncMessage = false
	context.mu = sync.Mutex{}
	context.response = make(chan proto.Message, 1)
	context.sendTime.Store(time.Now())

	// release the lock after setting the message context
	p.semaphore.Unlock()

	// put the message context in the mailbox of the recipient actor
	to.doReceive(context)

	// await patiently to receive the response from the actor
	for await := time.After(p.replyTimeout.Load()); ; {
		select {
		case response = <-context.response:
			return
		case <-await:
			err = ErrRequestTimeout
			// push the message as a deadletter
			p.emitDeadletter(context, err)
			return
		}
	}
}

// Tell sends an asynchronous message to another PID
func (p *pid) Tell(ctx context.Context, to PID, message proto.Message) error {
	// make sure the recipient actor is live
	if !to.IsRunning() {
		return ErrDead
	}

	// acquire a lock to set the message context
	p.semaphore.Lock()
	// release the lock after setting the message context
	defer p.semaphore.Unlock()
	// create a message context
	context := new(receiveContext)

	// set the needed properties of the message context
	context.ctx = ctx
	context.sender = p
	context.recipient = to
	context.message = message
	context.isAsyncMessage = true
	context.mu = sync.Mutex{}
	context.response = make(chan proto.Message, 1)
	context.sendTime.Store(time.Now())

	// put the message context in the mailbox of the recipient actor
	to.doReceive(context)

	return nil
}

// RemoteLookup look for an actor address on a remote node. If the actorSystem is nil then the lookup will be done
// using the same actor system as the PID actor system
func (p *pid) RemoteLookup(ctx context.Context, host string, port int, name string) (addr *addresspb.Address, err error) {
	// create an instance of remote client service
	remoteClient := internalpbconnect.NewRemotingServiceClient(
		p.httpClient,
		http.URL(host, port),
		connect.WithInterceptors(p.interceptor()),
		connect.WithGRPC(),
	)

	// prepare the request to send
	request := connect.NewRequest(&internalpb.RemoteLookupRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})
	// send the message and handle the error in case there is any
	response, err := remoteClient.RemoteLookup(ctx, request)
	// we know the error will always be a grpc error
	if err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil, nil
		}
		return nil, err
	}
	// return the response
	return response.Msg.GetAddress(), nil
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (p *pid) RemoteTell(ctx context.Context, to *addresspb.Address, message proto.Message) error {
	// marshal the message
	marshaled, err := anypb.New(message)
	if err != nil {
		return err
	}

	// create an instance of remote client service
	remoteClient := internalpbconnect.NewRemotingServiceClient(
		p.httpClient,
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(p.interceptor()),
		connect.WithGRPC(),
	)

	// construct the from address
	sender := &addresspb.Address{
		Host: p.ActorPath().Address().Host(),
		Port: int32(p.ActorPath().Address().Port()),
		Name: p.ActorPath().Name(),
		Id:   p.ActorPath().ID().String(),
	}

	// prepare the rpcRequest to send
	request := connect.NewRequest(&internalpb.RemoteTellRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   sender,
			Receiver: to,
			Message:  marshaled,
		},
		SendTime: timestamppb.Now(),
	})

	// add some debug logging
	p.logger.Debugf("sending a message to remote=(%s:%d)", to.GetHost(), to.GetPort())

	// send the message and handle the error in case there is any
	if _, err := remoteClient.RemoteTell(ctx, request); err != nil {
		p.logger.Error(errors.Wrapf(err, "failed to send message to remote=(%s:%d)", to.GetHost(), to.GetPort()))
		return err
	}

	// add some debug logging
	p.logger.Debugf("message successfully sent to remote=(%s:%d)", to.GetHost(), to.GetPort())
	return nil
}

// RemoteAsk sends a synchronous message to another actor remotely and expect a response.
func (p *pid) RemoteAsk(ctx context.Context, to *addresspb.Address, message proto.Message) (response *anypb.Any, err error) {
	// marshal the message
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, err
	}

	// create an instance of remote client service
	remoteClient := internalpbconnect.NewRemotingServiceClient(
		p.httpClient,
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(p.interceptor()),
		connect.WithGRPC(),
	)

	// prepare the rpcRequest to send
	rpcRequest := connect.NewRequest(
		&internalpb.RemoteAskRequest{
			RemoteMessage: &internalpb.RemoteMessage{
				Sender:   RemoteNoSender,
				Receiver: to,
				Message:  marshaled,
			},
			SendTime: timestamppb.Now(),
		})
	// send the request
	rpcResponse, rpcErr := remoteClient.RemoteAsk(ctx, rpcRequest)
	// handle the error
	if rpcErr != nil {
		return nil, rpcErr
	}

	return rpcResponse.Msg.GetMessage(), nil
}

// Shutdown gracefully shuts down the given actor
// All current messages in the mailbox will be processed before the actor shutdown after a period of time
// that can be configured. All child actors will be gracefully shutdown.
func (p *pid) Shutdown(ctx context.Context) error {
	// acquire the shutdown lock
	p.stopSemaphore.Lock()
	// release the lock
	defer p.stopSemaphore.Unlock()

	p.logger.Info("Shutdown process has started...")

	// check whether the actor is still alive. Maybe it has been passivated already
	if !p.isRunning.Load() {
		p.logger.Infof("Actor=%s is offline. Maybe it has been passivated or stopped already", p.ActorPath().String())
		return nil
	}

	// stop the passivation listener
	if p.passivateAfter.Load() > 0 {
		// add some debug logging
		p.logger.Debug("sending a signal to stop passivation listener....")
		p.haltPassivationLnr <- types.Unit{}
	}

	// stop the actor
	if err := p.doStop(ctx); err != nil {
		p.logger.Errorf("failed to stop actor=(%s)", p.ActorPath().String())
		return err
	}

	p.logger.Infof("Actor=%s successfully shutdown", p.ActorPath().String())
	return nil
}

// Watch a pid for errors, and send on the returned channel if an error occurred
func (p *pid) Watch(pid PID) {
	// create a watcher
	w := &watchMan{
		ID:      p,
		ErrChan: make(chan error, 1),
		Done:    make(chan types.Unit, 1),
	}
	// add the watcher to the list of watchMen
	pid.watchers().Append(w)
	// supervise the PID
	go p.supervise(pid, w)
}

// UnWatch stops watching a given actor
func (p *pid) UnWatch(pid PID) {
	// iterate the watchMen list
	for item := range pid.watchers().Iter() {
		// grab the item value
		w := item.Value
		// locate the given watcher
		if w.ID.ActorPath() == p.ActorPath() {
			// stop the watching go routine
			w.Done <- types.Unit{}
			// remove the watcher from the list
			pid.watchers().Delete(item.Index)
			break
		}
	}
}

// Watchers return the list of watchMen
func (p *pid) watchers() *slices.ConcurrentSlice[*watchMan] {
	return p.watchMen
}

// doReceive pushes a given message to the actor receiveContextBuffer
func (p *pid) doReceive(ctx ReceiveContext) {
	// acquire the lock and release it once done
	p.semaphore.Lock()
	defer p.semaphore.Unlock()

	// set the last processing time
	p.lastProcessingTime.Store(time.Now())

	// set the start processing time
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		p.lastProcessingDuration.Store(duration)
	}()

	// push the message to the actor mailbox
	if err := p.mailbox.Push(ctx); err != nil {
		// add a warning log because the mailbox is full and do nothing
		p.logger.Warn(err)
		// push the message as a deadletter
		p.emitDeadletter(ctx, err)
		return
	}
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor will not be started
func (p *pid) init(ctx context.Context) error {
	// add some logging info
	p.logger.Info("Initialization process has started...")
	// create a context to handle the start timeout
	cancelCtx, cancel := context.WithTimeout(ctx, p.initTimeout.Load())
	// cancel the context when done
	defer cancel()
	// create a new retrier that will try a maximum of `initMaxRetries` times, with
	// an initial delay of 100 ms and a maximum delay of 1 second
	retrier := retry.NewRetrier(int(p.initMaxRetries.Load()), 100*time.Millisecond, time.Second)
	// init the actor initialization receive
	err := retrier.RunContext(cancelCtx, p.Actor.PreStart)
	// handle backoff error
	if err != nil {
		return ErrInitFailure(err)
	}
	// set the actor is ready
	p.semaphore.Lock()
	p.isRunning.Store(true)
	p.semaphore.Unlock()
	// add some logging info
	p.logger.Info("Initialization process successfully completed.")
	return nil
}

// reset re-initializes the actor PID
func (p *pid) reset() {
	// reset the various settings of the PID
	p.lastProcessingTime = atomic.Time{}
	p.passivateAfter.Store(DefaultPassivationTimeout)
	p.replyTimeout.Store(DefaultReplyTimeout)
	p.shutdownTimeout.Store(DefaultShutdownTimeout)
	p.initMaxRetries.Store(DefaultInitMaxRetries)
	p.lastProcessingDuration.Store(0)
	p.initTimeout.Store(0)
	p.children = newPIDMap(10)
	p.watchMen = slices.NewConcurrentSlice[*watchMan]()
	p.telemetry = telemetry.New()
	// reset the mailbox
	p.mailbox.Reset()
	// reset the behavior stack
	p.resetBehavior()
}

func (p *pid) freeWatchers(ctx context.Context) {
	p.logger.Debug("freeing all watcher actors...")

	// grab the actor watchers
	watchers := p.watchers()
	if watchers.Len() > 0 {
		// iterate the list of watchers
		for item := range watchers.Iter() {
			// grab the item value
			watcher := item.Value
			// notified the watcher with the Terminated message
			terminated := &messagespb.Terminated{}
			// only send the parent actor when it is running
			if watcher.ID.IsRunning() {
				// send the notification the watcher
				// TODO: handle error and push to some system dead-letters queue
				_ = p.Tell(ctx, watcher.ID, terminated)
				// unwatch the child actor
				watcher.ID.UnWatch(p)
				// remove the actor from the list of watcher's children
				watcher.ID.removeChild(p)
			}
		}
	}
}

func (p *pid) freeChildren(ctx context.Context) {
	p.logger.Debug("freeing all child actors...")

	// iterate the child actors and shut them down
	for _, child := range p.Children() {
		// unwatch the child
		p.UnWatch(child)
		// remove the child from the list
		p.children.Delete(child.ActorPath())
		// stop the child actor
		if err := child.Shutdown(ctx); err != nil {
			p.logger.Panic(err)
		}
	}
}

// receive handles every mail in the actor receiveContextBuffer
func (p *pid) receive() {
	// run the processing loop
	for {
		select {
		case <-p.shutdownSignal:
			return
		default:
			// only grab the message when the mailbox is not empty
			if !p.mailbox.IsEmpty() {
				// grab the message from the mailbox. Ignore the error when the mailbox is empty
				received, _ := p.mailbox.Pop()
				// switch on the type of message
				switch received.Message().(type) {
				case *messagespb.PoisonPill:
					// stop the actor
					_ = p.Shutdown(received.Context())
				default:
					p.handleReceived(received)
				}
			}
		}
	}
}

func (p *pid) handleReceived(received ReceiveContext) {
	// recover from a panic attack
	defer func() {
		if r := recover(); r != nil {
			// construct the error to return
			err := fmt.Errorf("%s", r)
			// send the error to the watchMen
			for item := range p.watchMen.Iter() {
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
func (p *pid) supervise(cid PID, watcher *watchMan) {
	for {
		select {
		case <-watcher.Done:
			p.logger.Debugf("stop watching cid=(%s)", cid.ActorPath().String())
			return
		case err := <-watcher.ErrChan:
			p.logger.Errorf("child actor=(%s) is failing: Err=%v", cid.ActorPath().String(), err)
			switch p.supervisorStrategy {
			case StopDirective:
				// unwatch the given actor
				p.UnWatch(cid)
				// remove the actor from the children map
				p.children.Delete(cid.ActorPath())
				// shutdown the actor and panic in case of error
				if err := cid.Shutdown(context.Background()); err != nil {
					// this can enter into some infinite loop if we panic
					// since we are just shutting down the actor we can just log the error
					// TODO: rethink properly about PostStop error handling
					p.logger.Error(err)
				}
			case RestartDirective:
				// unwatch the given actor
				p.UnWatch(cid)
				// restart the actor
				if err := cid.Restart(context.Background()); err != nil {
					p.logger.Panic(err)
				}
				// re-watch the actor
				p.Watch(cid)
			default:
				// unwatch the given actor
				p.UnWatch(cid)
				// remove the actor from the children map
				p.children.Delete(cid.ActorPath())
				// shutdown the actor and panic in case of error
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
	// create the ticker
	p.logger.Infof("passivation timeout is (%s)", p.passivateAfter.Load().String())
	ticker := time.NewTicker(p.passivateAfter.Load())
	// create the stop ticker signal
	tickerStopSig := make(chan types.Unit, 1)

	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				// check whether the actor is idle or not
				idleTime := time.Since(p.lastProcessingTime.Load())
				// check whether the actor is idle
				if idleTime >= p.passivateAfter.Load() {
					// set the done channel to stop the ticker
					tickerStopSig <- types.Unit{}
					return
				}
			case <-p.haltPassivationLnr:
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

	// only passivate when actor is alive
	if !p.IsRunning() {
		// add some logging info
		p.logger.Infof("Actor=%s is offline. No need to passivate", p.ActorPath().String())
		return
	}

	// acquire the shutdown lock
	p.stopSemaphore.Lock()
	// release the lock
	defer p.stopSemaphore.Unlock()

	// add some logging info
	p.logger.Infof("Passivation mode has been triggered for actor=%s...", p.ActorPath().String())

	// create a context
	ctx := context.Background()

	// stop the actor
	if err := p.doStop(ctx); err != nil {
		// TODO: rethink properly about PostStop error handling
		p.logger.Errorf("failed to passivate actor=(%s): reason=(%v)", p.ActorPath().String(), err)
		return
	}
	p.logger.Infof("Actor=%s successfully passivated", p.ActorPath().String())
}

// interceptor create an interceptor based upon the telemetry provided
func (p *pid) interceptor() connect.Interceptor {
	return otelconnect.NewInterceptor(
		otelconnect.WithTracerProvider(p.telemetry.TracerProvider),
		otelconnect.WithMeterProvider(p.telemetry.MeterProvider),
	)
}

// setBehavior is a utility function that helps set the actor behavior
func (p *pid) setBehavior(behavior Behavior) {
	p.semaphore.Lock()
	p.behaviorStack.Clear()
	p.behaviorStack.Push(behavior)
	p.semaphore.Unlock()
}

// resetBehavior is a utility function resets the actor behavior
func (p *pid) resetBehavior() {
	p.semaphore.Lock()
	p.behaviorStack.Clear()
	p.behaviorStack.Push(p.Receive)
	p.semaphore.Unlock()
}

// setBehaviorStacked adds a behavior to the actor's behaviorStack
func (p *pid) setBehaviorStacked(behavior Behavior) {
	p.semaphore.Lock()
	p.behaviorStack.Push(behavior)
	p.semaphore.Unlock()
}

// unsetBehaviorStacked sets the actor's behavior to the previous behavior
// prior to setBehaviorStacked is called
func (p *pid) unsetBehaviorStacked() {
	p.semaphore.Lock()
	p.behaviorStack.Pop()
	p.semaphore.Unlock()
}

// doStop stops the actor
func (p *pid) doStop(ctx context.Context) error {
	// stop receiving and sending messages
	p.isRunning.Store(false)

	// let us unstash all messages in case there are some
	// TODO: just signal stash processing done and ignore the messages or process them
	for p.stashBuffer != nil && !p.stashBuffer.IsEmpty() {
		if err := p.unstashAll(); err != nil {
			return err
		}
	}

	// wait for all messages in the mailbox to be processed
	// init a ticker that run every 10 ms to make sure we process all messages in the
	// mailbox.
	ticker := time.NewTicker(10 * time.Millisecond)
	timer := time.After(p.shutdownTimeout.Load())
	// create the ticker stop signal
	tickerStopSig := make(chan types.Unit)
	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				// no more message to process
				if p.mailbox.IsEmpty() {
					// tell the ticker to stop
					close(tickerStopSig)
					return
				}
			case <-timer:
				// tell the ticker to stop when timer is up
				close(tickerStopSig)
				return
			}
		}
	}()
	// listen to ticker stop signal
	<-tickerStopSig

	// signal we are shutting down to stop processing messages
	p.shutdownSignal <- types.Unit{}

	// close lingering http connections
	p.httpClient.CloseIdleConnections()

	// stop all the child actors
	p.freeChildren(ctx)
	// free the watchers
	p.freeWatchers(ctx)

	// add some logging
	p.logger.Infof("Shutdown process is on going for actor=%s...", p.ActorPath().String())

	// reset the actor
	p.reset()

	// perform some cleanup with the actor
	return p.Actor.PostStop(ctx)
}

// removeChild helps remove child actor
func (p *pid) removeChild(pid PID) {
	// first check whether the actor is ready to stop another actor
	if !p.IsRunning() {
		return
	}

	// check the pid is not nil
	if pid == nil || pid == NoSender {
		return
	}

	// grab the actor path
	path := pid.ActorPath()
	if cid, ok := p.children.Get(path); ok {
		if cid.IsRunning() {
			return
		}
		p.children.Delete(path)
	}
}

// emitDeadletter emit the given message to the deadletters queue
func (p *pid) emitDeadletter(recvCtx ReceiveContext, err error) {
	// only send to the stream when defined
	if p.eventsStream == nil {
		return
	}
	// marshal the message
	msg, _ := anypb.New(recvCtx.Message())
	// grab the sender
	var senderAddr *addresspb.Address
	if recvCtx.Sender() != nil || recvCtx.Sender() != NoSender {
		senderAddr = recvCtx.Sender().ActorPath().RemoteAddress()
	}

	// define the receiver
	receiver := p.actorPath.RemoteAddress()
	// create the deadletter
	deadletter := &eventspb.DeadletterEvent{
		Sender:   senderAddr,
		Receiver: receiver,
		Message:  msg,
		SendTime: timestamppb.Now(),
		Reason:   err.Error(),
	}
	// add to the events stream
	p.eventsStream.Publish(deadlettersTopic, deadletter)
}
