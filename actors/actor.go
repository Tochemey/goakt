package actors

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tochemey/goakt/logging"

	"github.com/cenkalti/backoff"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// ActorRef specifies an actor unique reference
// With the ActorRef one can send a message to the actor
type ActorRef struct {
	Actor
	// ID is the actor identifier. This identifier is unique within a cluster of actors
	// TODO need to rethink more about it
	ID uuid.UUID

	// helps determine whether the actor should handle messages or not.
	isReady bool
	// is captured whenever a message is sent to the actor
	lastProcessingTime time.Time

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
	logger logging.Logger

	rwMutex sync.RWMutex
}

// enforce compilation error
var _ actorRef = (*ActorRef)(nil)

// NewActorRef creates an actor given its unique identifier and return its reference
func NewActorRef(ctx context.Context, actor Actor, opts ...ActorOpt) *ActorRef {
	// generate an internal ID for the actor
	// TODO refactor the internal ID generation
	id := uuid.New()
	// create the actor ref
	actorRef := &ActorRef{
		ID:                 id,
		isReady:            false,
		lastProcessingTime: time.Time{},
		passivateAfter:     5 * time.Second,
		sendRecvTimeout:    5 * time.Second,
		initMaxRetries:     5,
		mailbox:            make(chan any),
		shutdownSignal:     make(chan struct{}),
		rwMutex:            sync.RWMutex{},
		Actor:              actor,
		logger:             logging.DefaultLogger,
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

// Heartbeat returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (a *ActorRef) Heartbeat() bool {
	a.rwMutex.Lock()
	defer a.rwMutex.Unlock()
	return a.isReady
}

// Send sends a given message to the actor
func (a *ActorRef) Send(ctx context.Context, message proto.Message) error {
	// let us make sure the actor is ready to receive messages before sending the given message
	a.rwMutex.Lock()
	defer a.rwMutex.Unlock()
	// reject message when the actor is not ready to process messages
	if !a.isReady {
		return ErrNotReady
	}
	// set the last processing time
	a.lastProcessingTime = time.Now()
	// push the message to the mailbox for the actor to receive
	// create an error channel
	errChan := make(chan error)
	// create the message
	msg := &send{
		ctx:     ctx,
		message: message,
		errChan: errChan,
	}
	// push the message to the actor mailbox
	a.mailbox <- msg
	// await patiently for the error
	for e := range msg.errChan {
		return e
	}
	// return
	return nil
}

// SendReply sends a given message to the actor and expect a reply in return
func (a *ActorRef) SendReply(ctx context.Context, message proto.Message) (proto.Message, error) {
	// let us make sure the actor is ready to receive messages before sending the given message
	a.rwMutex.Lock()
	defer a.rwMutex.Unlock()
	if !a.isReady {
		return nil, ErrNotReady
	}

	// set the last processing time
	a.lastProcessingTime = time.Now()
	// create the ask message
	msg := &sendRecv{
		ctx:     ctx,
		message: message,
		reply:   make(chan proto.Message),
		errChan: make(chan error),
	}
	// push the message to the mailbox
	a.mailbox <- msg
	// await patiently for the reply or an error
	select {
	case reply := <-msg.reply:
		return reply, nil
	case err := <-msg.errChan:
		return nil, err
	}
}

// Shutdown gracefully shuts down the given actor
// All current messages in the mailbox will be processed before the actor shutdown
func (a *ActorRef) Shutdown(ctx context.Context) {
	a.logger.Info("Shutdown process has started...")
	// acquire a lock
	a.rwMutex.Lock()
	// stop future messages
	a.isReady = false
	// unlock
	a.rwMutex.Unlock()
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
	a.isReady = true
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
			idleTime := time.Since(a.lastProcessingTime)
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
				w := &Waiter[proto.Message]{}
				w.wg.Add(1)

				go func() {
					// recover from a panic attack
					defer func() {
						if r := recover(); r != nil {
							// set the error
							w.SetError(fmt.Errorf("%s", r))
							w.wg.Done()
						}
					}()
					// send the message to actor to receive
					err := a.Actor.Receive(msg.ctx, msg.message)
					// set the waiter error
					w.SetError(err)
					w.wg.Done()
				}()

				go func() {
					// make sure to close the message error channel
					// when we are done processing the message
					defer close(msg.errChan)
					// wait for processing is done
					w.wg.Wait()
					// set the message error channel to response received
					msg.errChan <- w.err
					// signal we are done processing the message
					done <- struct{}{}
				}()

				// free go routines
				<-done
			case *sendRecv:
				done := make(chan struct{})
				// create an instance of promise
				w := &Waiter[proto.Message]{}
				w.wg.Add(1)

				go func() {
					// recover from a panic attack
					defer func() {
						if r := recover(); r != nil {
							// set the error
							w.SetError(fmt.Errorf("%s", r))
							w.wg.Done()
						}
					}()
					// send the message to actor to receive
					reply, err := a.Actor.ReceiveReply(msg.ctx, msg.message)
					w.SetError(err)
					w.SetResult(reply)
					w.wg.Done()
				}()

				go func() {
					// make sure to close the message channels
					// when we are done processing the message
					defer func() {
						close(msg.errChan)
						close(msg.reply)
					}()

					// wait for the processing to complete or timed out
					if err := WaitGroupTimeout(&w.wg, a.sendRecvTimeout); err != nil {
						w.SetError(err)
					}
					// handle the processing error
					if w.err != nil {
						msg.errChan <- w.err
						// signal we are done processing the message
						done <- struct{}{}
						return
					}
					// set the result to the message reply channel
					msg.reply <- w.res
					// signal we are done processing the message
					done <- struct{}{}
				}()
				// free go routines
				<-done
			}
		}
	}
}
