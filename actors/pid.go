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
)

// NoSender means that there is no sender
var NoSender = new(pid)

// PID defines the various actions one can perform on a given actor
type PID interface {
	// Send sends a given message to the actor within a time frame.
	// One can reply to the sender of the message in case a sender is set or set the reply in case we are dealing
	// with a request-response message format.
	Send(message Message) error
	// Shutdown gracefully shuts down the given actor
	Shutdown(ctx context.Context) error
	// IsOnline returns true when the actor is online ready to process messages and false
	// when the actor is stopped or not started at all
	IsOnline() bool
	// TotalProcessed returns the total number of messages processed by the actor
	// at a given point in time while the actor heart is still beating
	TotalProcessed(ctx context.Context) uint64
	// ErrorsCount returns the total number of panic attacks that occur while the actor is processing messages
	// at a given point in time while the actor heart is still beating
	ErrorsCount(ctx context.Context) uint64
	// SpawnChild creates a child actor
	SpawnChild(ctx context.Context, kind string, actor Actor) (PID, error)
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
}

// pid specifies an actor unique process
// With the pid one can send a message to the actor
type pid struct {
	Actor

	// addr represents the actor unique address
	addr Address

	// helps determine whether the actor should handle messages or not.
	isOnline bool
	// is captured whenever a mail is sent to the actor
	lastProcessingTime atomic.Time

	// specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 5 seconds
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
	mailbox chan Message
	// receives a shutdown signal. Once the signal is received
	// the actor is shut down gracefully.
	shutdownSignal     chan Unit
	haltPassivationLnr chan Unit
	errChan            chan error

	// set of watchers watching the given actor
	watchers *tools.ConcurrentSlice[*Watcher]

	// hold the list of the children
	children *pidMap

	// the actor system
	system ActorSystem
	// holds the kind of actor
	kind string

	//  specifies the logger to use
	logger log.Logger

	// definition of the various counters
	panicCounter           *atomic.Uint64
	receivedMessageCounter *atomic.Uint64
	lastProcessingDuration *atomic.Duration

	mu sync.RWMutex

	// supervisor strategy
	supervisorStrategy actorsv1.Strategy
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
		passivateAfter:         2 * time.Second,
		sendRecvTimeout:        100 * time.Millisecond,
		shutdownTimeout:        2 * time.Second,
		initMaxRetries:         5,
		mailbox:                make(chan Message, 1000),
		shutdownSignal:         make(chan Unit, 1),
		haltPassivationLnr:     make(chan Unit, 1),
		logger:                 log.DefaultLogger,
		panicCounter:           atomic.NewUint64(0),
		receivedMessageCounter: atomic.NewUint64(0),
		lastProcessingDuration: atomic.NewDuration(0),
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
	if pid.system != nil && pid.kind != "" {
		// create the address of the given actor and set it
		pid.addr = GetAddress(pid.system, pid.kind, actor.ID())
	}

	// initialize the actor and init processing messages
	pid.init(ctx)
	// init processing messages
	go pid.receive()
	// init the idle checker loop
	go pid.passivationListener()
	// return the actor reference
	return pid
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
	}

	// reset the actor
	p.reset()
	// initialize the actor
	p.init(ctx)
	// init processing messages
	go p.receive()
	// init the idle checker loop
	go p.passivationListener()
	// successful restart
	return nil
}

// SpawnChild creates a child actor and start watching it for error
func (p *pid) SpawnChild(ctx context.Context, kind string, actor Actor) (PID, error) {
	// first check whether the actor is ready to start another actor
	if !p.IsOnline() {
		return nil, ErrNotReady
	}

	// create the address of the given actor
	addr := GetAddress(p.ActorSystem(), kind, actor.ID())
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
		withKind(kind),
		withSupervisorStrategy(p.supervisorStrategy),
		withShutdownTimeout(p.shutdownTimeout),
		withAddress(addr))

	// add the pid to the map
	p.children.Set(cid)

	// let us start watching it
	p.Watch(cid)

	return cid, nil
}

// TotalProcessed returns the total number of messages processed by the actor
// at a given time while the actor is still alive
func (p *pid) TotalProcessed(ctx context.Context) uint64 {
	return p.receivedMessageCounter.Load()
}

// ErrorsCount returns the total number of panic attacks that occur while the actor is processing messages
// at a given point in time while the actor heart is still beating
func (p *pid) ErrorsCount(ctx context.Context) uint64 {
	return p.panicCounter.Load()
}

// Send sends a given message to the actor
func (p *pid) Send(message Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// reject message when the actor is not ready to process messages
	if !p.isOnline {
		return ErrNotReady
	}
	// set the last processing time
	p.lastProcessingTime.Store(time.Now())

	// set the start processing time
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		p.lastProcessingDuration.Store(duration)
	}()

	// set the error channel
	p.errChan = make(chan error, 1)
	// push the message to the actor mailbox
	p.mailbox <- message
	// increase the received message counter
	p.receivedMessageCounter.Inc()
	// await patiently for the error
	ctx, cancel := context.WithTimeout(message.Context(), p.sendRecvTimeout)
	defer cancel()
	select {
	case err := <-p.errChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
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
	p.mailbox = make(chan Message, 1000)
	// reset the children map
	p.children = newPIDMap(10)
	// reset the various counters
	p.panicCounter = atomic.NewUint64(0)
	p.receivedMessageCounter = atomic.NewUint64(0)
	p.lastProcessingDuration = atomic.NewDuration(0)
	// reset the channels
	p.shutdownSignal = make(chan Unit, 1)
	p.haltPassivationLnr = make(chan Unit, 1)
}

func (p *pid) freeChildren(ctx context.Context) {
	// iterate the pids and shutdown the child actors
	for _, child := range p.children.All() {
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
