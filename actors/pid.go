package actors

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/pkg/grpc"
	"github.com/tochemey/goakt/pkg/tools"
	"github.com/tochemey/goakt/telemetry"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

type watchMan struct {
	Parent  PID        // the Parent of the actor watching
	ErrChan chan error // the channel where to pass error message
	Done    chan Unit
}

// PID defines the various actions one can perform on a given actor
type PID interface {
	// Shutdown gracefully shuts down the given actor
	Shutdown(ctx context.Context) error
	// IsOnline returns true when the actor is online ready to process messages and false
	// when the actor is stopped or not started at all
	IsOnline() bool
	// ReceivedCount returns the total number of messages processed by the actor
	// at a given point in time while the actor heart is still beating
	ReceivedCount() uint64
	// ErrorsCount returns the total number of panic attacks that occur while the actor is processing messages
	// at a given point in time while the actor heart is still beating
	ErrorsCount() uint64
	// SpawnChild creates a child actor
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
	// Tell sends an asynchronous message to another PID
	Tell(ctx context.Context, to PID, message proto.Message) error
	// Ask sends a synchronous message to another actor and expect a response.
	Ask(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error)
	// RemoteTell sends a message to an actor remotely without expecting any reply
	RemoteTell(ctx context.Context, to *pb.Address, message proto.Message) error
	// RemoteAsk is used to send a message to an actor remotely and expect a response
	// immediately. With this type of message the receiver cannot communicate back to Sender
	// except reply the message with a response. This one-way communication.
	RemoteAsk(ctx context.Context, to *pb.Address, message proto.Message) (response proto.Message, err error)
	// RemoteLookup look for an actor address on a remote node. If the actorSystem is nil then the lookup will be done
	// using the same actor system as the PID actor system
	RemoteLookup(ctx context.Context, host string, port int, name string) (addr *pb.Address, err error)
	// RestartCount returns the number of times the actor has restarted
	RestartCount() uint64
	// MailboxSize returns the mailbox size a given time
	MailboxSize() uint64
	// Children returns the list of all the children of the given actor
	Children() []PID
	// Behaviors returns the behavior stack
	behaviors() BehaviorStack
	// push a message to the actor's mailbox
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
	// watchers returns the list of watchMen
	watchers() *tools.ConcurrentSlice[*watchMan]
}

// pid specifies an actor unique process
// With the pid one can send a receiveContext to the actor
type pid struct {
	Actor

	// specifies the actor path
	actorPath *Path

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
	sendReplyTimeout time.Duration

	// specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5 seconds
	initMaxRetries int

	// shutdownTimeout specifies how long to wait for the remaining messages
	// in the actor mailbox to be processed before completing the shutdown process
	// the default value is 5 seconds
	shutdownTimeout time.Duration

	// specifies the actor mailbox
	mailbox Mailbox

	// receives a shutdown signal. Once the signal is received
	// the actor is shut down gracefully.
	shutdownSignal     chan Unit
	haltPassivationLnr chan Unit

	// set of watchMen watching the given actor
	watchMen *tools.ConcurrentSlice[*watchMan]

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
	mailboxSizeCounter     *atomic.Uint64

	mu sync.RWMutex

	// supervisor strategy
	supervisorStrategy pb.StrategyDirective

	// specifies the current actor behavior
	behaviorStack BehaviorStack

	// observability settings
	telemetry *telemetry.Telemetry
}

// enforce compilation error
var _ PID = (*pid)(nil)

// newPID creates a new pid
func newPID(ctx context.Context, actorPath *Path, actor Actor, opts ...pidOption) *pid {
	// create the actor PID
	pid := &pid{
		Actor:                  actor,
		isOnline:               false,
		lastProcessingTime:     atomic.Time{},
		passivateAfter:         2 * time.Minute,
		sendReplyTimeout:       100 * time.Millisecond,
		shutdownTimeout:        2 * time.Second,
		initMaxRetries:         5,
		mailbox:                NewMailbox(),
		shutdownSignal:         make(chan Unit, 1),
		haltPassivationLnr:     make(chan Unit, 1),
		logger:                 log.DefaultLogger,
		panicCounter:           atomic.NewUint64(0),
		receivedMessageCounter: atomic.NewUint64(0),
		lastProcessingDuration: atomic.NewDuration(0),
		restartCounter:         atomic.NewUint64(0),
		mailboxSizeCounter:     atomic.NewUint64(0),
		mu:                     sync.RWMutex{},
		children:               newPIDMap(10),
		supervisorStrategy:     pb.StrategyDirective_STOP_DIRECTIVE,
		watchMen:               tools.NewConcurrentSlice[*watchMan](),
		telemetry:              telemetry.New(),
		actorPath:              actorPath,
	}

	// set the custom options to override the default values
	for _, opt := range opts {
		opt(pid)
	}

	// initialize the actor and init processing messages
	pid.init(ctx)
	// start the mailbox
	pid.mailbox.Start()
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

	// register metrics. However, we don't panic when we fail to register
	// we just log it for now
	// TODO decide what to do when we fail to register the metrics or export the metrics registration as public
	if err := pid.registerMetrics(); err != nil {
		pid.logger.Error(errors.Wrapf(err, "failed to register actor=%s metrics", pid.ActorPath().String()))
	}

	// return the actor reference
	return pid
}

// Children returns the list of all the children of the given actor
func (p *pid) Children() []PID {
	p.mu.Lock()
	kiddos := p.children.List()
	p.mu.Unlock()
	return kiddos
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

// ActorPath returns the path of the actor
func (p *pid) ActorPath() *Path {
	p.mu.Lock()
	path := p.actorPath
	p.mu.Unlock()
	return path
}

// Restart restarts the actor. This call can panic which is the expected behaviour.
// During restart all messages that are in the mailbox and not yet processed will be ignored
func (p *pid) Restart(ctx context.Context) error {
	// first check whether we have an empty PID
	if p == nil || p.actorPath == nil {
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
func (p *pid) SpawnChild(ctx context.Context, name string, actor Actor) (PID, error) {
	// first check whether the actor is ready to start another actor
	if !p.IsOnline() {
		return nil, ErrNotReady
	}

	// create the child actor path
	childActorPath := NewPath(name, p.ActorPath().Address()).WithParent(p.ActorPath())

	// check whether the child actor already exist and just return the PID
	if cid, ok := p.children.Get(childActorPath); ok {
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
	cid := newPID(ctx,
		childActorPath,
		actor,
		withInitMaxRetries(p.initMaxRetries),
		withPassivationAfter(p.passivateAfter),
		withSendReplyTimeout(p.sendReplyTimeout),
		withCustomLogger(p.logger),
		withActorSystem(p.system),
		withSupervisorStrategy(p.supervisorStrategy),
		withShutdownTimeout(p.shutdownTimeout))

	// add the pid to the map
	p.children.Set(cid)

	// let us start watching it
	p.Watch(cid)

	return cid, nil
}

// MailboxSize returns the mailbox size a given time
func (p *pid) MailboxSize() uint64 {
	return p.mailboxSizeCounter.Load()
}

// ReceivedCount returns the total number of messages processed by the actor
// at a given time while the actor is still alive
func (p *pid) ReceivedCount() uint64 {
	return p.receivedMessageCounter.Load()
}

// ErrorsCount returns the total number of panic attacks that occur while the actor is processing messages
// at a given point in time while the actor heart is still beating
func (p *pid) ErrorsCount() uint64 {
	return p.panicCounter.Load()
}

// RestartCount returns the number of times the actor has restarted
func (p *pid) RestartCount() uint64 {
	return p.restartCounter.Load()
}

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (p *pid) Ask(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
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
	for await := time.After(p.sendReplyTimeout); ; {
		select {
		case response = <-context.response:
			return
		case <-await:
			err = ErrRequestTimeout
			return
		}
	}
}

// Tell sends an asynchronous message to another PID
func (p *pid) Tell(ctx context.Context, to PID, message proto.Message) error {
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

// RemoteLookup look for an actor address on a remote node. If the actorSystem is nil then the lookup will be done
// using the same actor system as the PID actor system
func (p *pid) RemoteLookup(ctx context.Context, host string, port int, name string) (addr *pb.Address, err error) {
	// create an instance of remote client service
	rpcConn, _ := grpc.GetClientConn(ctx, fmt.Sprintf("%s:%d", host, port))
	remoteClient := pb.NewRemotingServiceClient(rpcConn)

	// prepare the request to send
	request := &pb.RemoteLookupRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	}
	// send the message and handle the error in case there is any
	response, err := remoteClient.RemoteLookup(ctx, request)
	// we know the error will always be a grpc error
	if err != nil {
		// get the status error
		s := status.Convert(err)
		if s.Code() == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}

	// return the response
	return response.GetAddress(), nil
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (p *pid) RemoteTell(ctx context.Context, to *pb.Address, message proto.Message) error {
	// marshal the message
	marshaled, err := anypb.New(message)
	if err != nil {
		return err
	}

	// create an instance of remote client service
	rpcConn, _ := grpc.GetClientConn(ctx, fmt.Sprintf("%s:%d", to.GetHost(), to.GetPort()))
	remoteClient := pb.NewRemotingServiceClient(rpcConn)

	// construct the from address
	sender := &pb.Address{
		Host: p.ActorPath().Address().Host(),
		Port: int32(p.ActorPath().Address().Port()),
		Name: p.ActorPath().Name(),
		Id:   p.ActorPath().ID().String(),
	}

	// prepare the rpcRequest to send
	request := &pb.RemoteTellRequest{
		RemoteMessage: &pb.RemoteMessage{
			Sender:   sender,
			Receiver: to,
			Message:  marshaled,
		},
	}
	// send the message and handle the error in case there is any
	if _, err := remoteClient.RemoteTell(ctx, request); err != nil {
		return err
	}
	return nil
}

// RemoteAsk sends a synchronous message to another actor remotely and expect a response.
func (p *pid) RemoteAsk(ctx context.Context, to *pb.Address, message proto.Message) (response proto.Message, err error) {
	// marshal the message
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, err
	}

	// create an instance of remote client service
	rpcConn, _ := grpc.GetClientConn(ctx, fmt.Sprintf("%s:%d", to.GetHost(), to.GetPort()))
	remoteClient := pb.NewRemotingServiceClient(rpcConn)
	// prepare the rpcRequest to send
	rpcRequest := &pb.RemoteAskRequest{
		Receiver: to,
		Message:  marshaled,
	}
	// send the request
	rpcResponse, rpcErr := remoteClient.RemoteAsk(ctx, rpcRequest)
	// handle the error
	if rpcErr != nil {
		return nil, rpcErr
	}

	return rpcResponse.GetMessage(), nil
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
		p.logger.Infof("Actor=%s is offline. Maybe it has been passivated or stopped already", p.ActorPath().String())
		return nil
	}

	// stop the actor PID
	p.stop(ctx)

	// shutdown the mailbox
	p.mailbox.Shutdown()

	// add some logging
	p.logger.Info("Shutdown process is on going for actor=%s", p.ActorPath().String())
	// signal we are shutting down to stop processing messages
	p.shutdownSignal <- Unit{}
	// perform some cleanup with the actor
	if err := p.Actor.PostStop(ctx); err != nil {
		p.logger.Error(fmt.Errorf("failed to stop the underlying receiver for actor=%s. Cause:%v", p.ActorPath().String(), err))
		return err
	}
	p.logger.Infof("Actor=%s successfully shutdown", p.ActorPath().String())
	return nil
}

// Watch a pid for errors, and send on the returned channel if an error occurred
func (p *pid) Watch(pid PID) {
	// create a watcher
	w := &watchMan{
		Parent:  p,
		ErrChan: make(chan error, 1),
		Done:    make(chan Unit, 1),
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
		if w.Parent.ActorPath() == p.ActorPath() {
			// stop the watching go routine
			w.Done <- Unit{}
			// remove the watcher from the list
			pid.watchers().Delete(item.Index)
			break
		}
	}
}

// Watchers return the list of watchMen
func (p *pid) watchers() *tools.ConcurrentSlice[*watchMan] {
	return p.watchMen
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
	p.mailbox.Post(ctx)
	// increment the mailbox size counter
	p.mailboxSizeCounter.Store(p.mailbox.Size())
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
	p.mailbox.Restart()
	// reset the children map
	p.children = newPIDMap(10)
	// reset the various counters
	p.panicCounter = atomic.NewUint64(0)
	p.receivedMessageCounter = atomic.NewUint64(0)
	p.lastProcessingDuration = atomic.NewDuration(0)
	p.restartCounter = atomic.NewUint64(0)
	p.mailboxSizeCounter = atomic.NewUint64(0)
	// reset the channels
	p.shutdownSignal = make(chan Unit, 1)
	p.haltPassivationLnr = make(chan Unit, 1)
	// reset the behavior stack
	p.resetBehaviorStack()
	// register metrics. However, we don't panic when we fail to register
	// we just log it for now
	// TODO decide what to do when we fail to register the metrics or export the metrics registration as public
	if err := p.registerMetrics(); err != nil {
		p.logger.Error(errors.Wrapf(err, "failed to register actor=%s metrics", p.ActorPath().String()))
	}
}

func (p *pid) resetBehaviorStack() {
	// reset the behavior
	behaviorStack := NewBehaviorStack()
	behaviorStack.Push(p.Receive)
	p.behaviorStack = behaviorStack
}

func (p *pid) freeChildren(ctx context.Context) {
	// iterate the child actors and shut them down
	for _, child := range p.children.List() {
		// stop the child actor
		if err := child.Shutdown(ctx); err != nil {
			panic(err)
		}
		// unwatch the child
		p.UnWatch(child)
		p.children.Delete(child.ActorPath())
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
				if p.mailbox.Size() == 0 {
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
	p.logger.Infof("Remaining messages in the mailbox have been processed for actor=%s", p.ActorPath().String())
	// stop the ticker
	ticker.Stop()
}

// Behaviors returns the behavior stack
func (p *pid) behaviors() BehaviorStack {
	p.mu.Lock()
	behaviors := p.behaviorStack
	p.mu.Unlock()
	return behaviors
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

// registerMetrics register the PID metrics with OTel instrumentation.
func (p *pid) registerMetrics() error {
	// grab the OTel meter
	meter := p.telemetry.Meter
	// create an instance of the ActorMetrics
	metrics, err := telemetry.NewMetrics(meter)
	// handle the error
	if err != nil {
		return err
	}

	// register the metrics
	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		observer.ObserveInt64(metrics.ReceivedCount, int64(p.ReceivedCount()))
		observer.ObserveInt64(metrics.PanicCount, int64(p.ErrorsCount()))
		observer.ObserveInt64(metrics.RestartedCount, int64(p.RestartCount()))
		observer.ObserveInt64(metrics.MailboxSize, int64(p.MailboxSize()))
		return nil
	}, metrics.ReceivedCount,
		metrics.RestartedCount,
		metrics.PanicCount,
		metrics.MailboxSize)

	return err
}

// receive handles every mail in the actor mailbox
func (p *pid) receive() {
	// make sure wires the calling goroutine to its current operating system thread
	// and that the calling goroutine will always execute in that thread
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// run the processing loop
	for {
		select {
		case <-p.shutdownSignal:
			return
		default:
			// read the data from mailbox
			if received := p.mailbox.Read(); received != nil {
				func() {
					// recover from a panic attack
					defer func() {
						if r := recover(); r != nil {
							// construct the error to return
							err := fmt.Errorf("%s", r)
							// send the error to the watchMen
							for item := range p.watchMen.Iter() {
								item.Value.ErrChan <- err
							}
							// increase the panic counter
							p.panicCounter.Inc()
						}
					}()
					// send the message to the current actor behavior
					if behavior, ok := p.behaviorStack.Peek(); ok {
						behavior(received)
					}
				}()
			}
		}
	}
}

// supervise watches for child actor's failure and act based upon the supervisory strategy
func (p *pid) supervise(cid PID, watcher *watchMan) {
	for {
		select {
		case <-watcher.Done:
			return
		case err := <-watcher.ErrChan:
			p.logger.Errorf("child actor=%s is panicking: Err=%v", cid.ActorPath().String(), err)
			switch p.supervisorStrategy {
			case pb.StrategyDirective_STOP_DIRECTIVE:
				// shutdown the actor and panic in case of error
				if err := cid.Shutdown(context.Background()); err != nil {
					panic(err)
				}
				// unwatch the given actor
				p.UnWatch(cid)
				// remove the actor from the children map
				p.children.Delete(cid.ActorPath())
			case pb.StrategyDirective_RESTART_DIRECTIVE:
				// restart the actor
				if err := cid.Restart(context.Background()); err != nil {
					panic(err)
				}
			default:
				// shutdown the actor and panic in case of error
				if err := cid.Shutdown(context.Background()); err != nil {
					panic(err)
				}
				// unwatch the given actor
				p.UnWatch(cid)
				// remove the actor from the children map
				p.children.Delete(cid.ActorPath())
			}
		}
	}
}

// passivationListener checks whether the actor is processing messages or not.
// when the actor is idle, it automatically shuts down to free resources
func (p *pid) passivationListener() {
	p.logger.Info("start the passivation listener...")
	// create the ticker
	ticker := time.NewTicker(p.passivateAfter)
	// create the stop ticker signal
	tickerStopSig := make(chan Unit, 1)

	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				// check whether the actor is idle or not
				idleTime := time.Since(p.lastProcessingTime.Load())
				// check whether the actor is idle
				if idleTime >= p.passivateAfter {
					// set the done channel to stop the ticker
					tickerStopSig <- Unit{}
					return
				}
			case <-p.haltPassivationLnr:
				// set the done channel to stop the ticker
				tickerStopSig <- Unit{}
				return
			}
		}
	}()
	// wait for the stop signal to stop the ticker
	<-tickerStopSig
	// stop the ticker
	ticker.Stop()
	// only passivate when actor is alive
	if !p.IsOnline() {
		// add some logging info
		p.logger.Infof("Actor=%s is offline. No need to passivate", p.ActorPath().String())
		return
	}

	// add some logging info
	p.logger.Infof("Passivation mode has been triggered for actor=%s...", p.ActorPath().String())
	// passivate the actor
	p.passivate()
}

func (p *pid) passivate() {
	// create a context
	ctx := context.Background()
	// stop the actor PID
	p.stop(ctx)
	// shutdown the mailbox
	p.mailbox.Shutdown()
	// perform some cleanup with the actor
	if err := p.Actor.PostStop(ctx); err != nil {
		panic(err)
	}
}
