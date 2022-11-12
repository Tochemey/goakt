package actors

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
)

// actorRef defines the various actions one can perform on a given actor
type actorRef interface {
	// Send sends a given message to the actor in a fire-and-forget pattern
	Send(ctx context.Context, message proto.Message) error
	// SendReply sends a given message to the actor and expect a reply in return
	SendReply(ctx context.Context, message proto.Message) (proto.Message, error)
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

// ActorRef specifies an actor unique reference
// With the ActorRef one can send a message to the actor
type ActorRef struct {
	Actor

	// addr represents the actor unique address
	addr Address

	// helps determine whether the actor should handle messages or not.
	isReady *atomic.Bool
	// is captured whenever a message is sent to the actor
	lastProcessingTime atomic.Time

	// specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 5s
	passivateAfter time.Duration

	// specifies how long the sender of a message should wait to receive a reply
	// when using SendReply. The default value is 5s
	sendRecvTimeout time.Duration

	// specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	initMaxRetries int

	// specifies the actor mailbox
	mailbox chan any
	// receives a shutdown signal. Once the signal is received
	// the actor is shut down gracefully.
	shutdownSignal chan struct{}

	//  specifies the logger to use
	logger log.Logger

	rwMutex sync.RWMutex

	// definition of the various counters
	panicCounter           *atomic.Uint64
	receivedMessageCounter *atomic.Uint64
	lastProcessingDuration *atomic.Duration
}

// enforce compilation error
var _ actorRef = (*ActorRef)(nil)

// NewActorRef creates an actor given its unique identifier and return its reference
func NewActorRef(ctx context.Context, actor Actor, opts ...ActorRefOption) *ActorRef {
	// create the actor ref
	actorRef := &ActorRef{
		Actor:                  actor,
		addr:                   "",
		isReady:                atomic.NewBool(false),
		lastProcessingTime:     atomic.Time{},
		passivateAfter:         5 * time.Second,
		sendRecvTimeout:        5 * time.Second,
		initMaxRetries:         5,
		mailbox:                make(chan any, 1000),
		shutdownSignal:         make(chan struct{}),
		logger:                 log.DefaultLogger,
		rwMutex:                sync.RWMutex{},
		panicCounter:           atomic.NewUint64(0),
		receivedMessageCounter: atomic.NewUint64(0),
		lastProcessingDuration: atomic.NewDuration(0),
	}
	// set the custom options to override the default values
	for _, opt := range opts {
		opt(actorRef)
	}

	// initialize the actor and init processing messages
	actorRef.init(ctx)
	// init processing messages
	go actorRef.receive()
	// init the idle checker loop
	go actorRef.passivationListener()
	// return the actor reference
	return actorRef
}

// IsReady returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (a *ActorRef) IsReady(ctx context.Context) bool {
	return a.isReady.Load()
}

// TotalProcessed returns the total number of messages processed by the actor
// at a given time while the actor is still alive
func (a *ActorRef) TotalProcessed(ctx context.Context) uint64 {
	return a.receivedMessageCounter.Load()
}

// ErrorsCount returns the total number of panic attacks that occur while the actor is processing messages
// at a given point in time while the actor heart is still beating
func (a *ActorRef) ErrorsCount(ctx context.Context) uint64 {
	return a.panicCounter.Load()
}

// Send sends a given message to the actor
func (a *ActorRef) Send(ctx context.Context, message proto.Message) error {
	// reject message when the actor is not ready to process messages
	if !a.IsReady(ctx) {
		return ErrNotReady
	}
	// set the last processing time
	a.lastProcessingTime.Store(time.Now())
	// push the message to the mailbox for the actor to receive
	// create an error channel
	errChan := make(chan error)
	// create the message
	msg := &send{
		ctx:     ctx,
		message: message,
		errChan: errChan,
	}

	// set the start processing time
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		a.lastProcessingDuration.Store(duration)
	}()

	// push the message to the actor mailbox
	a.mailbox <- msg
	// increase the received message counter
	a.receivedMessageCounter.Inc()
	// await patiently for the error
	for e := range msg.errChan {
		return e
	}
	// return
	return nil
}

// SendReply sends a given message to the actor and expect a reply in return
func (a *ActorRef) SendReply(ctx context.Context, message proto.Message) (proto.Message, error) {
	if !a.IsReady(ctx) {
		return nil, ErrNotReady
	}

	// set the last processing time
	a.lastProcessingTime.Store(time.Now())
	// create the ask message
	msg := &sendRecv{
		ctx:      ctx,
		message:  message,
		response: make(chan *response),
	}
	// set the start processing time
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		a.lastProcessingDuration.Store(duration)
	}()

	// push the message to the mailbox
	a.mailbox <- msg
	// increase the received message counter
	a.receivedMessageCounter.Inc()
	// await patiently for the reply or an error
	r := <-msg.response
	return r.reply, r.err
}

// Shutdown gracefully shuts down the given actor
// All current messages in the mailbox will be processed before the actor shutdown
func (a *ActorRef) Shutdown(ctx context.Context) {
	a.logger.Info("Shutdown process has started...")
	// stop future messages
	a.isReady.Store(false)
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
	a.Actor.Stop(ctx)
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor is automatically shutdown
func (a *ActorRef) init(ctx context.Context) {
	// add some logging info
	a.logger.Info("Initialization process has started...")
	// create the exponential backoff object
	expoBackoff := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), uint64(a.initMaxRetries))
	// init the actor initialization receive
	err := backoff.Retry(func() error {
		return a.Actor.Init(ctx)
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
	a.isReady.Store(true)
	// add some logging info
	a.logger.Info("Initialization process successfully completed.")
}

// passivationListener checks whether the actor is processing messages or not.
// when the actor is idle, it automatically shuts down to free resources
func (a *ActorRef) passivationListener() {
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

// receive handles every message in the actor mailbox
func (a *ActorRef) receive() {
	// run the processing loop
	for {
		select {
		case <-a.shutdownSignal:
			return
		case received := <-a.mailbox:
			// handle message per message type
			switch msg := received.(type) {
			case *send:
				done := make(chan struct{})

				go func() {
					// close the msg error channel
					defer close(msg.errChan)
					// recover from a panic attack
					defer func() {
						if r := recover(); r != nil {
							// send the error
							msg.errChan <- fmt.Errorf("%s", r)
							// increase the panic counter
							a.panicCounter.Inc()
							// signal the go routine we are done
							close(done)
						}
					}()
					// send the message to actor to receive
					err := a.Actor.Receive(msg.ctx, msg.message)
					msg.errChan <- err
					// signal the go routine we are done
					close(done)
				}()

			case *sendRecv:
				// create a variable that will hold the processed message response
				resp := make(chan *response, 1)
				// create the cancellation context with the timeout setting
				ctx, cancel := context.WithTimeout(msg.ctx, a.sendRecvTimeout)
				// start processing the received message
				go func() {
					response := new(response)
					// recover from a panic attack
					defer func() {
						if r := recover(); r != nil {
							// set the error
							response.err = fmt.Errorf("%s", r)
							// increase the panic counter
							a.panicCounter.Inc()
							// set the response
							resp <- response
						}
					}()
					// send the message to actor to receive
					response.reply, response.err = a.Actor.ReceiveReply(ctx, msg.message)
					// send the response the msg channel response
					resp <- response
				}()

				select {
				case r := <-resp:
					msg.response <- r
				case <-ctx.Done():
					msg.response <- &response{
						err: ErrTimeout,
					}
				}
				// close the message response channel
				close(msg.response)
				// release resources when operation complete before timeout
				cancel()
			}
		}
	}
}
