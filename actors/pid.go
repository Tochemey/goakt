package actors

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cenkalti/backoff"
	actorsv1 "github.com/tochemey/goakt/gen/actors/v1"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/tools"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
)

// NoSender means that there is no sender
var NoSender PID

// PID defines the various actions one can perform on a given actor
type PID interface {
	// Shutdown gracefully shuts down the given actor
	Shutdown(ctx context.Context) error
	// IsOnline returns true when the actor is online ready to process messages and false
	// when the actor is stopped or not started at all
	IsOnline() bool
	// ReceivedCount returns the total number of messages processed by the actor
	// at a given point in time while the actor heart is still beating
	ReceivedCount(ctx context.Context) uint64
	// ErrorsCount returns the total number of panic attacks that occur while the actor is processing messages
	// at a given point in time while the actor heart is still beating
	ErrorsCount(ctx context.Context) uint64
	// SpawnChild creates a child actor
	SpawnChild(ctx context.Context, kind, id string, actor Actor) (PID, error)
	// Restart restarts the actor
	Restart(ctx context.Context) error
	// Watch an actor
	Watch(pid PID)
	// UnWatch stops watching a given actor
	UnWatch(pid PID)
	// ActorSystem returns the underlying actor system
	ActorSystem() ActorSystem
	// Address returns the address of the actor
	Address() Address
	// Watchers returns the list of watchers
	Watchers() *tools.ConcurrentSlice[*Watcher]
	// LocalID returns the actor unique identifier
	LocalID() *LocalID
	// SendAsync sends an asynchronous message to another PID
	SendAsync(ctx context.Context, to PID, message proto.Message) error
	// SendSync sends a synchronous message to another actor and expect a response.
	SendSync(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error)
	// RestartCount returns the number of times the actor has restarted
	RestartCount(ctx context.Context) uint64
	// Children returns the list of all the children of the given actor
	Children(ctx context.Context) []PID
	// Behaviors returns the behavior stack
	behaviors() BehaviorStack
	// push a message to the actor's mailbix
	doReceive(ctx ReceiveContext)
	// setBehavior is a utility function that helps set the actor behavior
	setBehavior(behavior Behavior)
	// setBehaviorStacked adds a behavior to the actor's behaviors
	setBehaviorStacked(behavior Behavior)
	// unsetBehaviorStacked sets the actor's behavior to the previous behavior
	// prior to setBehaviorStacked is called
	unsetBehaviorStacked()
	// resetBehavior is a utility function resets the actor behavior
	resetBehavior()
}

// pid specifies an actor unique process
// With the pid one can send a receiveContext to the actor
type pid struct {
	Actor

	// addr represents the actor unique address
	addr Address

	// actor unique identifier
	id *LocalID

	// helps determine whether the actor should handle messages or not.
	isOnline bool
	// is captured whenever a mail is sent to the actor
	lastProcessingTime atomic.Time

	// specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 120 seconds
	passivateAfter time.Duration

	// specifies how long the sender of a mail should wait to receive a reply
	// when using SendReply. The default value is 5s
	sendRecvTimeout time.Duration

	// specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5 seconds
	initMaxRetries int

	// shutdownTimeout specifies how long to wait for the remaining messages
	// in the actor mailbox to be processed before completing the shutdown process
	// the default value is 5 seconds
	shutdownTimeout time.Duration

	// specifies the actor mailbox
	mailbox chan ReceiveContext
	// receives a shutdown signal. Once the signal is received
	// the actor is shut down gracefully.
	shutdownSignal     chan Unit
	haltPassivationLnr chan Unit

	// set of watchers watching the given actor
	watchers *tools.ConcurrentSlice[*Watcher]

	// hold the list of the children
	children *pidMap

	// the actor system
	system ActorSystem

	// specifies the logger to use
	logger log.Logger

	// definition of the various counters
	panicCounter           *atomic.Uint64
	receivedMessageCounter *atomic.Uint64
	restartCounter         *atomic.Uint64
	lastProcessingDuration *atomic.Duration

	mu sync.RWMutex

	// supervisor strategy
	supervisorStrategy actorsv1.Strategy

	// specifies the current actor behavior
	behaviorStack BehaviorStack
}

// enforce compilation error
var _ PID = (*pid)(nil)

// newPID creates a new pid
func newPID(ctx context.Context, actor Actor, opts ...pidOption) *pid {
	// create the actor PID
	pid := &pid{
		Actor:                  actor,
		addr:                   "",
		isOnline:               false,
		lastProcessingTime:     atomic.Time{},
		passivateAfter:         2 * time.Minute,
		sendRecvTimeout:        100 * time.Millisecond,
		shutdownTimeout:        2 * time.Second,
		initMaxRetries:         5,
		mailbox:                make(chan ReceiveContext, 1000),
		shutdownSignal:         make(chan Unit, 1),
		haltPassivationLnr:     make(chan Unit, 1),
		logger:                 log.DefaultLogger,
		panicCounter:           atomic.NewUint64(0),
		receivedMessageCounter: atomic.NewUint64(0),
		lastProcessingDuration: atomic.NewDuration(0),
		restartCounter:         atomic.NewUint64(0),
		mu:                     sync.RWMutex{},
		children:               newPIDMap(10),
		supervisorStrategy:     actorsv1.Strategy_STOP,
		watchers:               tools.NewConcurrentSlice[*Watcher](),
	}
	// set the custom options to override the default values
	for _, opt := range opts {
		opt(pid)
	}

	// let us set the addr when the actor system and kind are set
	if pid.system != nil && pid.LocalID().Kind() != "" {
		// create the address of the given actor and set it
		pid.addr = GetAddress(pid.system, pid.LocalID().Kind(), pid.LocalID().ID())
	}

	// initialize the actor and init processing messages
	pid.init(ctx)
	// init processing messages
	go pid.receive()
	// init the passivation listener loop iff passivation is set
	if pid.passivateAfter > 0 {
		go pid.passivationListener()
	}

	// set the actor behavior stack
	behaviorStack := NewBehaviorStack()
	behaviorStack.Push(pid.Receive)
	pid.behaviorStack = behaviorStack

	// return the actor reference
	return pid
}

// Behaviors returns the behavior stack
func (p *pid) behaviors() BehaviorStack {
	p.mu.Lock()
	behaviors := p.behaviorStack
	p.mu.Unlock()
	return behaviors
}

// Children returns the list of all the children of the given actor
func (p *pid) Children(ctx context.Context) []PID {
	p.mu.Lock()
	kiddos := p.children.List()
	p.mu.Unlock()
	return kiddos
}

// LocalID returns the unique actor identifier
func (p *pid) LocalID() *LocalID {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.id == nil {
		return &LocalID{}
	}
	return p.id
}

// IsOnline returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (p *pid) IsOnline() bool {
	p.mu.RLock()
	ready := p.isOnline
	p.mu.RUnlock()
	return ready
}

// ActorSystem returns the actor system
func (p *pid) ActorSystem() ActorSystem {
	p.mu.Lock()
	sys := p.system
	p.mu.Unlock()
	return sys
}

// Address return the address of the actor
func (p *pid) Address() Address {
	p.mu.Lock()
	address := p.addr
	p.mu.Unlock()
	return address
}

// Restart restarts the actor. This call can panic which is the expected behaviour.
// During restart all messages that are in the mailbox and not yet processed will be ignored
func (p *pid) Restart(ctx context.Context) error {
	// first check whether we have an empty PID
	if p == nil || p.addr == "" {
		return ErrUndefinedActor
	}
	// check whether the actor is ready and stop it
	if p.IsOnline() {
		// stop the actor
		if err := p.Shutdown(ctx); err != nil {
			return err
		}
		// wait a while for the shutdown process to complete
		ticker := time.NewTicker(10 * time.Millisecond)
		// create the ticker stop signal
		tickerStopSig := make(chan Unit, 1)
		go func() {
			for range ticker.C {
				// stop ticking once the actor is offline
				if !p.IsOnline() {
					tickerStopSig <- Unit{}
					return
				}
			}
		}()
		<-tickerStopSig
		ticker.Stop()
	}

	// reset the actor
	p.reset()
	// initialize the actor
	p.init(ctx)
	// init processing messages
	go p.receive()
	// init the passivation listener loop iff passivation is set
	if p.passivateAfter > 0 {
		go p.passivationListener()
	}
	// increment the restart counter
	p.restartCounter.Inc()
	// successful restart
	return nil
}

// SpawnChild creates a child actor and start watching it for error
func (p *pid) SpawnChild(ctx context.Context, kind, id string, actor Actor) (PID, error) {
	// first check whether the actor is ready to start another actor
	if !p.IsOnline() {
		return nil, ErrNotReady
	}

	// create the address of the given actor
	addr := GetAddress(p.ActorSystem(), kind, id)
	// check whether the child actor already exist and just return the PID
	if cid, ok := p.children.Get(addr); ok {
		// check whether the actor is stopped
		if !cid.IsOnline() {
			// then reboot it
			if err := cid.Restart(ctx); err != nil {
				return nil, err
			}
		}
		return cid, nil
	}

	// create the child pid
	cid := newPID(ctx, actor,
		withInitMaxRetries(p.initMaxRetries),
		withPassivationAfter(p.passivateAfter),
		withSendReplyTimeout(p.sendRecvTimeout),
		withCustomLogger(p.logger),
		withActorSystem(p.system),
		withLocalID(kind, id),
		withSupervisorStrategy(p.supervisorStrategy),
		withShutdownTimeout(p.shutdownTimeout),
		withAddress(addr))

	// add the pid to the map
	p.children.Set(cid)

	// let us start watching it
	p.Watch(cid)

	return cid, nil
}

// ReceivedCount returns the total number of messages processed by the actor
// at a given time while the actor is still alive
func (p *pid) ReceivedCount(ctx context.Context) uint64 {
	return p.receivedMessageCounter.Load()
}

// ErrorsCount returns the total number of panic attacks that occur while the actor is processing messages
// at a given point in time while the actor heart is still beating
func (p *pid) ErrorsCount(ctx context.Context) uint64 {
	return p.panicCounter.Load()
}

// RestartCount returns the number of times the actor has restarted
func (p *pid) RestartCount(ctx context.Context) uint64 {
	return p.restartCounter.Load()
}

// SendSync sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (p *pid) SendSync(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
	// make sure the actor is live
	if !to.IsOnline() {
		return nil, ErrNotReady
	}

	// acquire a lock to set the message context
	p.mu.Lock()

	// check whether we do have at least one behavior
	if p.behaviorStack.IsEmpty() {
		// release the lock after setting the message context
		p.mu.Unlock()
		return nil, ErrEmptyBehavior
	}

	// create a receiver context
	context := new(receiveContext)

	// set the needed properties of the message context
	context.ctx = ctx
	context.sender = p
	context.recipient = to
	context.message = message
	context.isAsyncMessage = false
	context.mu = sync.Mutex{}

	// release the lock after setting the message context
	p.mu.Unlock()

	// put the message context in the mailbox of the recipient actor
	to.doReceive(context)

	// await patiently to receive the response from the actor
	for await := time.After(p.sendRecvTimeout); ; {
		select {
		case response = <-context.response:
			return
		case <-await:
			err = ErrRequestTimeout
			return
		}
	}
}

// SendAsync sends an asynchronous message to another PID
func (p *pid) SendAsync(ctx context.Context, to PID, message proto.Message) error {
	// make sure the recipient actor is live
	if !to.IsOnline() {
		return ErrNotReady
	}

	// acquire a lock to set the message context
	p.mu.Lock()

	// check whether we do have at least one behavior
	if p.behaviorStack.IsEmpty() {
		// release the lock after setting the message context
		p.mu.Unlock()
		return ErrEmptyBehavior
	}

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

	// release the lock after setting the message context
	p.mu.Unlock()

	// put the message context in the mailbox of the recipient actor
	to.doReceive(context)

	return nil
}

// Shutdown gracefully shuts down the given actor
// All current messages in the mailbox will be processed before the actor shutdown after a period of time
// that can be configured. All child actors will be gracefully shutdown.
func (p *pid) Shutdown(ctx context.Context) error {
	p.logger.Info("Shutdown process has started...")
	// stop the passivation listener
	p.haltPassivationLnr <- Unit{}

	// check whether the actor is still alive. Maybe it has been passivated already
	if !p.IsOnline() {
		p.logger.Infof("Actor=%s is offline. Maybe it has been passivated or stopped already", p.Address())
		return nil
	}

	// stop the actor PID
	p.stop(ctx)

	// add some logging
	p.logger.Info("Shutdown process is on going for actor=%s...", p.Address())
	// signal we are shutting down to stop processing messages
	p.shutdownSignal <- Unit{}
	// perform some cleanup with the actor
	if err := p.Actor.PostStop(ctx); err != nil {
		p.logger.Error(fmt.Errorf("failed to stop the underlying receiver for actor=%s. Cause:%v", p.addr, err))
		return err
	}
	p.logger.Infof("Actor=%s successfully shutdown", p.Address())
	return nil
}

// Watchers return the list of watchers
func (p *pid) Watchers() *tools.ConcurrentSlice[*Watcher] {
	return p.watchers
}

// Watch a pid for errors, and send on the returned channel if an error occurred
func (p *pid) Watch(pid PID) {
	// create a watcher
	w := &Watcher{
		Parent:  p,
		ErrChan: make(chan error, 1),
		Done:    make(chan Unit, 1),
	}
	// add the watcher to the list of watchers
	pid.Watchers().Append(w)
	// supervise the PID
	go p.supervise(pid, w)
}

// UnWatch stops watching a given actor
func (p *pid) UnWatch(pid PID) {
	// iterate the watchers list
	for item := range pid.Watchers().Iter() {
		// grab the item value
		w := item.Value
		// locate the given watcher
		if w.Parent.Address() == p.Address() {
			// stop the watching go routine
			w.Done <- Unit{}
			// remove the watcher from the list
			pid.Watchers().Delete(item.Index)
			break
		}
	}
}

// doReceive pushes a given message to the actor mailbox
func (p *pid) doReceive(ctx ReceiveContext) {
	// acquire the lock and release it once done
	p.mu.Lock()
	defer p.mu.Unlock()
	// set the last processing time
	p.lastProcessingTime.Store(time.Now())

	// set the start processing time
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		p.lastProcessingDuration.Store(duration)
	}()

	// push the message to the actor mailbox
	p.mailbox <- ctx
	// increase the received message counter
	p.receivedMessageCounter.Inc()
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor is automatically shutdown
func (p *pid) init(ctx context.Context) {
	// add some logging info
	p.logger.Info("Initialization process has started...")
	// create the exponential backoff object
	expoBackoff := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), uint64(p.initMaxRetries))
	// init the actor initialization receive
	err := backoff.Retry(func() error {
		return p.Actor.PreStart(ctx)
	}, expoBackoff)
	// handle backoff error
	if err != nil {
		// log the error
		p.logger.Error(err.Error())
		// reset the actor
		p.reset()
		return
	}
	// set the actor is ready
	p.mu.Lock()
	p.isOnline = true
	p.mu.Unlock()
	// add some logging info
	p.logger.Info("Initialization process successfully completed.")
}

func (p *pid) reset() {
	// reset the mailbox
	p.mailbox = make(chan ReceiveContext, 1000)
	// reset the children map
	p.children = newPIDMap(10)
	// reset the various counters
	p.panicCounter = atomic.NewUint64(0)
	p.receivedMessageCounter = atomic.NewUint64(0)
	p.lastProcessingDuration = atomic.NewDuration(0)
	p.restartCounter = atomic.NewUint64(0)
	// reset the channels
	p.shutdownSignal = make(chan Unit, 1)
	p.haltPassivationLnr = make(chan Unit, 1)
	// reset the behavior stack
	p.resetBehaviorStack()
}

func (p *pid) resetBehaviorStack() {
	// reset the behavior
	behaviorStack := NewBehaviorStack()
	behaviorStack.Push(p.Receive)
	p.behaviorStack = behaviorStack
}

func (p *pid) freeChildren(ctx context.Context) {
	// iterate the pids and shutdown the child actors
	for _, child := range p.children.List() {
		// stop the child actor
		if err := child.Shutdown(ctx); err != nil {
			panic(err)
		}
		// unwatch the child
		p.UnWatch(child)
		p.children.Delete(child.Address())
	}
}

func (p *pid) stop(ctx context.Context) {
	// stop the actor to receive and send message
	p.mu.Lock()
	p.isOnline = false
	p.mu.Unlock()
	// stop all the child actors
	p.freeChildren(ctx)
	// wait for all messages in the mailbox to be processed
	// init a ticker that run every 10 ms to make sure we process all messages in the
	// mailbox.
	ticker := time.NewTicker(10 * time.Millisecond)
	timer := time.After(p.shutdownTimeout)
	// create the ticker stop signal
	tickerStopSig := make(chan Unit)
	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				// no more message to process
				if len(p.mailbox) == 0 {
					// tell the ticker to stop
					tickerStopSig <- Unit{}
					return
				}
			case <-timer:
				// tell the ticker to stop when timer is up
				tickerStopSig <- Unit{}
				return
			}
		}
	}()
	// listen to ticker stop signal
	<-tickerStopSig
	// signal we are shutting down to stop processing messages
	p.shutdownSignal <- Unit{}
	// add some logging
	p.logger.Infof("Remaining messages in the mailbox have been processed for actor=%s", p.Address())
	// stop the ticker
	ticker.Stop()
}

// setBehavior is a utility function that helps set the actor behavior
func (p *pid) setBehavior(behavior Behavior) {
	p.mu.Lock()
	p.behaviorStack.Clear()
	p.behaviorStack.Push(behavior)
	p.mu.Unlock()
}

// resetBehavior is a utility function resets the actor behavior
func (p *pid) resetBehavior() {
	p.mu.Lock()
	p.behaviorStack.Clear()
	p.behaviorStack.Push(p.Receive)
	p.mu.Unlock()
}

// setBehaviorStacked adds a behavior to the actor's behaviorStack
func (p *pid) setBehaviorStacked(behavior Behavior) {
	p.mu.Lock()
	p.behaviorStack.Push(behavior)
	p.mu.Unlock()
}

// unsetBehaviorStacked sets the actor's behavior to the previous behavior
// prior to setBehaviorStacked is called
func (p *pid) unsetBehaviorStacked() {
	p.mu.Lock()
	p.behaviorStack.Pop()
	p.mu.Unlock()
}
