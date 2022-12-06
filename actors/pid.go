package actors

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cenkalti/backoff"
	actorspb "github.com/tochemey/goakt/gen/actors/v1"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

// NoSender means that there is no sender
var NoSender = new(PID)

// processor defines the various actions one can perform on a given actor
type processor interface {
	// Send sends a given message to the actor in a fire-and-forget pattern
	Send(message Message) error
	// Shutdown gracefully shuts down the given actor
	Shutdown(ctx context.Context)
	// IsReady returns true when the actor is alive ready to process messages and false
	// when the actor is stopped or not started at all
	IsReady(ctx context.Context) bool
	// TotalProcessed returns the total number of messages processed by the actor
	// at a given point in time while the actor heart is still beating
	TotalProcessed(ctx context.Context) uint64
	// ErrorsCount returns the total number of panic attacks that occur while the actor is processing messages
	// at a given point in time while the actor heart is still beating
	ErrorsCount(ctx context.Context) uint64
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
	// any further resources like memory and cpu. The default value is 5s
	passivateAfter time.Duration

	// specifies how long the sender of a mail should wait to receive a reply
	// when using SendReply. The default value is 5s
	sendRecvTimeout time.Duration

	// specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	initMaxRetries int

	// specifies the actor mailbox
	mailbox chan Message
	// receives a shutdown signal. Once the signal is received
	// the actor is shut down gracefully.
	shutdownSignal chan struct{}
	errChan        chan error

	//  specifies the logger to use
	logger log.Logger

	// definition of the various counters
	panicCounter           *atomic.Uint64
	receivedMessageCounter *atomic.Uint64
	lastProcessingDuration *atomic.Duration

	mu sync.RWMutex
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
		initMaxRetries:         5,
		mailbox:                make(chan Message, 1000),
		shutdownSignal:         make(chan struct{}),
		logger:                 log.DefaultLogger,
		panicCounter:           atomic.NewUint64(0),
		receivedMessageCounter: atomic.NewUint64(0),
		lastProcessingDuration: atomic.NewDuration(0),
	}
	// set the custom options to override the default values
	for _, opt := range opts {
		opt(pid)
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
func (a *PID) IsReady(ctx context.Context) bool {
	a.mu.RLock()
	ready := a.isReady
	a.mu.RUnlock()
	return ready
}

// TotalProcessed returns the total number of messages processed by the actor
// at a given time while the actor is still alive
func (a *PID) TotalProcessed(ctx context.Context) uint64 {
	return a.receivedMessageCounter.Load()
}

// ErrorsCount returns the total number of panic attacks that occur while the actor is processing messages
// at a given point in time while the actor heart is still beating
func (a *PID) ErrorsCount(ctx context.Context) uint64 {
	return a.panicCounter.Load()
}

// Send sends a given message to the actor
func (a *PID) Send(message Message) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	// reject message when the actor is not ready to process messages
	if !a.isReady {
		return ErrNotReady
	}
	// set the last processing time
	a.lastProcessingTime.Store(time.Now())

	// set the start processing time
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		a.lastProcessingDuration.Store(duration)
	}()

	// set the error channel
	a.errChan = make(chan error, 1)
	// push the message to the actor mailbox
	a.mailbox <- message
	// increase the received message counter
	a.receivedMessageCounter.Inc()
	// await patiently for the error
	ctx, cancel := context.WithTimeout(message.Context(), a.sendRecvTimeout)
	defer cancel()
	select {
	case err := <-a.errChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Shutdown gracefully shuts down the given actor
// All current messages in the mailbox will be processed before the actor shutdown
func (a *PID) Shutdown(ctx context.Context) {
	a.logger.Info("Shutdown process has started...")
	// stop future messages
	a.mu.Lock()
	a.isReady = false
	a.mu.Unlock()
	// wait for all messages in the mailbox to be processed
	// init a ticker that run every 20 ms to make sure we process all messages in the
	// mailbox.
	ticker := time.NewTicker(20 * time.Millisecond)
	// create the ticker stop signal
	tickerStopSig := make(chan struct{})
	// init ticking
	go func() {
		for range ticker.C {
			// no more message to process
			if len(a.mailbox) == 0 {
				// tell the ticker to stop
				tickerStopSig <- struct{}{}
			}
		}
	}()
	// listen to ticker stop signal
	<-tickerStopSig
	// add some logging
	a.logger.Info("Remaining messages in the mailbox have been processed!!!")
	// stop the ticker
	ticker.Stop()
	// add some logging
	a.logger.Info("Shutdown process is on going...")
	// signal we are shutting down to stop processing messages
	a.shutdownSignal <- struct{}{}
	// perform some cleanup with the actor
	a.Actor.PostStop(ctx)
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor is automatically shutdown
func (a *PID) init(ctx context.Context) {
	// add some logging info
	a.logger.Info("Initialization process has started...")
	// create the exponential backoff object
	expoBackoff := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), uint64(a.initMaxRetries))
	// init the actor initialization receive
	err := backoff.Retry(func() error {
		return a.Actor.PreStart(ctx)
	}, expoBackoff)
	// handle backoff error
	if err != nil {
		// log the error
		a.logger.Error(err.Error())
		// shutdown the actor
		a.Shutdown(ctx)
		return
	}
	// set the actor is ready
	a.mu.Lock()
	a.isReady = true
	a.mu.Unlock()
	// add some logging info
	a.logger.Info("Initialization process successfully completed.")
}

// passivationListener checks whether the actor is processing messages or not.
// when the actor is idle, it automatically shuts down to free resources
func (a *PID) passivationListener() {
	// create the ticker
	ticker := time.NewTicker(a.passivateAfter)
	// create the stop ticker signal
	tickerStopSig := make(chan struct{})

	// init ticking
	go func() {
		for range ticker.C {
			// check whether the actor is idle or not
			idleTime := time.Since(a.lastProcessingTime.Load())
			// check whether the actor is idle
			if idleTime >= a.passivateAfter {
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
	a.logger.Info("Passivation mode has been triggered...")
	// init the shutdown process
	a.Shutdown(context.Background())
}

// receive handles every mail in the actor mailbox
func (a *PID) receive() {
	// run the processing loop
	for {
		select {
		case <-a.shutdownSignal:
			return
		case received := <-a.mailbox:
			msg := received.Payload()
			switch msg.(type) {
			case *actorspb.Terminate:
				a.Shutdown(received.Context())
			default:
				done := make(chan struct{})
				go func() {
					// close the msg error channel
					defer close(a.errChan)
					defer close(done)
					// recover from a panic attack
					defer func() {
						if r := recover(); r != nil {
							// send the error
							a.errChan <- fmt.Errorf("%s", r)
							// increase the panic counter
							a.panicCounter.Inc()
						}
					}()
					// send the message to actor to receive
					err := a.Receive(received)
					a.errChan <- err
				}()
			}
		}
	}
}

// pidOption represents the actor ref
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

// withAddress sets the address of the actor ref
func withAddress(addr Address) pidOption {
	return func(ref *PID) {
		ref.addr = addr
	}
}
