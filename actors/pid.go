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

package actors

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

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/future"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/errorschain"
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
// to pipe the result to the appropriate PID
type taskCompletion struct {
	Receiver *PID
	Task     future.Task
}

// PID specifies an actor unique process
// With the PID one can send a ReceiveContext to the actor
type PID struct {
	// specifies the message processor
	actor Actor

	// specifies the actor address
	address *address.Address

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

	// specifies the init timeout.
	// the default initialization timeout is 1s
	initTimeout atomic.Duration

	// shutdownTimeout specifies the graceful shutdown timeout
	// the default value is 5 seconds
	shutdownTimeout atomic.Duration

	// specifies the actor mailbox
	mailbox *mailbox

	haltPassivationLnr chan types.Unit

	// hold the watchersList watching the given actor
	watchersList *slice.Safe[*watcher]

	// hold the list of the children
	children *pidMap

	// hold the list of watched actors
	watchedList *pidMap

	// the actor system
	system ActorSystem

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
func newPID(ctx context.Context, address *address.Address, actor Actor, opts ...pidOption) (*PID, error) {
	// actor address is required
	if address == nil {
		return nil, errors.New("address is required")
	}

	// validate the address
	if err := address.Validate(); err != nil {
		return nil, err
	}

	p := &PID{
		actor:                          actor,
		latestReceiveTime:              atomic.Time{},
		haltPassivationLnr:             make(chan types.Unit, 1),
		logger:                         log.New(log.ErrorLevel, os.Stderr),
		children:                       newPIDMap(10),
		supervisorDirective:            DefaultSupervisoryStrategy,
		watchersList:                   slice.New[*watcher](),
		watchedList:                    newPIDMap(10),
		telemetry:                      telemetry.New(),
		address:                        address,
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

	if err := p.init(ctx); err != nil {
		return nil, err
	}

	go p.receive()
	go p.notifyWatchers()
	if p.passivateAfter.Load() > 0 {
		go p.passivationLoop()
	}

	if p.metricEnabled.Load() {
		metrics, err := metric.NewActorMetric(p.telemetry.Meter())
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
func (pid *PID) ID() string {
	return pid.Address().String()
}

// Name returns the actor given name
func (pid *PID) Name() string {
	return pid.Address().Name()
}

// Equals is a convenient method to compare two PIDs
func (pid *PID) Equals(to *PID) bool {
	return strings.EqualFold(pid.ID(), to.ID())
}

// Actor returns the underlying Actor
func (pid *PID) Actor() Actor {
	return pid.actor
}

// Child returns the named child actor if it is alive
func (pid *PID) Child(name string) (*PID, error) {
	if !pid.IsRunning() {
		return nil, ErrDead
	}

	childAddress := address.New(name, pid.Address().System(), pid.Address().Host(), pid.Address().Port()).WithParent(pid.Address())
	if cid, ok := pid.children.get(childAddress); ok {
		pid.childrenCount.Inc()
		return cid, nil
	}
	return nil, ErrActorNotFound(childAddress.String())
}

// Parents returns the list of all direct parents of a given actor.
// Only alive actors are included in the list or an empty list is returned
func (pid *PID) Parents() []*PID {
	pid.fieldsLocker.Lock()
	watchers := pid.watchersList
	pid.fieldsLocker.Unlock()
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
func (pid *PID) Children() []*PID {
	pid.fieldsLocker.RLock()
	children := pid.children.pids()
	pid.fieldsLocker.RUnlock()

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
func (pid *PID) Stop(ctx context.Context, cid *PID) error {
	if !pid.IsRunning() {
		return ErrDead
	}

	if cid == nil || cid == NoSender {
		return ErrUndefinedActor
	}

	pid.fieldsLocker.RLock()
	children := pid.children
	pid.fieldsLocker.RUnlock()

	if cid, ok := children.get(cid.Address()); ok {
		if err := cid.Shutdown(ctx); err != nil {
			return err
		}

		return nil
	}

	return ErrActorNotFound(cid.Address().String())
}

// IsRunning returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (pid *PID) IsRunning() bool {
	return pid != nil && pid != NoSender && pid.running.Load()
}

// ActorSystem returns the actor system
func (pid *PID) ActorSystem() ActorSystem {
	pid.fieldsLocker.RLock()
	sys := pid.system
	pid.fieldsLocker.RUnlock()
	return sys
}

// Address returns address of the actor
func (pid *PID) Address() *address.Address {
	pid.fieldsLocker.RLock()
	path := pid.address
	pid.fieldsLocker.RUnlock()
	return path
}

// Restart restarts the actor.
// During restart all messages that are in the mailbox and not yet processed will be ignored
func (pid *PID) Restart(ctx context.Context) error {
	if pid == nil || pid.Address() == nil {
		return ErrUndefinedActor
	}

	pid.logger.Debugf("restarting actor=(%s)", pid.address.String())

	if pid.IsRunning() {
		if err := pid.Shutdown(ctx); err != nil {
			return err
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		tickerStopSig := make(chan types.Unit, 1)
		go func() {
			for range ticker.C {
				if !pid.IsRunning() {
					tickerStopSig <- types.Unit{}
					return
				}
			}
		}()
		<-tickerStopSig
		ticker.Stop()
	}

	pid.resetBehavior()
	if err := pid.init(ctx); err != nil {
		return err
	}
	go pid.receive()
	go pid.notifyWatchers()
	if pid.passivateAfter.Load() > 0 {
		go pid.passivationLoop()
	}

	pid.restartCount.Inc()

	if pid.eventsStream != nil {
		pid.eventsStream.Publish(eventsTopic, &goaktpb.ActorRestarted{
			Address:     pid.Address().Address,
			RestartedAt: timestamppb.Now(),
		})
	}

	return nil
}

// SpawnChild creates a child actor and start watching it for error
// When the given child actor already exists its PID will only be returned
func (pid *PID) SpawnChild(ctx context.Context, name string, actor Actor) (*PID, error) {
	if !pid.IsRunning() {
		return nil, ErrDead
	}

	childAddress := address.New(name, pid.Address().System(), pid.Address().Host(), pid.Address().Port()).WithParent(pid.Address())
	pid.fieldsLocker.RLock()
	children := pid.children
	pid.fieldsLocker.RUnlock()

	if cid, ok := children.get(childAddress); ok {
		return cid, nil
	}

	pid.fieldsLocker.RLock()

	// create the child actor options child inherit parent's options
	opts := []pidOption{
		withInitMaxRetries(int(pid.initMaxRetries.Load())),
		withPassivationAfter(pid.passivateAfter.Load()),
		withAskTimeout(pid.askTimeout.Load()),
		withCustomLogger(pid.logger),
		withActorSystem(pid.system),
		withSupervisorDirective(pid.supervisorDirective),
		withEventsStream(pid.eventsStream),
		withInitTimeout(pid.initTimeout.Load()),
		withShutdownTimeout(pid.shutdownTimeout.Load()),
	}

	if pid.metricEnabled.Load() {
		opts = append(opts, withMetric())
	}

	cid, err := newPID(ctx,
		childAddress,
		actor,
		opts...,
	)

	if err != nil {
		pid.fieldsLocker.RUnlock()
		return nil, err
	}

	pid.children.set(cid)
	eventsStream := pid.eventsStream

	pid.fieldsLocker.RUnlock()

	pid.Watch(cid)

	if eventsStream != nil {
		eventsStream.Publish(eventsTopic, &goaktpb.ActorChildCreated{
			Address:   cid.Address().Address,
			CreatedAt: timestamppb.Now(),
			Parent:    pid.Address().Address,
		})
	}

	// set the actor in the given actor system registry
	if pid.ActorSystem() != nil {
		pid.ActorSystem().setActor(cid)
	}

	return cid, nil
}

// StashSize returns the stash buffer size
func (pid *PID) StashSize() uint64 {
	if pid.stashBuffer == nil {
		return 0
	}
	return uint64(pid.stashBuffer.Len())
}

// PipeTo processes a long-running task and pipes the result to the provided actor.
// The successful result of the task will be put onto the provided actor mailbox.
// This is useful when interacting with external services.
// It’s common that you would like to use the value of the response in the actor when the long-running task is completed
func (pid *PID) PipeTo(ctx context.Context, to *PID, task future.Task) error {
	if task == nil {
		return ErrUndefinedTask
	}

	if !to.IsRunning() {
		return ErrDead
	}

	go pid.handleCompletion(ctx, &taskCompletion{
		Receiver: to,
		Task:     task,
	})

	return nil
}

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (pid *PID) Ask(ctx context.Context, to *PID, message proto.Message) (response proto.Message, err error) {
	if !to.IsRunning() {
		return nil, ErrDead
	}

	receiveContext := newReceiveContext(ctx, pid, to, message)
	to.doReceive(receiveContext)
	timeout := pid.askTimeout.Load()

	select {
	case result := <-receiveContext.response:
		pid.recordLatestReceiveDurationMetric(ctx)
		return result, nil
	case <-time.After(timeout):
		pid.recordLatestReceiveDurationMetric(ctx)
		err = ErrRequestTimeout
		pid.toDeadletterQueue(receiveContext, err)
		return nil, err
	}
}

// Tell sends an asynchronous message to another PID
func (pid *PID) Tell(ctx context.Context, to *PID, message proto.Message) error {
	if !to.IsRunning() {
		return ErrDead
	}

	to.doReceive(newReceiveContext(ctx, pid, to, message))
	pid.recordLatestReceiveDurationMetric(ctx)
	return nil
}

// SendAsync sends an asynchronous message to a given actor.
// The location of the given actor is transparent to the caller.
func (pid *PID) SendAsync(ctx context.Context, actorName string, message proto.Message) error {
	if !pid.IsRunning() {
		return ErrDead
	}

	addr, cid, err := pid.ActorSystem().ActorOf(ctx, actorName)
	if err != nil {
		return err
	}

	if cid != nil {
		return pid.Tell(ctx, cid, message)
	}

	return pid.RemoteTell(ctx, addr, message)
}

// SendSync sends a synchronous message to another actor and expect a response.
// The location of the given actor is transparent to the caller.
// This block until a response is received or timed out.
func (pid *PID) SendSync(ctx context.Context, actorName string, message proto.Message) (response proto.Message, err error) {
	if !pid.IsRunning() {
		return nil, ErrDead
	}

	addr, cid, err := pid.ActorSystem().ActorOf(ctx, actorName)
	if err != nil {
		return nil, err
	}

	if cid != nil {
		return pid.Ask(ctx, cid, message)
	}

	reply, err := pid.RemoteAsk(ctx, addr, message)
	if err != nil {
		return nil, err
	}
	return reply.UnmarshalNew()
}

// BatchTell sends an asynchronous bunch of messages to the given PID
// The messages will be processed one after the other in the order they are sent.
// This is a design choice to follow the simple principle of one message at a time processing by actors.
// When BatchTell encounter a single message it will fall back to a Tell call.
func (pid *PID) BatchTell(ctx context.Context, to *PID, messages ...proto.Message) error {
	for _, message := range messages {
		if err := pid.Tell(ctx, to, message); err != nil {
			return err
		}
	}
	return nil
}

// BatchAsk sends a synchronous bunch of messages to the given PID and expect responses in the same order as the messages.
// The messages will be processed one after the other in the order they are sent.
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func (pid *PID) BatchAsk(ctx context.Context, to *PID, messages ...proto.Message) (responses chan proto.Message, err error) {
	responses = make(chan proto.Message, len(messages))
	defer close(responses)

	for i := 0; i < len(messages); i++ {
		response, err := pid.Ask(ctx, to, messages[i])
		if err != nil {
			return nil, err
		}
		responses <- response
	}
	return
}

// RemoteLookup look for an actor address on a remote node.
func (pid *PID) RemoteLookup(ctx context.Context, host string, port int, name string) (addr *goaktpb.Address, err error) {
	remoteClient := pid.remotingClient(host, port)
	request := connect.NewRequest(&internalpb.RemoteLookupRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})

	response, err := remoteClient.RemoteLookup(ctx, request)
	if err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil, nil
		}
		return nil, err
	}

	return response.Msg.GetAddress(), nil
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (pid *PID) RemoteTell(ctx context.Context, to *address.Address, message proto.Message) error {
	marshaled, err := anypb.New(message)
	if err != nil {
		return err
	}

	remoteService := pid.remotingClient(to.GetHost(), int(to.GetPort()))

	sender := &goaktpb.Address{
		Host: pid.Address().Host(),
		Port: int32(pid.Address().Port()),
		Name: pid.Address().Name(),
		Id:   pid.Address().ID(),
	}

	request := &internalpb.RemoteTellRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   sender,
			Receiver: to.Address,
			Message:  marshaled,
		},
	}

	pid.logger.Debugf("sending a message to remote=(%s:%d)", to.GetHost(), to.GetPort())
	stream := remoteService.RemoteTell(ctx)
	if err := stream.Send(request); err != nil {
		if eof(err) {
			if _, err := stream.CloseAndReceive(); err != nil {
				return err
			}
			return nil
		}
		fmtErr := fmt.Errorf("failed to send message to remote=(%s:%d): %w", to.GetHost(), to.GetPort(), err)
		pid.logger.Error(fmtErr)
		return fmtErr
	}

	if _, err := stream.CloseAndReceive(); err != nil {
		fmtErr := fmt.Errorf("failed to send message to remote=(%s:%d): %w", to.GetHost(), to.GetPort(), err)
		pid.logger.Error(fmtErr)
		return fmtErr
	}

	pid.logger.Debugf("message successfully sent to remote=(%s:%d)", to.GetHost(), to.GetPort())
	return nil
}

// RemoteAsk sends a synchronous message to another actor remotely and expect a response.
func (pid *PID) RemoteAsk(ctx context.Context, to *address.Address, message proto.Message) (response *anypb.Any, err error) {
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, err
	}

	remoteService := pid.remotingClient(to.GetHost(), int(to.GetPort()))

	senderAddress := pid.Address()
	sender := &goaktpb.Address{
		Host: senderAddress.Host(),
		Port: int32(senderAddress.Port()),
		Name: senderAddress.Name(),
		Id:   senderAddress.ID(),
	}

	request := &internalpb.RemoteAskRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   sender,
			Receiver: to.Address,
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

// RemoteBatchTell sends a batch of messages to a remote actor in a way fire-and-forget manner
// Messages are processed one after the other in the order they are sent.
func (pid *PID) RemoteBatchTell(ctx context.Context, to *address.Address, messages ...proto.Message) error {
	if len(messages) == 1 {
		return pid.RemoteTell(ctx, to, messages[0])
	}

	sender := &goaktpb.Address{
		Host: pid.Address().Host(),
		Port: int32(pid.Address().Port()),
		Name: pid.Address().Name(),
		Id:   pid.Address().ID(),
	}

	var requests []*internalpb.RemoteTellRequest
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return ErrInvalidRemoteMessage(err)
		}

		requests = append(requests, &internalpb.RemoteTellRequest{
			RemoteMessage: &internalpb.RemoteMessage{
				Sender:   sender,
				Receiver: to.Address,
				Message:  packed,
			},
		})
	}

	remoteService := pid.remotingClient(to.GetHost(), int(to.GetPort()))

	stream := remoteService.RemoteTell(ctx)
	for _, request := range requests {
		if err := stream.Send(request); err != nil {
			if eof(err) {
				if _, err := stream.CloseAndReceive(); err != nil {
					return err
				}
				return nil
			}
			return err
		}
	}

	// close the connection
	if _, err := stream.CloseAndReceive(); err != nil {
		return err
	}

	return nil
}

// RemoteBatchAsk sends a synchronous bunch of messages to a remote actor and expect responses in the same order as the messages.
// Messages are processed one after the other in the order they are sent.
// This can hinder performance if it is not properly used.
func (pid *PID) RemoteBatchAsk(ctx context.Context, to *address.Address, messages ...proto.Message) (responses []*anypb.Any, err error) {
	sender := &goaktpb.Address{
		Host: pid.Address().Host(),
		Port: int32(pid.Address().Port()),
		Name: pid.Address().Name(),
		Id:   pid.Address().ID(),
	}

	var requests []*internalpb.RemoteAskRequest
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return nil, ErrInvalidRemoteMessage(err)
		}

		requests = append(requests, &internalpb.RemoteAskRequest{
			RemoteMessage: &internalpb.RemoteMessage{
				Sender:   sender,
				Receiver: to.Address,
				Message:  packed,
			},
		})
	}

	remoteService := pid.remotingClient(to.GetHost(), int(to.GetPort()))
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

			responses = append(responses, resp.GetMessage())
		}
	}()

	for _, request := range requests {
		err := stream.Send(request)
		if err != nil {
			return nil, err
		}
	}

	if err := stream.CloseRequest(); err != nil {
		return nil, err
	}

	err = <-errc
	if eof(err) {
		return responses, nil
	}

	if err != nil {
		return nil, err
	}

	return
}

// RemoteStop stops an actor on a remote node
func (pid *PID) RemoteStop(ctx context.Context, host string, port int, name string) error {
	remoteService := pid.remotingClient(host, port)
	request := connect.NewRequest(&internalpb.RemoteStopRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})
	if _, err := remoteService.RemoteStop(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil
		}
		return err
	}
	return nil
}

// RemoteSpawn creates an actor on a remote node. The given actor needs to be registered on the remote node using the Register method of ActorSystem
func (pid *PID) RemoteSpawn(ctx context.Context, host string, port int, name, actorType string) error {
	remoteService := pid.remotingClient(host, port)
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
func (pid *PID) RemoteReSpawn(ctx context.Context, host string, port int, name string) error {
	remoteService := pid.remotingClient(host, port)
	request := connect.NewRequest(&internalpb.RemoteReSpawnRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})
	if _, err := remoteService.RemoteReSpawn(ctx, request); err != nil {
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
func (pid *PID) Shutdown(ctx context.Context) error {
	pid.stopLocker.Lock()
	defer pid.stopLocker.Unlock()

	pid.logger.Info("Shutdown process has started...")

	if !pid.running.Load() {
		pid.logger.Infof("Actor=%s is offline. Maybe it has been passivated or stopped already", pid.Address().String())
		return nil
	}

	if pid.passivateAfter.Load() > 0 {
		pid.logger.Debug("sending a signal to stop passivation listener....")
		pid.haltPassivationLnr <- types.Unit{}
	}

	if err := pid.doStop(ctx); err != nil {
		pid.logger.Errorf("failed to cleanly stop actor=(%s)", pid.ID())
		return err
	}

	if pid.eventsStream != nil {
		pid.eventsStream.Publish(eventsTopic, &goaktpb.ActorStopped{
			Address:   pid.Address().Address,
			StoppedAt: timestamppb.Now(),
		})
	}

	pid.logger.Infof("Actor=%s successfully shutdown", pid.ID())
	return nil
}

// Watch a pid for errors, and send on the returned channel if an error occurred
func (pid *PID) Watch(cid *PID) {
	w := &watcher{
		WatcherID: pid,
		ErrChan:   make(chan error, 1),
		Done:      make(chan types.Unit, 1),
	}
	cid.watchers().Append(w)
	pid.watchees().set(cid)
	go pid.supervise(cid, w)
}

// UnWatch stops watching a given actor
func (pid *PID) UnWatch(cid *PID) {
	for _, item := range cid.watchers().Items() {
		w := item.Value
		if w.WatcherID.Equals(cid) {
			w.Done <- types.Unit{}
			pid.watchees().delete(cid.Address())
			cid.watchers().Delete(item.Index)
			break
		}
	}
}

// Logger returns the logger sets when creating the PID
func (pid *PID) Logger() log.Logger {
	pid.fieldsLocker.Lock()
	logger := pid.logger
	pid.fieldsLocker.Unlock()
	return logger
}

// watchers return the list of watchersList
func (pid *PID) watchers() *slice.Safe[*watcher] {
	return pid.watchersList
}

// watchees returns the list of actors watched by this actor
func (pid *PID) watchees() *pidMap {
	return pid.watchedList
}

// doReceive pushes a given message to the actor receiveContextBuffer
func (pid *PID) doReceive(receiveCtx *ReceiveContext) {
	pid.latestReceiveTime.Store(time.Now())
	pid.mailbox.Push(receiveCtx)
	pid.receiveSignal <- types.Unit{}
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor will not be started
func (pid *PID) init(ctx context.Context) error {
	pid.logger.Info("Initialization process has started...")

	cancelCtx, cancel := context.WithTimeout(ctx, pid.initTimeout.Load())
	defer cancel()

	// create a new retrier that will try a maximum of `initMaxRetries` times, with
	// an initial delay of 100 ms and a maximum delay of 1 second
	retrier := retry.NewRetrier(int(pid.initMaxRetries.Load()), 100*time.Millisecond, time.Second)
	if err := retrier.RunContext(cancelCtx, pid.actor.PreStart); err != nil {
		e := ErrInitFailure(err)
		return e
	}

	pid.fieldsLocker.Lock()
	pid.running.Store(true)
	pid.fieldsLocker.Unlock()

	pid.logger.Info("Initialization process successfully completed.")

	if pid.eventsStream != nil {
		pid.eventsStream.Publish(eventsTopic, &goaktpb.ActorStarted{
			Address:   pid.Address().Address,
			StartedAt: timestamppb.Now(),
		})
	}

	return nil
}

// reset re-initializes the actor PID
func (pid *PID) reset() {
	pid.latestReceiveTime.Store(time.Time{})
	pid.passivateAfter.Store(DefaultPassivationTimeout)
	pid.askTimeout.Store(DefaultAskTimeout)
	pid.shutdownTimeout.Store(DefaultShutdownTimeout)
	pid.initMaxRetries.Store(DefaultInitMaxRetries)
	pid.latestReceiveDuration.Store(0)
	pid.initTimeout.Store(DefaultInitTimeout)
	pid.children.reset()
	pid.watchersList.Reset()
	pid.telemetry = telemetry.New()
	pid.behaviorStack.Reset()
	if pid.metricEnabled.Load() {
		if err := pid.registerMetrics(); err != nil {
			fmtErr := fmt.Errorf("failed to register actor=%s metrics: %w", pid.ID(), err)
			pid.logger.Error(fmtErr)
		}
	}
	pid.processedCount.Store(0)
}

func (pid *PID) freeWatchers(ctx context.Context) error {
	pid.logger.Debug("freeing all watcher actors...")
	watchers := pid.watchers()
	if watchers.Len() > 0 {
		for _, item := range watchers.Items() {
			watcher := item.Value
			terminated := &goaktpb.Terminated{
				ActorId: pid.ID(),
			}
			if watcher.WatcherID.IsRunning() {
				pid.logger.Debugf("watcher=(%s) releasing watched=(%s)", watcher.WatcherID.ID(), pid.ID())
				if err := pid.Tell(ctx, watcher.WatcherID, terminated); err != nil {
					return err
				}
				watcher.WatcherID.UnWatch(pid)
				watcher.WatcherID.removeChild(pid)
				pid.logger.Debugf("watcher=(%s) released watched=(%s)", watcher.WatcherID.ID(), pid.ID())
			}
		}
	}
	return nil
}

func (pid *PID) freeWatchees(ctx context.Context) error {
	pid.logger.Debug("freeing all watched actors...")
	for _, watched := range pid.watchedList.pids() {
		pid.logger.Debugf("watcher=(%s) unwatching actor=(%s)", pid.ID(), watched.ID())
		pid.UnWatch(watched)
		if err := watched.Shutdown(ctx); err != nil {
			errwrap := fmt.Errorf("watcher=(%s) failed to unwatch actor=(%s): %w",
				pid.ID(), watched.ID(), err)
			return errwrap
		}
		pid.logger.Debugf("watcher=(%s) successfully unwatch actor=(%s)", pid.ID(), watched.ID())
	}
	return nil
}

func (pid *PID) freeChildren(ctx context.Context) error {
	pid.logger.Debug("freeing all child actors...")
	for _, child := range pid.Children() {
		pid.logger.Debugf("parent=(%s) disowning child=(%s)", pid.ID(), child.ID())
		pid.UnWatch(child)
		pid.children.delete(child.Address())
		if err := child.Shutdown(ctx); err != nil {
			errwrap := fmt.Errorf(
				"parent=(%s) failed to disown child=(%s): %w", pid.ID(), child.ID(),
				err)
			return errwrap
		}
		pid.logger.Debugf("parent=(%s) successfully disown child=(%s)", pid.ID(), child.ID())
	}
	return nil
}

// receive extracts every message from the actor mailbox
func (pid *PID) receive() {
	for {
		select {
		case <-pid.receiveStopSignal:
			return
		case <-pid.receiveSignal:
			if received := pid.mailbox.Pop(); received != nil {
				switch received.Message().(type) {
				case *goaktpb.PoisonPill:
					// stop the actor
					_ = pid.Shutdown(received.Context())
				default:
					pid.handleReceived(received)
				}
			}
		}
	}
}

// handleReceived picks the right behavior and processes the message
func (pid *PID) handleReceived(received *ReceiveContext) {
	defer pid.recovery(received)

	if behavior := pid.behaviorStack.Peek(); behavior != nil {
		pid.processedCount.Inc()
		behavior(received)
	}
}

// recovery is called upon after message is processed
func (pid *PID) recovery(received *ReceiveContext) {
	if r := recover(); r != nil {
		pid.watcherNotificationChan <- fmt.Errorf("%s", r)
		return
	}
	// no panic or recommended way to handle error
	pid.watcherNotificationChan <- received.getError()
}

// passivationLoop checks whether the actor is processing public or not.
// when the actor is idle, it automatically shuts down to free resources
func (pid *PID) passivationLoop() {
	pid.logger.Info("start the passivation listener...")
	pid.logger.Infof("passivation timeout is (%s)", pid.passivateAfter.Load().String())
	ticker := time.NewTicker(pid.passivateAfter.Load())
	tickerStopSig := make(chan types.Unit, 1)

	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				idleTime := time.Since(pid.latestReceiveTime.Load())
				if idleTime >= pid.passivateAfter.Load() {
					tickerStopSig <- types.Unit{}
					return
				}
			case <-pid.haltPassivationLnr:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()

	if !pid.IsRunning() {
		pid.logger.Infof("Actor=%s is offline. No need to passivate", pid.Address().String())
		return
	}

	pid.logger.Infof("Passivation mode has been triggered for actor=%s...", pid.Address().String())

	ctx := context.Background()
	if err := pid.doStop(ctx); err != nil {
		// TODO: rethink properly about PostStop error handling
		pid.logger.Errorf("failed to passivate actor=(%s): reason=(%v)", pid.Address().String(), err)
		return
	}

	if pid.eventsStream != nil {
		event := &goaktpb.ActorPassivated{
			Address:      pid.Address().Address,
			PassivatedAt: timestamppb.Now(),
		}
		pid.eventsStream.Publish(eventsTopic, event)
	}

	pid.logger.Infof("Actor=%s successfully passivated", pid.Address().String())
}

// setBehavior is a utility function that helps set the actor behavior
func (pid *PID) setBehavior(behavior Behavior) {
	pid.fieldsLocker.Lock()
	pid.behaviorStack.Reset()
	pid.behaviorStack.Push(behavior)
	pid.fieldsLocker.Unlock()
}

// resetBehavior is a utility function resets the actor behavior
func (pid *PID) resetBehavior() {
	pid.fieldsLocker.Lock()
	pid.behaviorStack.Push(pid.actor.Receive)
	pid.fieldsLocker.Unlock()
}

// setBehaviorStacked adds a behavior to the actor's behaviorStack
func (pid *PID) setBehaviorStacked(behavior Behavior) {
	pid.fieldsLocker.Lock()
	pid.behaviorStack.Push(behavior)
	pid.fieldsLocker.Unlock()
}

// unsetBehaviorStacked sets the actor's behavior to the previous behavior
// prior to setBehaviorStacked is called
func (pid *PID) unsetBehaviorStacked() {
	pid.fieldsLocker.Lock()
	pid.behaviorStack.Pop()
	pid.fieldsLocker.Unlock()
}

// doStop stops the actor
func (pid *PID) doStop(ctx context.Context) error {
	pid.running.Store(false)

	// TODO: just signal stash processing done and ignore the messages or process them
	if pid.stashBuffer != nil {
		if err := pid.unstashAll(); err != nil {
			pid.logger.Errorf("actor=(%s) failed to unstash messages", pid.Address().String())
			return err
		}
	}

	// wait for all messages in the mailbox to be processed
	// init a ticker that run every 10 ms to make sure we process all messages in the
	// mailbox.
	ticker := time.NewTicker(10 * time.Millisecond)
	timer := time.After(pid.shutdownTimeout.Load())
	tickerStopSig := make(chan types.Unit)
	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				if pid.mailbox.IsEmpty() {
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
	pid.httpClient.CloseIdleConnections()
	pid.watchersNotificationStopSignal <- types.Unit{}
	pid.receiveStopSignal <- types.Unit{}

	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(pid.freeWatchees(ctx)).
		AddError(pid.freeChildren(ctx)).
		AddError(pid.freeWatchers(ctx)).
		Error(); err != nil {
		pid.reset()
		return err
	}

	pid.logger.Infof("Shutdown process is on going for actor=%s...", pid.Address().String())
	pid.reset()
	return pid.actor.PostStop(ctx)
}

// removeChild helps remove child actor
func (pid *PID) removeChild(cid *PID) {
	if !pid.IsRunning() {
		return
	}

	if cid == nil || cid == NoSender {
		return
	}

	path := cid.Address()
	if c, ok := pid.children.get(path); ok {
		if c.IsRunning() {
			return
		}
		pid.children.delete(path)
	}
}

// notifyWatchers send error to watchers
func (pid *PID) notifyWatchers() {
	for {
		select {
		case err := <-pid.watcherNotificationChan:
			if err != nil {
				for _, item := range pid.watchers().Items() {
					item.Value.ErrChan <- err
				}
			}
		case <-pid.watchersNotificationStopSignal:
			return
		}
	}
}

// toDeadletterQueue sends message to deadletter queue
func (pid *PID) toDeadletterQueue(receiveCtx *ReceiveContext, err error) {
	// the message is lost
	if pid.eventsStream == nil {
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
		senderAddr = receiveCtx.Sender().Address().Address
	}

	pid.eventsStream.Publish(eventsTopic, &goaktpb.Deadletter{
		Sender:   senderAddr,
		Receiver: pid.Address().Address,
		Message:  msg,
		SendTime: timestamppb.Now(),
		Reason:   err.Error(),
	})
}

// registerMetrics register the PID metrics with OTel instrumentation.
func (pid *PID) registerMetrics() error {
	meter := pid.telemetry.Meter()
	metrics := pid.metrics
	_, err := meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		observer.ObserveInt64(metrics.ChildrenCount(), pid.childrenCount.Load())
		observer.ObserveInt64(metrics.StashCount(), int64(pid.StashSize()))
		observer.ObserveInt64(metrics.RestartCount(), pid.restartCount.Load())
		observer.ObserveInt64(metrics.ProcessedCount(), pid.processedCount.Load())
		return nil
	}, metrics.ChildrenCount(),
		metrics.StashCount(),
		metrics.ProcessedCount(),
		metrics.RestartCount())

	return err
}

// clientOptions returns the gRPC client connections options
func (pid *PID) clientOptions() []connect.ClientOption {
	var interceptor *otelconnect.Interceptor
	if pid.metricEnabled.Load() {
		// no need to handle the error because a NoOp trace and meter provider will be
		// returned by the telemetry engine when none is provided
		interceptor, _ = otelconnect.NewInterceptor(
			otelconnect.WithTracerProvider(pid.telemetry.TraceProvider()),
			otelconnect.WithMeterProvider(pid.telemetry.MeterProvider()))
	}

	var clientOptions []connect.ClientOption
	if interceptor != nil {
		clientOptions = append(clientOptions, connect.WithInterceptors(interceptor))
	}
	return clientOptions
}

// remotingClient returns an instance of the Remote Service client
func (pid *PID) remotingClient(host string, port int) internalpbconnect.RemotingServiceClient {
	return internalpbconnect.NewRemotingServiceClient(
		pid.httpClient,
		http.URL(host, port),
		pid.clientOptions()...,
	)
}

// handleCompletion processes a long-running task and pipe the result to
// the completion receiver
func (pid *PID) handleCompletion(ctx context.Context, completion *taskCompletion) {
	// defensive programming
	if completion == nil ||
		completion.Receiver == nil ||
		completion.Receiver == NoSender ||
		completion.Task == nil {
		pid.logger.Error(ErrUndefinedTask)
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
		pid.logger.Error(err)
		return
	}

	// make sure that the receiver is still alive
	to := completion.Receiver
	if !to.IsRunning() {
		pid.logger.Errorf("unable to pipe message to actor=(%s): not running", to.Address().String())
		return
	}

	messageContext := newReceiveContext(ctx, pid, to, result.Success())
	to.doReceive(messageContext)
}

// supervise watches for child actor's failure and act based upon the supervisory strategy
func (pid *PID) supervise(cid *PID, watcher *watcher) {
	for {
		select {
		case <-watcher.Done:
			pid.logger.Debugf("stop watching cid=(%s)", cid.ID())
			return
		case err := <-watcher.ErrChan:
			pid.logger.Errorf("child actor=(%s) is failing: Err=%v", cid.ID(), err)
			switch directive := pid.supervisorDirective.(type) {
			case *StopDirective:
				pid.handleStopDirective(cid)
			case *RestartDirective:
				pid.handleRestartDirective(cid, directive.MaxNumRetries(), directive.Timeout())
			case *ResumeDirective:
				// pass
			default:
				pid.handleStopDirective(cid)
			}
		}
	}
}

// handleStopDirective handles the testSupervisor stop directive
func (pid *PID) handleStopDirective(cid *PID) {
	pid.UnWatch(cid)
	pid.children.delete(cid.Address())
	if err := cid.Shutdown(context.Background()); err != nil {
		// this can enter into some infinite loop if we panic
		// since we are just shutting down the actor we can just log the error
		// TODO: rethink properly about PostStop error handling
		pid.logger.Error(err)
	}
}

// handleRestartDirective handles the testSupervisor restart directive
func (pid *PID) handleRestartDirective(cid *PID, maxRetries uint32, timeout time.Duration) {
	pid.UnWatch(cid)
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
		pid.logger.Error(err)
		// remove the actor in case it is a child and stop it
		pid.children.delete(cid.Address())
		if err := cid.Shutdown(ctx); err != nil {
			pid.logger.Error(err)
		}
		return
	}
	pid.Watch(cid)
}

func (pid *PID) recordLatestReceiveDurationMetric(ctx context.Context) {
	// record processing time
	pid.latestReceiveDuration.Store(time.Since(pid.latestReceiveTime.Load()))
	if pid.metricEnabled.Load() {
		pid.metrics.LastReceivedDuration().Record(ctx, pid.latestReceiveDuration.Load().Milliseconds())
	}
}
