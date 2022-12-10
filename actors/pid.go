package actors

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cenkalti/backoff"
	actorsv1 "github.com/tochemey/goakt/gen/actors/v1"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
	"golang.org/x/sync/errgroup"
)

// NoSender means that there is no sender
var NoSender = new(PID)

// processor defines the various actions one can perform on a given actor
type processor interface {
	// Send sends a given message to the actor in a fire-and-forget pattern
	Send(message Message) error
	// Shutdown gracefully shuts down the given actor
	Shutdown(ctx context.Context) error
	// IsReady returns true when the actor is alive ready to process messages and false
	// when the actor is stopped or not started at all
	IsReady(ctx context.Context) bool
	// TotalProcessed returns the total number of messages processed by the actor
	// at a given point in time while the actor heart is still beating
	TotalProcessed(ctx context.Context) uint64
	// ErrorsCount returns the total number of panic attacks that occur while the actor is processing messages
	// at a given point in time while the actor heart is still beating
	ErrorsCount(ctx context.Context) uint64
	// SpawnChild creates a child actor of its own kind
	SpawnChild(ctx context.Context, actor Actor) (*PID, error)
	// Restart restarts the actor
	Restart(ctx context.Context)
}

// PID specifies an actor unique process
// With the PID one can send a message to the actor
type PID struct {
	Actor

	// addr represents the actor unique address
	addr Address

	// helps determine whether the actor should handle messages or not.
	isReady bool
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
	shutdownSignal chan Unit
	errChan        chan error
	watchChan      chan error

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

	mu         sync.RWMutex
	childrenMu sync.RWMutex

	// supervisor strategy
	supervisorStrategy actorsv1.Strategy
}

// enforce compilation error
var _ processor = (*PID)(nil)

// NewPID creates a new PID
func NewPID(ctx context.Context, actor Actor, opts ...pidOption) *PID {
	// create the actor PID
	pid := &PID{
		Actor:                  actor,
		addr:                   "",
		isReady:                false,
		lastProcessingTime:     atomic.Time{},
		passivateAfter:         5 * time.Second,
		sendRecvTimeout:        5 * time.Second,
		shutdownTimeout:        5 * time.Second,
		initMaxRetries:         5,
		mailbox:                make(chan Message, 1000),
		shutdownSignal:         make(chan Unit),
		logger:                 log.DefaultLogger,
		panicCounter:           atomic.NewUint64(0),
		receivedMessageCounter: atomic.NewUint64(0),
		lastProcessingDuration: atomic.NewDuration(0),
		mu:                     sync.RWMutex{},
		childrenMu:             sync.RWMutex{},
		children:               newPIDMap(10),
		supervisorStrategy:     actorsv1.Strategy_STOP,
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

// IsReady returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (p *PID) IsReady(ctx context.Context) bool {
	p.mu.RLock()
	ready := p.isReady
	p.mu.RUnlock()
	return ready
}

// Restart restarts the actor. This call can panic which is the expected behaviour.
// During restart all messages that are in the mailbox and not yet processed will be ignored
func (p *PID) Restart(ctx context.Context) {
	// first check whether we have an empty PID
	if p == nil || p.addr == "" {
		panic(ErrUndefinedActor)
	}
	// check whether the actor is ready and stop it
	if p.IsReady(ctx) {
		// stop the actor
		if err := p.Shutdown(ctx); err != nil {
			panic(err)
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
}

// SpawnChild creates a child actor and start watching it for error
func (p *PID) SpawnChild(ctx context.Context, actor Actor) (*PID, error) {
	// first check whether the actor is ready to start another actor
	if !p.IsReady(ctx) {
		return nil, ErrNotReady
	}

	// let us acquire the children lock
	p.childrenMu.Lock()
	defer p.childrenMu.Unlock()

	// create the address of the given actor
	addr := GetAddress(p.system, p.kind, actor.ID())
	// check whether the child actor already exist and just return the PID
	if pid, ok := p.children.Get(addr); ok {
		// TODO check whether the actor is passivated and restart it
		// TODO implement a restart mechanism
		return pid, nil
	}

	// create the child pid
	pid := NewPID(ctx, actor,
		withInitMaxRetries(p.initMaxRetries),
		withPassivationAfter(p.passivateAfter),
		withSendReplyTimeout(p.sendRecvTimeout),
		withCustomLogger(p.logger),
		withActorSystem(p.system),
		withKind(p.kind),
		withSupervisorStrategy(p.supervisorStrategy),
		withShutdownTimeout(p.shutdownTimeout),
		withAddress(addr))

	// add the pid to the map
	p.children.Set(pid)

	// let us start watching it
	watch := pid.watch()
	// TODO make sure we don't leak memory with this go routine
	go func() {
		err := <-watch
		p.logger.Errorf("child actor=%s is panicing: Err=%v", pid.addr, err)
		switch p.supervisorStrategy {
		case actorsv1.Strategy_STOP:
			// shutdown the actor and panic in case of error
			if err := pid.Shutdown(context.Background()); err != nil {
				panic(err)
			}
			// remove the actor from the children map
			pid.children.Delete(pid.addr)
		case actorsv1.Strategy_RESTART:
			// restart the actor
			pid.Restart(context.Background())
		default:
			// shutdown the actor and panic in case of error
			if err := pid.Shutdown(context.Background()); err != nil {
				panic(err)
			}
			// remove the actor from the children map
			pid.children.Delete(pid.addr)
		}
	}()

	return pid, nil
}

// TotalProcessed returns the total number of messages processed by the actor
// at a given time while the actor is still alive
func (p *PID) TotalProcessed(ctx context.Context) uint64 {
	return p.receivedMessageCounter.Load()
}

// ErrorsCount returns the total number of panic attacks that occur while the actor is processing messages
// at a given point in time while the actor heart is still beating
func (p *PID) ErrorsCount(ctx context.Context) uint64 {
	return p.panicCounter.Load()
}

// Send sends a given message to the actor
func (p *PID) Send(message Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// reject message when the actor is not ready to process messages
	if !p.isReady {
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
func (p *PID) Shutdown(ctx context.Context) error {
	p.logger.Info("Shutdown process has started...")
	// stop all the child actors
	p.freeChildren(ctx)
	// stop future messages
	p.mu.Lock()
	p.isReady = false
	p.mu.Unlock()
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
	// add some logging
	p.logger.Info("Remaining messages in the mailbox have been processed!!!")
	// stop the ticker
	ticker.Stop()
	// add some logging
	p.logger.Info("Shutdown process is on going...")
	// signal we are shutting down to stop processing messages
	p.shutdownSignal <- Unit{}
	// perform some cleanup with the actor
	return p.Actor.PostStop(ctx)
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor is automatically shutdown
func (p *PID) init(ctx context.Context) {
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
		// shutdown the actor
		p.Shutdown(ctx)
		return
	}
	// set the actor is ready
	p.mu.Lock()
	p.isReady = true
	p.mu.Unlock()
	// add some logging info
	p.logger.Info("Initialization process successfully completed.")
}

// passivationListener checks whether the actor is processing messages or not.
// when the actor is idle, it automatically shuts down to free resources
func (p *PID) passivationListener() {
	// create the ticker
	ticker := time.NewTicker(p.passivateAfter)
	// create the stop ticker signal
	tickerStopSig := make(chan struct{})

	// init ticking
	go func() {
		for range ticker.C {
			// check whether the actor is idle or not
			idleTime := time.Since(p.lastProcessingTime.Load())
			// check whether the actor is idle
			if idleTime >= p.passivateAfter {
				// set the done channel to stop the ticker
				tickerStopSig <- struct{}{}
			}
		}
	}()
	// wait for the stop signal to stop the ticker
	<-tickerStopSig
	// stop the ticker
	ticker.Stop()
	// add some logging info
	p.logger.Info("Passivation mode has been triggered...")
	// init the shutdown process
	if err := p.Shutdown(context.Background()); err != nil {
		// FIXME fix the panic
		panic(err)
	}
}

// receive handles every mail in the actor mailbox
func (p *PID) receive() {
	// run the processing loop
	for {
		select {
		case <-p.shutdownSignal:
			return
		case received := <-p.mailbox:
			msg := received.Payload()
			switch msg.(type) {
			case *actorsv1.PoisonPill:
				if err := p.Shutdown(received.Context()); err != nil {
					// FIXME fix the panic
					panic(err)
				}
			default:
				done := make(chan struct{})
				go func() {
					// close the msg error channel
					defer close(p.errChan)
					defer close(done)
					// recover from a panic attack
					defer func() {
						if r := recover(); r != nil {
							// send the error to the channel
							p.errChan <- fmt.Errorf("%s", r)
							// increase the panic counter
							p.panicCounter.Inc()
						}
					}()
					// send the message to actor to receive
					err := p.Receive(received)
					// set the error channel
					p.errChan <- err
				}()
			}
		}
	}
}

// watch a pid for errors, and send on the returned channel if an error occurred
func (p *PID) watch() chan error {
	errChan := make(chan error)
	go func() {
		err := <-p.watchChan
		errChan <- err
	}()
	return errChan
}

func (p *PID) reset() {
	// reset the mailbox
	p.mailbox = make(chan Message, 1000)
	// reset the children map
	p.children = newPIDMap(10)
	// reset the various counters
	p.panicCounter = atomic.NewUint64(0)
	p.receivedMessageCounter = atomic.NewUint64(0)
	p.lastProcessingDuration = atomic.NewDuration(0)
}

func (p *PID) freeChildren(ctx context.Context) {
	g, ctx := errgroup.WithContext(ctx)
	// iterate the pids and shutdown the child actors
	for _, child := range p.children.All() {
		child := child // golang closure
		g.Go(func() error {
			return child.Shutdown(ctx)
		})
	}

	// handle any eventual error
	if err := g.Wait(); err != nil {
		panic(err)
	}

	// remove all the child actors
	for _, child := range p.children.All() {
		p.children.Delete(child.addr)
	}
}

// pidOption represents the PID
type pidOption func(ref *PID)

// withPassivationAfter sets the actor passivation time
func withPassivationAfter(duration time.Duration) pidOption {
	return func(ref *PID) {
		ref.passivateAfter = duration
	}
}

// withSendReplyTimeout sets how long in seconds an actor should reply a command
// in a receive-reply pattern
func withSendReplyTimeout(timeout time.Duration) pidOption {
	return func(ref *PID) {
		ref.sendRecvTimeout = timeout
	}
}

// withInitMaxRetries sets the number of times to retry an actor init process
func withInitMaxRetries(max int) pidOption {
	return func(ref *PID) {
		ref.initMaxRetries = max
	}
}

// withCustomLogger sets the logger
func withCustomLogger(logger log.Logger) pidOption {
	return func(ref *PID) {
		ref.logger = logger
	}
}

// withAddress sets the address of the PID
func withAddress(addr Address) pidOption {
	return func(ref *PID) {
		ref.addr = addr
	}
}

// withActorSystem set the actor system of the PID
func withActorSystem(sys ActorSystem) pidOption {
	return func(ref *PID) {
		ref.system = sys
	}
}

// withKind set the kind of actor represented by the PID
func withKind(kind string) pidOption {
	return func(ref *PID) {
		ref.kind = kind
	}
}

// withSupervisorStrategy sets the supervisor strategy to used when dealing
// with child actors
func withSupervisorStrategy(strategy actorsv1.Strategy) pidOption {
	return func(ref *PID) {
		ref.supervisorStrategy = strategy
	}
}

// withShutdownTimeout sets the shutdown timeout
func withShutdownTimeout(duration time.Duration) pidOption {
	return func(ref *PID) {
		ref.shutdownTimeout = duration
	}
}
