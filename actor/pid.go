/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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
	"runtime"
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

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/future"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/errorschain"
	"github.com/tochemey/goakt/v3/internal/eventstream"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/ticker"
	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/log"
)

// specifies the state in which the PID is
// regarding message processing
type processingState int32

const (
	// idle means there are no messages to process
	idle processingState = iota
	// busy means the PID is processing messages
	busy
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
	started   atomic.Bool
	stopping  atomic.Bool
	suspended atomic.Bool

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
	eventsStream eventstream.Stream

	// set the metrics settings
	restartCount   *atomic.Int64
	processedCount *atomic.Int64

	// supervisor strategy
	supervisor            *Supervisor
	supervisionChan       chan error
	supervisionStopSignal chan types.Unit

	// atomic flag indicating whether the actor is processing messages
	processing atomic.Int32

	remoting *Remoting

	goScheduler *goScheduler
	startedAt   *atomic.Int64
	isSingleton atomic.Bool
	relocatable atomic.Bool
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
		address:               address,
		fieldsLocker:          new(sync.RWMutex),
		stopLocker:            new(sync.Mutex),
		mailbox:               NewUnboundedMailbox(),
		stashBox:              nil,
		stashLocker:           &sync.Mutex{},
		eventsStream:          nil,
		restartCount:          atomic.NewInt64(0),
		processedCount:        atomic.NewInt64(0),
		processingTimeLocker:  new(sync.Mutex),
		supervisionChan:       make(chan error, 1),
		supervisionStopSignal: make(chan types.Unit, 1),
		remoting:              NewRemoting(),
		goScheduler:           newGoScheduler(300),
		supervisor:            NewSupervisor(),
		startedAt:             atomic.NewInt64(0),
	}

	pid.initMaxRetries.Store(DefaultInitMaxRetries)
	pid.latestReceiveDuration.Store(0)
	pid.isSingleton.Store(false)
	pid.started.Store(false)
	pid.stopping.Store(false)
	pid.suspended.Store(false)
	pid.passivateAfter.Store(DefaultPassivationTimeout)
	pid.initTimeout.Store(DefaultInitTimeout)
	pid.processing.Store(int32(idle))
	pid.relocatable.Store(true)

	for _, opt := range opts {
		opt(pid)
	}

	behaviorStack := newBehaviorStack()
	behaviorStack.Push(pid.actor.Receive)
	pid.behaviorStack = behaviorStack

	if err := pid.init(ctx); err != nil {
		return nil, err
	}

	pid.supervisionLoop()
	if pid.passivateAfter.Load() > 0 {
		go pid.passivationLoop()
	}

	receiveContext := getContext()
	receiveContext.build(ctx, NoSender, pid, new(goaktpb.PostStart), true)
	pid.doReceive(receiveContext)

	pid.startedAt.Store(time.Now().Unix())
	return pid, nil
}

// Metric returns the actor system metrics.
// The metric does not include any cluster data
func (pid *PID) Metric(ctx context.Context) *ActorMetric {
	if pid.IsRunning() {
		var (
			uptime                  = pid.Uptime()
			latestProcessedDuration = pid.LatestProcessedDuration()
			childrenCount           = pid.ChildrenCount()
			deadlettersCount        = pid.getDeadlettersCount(ctx)
			restartCount            = pid.RestartCount()
			processedCount          = pid.ProcessedCount() - 1 // 1 because of the PostStart message
			stashSize               = pid.StashSize()
		)
		return &ActorMetric{
			deadlettersCount:        uint64(deadlettersCount),
			childrenCount:           uint64(childrenCount),
			uptime:                  uptime,
			latestProcessedDuration: latestProcessedDuration,
			restartCount:            uint64(restartCount),
			processedCount:          uint64(processedCount),
			stashSize:               stashSize,
		}
	}
	return nil
}

// Uptime returns the number of seconds since the actor started
func (pid *PID) Uptime() int64 {
	if pid.IsRunning() {
		return time.Now().Unix() - pid.startedAt.Load()
	}
	return 0
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
	if cidNode, ok := pid.system.tree().GetNode(childAddress.String()); ok {
		cid := cidNode.GetValue()
		if cid.IsRunning() {
			return cid, nil
		}
	}
	return nil, ErrActorNotFound(childAddress.String())
}

// Parent returns the parent of this PID
func (pid *PID) Parent() *PID {
	tree := pid.ActorSystem().tree()
	parentNode, ok := tree.ParentAt(pid, 0)
	if !ok {
		return nil
	}
	return parentNode.GetValue()
}

// Children returns the list of all the direct descendants of the given actor
// Only alive actors are included in the list or an empty list is returned
func (pid *PID) Children() []*PID {
	pid.fieldsLocker.RLock()
	tree := pid.ActorSystem().tree()
	pnode, ok := tree.GetNode(pid.ID())
	if !ok {
		pid.fieldsLocker.RUnlock()
		return nil
	}

	descendants := pnode.Descendants.Items()
	cids := make([]*PID, 0, len(descendants))
	for _, cnode := range descendants {
		cid := cnode.GetValue()
		if cid.IsRunning() {
			cids = append(cids, cid)
		}
	}

	pid.fieldsLocker.RUnlock()
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
	tree := pid.system.tree()
	if _, ok := tree.GetNode(cid.Address().String()); ok {
		if err := cid.Shutdown(ctx); err != nil {
			pid.fieldsLocker.RUnlock()
			return err
		}

		// remove the node from the tree
		tree.DeleteNode(cid)
		pid.fieldsLocker.RUnlock()
		return nil
	}

	pid.fieldsLocker.RUnlock()
	return ErrActorNotFound(cid.Address().String())
}

// IsRunning returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (pid *PID) IsRunning() bool {
	return pid != nil && pid.started.Load() && !pid.suspended.Load()
}

// IsSuspended returns true when the actor is suspended
// A suspended actor is a faulty actor
func (pid *PID) IsSuspended() bool {
	return pid.suspended.Load()
}

// IsSingleton returns true when the actor is a singleton.
//
// A singleton actor is instantiated when cluster mode is enabled.
// A singleton actor like any other actor is created only once within the system and in the cluster.
// A singleton actor is created with the default supervisor strategy and directive.
// A singleton actor once created lives throughout the lifetime of the given actor system.
//
// The singleton actor is created on the oldest node in the cluster.
// When the oldest node leaves the cluster unexpectedly, the singleton is restarted on the new oldest node.
// This is useful for managing shared resources or coordinating tasks that should be handled by a single actor.
func (pid *PID) IsSingleton() bool {
	return pid.isSingleton.Load()
}

// IsRelocatable determines whether the actor can be relocated to another node when its host node shuts down unexpectedly.
// By default, actors are relocatable to ensure system resilience and high availability.
// However, this behavior can be disabled during the actor's creation using the WithRelocationDisabled option.
//
// Returns true if relocation is allowed, and false if relocation is disabled.
func (pid *PID) IsRelocatable() bool {
	return pid.relocatable.Load()
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
// During restart all messages that are in the mailbox and not yet processed will be ignored.
// Only the direct alive children of the given actor will be shudown and respawned with their initial state.
// Bear in mind that restarting an actor will reinitialize the actor to initial state.
// In case any of the direct child restart fails the given actor will not be started at all.
func (pid *PID) Restart(ctx context.Context) error {
	if pid == nil || pid.Address() == nil {
		return ErrUndefinedActor
	}

	pid.logger.Debugf("restarting actor=(%s)", pid.Name())
	actorSystem := pid.ActorSystem()
	tree := actorSystem.tree()
	janitor := actorSystem.getDeathWatch()

	// prepare the child actors to respawn
	// because during the restart process they will be gone
	// from the system and need to be restarted. Only direct children that are alive
	children := pid.Children()
	// get the parent node of the actor
	parent := NoSender
	if pnode, ok := tree.ParentAt(pid, 0); ok {
		parent = pnode.GetValue()
	}

	if pid.IsRunning() {
		if err := pid.Shutdown(ctx); err != nil {
			return err
		}
		ticker := ticker.New(10 * time.Millisecond)
		ticker.Start()
		tickerStopSig := make(chan types.Unit, 1)
		go func() {
			for range ticker.Ticks {
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

	if !pid.IsSuspended() {
		// re-add the actor back to the actors tree
		// no need to handle the error here because the only time this method
		// returns an error if when the parent does not exist which was taken care of in the
		// lines above
		_ = tree.AddNode(parent, pid)
		tree.AddWatcher(pid, janitor)
	}

	// restart all the previous children
	eg, ctx := errgroup.WithContext(ctx)
	for _, child := range children {
		child := child
		eg.Go(func() error {
			if err := child.Restart(ctx); err != nil {
				return err
			}

			if !child.IsSuspended() {
				// re-add the child back to the tree
				// since these calls are idempotent
				_ = tree.AddNode(pid, child)
				tree.AddWatcher(child, janitor)
			}
			return nil
		})
	}

	// wait for the child actor to spawn
	if err := eg.Wait(); err != nil {
		// disable messages processing
		pid.stopping.Store(true)
		pid.started.Store(false)
		return fmt.Errorf("actor=(%s) failed to restart: %w", pid.Name(), err)
	}

	pid.processing.Store(int32(idle))
	pid.suspended.Store(false)
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

	pid.logger.Debugf("actor=(%s) successfully restarted..:)", pid.Name())
	return nil
}

// RestartCount returns the total number of re-starts by the given PID
func (pid *PID) RestartCount() int {
	count := pid.restartCount.Load()
	return int(count)
}

// ChildrenCount returns the total number of childrenMap for the given PID
func (pid *PID) ChildrenCount() int {
	descendants := pid.Children()
	return len(descendants)
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
	tree := pid.system.tree()
	if cnode, ok := tree.GetNode(childAddress.String()); ok {
		cid := cnode.GetValue()
		if cid.IsRunning() {
			return cid, nil
		}
	}

	// create the child actor options child inherit parent's options
	pidOptions := []pidOption{
		withInitMaxRetries(int(pid.initMaxRetries.Load())),
		withCustomLogger(pid.logger),
		withActorSystem(pid.system),
		withEventsStream(pid.eventsStream),
		withInitTimeout(pid.initTimeout.Load()),
		withRemoting(pid.remoting),
	}

	spawnConfig := newSpawnConfig(opts...)
	if spawnConfig.mailbox != nil {
		pidOptions = append(pidOptions, withMailbox(spawnConfig.mailbox))
	}

	// set the supervisor strategies when defined
	if spawnConfig.supervisor != nil {
		pidOptions = append(pidOptions, withSupervisor(spawnConfig.supervisor))
	}

	// set the relocation flag
	if spawnConfig.relocatable {
		pidOptions = append(pidOptions, withRelocationDisabled())
	}

	// disable passivation for system actor
	switch {
	case isReservedName(name):
		pidOptions = append(pidOptions, withPassivationDisabled())
	default:
		switch {
		case spawnConfig.passivateAfter == nil:
			// use the parent passivation settings
			pidOptions = append(pidOptions, withPassivationAfter(pid.passivateAfter.Load()))
		case *spawnConfig.passivateAfter < longLived:
			// use custom passivation setting
			pidOptions = append(pidOptions, withPassivationAfter(*spawnConfig.passivateAfter))
		default:
			// the only time the actor will stop is when its parent stop
			pidOptions = append(pidOptions, withPassivationDisabled())
		}
	}

	// create the child PID
	cid, err := newPID(
		ctx,
		childAddress,
		actor,
		pidOptions...,
	)

	if err != nil {
		return nil, err
	}

	// no need to handle the error because the given parent exist and running
	// that check was done in the above lines
	_ = tree.AddNode(pid, cid)
	tree.AddWatcher(cid, pid.ActorSystem().getDeathWatch())

	eventsStream := pid.eventsStream
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
	pid.ActorSystem().broadcastActor(cid)
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

	receiveContext := getContext()
	receiveContext.build(ctx, pid, to, message, false)
	to.doReceive(receiveContext)

	timer := timers.Get(timeout)

	select {
	case result := <-receiveContext.response:
		timers.Put(timer)
		return result, nil
	case <-timer.C:
		err = ErrRequestTimeout
		pid.toDeadletters(receiveContext, err)
		timers.Put(timer)
		return nil, err
	}
}

// Tell sends an asynchronous message to another PID
func (pid *PID) Tell(ctx context.Context, to *PID, message proto.Message) error {
	if !to.IsRunning() {
		return ErrDead
	}

	receiveContext := getContext()
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

	remoteClient := pid.remoting.serviceClient(host, port)
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

	remoteService := pid.remoting.serviceClient(to.GetHost(), int(to.GetPort()))

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

	remoteService := pid.remoting.serviceClient(to.GetHost(), int(to.GetPort()))

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

	remoteService := pid.remoting.serviceClient(to.GetHost(), int(to.GetPort()))

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

	remoteService := pid.remoting.serviceClient(to.GetHost(), int(to.GetPort()))
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

	remoteService := pid.remoting.serviceClient(host, port)
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

	remoteService := pid.remoting.serviceClient(host, port)
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

	remoteService := pid.remoting.serviceClient(host, port)
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
	pid.logger.Infof("shutdown process has started for actor=(%s)...", pid.Name())

	if !pid.started.Load() {
		pid.logger.Infof("actor=%s is offline. Maybe it has been passivated or stopped already", pid.Name())
		pid.stopLocker.Unlock()
		return nil
	}

	if pid.passivateAfter.Load() > 0 {
		pid.stopping.Store(true)
		pid.logger.Debug("sending a signal to stop passivation listener....")
		pid.haltPassivationLnr <- types.Unit{}
	}

	if err := pid.doStop(ctx); err != nil {
		pid.logger.Errorf("actor=(%s) failed to cleanly stop", pid.Name())
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
	pid.logger.Infof("actor=%s successfully shutdown", pid.Name())
	return nil
}

// Watch watches a given actor for a Terminated message when the watched actor shutdown
func (pid *PID) Watch(cid *PID) {
	pid.ActorSystem().tree().AddWatcher(pid, cid)
}

// UnWatch stops watching a given actor
func (pid *PID) UnWatch(cid *PID) {
	tree := pid.ActorSystem().tree()
	pnode, ok := tree.GetNode(pid.ID())
	if !ok {
		return
	}

	cnode, ok := tree.GetNode(cid.ID())
	if !ok {
		return
	}

	for index, watchee := range pnode.Watchees.Items() {
		if watchee.GetValue().Equals(cid) {
			pnode.Watchees.Delete(index)
			break
		}
	}

	for index, watcher := range cnode.Watchers.Items() {
		if watcher.GetValue().Equals(pid) {
			cnode.Watchers.Delete(index)
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

// doReceive pushes a given message to the actor mailbox
// and signals the receiveLoop to process it
func (pid *PID) doReceive(receiveCtx *ReceiveContext) {
	if err := pid.mailbox.Enqueue(receiveCtx); err != nil {
		// add a warning log because the mailbox is full and do nothing
		pid.logger.Warn(err)
		// push the message as a deadletter
		pid.toDeadletters(receiveCtx, err)
	}
	pid.schedule()
}

// schedule  schedules that a message has arrived and wake up the
// message processing loop
func (pid *PID) schedule() {
	// only signal if the actor is not already processing messages
	if pid.processing.CompareAndSwap(int32(idle), int32(busy)) {
		pid.goScheduler.Schedule(pid.receiveLoop)
	}
}

// receiveLoop extracts every message from the actor mailbox
// and pass it to the appropriate behavior for handling
func (pid *PID) receiveLoop() {
	var received *ReceiveContext
	counter, throughput := 0, pid.goScheduler.Throughput()
	for {
		if counter > throughput {
			counter = 0
			runtime.Gosched()
		}

		counter++
		if received != nil {
			releaseContext(received)
			received = nil
		}

		if received = pid.mailbox.Dequeue(); received != nil {
			// Process the message
			switch msg := received.Message().(type) {
			case *goaktpb.PoisonPill:
				_ = pid.Shutdown(received.Context())
			case *internalpb.HandleFault:
				pid.handleFaultyChild(received.Sender(), msg)
			default:
				pid.handleReceived(received)
			}
		}

		// If no more messages, change busy state to idle
		if !pid.processing.CompareAndSwap(int32(busy), int32(idle)) {
			return
		}

		// Check if new messages were added in the meantime and restart processing
		if !pid.mailbox.IsEmpty() && pid.processing.CompareAndSwap(int32(idle), int32(busy)) {
			continue
		}
		return
	}
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
		switch err, ok := r.(error); {
		case ok:
			var pe *PanicError
			if errors.As(err, &pe) {
				// in case PanicError is sent just forward it
				pid.supervisionChan <- pe
				return
			}

			// this is a normal error just wrap it with some stack trace
			// for rich logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			pid.supervisionChan <- NewPanicError(
				fmt.Errorf("%w at %s[%s:%d]", err, runtime.FuncForPC(pc).Name(), fn, line),
			)

		default:
			// we have no idea what panic it is. Enrich it with some stack trace for rich
			// logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			pid.supervisionChan <- NewPanicError(
				fmt.Errorf("%#v at %s[%s:%d]", r, runtime.FuncForPC(pc).Name(), fn, line),
			)
		}
		return
	}
	if err := received.getError(); err != nil {
		pid.supervisionChan <- err
	}
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor will not be started
func (pid *PID) init(ctx context.Context) error {
	pid.logger.Infof("%s starting...", pid.Name())

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
	pid.logger.Infof("%s successfully started.", pid.Name())

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
	pid.behaviorStack.Reset()
	pid.processedCount.Store(0)
	pid.restartCount.Store(0)
	pid.startedAt.Store(0)
	pid.stopping.Store(false)
	pid.suspended.Store(false)
	pid.supervisor.Reset()
	pid.mailbox.Dispose()
	pid.isSingleton.Store(false)
	pid.relocatable.Store(true)
}

// freeWatchers releases all the actors watching this actor
func (pid *PID) freeWatchers(ctx context.Context) error {
	logger := pid.logger
	logger.Debugf("%s freeing all watcher actors...", pid.Name())
	pnode, ok := pid.ActorSystem().tree().GetNode(pid.ID())
	if !ok {
		pid.logger.Debugf("%s node not found in the actors tree", pid.Name())
		return nil
	}

	watchers := pnode.Watchers
	if watchers.Len() > 0 {
		eg, ctx := errgroup.WithContext(ctx)
		for _, watcher := range watchers.Items() {
			watcher := watcher
			eg.Go(func() error {
				terminated := &goaktpb.Terminated{
					ActorId: pid.ID(),
				}

				wid := watcher.GetValue()
				if wid.IsRunning() {
					logger.Debugf("watcher=(%s) releasing watched=(%s)", wid.Name(), pid.Name())
					// ignore error here because the watcher is running
					_ = pid.Tell(ctx, wid, terminated)
					wid.UnWatch(pid)
					logger.Debugf("watcher=(%s) released watched=(%s)", wid.Name(), pid.Name())
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			logger.Errorf("watcher=(%s) failed to free all watcher error: %v", pid.Name(), err)
			return err
		}
		logger.Debugf("%s successfully frees all watcher actors...", pid.Name())
		return nil
	}
	logger.Debugf("%s does not have any watcher actors. Maybe already freed.", pid.Name())
	return nil
}

// freeWatchees releases all actors that have been watched by this actor
func (pid *PID) freeWatchees() error {
	logger := pid.logger
	logger.Debugf("%s freeing all watched actors...", pid.Name())
	pnode, ok := pid.ActorSystem().tree().GetNode(pid.ID())
	if !ok {
		pid.logger.Debugf("%s node not found in the actors tree", pid.Name())
		return nil
	}

	size := pnode.Watchees.Len()
	if size > 0 {
		eg := new(errgroup.Group)
		for _, watched := range pnode.Watchees.Items() {
			watched := watched
			wid := watched.GetValue()
			eg.Go(func() error {
				logger.Debugf("watcher=(%s) unwatching actor=(%s)", pid.Name(), wid.Name())
				pid.UnWatch(wid)
				logger.Debugf("watcher=(%s) successfully unwatch actor=(%s)", pid.Name(), wid.Name())
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			logger.Errorf("watcher=(%s) failed to unwatch actors: %v", pid.Name(), err)
			return err
		}
		logger.Debugf("%s successfully unwatch all watched actors...", pid.Name())
		return nil
	}
	logger.Debugf("%s does not have any watched actors. Maybe already freed.", pid.Name())
	return nil
}

// freeChildren releases all child actors
func (pid *PID) freeChildren(ctx context.Context) error {
	logger := pid.logger
	logger.Debugf("%s freeing all descendant actors...", pid.Name())

	tree := pid.ActorSystem().tree()
	pnode, ok := tree.GetNode(pid.ID())
	if !ok {
		pid.logger.Debugf("%s node not found in the actors tree", pid.Name())
		return nil
	}

	if descendants, ok := tree.Descendants(pid); ok && len(descendants) > 0 {
		eg, ctx := errgroup.WithContext(ctx)
		for index, descendant := range descendants {
			descendant := descendant
			child := descendant.GetValue()
			index := index
			eg.Go(func() error {
				logger.Debugf("parent=(%s) disowning descendant=(%s)", pid.Name(), child.Name())

				pid.UnWatch(child)
				pnode.Descendants.Delete(index)

				if err := child.Shutdown(ctx); err != nil {
					errwrap := fmt.Errorf(
						"parent=(%s) failed to disown descendant=(%s): %w", pid.Name(), child.Name(),
						err,
					)
					return errwrap
				}
				logger.Debugf("parent=(%s) successfully disown descendant=(%s)", pid.Name(), child.Name())
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			logger.Errorf("parent=(%s) failed to free all descendant actors: %v", pid.Name(), err)
			return err
		}
		logger.Debugf("%s successfully free all descendant actors...", pid.Name())
		return nil
	}
	pid.logger.Debugf("%s does not have any children. Maybe already freed.", pid.Name())
	return nil
}

// passivationLoop checks whether the actor is processing public or not.
// when the actor is idle, it automatically shuts down to free resources
func (pid *PID) passivationLoop() {
	pid.logger.Info("start the passivation listener...")
	pid.logger.Infof("passivation timeout is (%s)", pid.passivateAfter.Load().String())
	ticker := ticker.New(pid.passivateAfter.Load())
	ticker.Start()
	tickerStopSig := make(chan types.Unit, 1)

	// start ticking
	go func() {
		for {
			select {
			case <-ticker.Ticks:
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

	if pid.stopping.Load() || pid.suspended.Load() {
		pid.logger.Infof("actor=%s is stopping or maybe suspended. No need to passivate", pid.Name())
		return
	}

	pid.logger.Infof("passivation mode has been triggered for actor=%s...", pid.Name())

	ctx := context.Background()
	if err := pid.doStop(ctx); err != nil {
		// TODO: rethink properly about PostStop error handling
		pid.logger.Errorf("failed to passivate actor=(%s): reason=(%v)", pid.Name(), err)
		return
	}

	if pid.eventsStream != nil {
		event := &goaktpb.ActorPassivated{
			Address:      pid.Address().Address,
			PassivatedAt: timestamppb.Now(),
		}
		pid.eventsStream.Publish(eventsTopic, event)
	}

	pid.logger.Infof("actor=%s successfully passivated", pid.Name())
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
			pid.logger.Errorf("actor=(%s) failed to unstash messages", pid.Name())
			pid.started.Store(false)
			return err
		}
	}

	// stop supervisor loop
	pid.supervisionStopSignal <- types.Unit{}

	if pid.remoting != nil {
		pid.remoting.Close()
	}

	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(pid.freeWatchees()).
		AddError(pid.freeChildren(ctx)).
		Error(); err != nil {
		pid.started.Store(false)
		pid.reset()
		return err
	}

	// run the PostStop hook and let watchers know
	// you are terminated
	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(pid.actor.PostStop(ctx)).
		AddError(pid.freeWatchers(ctx)).Error(); err != nil {
		pid.started.Store(false)
		pid.reset()
		return err
	}

	pid.started.Store(false)
	pid.logger.Infof("shutdown process completed for actor=%s...", pid.Name())
	pid.reset()
	return nil
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

	// find a directive for the given error or check whether there
	// is a directive for any error type
	directive, ok := pid.supervisor.Directive(err)
	if !ok {
		// let us check whether we have all errors directive
		directive, ok = pid.supervisor.Directive(new(anyError))
		if !ok {
			pid.logger.Debugf("no supervisor directive found for error: %s", errorType(err))
			pid.suspend(err.Error())
			return
		}
	}

	msg := &internalpb.HandleFault{
		ActorId: pid.ID(),
		Message: err.Error(),
	}

	switch directive {
	case StopDirective:
		msg.Directive = &internalpb.HandleFault_Stop{
			Stop: new(internalpb.StopDirective),
		}
	case RestartDirective:
		msg.Directive = &internalpb.HandleFault_Restart{
			Restart: &internalpb.RestartDirective{
				MaxRetries: pid.supervisor.MaxRetries(),
				Timeout:    int64(pid.supervisor.Timeout()),
			},
		}
	case ResumeDirective:
		msg.Directive = &internalpb.HandleFault_Resume{
			Resume: &internalpb.ResumeDirective{},
		}
	default:
		pid.logger.Debugf("unknown directive: %T found for error: %s", directive, errorType(err))
		pid.suspend(err.Error())
		return
	}

	switch pid.supervisor.Strategy() {
	case OneForOneStrategy:
		msg.Strategy = internalpb.Strategy_STRATEGY_ONE_FOR_ONE
	case OneForAllStrategy:
		msg.Strategy = internalpb.Strategy_STRATEGY_ONE_FOR_ALL
	default:
		msg.Strategy = internalpb.Strategy_STRATEGY_ONE_FOR_ONE
	}

	if parent := pid.Parent(); parent != nil && !parent.Equals(NoSender) {
		_ = pid.Tell(context.Background(), parent, msg)
	}
}

// toDeadletters sends message to deadletter synthetic actor
func (pid *PID) toDeadletters(receiveCtx *ReceiveContext, err error) {
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
	var sender *goaktpb.Address
	if receiveCtx.Sender() != nil || receiveCtx.Sender() != NoSender {
		sender = receiveCtx.Sender().Address().Address
	}

	// get the deadletter synthetic actor and send a message to it
	receiver := pid.Address().Address
	deadletter := pid.ActorSystem().getDeadletter()
	_ = pid.Tell(context.Background(),
		deadletter,
		&internalpb.EmitDeadletter{
			Deadletter: &goaktpb.Deadletter{
				Sender:   sender,
				Receiver: receiver,
				Message:  msg,
				SendTime: timestamppb.Now(),
				Reason:   err.Error(),
			}})
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
	f := future.WithContext(ctx, completion.Task)
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
		pid.logger.Errorf("unable to pipe message to actor=(%s): not started", to.Name())
		return
	}

	messageContext := newReceiveContext(ctx, pid, to, result.Success())
	to.doReceive(messageContext)
}

// handleFaultyChild watches for child actor's failure and act based upon the supervisory strategy
func (pid *PID) handleFaultyChild(cid *PID, msg *internalpb.HandleFault) {
	if cid.ID() == msg.GetActorId() {
		message := msg.GetMessage()
		directive := msg.GetDirective()
		includeSiblings := msg.GetStrategy() == internalpb.Strategy_STRATEGY_ONE_FOR_ALL

		pid.logger.Errorf("child actor=(%s) is failing: Err=%s", cid.Name(), message)

		switch d := directive.(type) {
		case *internalpb.HandleFault_Stop:
			pid.handleStopDirective(cid, includeSiblings)
		case *internalpb.HandleFault_Restart:
			pid.handleRestartDirective(cid,
				d.Restart.GetMaxRetries(),
				time.Duration(d.Restart.GetTimeout()),
				includeSiblings)
		case *internalpb.HandleFault_Resume:
			// pass
		default:
			pid.handleStopDirective(cid, includeSiblings)
		}
		return
	}
}

// handleStopDirective handles the Behavior stop directive
func (pid *PID) handleStopDirective(cid *PID, includeSiblings bool) {
	ctx := context.Background()
	tree := pid.ActorSystem().tree()
	pids := []*PID{cid}

	if includeSiblings {
		if siblings, ok := tree.Siblings(cid); ok {
			for _, sibling := range siblings {
				pids = append(pids, sibling.GetValue())
			}
		}
	}

	eg, ctx := errgroup.WithContext(ctx)
	for _, spid := range pids {
		spid := spid
		eg.Go(func() error {
			pid.UnWatch(spid)
			if err := spid.Shutdown(ctx); err != nil {
				// just log the error and suspend the given sibling
				pid.logger.Error(fmt.Errorf("failed to shutdown actor=(%s): %w", spid.Name(), err))
				// we need to suspend the actor since its shutdown is the result of
				// one of its faulty siblings
				spid.suspend(err.Error())
				// no need to return an error
				return nil
			}
			// remove the sibling tree node
			tree.DeleteNode(spid)
			return nil
		})
	}

	// no error to handle
	_ = eg.Wait()
}

// handleRestartDirective handles the Behavior restart directive
func (pid *PID) handleRestartDirective(cid *PID, maxRetries uint32, timeout time.Duration, includeSiblings bool) {
	ctx := context.Background()
	tree := pid.ActorSystem().tree()
	pids := []*PID{cid}

	if includeSiblings {
		if siblings, ok := tree.Siblings(cid); ok {
			for _, sibling := range siblings {
				pids = append(pids, sibling.GetValue())
			}
		}
	}

	eg, ctx := errgroup.WithContext(ctx)
	for _, spid := range pids {
		spid := spid
		eg.Go(func() error {
			pid.UnWatch(spid)
			var err error

			switch {
			case maxRetries == 0 || timeout <= 0:
				err = spid.Restart(ctx)
			default:
				retrier := retry.NewRetrier(int(maxRetries), timeout, timeout)
				err = retrier.RunContext(ctx, cid.Restart)
			}

			if err != nil {
				pid.logger.Error(err)
				if err := spid.Shutdown(ctx); err != nil {
					pid.logger.Error(err)
					// we need to suspend the actor since it is faulty
					spid.suspend(err.Error())
				}
			}
			return nil
		})
	}
}

// childAddress returns the address of the given child actor provided the name
func (pid *PID) childAddress(name string) *address.Address {
	return address.New(name,
		pid.Address().System(),
		pid.Address().Host(),
		pid.Address().Port()).
		WithParent(pid.Address())
}

// suspend puts the actor in a suspension mode.
func (pid *PID) suspend(reason string) {
	pid.logger.Infof("%s going into suspension mode", pid.Name())
	pid.suspended.Store(true)
	// stop passivation loop
	pid.haltPassivationLnr <- types.Unit{}
	// stop the supervisor loop
	pid.supervisionStopSignal <- types.Unit{}
	// publish an event to the events stream
	pid.eventsStream.Publish(eventsTopic, &goaktpb.ActorSuspended{
		Address:     pid.Address().Address,
		SuspendedAt: timestamppb.Now(),
		Reason:      reason,
	})
}

// getDeadlettersCount gets deadletter
func (pid *PID) getDeadlettersCount(ctx context.Context) int64 {
	var (
		name    = pid.ID()
		to      = pid.ActorSystem().getDeadletter()
		from    = pid.ActorSystem().getSystemGuardian()
		message = &internalpb.GetDeadlettersCount{
			ActorId: &name,
		}
	)
	if to.IsRunning() {
		// ask the deadletter actor for the count
		// using the default ask timeout
		// note: no need to check for error because this call is internal
		message, _ := from.Ask(ctx, to, message, DefaultAskTimeout)
		// cast the response received from the deadletter
		deadlettersCount := message.(*internalpb.DeadlettersCount)
		return deadlettersCount.GetTotalCount()
	}
	return 0
}
