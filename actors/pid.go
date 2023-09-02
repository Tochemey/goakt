package actors

import (
	"context"
	"fmt"
	gothttp "net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/cenkalti/backoff"
	"github.com/pkg/errors"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/internal/goakt/v1/goaktv1connect"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/v1"
	"github.com/tochemey/goakt/pkg/http"
	"github.com/tochemey/goakt/pkg/slices"
	"github.com/tochemey/goakt/pkg/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// WatchMan is used to handle parent child relationship.
// This helps handle error propagation from a child actor using any of supervisory strategies
type WatchMan struct {
	Parent  PID        // the Parent of the actor watching
	ErrChan chan error // ErrChan the channel where to pass error message
	Done    chan Unit  // Done when watching is completed
}

const (
	// DefaultPassivationTimeout defines the default passivation timeout
	DefaultPassivationTimeout = 2 * time.Minute
	// DefaultReplyTimeout defines the default send/reply timeout
	DefaultReplyTimeout = 100 * time.Millisecond
	// DefaultInitMaxRetries defines the default value for retrying actor initialization
	DefaultInitMaxRetries = 5
	// DefaultSupervisoryStrategy defines the default supervisory strategy
	DefaultSupervisoryStrategy = StopDirective
	// DefaultShutdownTimeout defines the default shutdown timeout
	DefaultShutdownTimeout = 2 * time.Second

	defaultMailboxSize = 4096
)

// PID defines the various actions one can perform on a given actor
type PID interface {
	// Shutdown gracefully shuts down the given actor
	Shutdown(ctx context.Context) error
	// IsRunning returns true when the actor is running ready to process public and false
	// when the actor is stopped or not started at all
	IsRunning() bool
	// ReceivedCount returns the total number of public processed by the actor
	// at a given point in time while the actor heart is still beating
	ReceivedCount(ctx context.Context) uint64
	// ErrorsCount returns the total number of panic attacks that occur while the actor is processing public
	// at a given point in time while the actor heart is still beating
	ErrorsCount(ctx context.Context) uint64
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
	// ActorHandle returns the underlying actor
	ActorHandle() Actor
	// Tell sends an asynchronous message to another PID
	Tell(ctx context.Context, to PID, message proto.Message) error
	// Ask sends a synchronous message to another actor and expect a response.
	Ask(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error)
	// RemoteTell sends a message to an actor remotely without expecting any reply
	RemoteTell(ctx context.Context, to *pb.Address, message proto.Message) error
	// RemoteAsk is used to send a message to an actor remotely and expect a response
	// immediately. With this type of message the receiver cannot communicate back to Sender
	// except reply the message with a response. This one-way communication.
	RemoteAsk(ctx context.Context, to *pb.Address, message proto.Message) (response *anypb.Any, err error)
	// RemoteLookup look for an actor address on a remote node. If the actorSystem is nil then the lookup will be done
	// using the same actor system as the PID actor system
	RemoteLookup(ctx context.Context, host string, port int, name string) (addr *pb.Address, err error)
	// RestartCount returns the number of times the actor has restarted
	RestartCount(ctx context.Context) uint64
	// MailboxSize returns the mailbox size a given time
	MailboxSize(ctx context.Context) uint64
	// Children returns the list of all the children of the given actor
	Children(ctx context.Context) []PID
	// push a message to the actor's mailbox
	doReceive(ctx ReceiveContext)
	// watchers returns the list of watchMen
	watchers() *slices.ConcurrentSlice[*WatchMan]
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

	// specifies the actor path
	actorPath *Path

	// helps determine whether the actor should handle public or not.
	isRunning *atomic.Bool
	// is captured whenever a mail is sent to the actor
	lastProcessingTime atomic.Time

	// specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 120 seconds
	passivateAfter *atomic.Duration

	// specifies how long the sender of a mail should wait to receive a reply
	// when using SendReply. The default value is 5s
	sendReplyTimeout *atomic.Duration

	// specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5 seconds
	initMaxRetries *atomic.Int32

	// shutdownTimeout specifies the graceful shutdown timeout
	// the default value is 5 seconds
	shutdownTimeout *atomic.Duration

	// specifies the actor mailbox
	mailbox chan ReceiveContext
	// receives a shutdown signal. Once the signal is received
	// the actor is shut down gracefully.
	shutdownSignal     chan Unit
	haltPassivationLnr chan Unit

	// set of watchMen watching the given actor
	watchMen *slices.ConcurrentSlice[*WatchMan]

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

	// rwMutex that helps synchronize the pid in a concurrent environment
	// this helps protect the pid fields accessibility
	rwMutex       sync.RWMutex
	shutdownMutex sync.Mutex

	// supervisor strategy
	supervisorStrategy StrategyDirective

	// observability settings
	telemetry *telemetry.Telemetry

	// http client
	httpClient *gothttp.Client

	// specifies the current actor behavior
	behaviorStack *behaviorStack
}

// enforce compilation error
var _ PID = (*pid)(nil)

// newPID creates a new pid
func newPID(ctx context.Context, actorPath *Path, actor Actor, opts ...pidOption) *pid {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "NewPID")
	defer span.End()

	// create the actor PID
	pid := &pid{
		Actor:                  actor,
		isRunning:              atomic.NewBool(false),
		lastProcessingTime:     atomic.Time{},
		passivateAfter:         atomic.NewDuration(DefaultPassivationTimeout),
		sendReplyTimeout:       atomic.NewDuration(DefaultReplyTimeout),
		shutdownTimeout:        atomic.NewDuration(DefaultShutdownTimeout),
		initMaxRetries:         atomic.NewInt32(DefaultInitMaxRetries),
		mailbox:                make(chan ReceiveContext, defaultMailboxSize),
		shutdownSignal:         make(chan Unit, 1),
		haltPassivationLnr:     make(chan Unit, 1),
		logger:                 log.DefaultLogger,
		panicCounter:           atomic.NewUint64(0),
		receivedMessageCounter: atomic.NewUint64(0),
		lastProcessingDuration: atomic.NewDuration(0),
		restartCounter:         atomic.NewUint64(0),
		mailboxSizeCounter:     atomic.NewUint64(0),

		children:           newPIDMap(10),
		supervisorStrategy: DefaultSupervisoryStrategy,
		watchMen:           slices.NewConcurrentSlice[*WatchMan](),
		telemetry:          telemetry.New(),
		actorPath:          actorPath,
		rwMutex:            sync.RWMutex{},
		shutdownMutex:      sync.Mutex{},
		httpClient:         http.Client(),
	}

	// set the custom options to override the default values
	for _, opt := range opts {
		opt(pid)
	}

	// initialize the actor and init processing public
	pid.init(ctx)
	// init processing public
	go pid.receive()
	// init the passivation listener loop iff passivation is set
	if pid.passivateAfter.Load() > 0 {
		go pid.passivationListener()
	}

	// set the actor behavior stack
	behaviorStack := newBehaviorStack()
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

// ActorHandle returns the underlying Actor
func (p *pid) ActorHandle() Actor {
	return p.Actor
}

// Children returns the list of all the children of the given actor
func (p *pid) Children(ctx context.Context) []PID {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Children")
	defer span.End()
	p.rwMutex.RLock()
	kiddos := p.children.List()
	p.rwMutex.RUnlock()
	return kiddos
}

// IsRunning returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (p *pid) IsRunning() bool {
	return p.isRunning.Load()
}

// ActorSystem returns the actor system
func (p *pid) ActorSystem() ActorSystem {
	p.rwMutex.RLock()
	sys := p.system
	p.rwMutex.RUnlock()
	return sys
}

// ActorPath returns the path of the actor
func (p *pid) ActorPath() *Path {
	p.rwMutex.RLock()
	path := p.actorPath
	p.rwMutex.RUnlock()
	return path
}

// Restart restarts the actor. This call can panic which is the expected behaviour.
// During restart all messages that are in the mailbox and not yet processed will be ignored
func (p *pid) Restart(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Restart")
	defer span.End()

	// add some debug logging
	p.logger.Debugf("restarting actor=(%s)", p.actorPath.String())

	// first check whether we have an empty PID
	if p == nil || p.ActorPath() == nil {
		return ErrUndefinedActor
	}

	// only restart the actor when the actor is running
	if p.IsRunning() {
		// shutdown the actor
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
				if !p.IsRunning() {
					tickerStopSig <- Unit{}
					return
				}
			}
		}()
		<-tickerStopSig
		ticker.Stop()
	}

	// initialize the actor
	p.init(ctx)
	// init processing public
	go p.receive()
	// init the passivation listener loop iff passivation is set
	if p.passivateAfter.Load() > 0 {
		go p.passivationListener()
	}

	// reset the behavior
	p.resetBehavior()

	// register metrics. However, we don't panic when we fail to register
	// we just log it for now
	// TODO decide what to do when we fail to register the metrics or export the metrics registration as public
	if err := p.registerMetrics(); err != nil {
		p.logger.Error(errors.Wrapf(err, "failed to register actor=%s metrics", p.ActorPath().String()))
		return err
	}

	// increment the restart counter
	p.restartCounter.Inc()
	// successful restart
	return nil
}

// SpawnChild creates a child actor and start watching it for error
func (p *pid) SpawnChild(ctx context.Context, name string, actor Actor) (PID, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "SpawnChild")
	defer span.End()
	// first check whether the actor is ready to start another actor
	if !p.IsRunning() {
		return nil, ErrNotReady
	}

	// create the child actor path
	childActorPath := NewPath(name, p.ActorPath().Address()).WithParent(p.ActorPath())

	// check whether the child actor already exist and just return the PID
	if cid, ok := p.children.Get(childActorPath); ok {
		// check whether the actor is stopped
		if cid != nil && !cid.IsRunning() {
			// then reboot it
			if err := cid.Restart(ctx); err != nil {
				return nil, err
			}
		}
		return cid, nil
	}

	// acquire the lock
	p.rwMutex.Lock()
	// release the lock
	defer p.rwMutex.Unlock()

	// create the child pid
	cid := newPID(ctx,
		childActorPath,
		actor,
		withInitMaxRetries(int(p.initMaxRetries.Load())),
		withPassivationAfter(p.passivateAfter.Load()),
		withSendReplyTimeout(p.sendReplyTimeout.Load()),
		withCustomLogger(p.logger),
		withActorSystem(p.system),
		withSupervisorStrategy(p.supervisorStrategy),
		withShutdownTimeout(p.shutdownTimeout.Load()))

	// add the pid to the map
	p.children.Set(cid)

	// let us start watching it
	p.Watch(cid)

	return cid, nil
}

// MailboxSize returns the mailbox size a given time
func (p *pid) MailboxSize(ctx context.Context) uint64 {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "MailboxSize")
	defer span.End()
	return p.mailboxSizeCounter.Load()
}

// ReceivedCount returns the total number of messages processed by the actor
// at a given time while the actor is still alive
func (p *pid) ReceivedCount(ctx context.Context) uint64 {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "ReceivedCount")
	defer span.End()
	return p.receivedMessageCounter.Load()
}

// ErrorsCount returns the total number of panic attacks that occur while the actor is processing public
// at a given point in time while the actor heart is still beating
func (p *pid) ErrorsCount(ctx context.Context) uint64 {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "ErrorsCount")
	defer span.End()
	return p.panicCounter.Load()
}

// RestartCount returns the number of times the actor has restarted
func (p *pid) RestartCount(ctx context.Context) uint64 {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RestartCount")
	defer span.End()
	return p.restartCounter.Load()
}

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (p *pid) Ask(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Ask")
	defer span.End()
	// make sure the actor is live
	if !to.IsRunning() {
		return nil, ErrNotReady
	}

	// acquire a lock to set the message context
	p.rwMutex.Lock()
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

	// release the lock after setting the message context
	p.rwMutex.Unlock()

	// put the message context in the mailbox of the recipient actor
	to.doReceive(context)

	// await patiently to receive the response from the actor
	for await := time.After(p.sendReplyTimeout.Load()); ; {
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
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Tell")
	defer span.End()

	// make sure the recipient actor is live
	if !to.IsRunning() {
		return ErrNotReady
	}

	// acquire a lock to set the message context
	p.rwMutex.Lock()
	// release the lock after setting the message context
	defer p.rwMutex.Unlock()
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

	// put the message context in the mailbox of the recipient actor
	to.doReceive(context)

	return nil
}

// RemoteLookup look for an actor address on a remote node. If the actorSystem is nil then the lookup will be done
// using the same actor system as the PID actor system
func (p *pid) RemoteLookup(ctx context.Context, host string, port int, name string) (addr *pb.Address, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteLookup")
	defer span.End()

	// create an instance of remote client service
	remoteClient := goaktv1connect.NewRemoteMessagingServiceClient(
		p.httpClient,
		http.URL(host, port),
		connect.WithInterceptors(p.interceptor()),
		connect.WithGRPC(),
	)

	// prepare the request to send
	request := connect.NewRequest(&goaktpb.RemoteLookupRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})
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
	return response.Msg.GetAddress(), nil
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (p *pid) RemoteTell(ctx context.Context, to *pb.Address, message proto.Message) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteTell")
	defer span.End()

	// marshal the message
	marshaled, err := anypb.New(message)
	if err != nil {
		return err
	}

	// create an instance of remote client service
	remoteClient := goaktv1connect.NewRemoteMessagingServiceClient(
		p.httpClient,
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(p.interceptor()),
		connect.WithGRPC(),
	)

	// construct the from address
	sender := &pb.Address{
		Host: p.ActorPath().Address().Host(),
		Port: int32(p.ActorPath().Address().Port()),
		Name: p.ActorPath().Name(),
		Id:   p.ActorPath().ID().String(),
	}

	// prepare the rpcRequest to send
	request := connect.NewRequest(&goaktpb.RemoteTellRequest{
		RemoteMessage: &goaktpb.RemoteMessage{
			Sender:   sender,
			Receiver: to,
			Message:  marshaled,
		},
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
func (p *pid) RemoteAsk(ctx context.Context, to *pb.Address, message proto.Message) (response *anypb.Any, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteTell")
	defer span.End()

	// marshal the message
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, err
	}

	// create an instance of remote client service
	remoteClient := goaktv1connect.NewRemoteMessagingServiceClient(
		p.httpClient,
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(p.interceptor()),
		connect.WithGRPC(),
	)

	// prepare the rpcRequest to send
	rpcRequest := connect.NewRequest(
		&goaktpb.RemoteAskRequest{
			RemoteMessage: &goaktpb.RemoteMessage{
				Sender:   RemoteNoSender,
				Receiver: to,
				Message:  marshaled,
			},
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
// All current public in the mailbox will be processed before the actor shutdown after a period of time
// that can be configured. All child actors will be gracefully shutdown.
func (p *pid) Shutdown(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Shutdown")
	defer span.End()

	// acquire the shutdown lock
	p.shutdownMutex.Lock()
	// release the lock
	defer p.shutdownMutex.Unlock()

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
		p.haltPassivationLnr <- Unit{}
	}

	// signal we are shutting down to stop processing messages
	p.shutdownSignal <- Unit{}

	// stop receiving and sending messages
	p.isRunning.Store(false)
	// stop all the child actors
	p.freeChildren(ctx)

	// close lingering http connections
	p.httpClient.CloseIdleConnections()

	// add some logging
	p.logger.Infof("Shutdown process is on going for actor=%s...", p.ActorPath().String())

	// perform some cleanup with the actor
	if err := p.Actor.PostStop(ctx); err != nil {
		p.logger.Error(fmt.Errorf("failed to stop the underlying receiver for actor=%s. Cause:%v", p.ActorPath().String(), err))
		return err
	}

	// reset the actor
	p.reset()

	p.logger.Infof("Actor=%s successfully shutdown", p.ActorPath().String())
	return nil
}

// Watch a pid for errors, and send on the returned channel if an error occurred
func (p *pid) Watch(pid PID) {
	// create a watcher
	w := &WatchMan{
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
func (p *pid) watchers() *slices.ConcurrentSlice[*WatchMan] {
	return p.watchMen
}

// doReceive pushes a given message to the actor mailbox
func (p *pid) doReceive(ctx ReceiveContext) {
	// acquire the lock and release it once done
	p.rwMutex.Lock()
	defer p.rwMutex.Unlock()

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
	// increment the mailbox size counter
	p.mailboxSizeCounter.Store(uint64(len(p.mailbox)))
	// increase the received message counter
	p.receivedMessageCounter.Inc()
}

// init initializes the given actor and init processing public
// when the initialization failed the actor is automatically shutdown
func (p *pid) init(ctx context.Context) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Init")
	defer span.End()
	// add some logging info
	p.logger.Info("Initialization process has started...")
	// create the exponential backoff object
	expoBackoff := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), uint64(p.initMaxRetries.Load()))
	// init the actor initialization receive
	err := backoff.Retry(func() error {
		return p.Actor.PreStart(ctx)
	}, expoBackoff)
	// handle backoff error
	if err != nil {
		// log the error
		p.logger.Error(err.Error())
		return
	}
	// set the actor is ready
	p.rwMutex.Lock()
	p.isRunning.Store(true)
	p.rwMutex.Unlock()
	// add some logging info
	p.logger.Info("Initialization process successfully completed.")
}

// reset re-initializes the actor PID
func (p *pid) reset() {
	// reset the various settings of the PID
	p.lastProcessingTime = atomic.Time{}
	p.passivateAfter.Store(DefaultPassivationTimeout)
	p.sendReplyTimeout.Store(DefaultReplyTimeout)
	p.shutdownTimeout.Store(DefaultShutdownTimeout)
	p.initMaxRetries.Store(DefaultInitMaxRetries)
	p.panicCounter.Store(0)
	p.receivedMessageCounter.Store(0)
	p.lastProcessingDuration.Store(0)
	p.restartCounter.Store(0)
	p.mailboxSizeCounter.Store(0)
	p.children = newPIDMap(10)
	p.watchMen = slices.NewConcurrentSlice[*WatchMan]()
	p.telemetry = telemetry.New()
	// reset the behavior stack
	p.resetBehavior()
}

func (p *pid) freeChildren(ctx context.Context) {
	p.logger.Debug("freeing all child actors...")
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "FreeChildren")
	defer span.End()

	// iterate the child actors and shut them down
	for _, child := range p.Children(ctx) {
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

// registerMetrics register the PID metrics with OTel instrumentation.
func (p *pid) registerMetrics() error {
	// acquire lock
	p.rwMutex.Lock()
	// release the lock
	defer p.rwMutex.Unlock()

	// grab the OTel meter
	meter := p.telemetry.Meter
	// create an instance of the ActorMetrics
	metrics, err := telemetry.NewMetrics(meter)
	// handle the error
	if err != nil {
		return err
	}

	// define the common labels
	labels := []attribute.KeyValue{
		attribute.String("actor.name", p.actorPath.Name()),
		attribute.String("actor.address", p.actorPath.String()),
		attribute.String("actor.type", strings.Replace(fmt.Sprintf("%T", p.Actor), "*", "", 1)),
	}

	// register the metrics
	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		observer.ObserveInt64(metrics.ReceivedCount, int64(p.ReceivedCount(ctx)), metric.WithAttributes(labels...))
		observer.ObserveInt64(metrics.PanicCount, int64(p.ErrorsCount(ctx)), metric.WithAttributes(labels...))
		observer.ObserveInt64(metrics.RestartedCount, int64(p.RestartCount(ctx)), metric.WithAttributes(labels...))
		observer.ObserveInt64(metrics.MailboxSize, int64(p.MailboxSize(ctx)), metric.WithAttributes(labels...))
		return nil
	}, metrics.ReceivedCount,
		metrics.RestartedCount,
		metrics.PanicCount,
		metrics.MailboxSize)

	return err
}

// receive handles every mail in the actor mailbox
func (p *pid) receive() {
	// run the processing loop
	for {
		select {
		case <-p.shutdownSignal:
			return
		case received := <-p.mailbox:
			p.handleReceived(received)
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
			// increase the panic counter
			p.panicCounter.Inc()
		}
	}()
	// send the message to the current actor behavior
	if behavior, ok := p.behaviorStack.Peek(); ok {
		behavior(received)
	}
}

// supervise watches for child actor's failure and act based upon the supervisory strategy
func (p *pid) supervise(cid PID, watcher *WatchMan) {
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
					p.logger.Panic(err)
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
					p.logger.Panic(err)
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
	tickerStopSig := make(chan Unit, 1)

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
	if !p.IsRunning() {
		// add some logging info
		p.logger.Infof("Actor=%s is offline. No need to passivate", p.ActorPath().String())
		return
	}

	// acquire the shutdown lock
	p.shutdownMutex.Lock()
	// release the lock
	defer p.shutdownMutex.Unlock()

	// add some logging info
	p.logger.Infof("Passivation mode has been triggered for actor=%s...", p.ActorPath().String())

	// signal we are shutting down to stop processing messages
	p.shutdownSignal <- Unit{}

	// stop receiving and sending messages
	p.isRunning.Store(false)

	// create a context
	ctx := context.Background()

	// stop all the child actors
	p.freeChildren(ctx)

	// close lingering http connections
	p.httpClient.CloseIdleConnections()

	// perform some cleanup with the actor
	if err := p.Actor.PostStop(ctx); err != nil {
		panic(err)
	}

	// reset
	p.reset()
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
	p.rwMutex.Lock()
	p.behaviorStack.Clear()
	p.behaviorStack.Push(behavior)
	p.rwMutex.Unlock()
}

// resetBehavior is a utility function resets the actor behavior
func (p *pid) resetBehavior() {
	p.rwMutex.Lock()
	p.behaviorStack.Clear()
	p.behaviorStack.Push(p.Receive)
	p.rwMutex.Unlock()
}

// setBehaviorStacked adds a behavior to the actor's behaviorStack
func (p *pid) setBehaviorStacked(behavior Behavior) {
	p.rwMutex.Lock()
	p.behaviorStack.Push(behavior)
	p.rwMutex.Unlock()
}

// unsetBehaviorStacked sets the actor's behavior to the previous behavior
// prior to setBehaviorStacked is called
func (p *pid) unsetBehaviorStacked() {
	p.rwMutex.Lock()
	p.behaviorStack.Pop()
	p.rwMutex.Unlock()
}
