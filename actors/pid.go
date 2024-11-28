/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/flowchartsman/retry"
	"go.uber.org/atomic"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/future"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/errorschain"
	"github.com/tochemey/goakt/v2/internal/eventstream"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/slice"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
)

// specifies the state in which the PID is
// regarding message processing
type processingState int32

const (
	// idle means there are no messages to process
	idle processingState = iota
	// processing means the PID is processing messages
	processing
)

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

	// helps determine whether the actor should handle messages or not.
	started  atomic.Bool
	stopping atomic.Bool

	latestReceiveTime     atomic.Time
	latestReceiveDuration atomic.Duration

	// specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 120 seconds
	passivateAfter atomic.Duration

	// specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	initMaxRetries atomic.Int32

	// specifies the init timeout.
	// the default initialization timeout is 1s
	initTimeout atomic.Duration

	// specifies the actor mailbox
	mailbox Mailbox

	haltPassivationLnr chan types.Unit

	// hold the list of actors watching the given actor
	watchersList *slice.LockFree[*PID]

	// hold the list of actors watched by this PID
	watcheesMap *pidMap

	// hold the list of the childrenMap
	childrenMap *pidMap

	// the actor system
	system ActorSystem

	// specifies the logger to use
	logger log.Logger

	// various lockers to protect the PID fields
	// in a concurrent environment
	fieldsLocker         *sync.RWMutex
	stopLocker           *sync.Mutex
	processingTimeLocker *sync.Mutex

	// specifies the actor behavior stack
	behaviorStack *behaviorStack

	// stash settings
	stashBox    *UnboundedMailbox
	stashLocker *sync.Mutex

	// define an events stream
	eventsStream *eventstream.EventsStream

	// set the metrics settings
	restartCount   *atomic.Int64
	childrenCount  *atomic.Int64
	processedCount *atomic.Int64

	// supervisor strategy
	supervisorDirective   SupervisorDirective
	supervisionChan       chan error
	supervisionStopSignal chan types.Unit

	receiveSignal     chan types.Unit
	receiveStopSignal chan types.Unit

	// atomic flag indicating whether the actor is processing messages
	processingMessages atomic.Int32

	remoting *Remoting

	// specifies the actor parent
	// this field is set when this PID is a child actor
	parent *PID
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

	pid := &PID{
		actor:                 actor,
		latestReceiveTime:     atomic.Time{},
		haltPassivationLnr:    make(chan types.Unit, 1),
		logger:                log.New(log.ErrorLevel, os.Stderr),
		childrenMap:           newMap(),
		supervisorDirective:   DefaultSupervisoryStrategy,
		watchersList:          slice.NewLockFree[*PID](),
		watcheesMap:           newMap(),
		address:               address,
		fieldsLocker:          new(sync.RWMutex),
		stopLocker:            new(sync.Mutex),
		mailbox:               NewUnboundedMailbox(),
		stashBox:              nil,
		stashLocker:           &sync.Mutex{},
		eventsStream:          nil,
		restartCount:          atomic.NewInt64(0),
		childrenCount:         atomic.NewInt64(0),
		processedCount:        atomic.NewInt64(0),
		processingTimeLocker:  new(sync.Mutex),
		supervisionChan:       make(chan error, 1),
		supervisionStopSignal: make(chan types.Unit, 1),
		receiveSignal:         make(chan types.Unit, 1),
		receiveStopSignal:     make(chan types.Unit, 1),
		remoting:              NewRemoting(),
	}

	pid.initMaxRetries.Store(DefaultInitMaxRetries)
	pid.latestReceiveDuration.Store(0)
	pid.started.Store(false)
	pid.stopping.Store(false)
	pid.passivateAfter.Store(DefaultPassivationTimeout)
	pid.initTimeout.Store(DefaultInitTimeout)

	for _, opt := range opts {
		opt(pid)
	}

	behaviorStack := newBehaviorStack()
	behaviorStack.Push(pid.actor.Receive)
	pid.behaviorStack = behaviorStack

	if err := pid.init(ctx); err != nil {
		return nil, err
	}

	pid.receiveLoop()
	pid.supervisionLoop()
	if pid.passivateAfter.Load() > 0 {
		go pid.passivationLoop()
	}

	receiveContext := contextFromPool()
	receiveContext.build(ctx, NoSender, pid, new(goaktpb.PostStart), true)
	pid.doReceive(receiveContext)

	return pid, nil
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
	if pid == nil && to == nil {
		return true
	}

	if pid == nil || to == nil {
		return false
	}

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

	childAddress := pid.childAddress(name)
	if cid, ok := pid.childrenMap.Get(childAddress); ok {
		if cid.IsRunning() {
			return cid, nil
		}
	}
	return nil, ErrActorNotFound(childAddress.String())
}

// Parent returns the parent of this PID
func (pid *PID) Parent() *PID {
	return pid.parent
}

// Children returns the list of all the direct childrenMap of the given actor
// Only alive actors are included in the list or an empty list is returned
func (pid *PID) Children() []*PID {
	pid.fieldsLocker.RLock()
	children := pid.childrenMap.List()
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

	if !cid.IsRunning() {
		return ErrActorNotFound(cid.Address().String())
	}

	pid.fieldsLocker.RLock()
	children := pid.childrenMap
	pid.fieldsLocker.RUnlock()

	if cid, ok := children.Get(cid.Address()); ok {
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
	return pid.started.Load()
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

	pid.receiveLoop()
	pid.supervisionLoop()
	if pid.passivateAfter.Load() > 0 {
		go pid.passivationLoop()
	}

	pid.restartCount.Inc()

	if pid.eventsStream != nil {
		pid.eventsStream.Publish(
			eventsTopic, &goaktpb.ActorRestarted{
				Address:     pid.Address().Address,
				RestartedAt: timestamppb.Now(),
			},
		)
	}

	return nil
}

// RestartCount returns the total number of re-starts by the given PID
func (pid *PID) RestartCount() int {
	count := pid.restartCount.Load()
	return int(count)
}

// ChildrenCount returns the total number of childrenMap for the given PID
func (pid *PID) ChildrenCount() int {
	count := pid.childrenCount.Load()
	return int(count)
}

// ProcessedCount returns the total number of messages processed at a given time
func (pid *PID) ProcessedCount() int {
	count := pid.processedCount.Load()
	return int(count)
}

// LatestProcessedDuration returns the duration of the latest message processed
func (pid *PID) LatestProcessedDuration() time.Duration {
	pid.latestReceiveDuration.Store(time.Since(pid.latestReceiveTime.Load()))
	return pid.latestReceiveDuration.Load()
}

// SpawnChild creates a child actor and start watching it for error
// When the given child actor already exists its PID will only be returned
func (pid *PID) SpawnChild(ctx context.Context, name string, actor Actor, opts ...SpawnOption) (*PID, error) {
	if !pid.IsRunning() {
		return nil, ErrDead
	}

	childAddress := pid.childAddress(name)
	pid.fieldsLocker.RLock()
	children := pid.childrenMap
	pid.fieldsLocker.RUnlock()

	if cid, ok := children.Get(childAddress); ok {
		if cid.IsRunning() {
			return cid, nil
		}
	}

	pid.fieldsLocker.Lock()

	// create the child actor options child inherit parent's options
	pidOptions := []pidOption{
		withInitMaxRetries(int(pid.initMaxRetries.Load())),
		withCustomLogger(pid.logger),
		withActorSystem(pid.system),
		withEventsStream(pid.eventsStream),
		withInitTimeout(pid.initTimeout.Load()),
		withRemoting(pid.remoting),
		withParent(pid),
	}

	spawnConfig := newSpawnConfig(opts...)
	if spawnConfig.mailbox != nil {
		pidOptions = append(pidOptions, withMailbox(spawnConfig.mailbox))
	}

	// set the supervisor directive defines in the spawn options
	// otherwise fallback to the parent supervisor stragetgy directive
	if spawnConfig.supervisorDirective != nil {
		pidOptions = append(pidOptions, withSupervisorDirective(spawnConfig.supervisorDirective))
	} else {
		pidOptions = append(pidOptions, withSupervisorDirective(pid.supervisorDirective))
	}

	// disable passivation for system actor
	if isReservedName(name) {
		pidOptions = append(pidOptions, withPassivationDisabled())
	} else {
		pidOptions = append(pidOptions, withPassivationAfter(pid.passivateAfter.Load()))
	}

	// create the child PID
	cid, err := newPID(
		ctx,
		childAddress,
		actor,
		pidOptions...,
	)

	if err != nil {
		pid.fieldsLocker.Unlock()
		return nil, err
	}

	pid.childrenCount.Inc()
	pid.childrenMap.Set(cid)
	eventsStream := pid.eventsStream

	pid.fieldsLocker.Unlock()
	pid.Watch(cid)

	if eventsStream != nil {
		eventsStream.Publish(
			eventsTopic, &goaktpb.ActorChildCreated{
				Address:   cid.Address().Address,
				CreatedAt: timestamppb.Now(),
				Parent:    pid.Address().Address,
			},
		)
	}

	// set the actor in the given actor system registry
	if pid.ActorSystem() != nil {
		pid.ActorSystem().setActor(cid)
	}

	return cid, nil
}

// StashSize returns the stash buffer size
func (pid *PID) StashSize() uint64 {
	if pid.stashBox == nil {
		return 0
	}
	return uint64(pid.stashBox.Len())
}

// PipeTo processes a long-started task and pipes the result to the provided actor.
// The successful result of the task will be put onto the provided actor mailbox.
// This is useful when interacting with external services.
// It’s common that you would like to use the value of the response in the actor when the long-started task is completed
func (pid *PID) PipeTo(ctx context.Context, to *PID, task future.Task) error {
	if task == nil {
		return ErrUndefinedTask
	}

	if !to.IsRunning() {
		return ErrDead
	}

	go pid.handleCompletion(
		ctx, &taskCompletion{
			Receiver: to,
			Task:     task,
		},
	)

	return nil
}

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (pid *PID) Ask(ctx context.Context, to *PID, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	if !to.IsRunning() {
		return nil, ErrDead
	}

	if timeout <= 0 {
		return nil, ErrInvalidTimeout
	}

	receiveContext := contextFromPool()
	receiveContext.build(ctx, pid, to, message, false)
	to.doReceive(receiveContext)

	select {
	case result := <-receiveContext.response:
		return result, nil
	case <-time.After(timeout):
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

	receiveContext := contextFromPool()
	receiveContext.build(ctx, pid, to, message, true)

	to.doReceive(receiveContext)
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
func (pid *PID) SendSync(ctx context.Context, actorName string, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	if !pid.IsRunning() {
		return nil, ErrDead
	}

	addr, cid, err := pid.ActorSystem().ActorOf(ctx, actorName)
	if err != nil {
		return nil, err
	}

	if cid != nil {
		return pid.Ask(ctx, cid, message, timeout)
	}

	reply, err := pid.RemoteAsk(ctx, addr, message, timeout)
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
func (pid *PID) BatchAsk(ctx context.Context, to *PID, messages []proto.Message, timeout time.Duration) (responses chan proto.Message, err error) {
	responses = make(chan proto.Message, len(messages))
	defer close(responses)

	for i := 0; i < len(messages); i++ {
		response, err := pid.Ask(ctx, to, messages[i], timeout)
		if err != nil {
			return nil, err
		}
		responses <- response
	}
	return
}

// RemoteLookup look for an actor address on a remote node.
func (pid *PID) RemoteLookup(ctx context.Context, host string, port int, name string) (addr *goaktpb.Address, err error) {
	if pid.remoting == nil {
		return nil, ErrRemotingDisabled
	}

	remoteClient := pid.remoting.Client(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteLookupRequest{
			Host: host,
			Port: int32(port),
			Name: name,
		},
	)

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
	if pid.remoting == nil {
		return ErrRemotingDisabled
	}

	marshaled, err := anypb.New(message)
	if err != nil {
		return err
	}

	remoteService := pid.remoting.Client(to.GetHost(), int(to.GetPort()))

	request := &internalpb.RemoteTellRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   pid.Address().Address,
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
func (pid *PID) RemoteAsk(ctx context.Context, to *address.Address, message proto.Message, timeout time.Duration) (response *anypb.Any, err error) {
	if pid.remoting == nil {
		return nil, ErrRemotingDisabled
	}

	if timeout <= 0 {
		return nil, ErrInvalidTimeout
	}

	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, err
	}

	remoteService := pid.remoting.Client(to.GetHost(), int(to.GetPort()))

	senderAddress := pid.Address()

	request := &internalpb.RemoteAskRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   senderAddress.Address,
			Receiver: to.Address,
			Message:  marshaled,
		},
		Timeout: durationpb.New(timeout),
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
func (pid *PID) RemoteBatchTell(ctx context.Context, to *address.Address, messages []proto.Message) error {
	if pid.remoting == nil {
		return ErrRemotingDisabled
	}

	if len(messages) == 1 {
		return pid.RemoteTell(ctx, to, messages[0])
	}

	var requests []*internalpb.RemoteTellRequest
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return ErrInvalidRemoteMessage(err)
		}

		requests = append(
			requests, &internalpb.RemoteTellRequest{
				RemoteMessage: &internalpb.RemoteMessage{
					Sender:   pid.Address().Address,
					Receiver: to.Address,
					Message:  packed,
				},
			},
		)
	}

	remoteService := pid.remoting.Client(to.GetHost(), int(to.GetPort()))

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
func (pid *PID) RemoteBatchAsk(ctx context.Context, to *address.Address, messages []proto.Message, timeout time.Duration) (responses []*anypb.Any, err error) {
	if pid.remoting == nil {
		return nil, ErrRemotingDisabled
	}

	var requests []*internalpb.RemoteAskRequest
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return nil, ErrInvalidRemoteMessage(err)
		}

		requests = append(
			requests, &internalpb.RemoteAskRequest{
				RemoteMessage: &internalpb.RemoteMessage{
					Sender:   pid.Address().Address,
					Receiver: to.Address,
					Message:  packed,
				},
				Timeout: durationpb.New(timeout),
			},
		)
	}

	remoteService := pid.remoting.Client(to.GetHost(), int(to.GetPort()))
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
	if pid.remoting == nil {
		return ErrRemotingDisabled
	}

	remoteService := pid.remoting.Client(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteStopRequest{
			Host: host,
			Port: int32(port),
			Name: name,
		},
	)

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
	if pid.remoting == nil {
		return ErrRemotingDisabled
	}

	remoteService := pid.remoting.Client(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteSpawnRequest{
			Host:      host,
			Port:      int32(port),
			ActorName: name,
			ActorType: actorType,
		},
	)

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
	if pid.remoting == nil {
		return ErrRemotingDisabled
	}

	remoteService := pid.remoting.Client(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteReSpawnRequest{
			Host: host,
			Port: int32(port),
			Name: name,
		},
	)

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
	pid.logger.Info("Shutdown process has started...")

	if !pid.started.Load() {
		pid.logger.Infof("Actor=%s is offline. Maybe it has been passivated or stopped already", pid.Address().String())
		pid.stopLocker.Unlock()
		return nil
	}

	if pid.passivateAfter.Load() > 0 {
		pid.stopping.Store(true)
		pid.logger.Debug("sending a signal to stop passivation listener....")
		pid.haltPassivationLnr <- types.Unit{}
	}

	if err := pid.doStop(ctx); err != nil {
		pid.logger.Errorf("failed to cleanly stop actor=(%s)", pid.ID())
		pid.stopLocker.Unlock()
		return err
	}

	if pid.eventsStream != nil {
		pid.eventsStream.Publish(
			eventsTopic, &goaktpb.ActorStopped{
				Address:   pid.Address().Address,
				StoppedAt: timestamppb.Now(),
			},
		)
	}

	pid.stopLocker.Unlock()
	pid.logger.Infof("Actor=%s successfully shutdown", pid.ID())
	return nil
}

// Watch watches a given actor for a Terminated message when the watched actor shutdown
func (pid *PID) Watch(cid *PID) {
	cid.watchers().Append(pid)
	pid.watchees().Set(cid)
}

// UnWatch stops watching a given actor
func (pid *PID) UnWatch(cid *PID) {
	for index, watcher := range cid.watchers().Items() {
		if watcher.Equals(pid) {
			pid.watchees().Remove(cid.Address())
			cid.watchers().Delete(index)
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
func (pid *PID) watchers() *slice.LockFree[*PID] {
	return pid.watchersList
}

// watchees returns the list of actors watched by this actor
func (pid *PID) watchees() *pidMap {
	return pid.watcheesMap
}

// doReceive pushes a given message to the actor mailbox
// and signals the receiveLoop to process it
func (pid *PID) doReceive(receiveCtx *ReceiveContext) {
	if err := pid.mailbox.Enqueue(receiveCtx); err != nil {
		// add a warning log because the mailbox is full and do nothing
		pid.logger.Warn(err)
		// push the message as a deadletter
		pid.toDeadletterQueue(receiveCtx, err)
	}
	pid.signalMessage()
}

// signal that a message has arrived and wake up the actor if needed
func (pid *PID) signalMessage() {
	// only signal if the actor is not already processing messages
	if pid.processingMessages.CompareAndSwap(int32(idle), int32(processing)) {
		select {
		case pid.receiveSignal <- types.Unit{}:
		default:
		}
	}
}

// receiveLoop extracts every message from the actor mailbox
// and pass it to the appropriate behavior for handling
func (pid *PID) receiveLoop() {
	go func() {
		for {
			select {
			case <-pid.receiveStopSignal:
				return
			case <-pid.receiveSignal:
				var received *ReceiveContext
				if received != nil {
					returnToPool(received)
					received = nil
				}

				// Process all messages in the queue one by one
				for {
					received = pid.mailbox.Dequeue()
					if received == nil {
						// If no more messages, stop processing
						pid.processingMessages.Store(int32(idle))
						// Check if new messages were added in the meantime and restart processing
						if !pid.mailbox.IsEmpty() && pid.processingMessages.CompareAndSwap(int32(idle), int32(processing)) {
							continue
						}
						break
					}
					// Process the message
					switch msg := received.Message().(type) {
					case *goaktpb.PoisonPill:
						_ = pid.Shutdown(received.Context())
					case *internalpb.HandleFault:
						pid.handleFaultyChild(msg)
					default:
						pid.handleReceived(received)
					}
				}
			}
		}
	}()
}

// handleReceived picks the right behavior and processes the message
func (pid *PID) handleReceived(received *ReceiveContext) {
	defer pid.recovery(received)
	if behavior := pid.behaviorStack.Peek(); behavior != nil {
		pid.latestReceiveTime.Store(time.Now())
		pid.processedCount.Inc()
		behavior(received)
	}
}

// recovery is called upon after message is processed
func (pid *PID) recovery(received *ReceiveContext) {
	if r := recover(); r != nil {
		pid.supervisionChan <- fmt.Errorf("%s", r)
		return
	}
	// no panic or recommended way to handle error
	pid.supervisionChan <- received.getError()
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor will not be started
func (pid *PID) init(ctx context.Context) error {
	pid.logger.Info("Initialization process has started...")

	cancelCtx, cancel := context.WithTimeout(ctx, pid.initTimeout.Load())
	// create a new retrier that will try a maximum of `initMaxRetries` times, with
	// an initial delay of 100 ms and a maximum delay of 1 second
	retrier := retry.NewRetrier(int(pid.initMaxRetries.Load()), 100*time.Millisecond, time.Second)
	if err := retrier.RunContext(cancelCtx, pid.actor.PreStart); err != nil {
		e := ErrInitFailure(err)
		cancel()
		return e
	}

	pid.started.Store(true)
	pid.logger.Info("Initialization process successfully completed.")

	if pid.eventsStream != nil {
		pid.eventsStream.Publish(
			eventsTopic, &goaktpb.ActorStarted{
				Address:   pid.Address().Address,
				StartedAt: timestamppb.Now(),
			},
		)
	}

	cancel()
	return nil
}

// reset re-initializes the actor PID
func (pid *PID) reset() {
	pid.latestReceiveTime.Store(time.Time{})
	pid.passivateAfter.Store(DefaultPassivationTimeout)
	pid.initMaxRetries.Store(DefaultInitMaxRetries)
	pid.latestReceiveDuration.Store(0)
	pid.initTimeout.Store(DefaultInitTimeout)
	pid.childrenMap.Reset()
	pid.watchersList.Reset()
	pid.watchees().Reset()
	pid.behaviorStack.Reset()
	pid.processedCount.Store(0)
	pid.stopping.Store(false)
}

// freeWatchers releases all the actors watching this actor
func (pid *PID) freeWatchers(ctx context.Context) error {
	logger := pid.logger
	logger.Debugf("%s freeing all watcher actors...", pid.ID())
	watchers := pid.watchers()
	if watchers.Len() > 0 {
		eg, ctx := errgroup.WithContext(ctx)
		for _, watcher := range watchers.Items() {
			watcher := watcher
			eg.Go(func() error {
				terminated := &goaktpb.Terminated{
					ActorId: pid.ID(),
				}

				if watcher.IsRunning() {
					logger.Debugf("watcher=(%s) releasing watched=(%s)", watcher.ID(), pid.ID())
					if err := pid.Tell(ctx, watcher, terminated); err != nil {
						return err
					}

					watcher.UnWatch(pid)
					logger.Debugf("watcher=(%s) released watched=(%s)", watcher.ID(), pid.ID())
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			logger.Errorf("watcher=(%s) failed to free all watcher error: %v", pid.ID(), err)
			return err
		}
		logger.Debugf("%s successfully frees all watcher actors...", pid.ID())
		return nil
	}
	logger.Debugf("%s does not have any watcher actors", pid.ID())
	return nil
}

// freeWatchees releases all actors that have been watched by this actor
func (pid *PID) freeWatchees(ctx context.Context) error {
	logger := pid.logger
	logger.Debugf("%s freeing all watched actors...", pid.ID())
	size := pid.watcheesMap.Size()
	if size > 0 {
		eg, ctx := errgroup.WithContext(ctx)
		for _, watched := range pid.watcheesMap.List() {
			watched := watched
			eg.Go(func() error {
				logger.Debugf("watcher=(%s) unwatching actor=(%s)", pid.ID(), watched.ID())
				pid.UnWatch(watched)
				if err := watched.Shutdown(ctx); err != nil {
					errwrap := fmt.Errorf(
						"watcher=(%s) failed to unwatch actor=(%s): %w",
						pid.ID(), watched.ID(), err,
					)
					return errwrap
				}
				logger.Debugf("watcher=(%s) successfully unwatch actor=(%s)", pid.ID(), watched.ID())
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			logger.Errorf("watcher=(%s) failed to unwatch actors: %v", pid.ID(), err)
			return err
		}
		logger.Debugf("%s successfully unwatch all watched actors...", pid.ID())
		return nil
	}
	logger.Debugf("%s does not have any watched actors", pid.ID())
	return nil
}

// freeChildren releases all child actors
func (pid *PID) freeChildren(ctx context.Context) error {
	logger := pid.logger
	logger.Debugf("%s freeing all child actors...", pid.ID())
	size := pid.childrenMap.Size()
	if size > 0 {
		eg, ctx := errgroup.WithContext(ctx)
		for _, child := range pid.Children() {
			child := child
			eg.Go(func() error {
				logger.Debugf("parent=(%s) disowning child=(%s)", pid.ID(), child.ID())
				pid.UnWatch(child)
				pid.childrenMap.Remove(child.Address())
				if err := child.Shutdown(ctx); err != nil {
					errwrap := fmt.Errorf(
						"parent=(%s) failed to disown child=(%s): %w", pid.ID(), child.ID(),
						err,
					)
					return errwrap
				}
				logger.Debugf("parent=(%s) successfully disown child=(%s)", pid.ID(), child.ID())
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			logger.Errorf("parent=(%s) failed to free all child actors: %v", pid.ID(), err)
			return err
		}
		logger.Debugf("%s successfully free all child actors...", pid.ID())
		return nil
	}
	pid.logger.Debugf("%s does not have any children", pid.ID())
	return nil
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

	if pid.stopping.Load() {
		pid.logger.Infof("Actor=%s is stopping. No need to passivate", pid.Address().String())
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

// unsetBehaviorStacked sets the actor's behavior to the next behavior
// prior to setBehaviorStacked is called
func (pid *PID) unsetBehaviorStacked() {
	pid.fieldsLocker.Lock()
	pid.behaviorStack.Pop()
	pid.fieldsLocker.Unlock()
}

// doStop stops the actor
func (pid *PID) doStop(ctx context.Context) error {
	// TODO: just signal stash processing done and ignore the messages or process them
	if pid.stashBox != nil {
		if err := pid.unstashAll(); err != nil {
			pid.logger.Errorf("actor=(%s) failed to unstash messages", pid.Address().String())
			pid.started.Store(false)
			return err
		}
	}

	// wait for all messages in the mailbox to be processed
	// init a ticker that run every 10 ms to make sure we process all messages in the
	// mailbox within a second
	// TODO: revisit this timeout or discard all remaining messages in the mailbox
	ticker := time.NewTicker(10 * time.Millisecond)
	tickerStopSig := make(chan types.Unit)
	go func() {
		for {
			select {
			case <-ticker.C:
				if pid.mailbox.IsEmpty() {
					close(tickerStopSig)
					return
				}
			case <-time.After(2 * time.Second):
				close(tickerStopSig)
				return
			}
		}
	}()

	<-tickerStopSig
	if pid.remoting != nil {
		pid.remoting.Close()
	}

	pid.supervisionStopSignal <- types.Unit{}
	pid.receiveStopSignal <- types.Unit{}

	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(pid.freeWatchees(ctx)).
		AddError(pid.freeChildren(ctx)).
		AddError(pid.freeWatchers(ctx)).
		Error(); err != nil {
		pid.started.Store(false)
		pid.reset()
		return err
	}

	pid.started.Store(false)
	pid.logger.Infof("post shutdown process is on going for actor=%s...", pid.Address().String())
	pid.reset()
	return pid.actor.PostStop(ctx)
}

// setParent set the actor parent.
func (pid *PID) setParent(parent *PID) {
	pid.fieldsLocker.Lock()
	pid.parent = parent
	pid.fieldsLocker.Unlock()
}

// supervisionLoop send error to watchers
func (pid *PID) supervisionLoop() {
	go func() {
		for {
			select {
			case err := <-pid.supervisionChan:
				pid.notifyParent(err)
			case <-pid.supervisionStopSignal:
				return
			}
		}
	}()
}

// notifyParent sends a notification to the parent actor
func (pid *PID) notifyParent(err error) {
	if err == nil || errors.Is(err, ErrDead) {
		return
	}
	// send a message to the parent
	if pid.parent != nil {
		_ = pid.Tell(context.Background(), pid.parent, &internalpb.HandleFault{
			ActorId: pid.ID(),
			Message: err.Error(),
		})
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

	pid.eventsStream.Publish(
		eventsTopic, &goaktpb.Deadletter{
			Sender:   senderAddr,
			Receiver: pid.Address().Address,
			Message:  msg,
			SendTime: timestamppb.Now(),
			Reason:   err.Error(),
		},
	)
}

// handleCompletion processes a long-started task and pipe the result to
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
		pid.logger.Errorf("unable to pipe message to actor=(%s): not started", to.Address().String())
		return
	}

	messageContext := newReceiveContext(ctx, pid, to, result.Success())
	to.doReceive(messageContext)
}

// handleFaultyChild watches for child actor's failure and act based upon the supervisory strategy
func (pid *PID) handleFaultyChild(msg *internalpb.HandleFault) {
	for _, cid := range pid.childrenMap.List() {
		if cid.ID() == msg.GetActorId() {
			message := msg.GetMessage()
			pid.logger.Errorf("child actor=(%s) is failing: Err=%s", cid.ID(), message)
			switch directive := cid.supervisorDirective.(type) {
			case *StopDirective:
				pid.handleStopDirective(cid)
			case *RestartDirective:
				pid.handleRestartDirective(cid, directive.MaxNumRetries(), directive.Timeout())
			case *ResumeDirective:
				// pass
			default:
				pid.handleStopDirective(cid)
			}
			return
		}
	}
}

// handleStopDirective handles the Supervisor stop directive
func (pid *PID) handleStopDirective(cid *PID) {
	pid.UnWatch(cid)
	pid.childrenMap.Remove(cid.Address())
	if err := cid.Shutdown(context.Background()); err != nil {
		// this can enter into some infinite loop if we panic
		// since we are just shutting down the actor we can just log the error
		// TODO: rethink properly about PostStop error handling
		pid.logger.Error(err)
	}
}

// handleRestartDirective handles the Supervisor restart directive
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
		pid.childrenMap.Remove(cid.Address())
		if err := cid.Shutdown(ctx); err != nil {
			pid.logger.Error(err)
		}
		return
	}
	pid.Watch(cid)
}

// childAddress returns the address of the given child actor provided the name
func (pid *PID) childAddress(name string) *address.Address {
	return address.New(name,
		pid.Address().System(),
		pid.Address().Host(),
		pid.Address().Port()).
		WithParent(pid.Address())
}
