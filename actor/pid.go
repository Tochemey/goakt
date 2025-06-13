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

package actor

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
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/future"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/collection"
	"github.com/tochemey/goakt/v3/internal/errorschain"
	"github.com/tochemey/goakt/v3/internal/eventstream"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/ticker"
	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/internal/workerpool"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/passivation"
)

// specifies the state in which the PID is
// regarding message processing

const (
	// idle means there are no messages to process
	idle int32 = iota
	// busy means the PID is processing messages
	busy
)

// taskCompletion is used to track completions' taskCompletion
// to pipe the result to the appropriate PID
type taskCompletion struct {
	Receiver *PID
	Task     func() (proto.Message, error)
}

// PID specifies an actor unique process
// With the PID one can send a ReceiveContext to the actor
// PID helps to identify the actor in the local actor system
type PID struct {
	// specifies the message processor
	actor Actor

	// specifies the actor address
	address *address.Address

	latestReceiveTime     atomic.Time
	latestReceiveDuration atomic.Duration

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
	fieldsLocker *sync.RWMutex
	stopLocker   *sync.Mutex

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
	supervisionChan       chan *supervisionSignal
	supervisionStopSignal chan types.Unit

	// atomic flag indicating whether the actor is processing messages
	processing atomic.Int32

	remoting *Remoting

	workerPool  *workerpool.WorkerPool
	startedAt   *atomic.Int64
	isSingleton atomic.Bool
	relocatable atomic.Bool

	// the list of dependencies
	dependencies *collection.Map[string, extension.Dependency]

	processState        *pidState
	passivationStrategy passivation.Strategy
	passivationState    *passivationState
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
		supervisionChan:       make(chan *supervisionSignal, 1),
		supervisionStopSignal: make(chan types.Unit, 1),
		remoting:              NewRemoting(),
		supervisor:            NewSupervisor(),
		startedAt:             atomic.NewInt64(0),
		dependencies:          collection.NewMap[string, extension.Dependency](),
		processState:          new(pidState),
		passivationStrategy:   passivation.NewTimeBasedStrategy(DefaultPassivationTimeout),
		passivationState:      new(passivationState),
	}

	pid.initMaxRetries.Store(DefaultInitMaxRetries)
	pid.latestReceiveDuration.Store(0)
	pid.isSingleton.Store(false)
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
	if pid.passivationStrategy != nil {
		go pid.passivationLoop()
	}

	pid.fireSystemMessage(ctx, new(goaktpb.PostStart))

	pid.startedAt.Store(time.Now().Unix())
	return pid, nil
}

// Dependencies returns a slice containing all dependencies currently registered
// within the PID's local context.
//
// These dependencies are typically injected at actor initialization (via SpawnOptions)
// and made accessible during the actor's lifecycle. They can include services, clients,
// or any resources that the actor requires to operate.
//
// This method is useful for diagnostic tools, dynamic inspection, or cases where
// an actor needs to introspect its environment.
//
// Returns: A slice of Dependency instances associated with this PID.
func (pid *PID) Dependencies() []extension.Dependency {
	return pid.dependencies.Values()
}

// Dependency retrieves a single dependency by its unique identifier from the PID's
// registered dependencies.
func (pid *PID) Dependency(dependencyID string) extension.Dependency {
	if dependency, ok := pid.dependencies.Get(dependencyID); ok {
		return dependency
	}
	return nil
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
	if cidNode, ok := pid.system.tree().node(childAddress.String()); ok {
		cid := cidNode.value()
		if cid.IsRunning() {
			return cid, nil
		}
	}
	return nil, NewErrActorNotFound(childAddress.String())
}

// Parent returns the parent of this PID
func (pid *PID) Parent() *PID {
	tree := pid.ActorSystem().tree()
	parentNode, ok := tree.parentAt(pid, 0)
	if !ok {
		return nil
	}
	return parentNode.value()
}

// Children returns the list of all the direct descendants of the given actor
// Only alive actors are included in the list or an empty list is returned
func (pid *PID) Children() []*PID {
	pid.fieldsLocker.RLock()
	tree := pid.ActorSystem().tree()
	pnode, ok := tree.node(pid.ID())
	if !ok {
		pid.fieldsLocker.RUnlock()
		return nil
	}

	descendants := pnode.Descendants.Items()
	cids := make([]*PID, 0, len(descendants))
	for _, cnode := range descendants {
		cid := cnode.value()
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
		return NewErrActorNotFound(cid.Address().String())
	}

	pid.fieldsLocker.RLock()
	tree := pid.system.tree()
	if _, ok := tree.node(cid.Address().String()); ok {
		if err := cid.Shutdown(ctx); err != nil {
			pid.fieldsLocker.RUnlock()
			return err
		}

		// remove the node from the tree
		tree.deleteNode(cid)
		pid.fieldsLocker.RUnlock()
		return nil
	}

	pid.fieldsLocker.RUnlock()
	return NewErrActorNotFound(cid.Address().String())
}

// IsRunning returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (pid *PID) IsRunning() bool {
	return pid != nil && pid.processState.IsRunnable()
}

// IsSuspended returns true when the actor is suspended
// A suspended actor is a faulty actor
func (pid *PID) IsSuspended() bool {
	return pid.processState.IsSuspended()
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

// PassivationStrategy returns the given actor's passivation strategy.
func (pid *PID) PassivationStrategy() passivation.Strategy {
	pid.fieldsLocker.RLock()
	strategy := pid.passivationStrategy
	pid.fieldsLocker.RUnlock()
	return strategy
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
	deathWatch := actorSystem.getDeathWatch()

	// prepare the child actors to respawn
	// because during the restart process they will be gone
	// from the system and need to be restarted. Only direct children that are alive
	children := pid.Children()
	// get the parent node of the actor
	parent := NoSender
	if pnode, ok := tree.parentAt(pid, 0); ok {
		parent = pnode.value()
	}

	if pid.IsRunning() {
		if err := pid.Shutdown(ctx); err != nil {
			return err
		}
		tk := ticker.New(10 * time.Millisecond)
		tk.Start()
		tickerStopSig := make(chan types.Unit, 1)
		go func() {
			for range tk.Ticks {
				if !pid.IsRunning() {
					tickerStopSig <- types.Unit{}
					return
				}
			}
		}()
		<-tickerStopSig
		tk.Stop()
	}

	pid.resetBehavior()
	if err := pid.init(ctx); err != nil {
		return err
	}

	if !pid.IsSuspended() {
		// re-add the actor back to the actor tree and cluster
		if err := errorschain.New(errorschain.ReturnFirst()).
			AddErrorFn(func() error {
				if !parent.Equals(NoSender) {
					return tree.addNode(parent, pid)
				}
				return nil
			}).
			AddErrorFn(func() error { tree.addWatcher(pid, deathWatch); return nil }).
			AddErrorFn(func() error { return actorSystem.broadcastActor(pid) }).
			Error(); err != nil {
			return err
		}
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
				// re-add the child back to the tree and cluster
				// since these calls are idempotent
				if err := errorschain.New(errorschain.ReturnFirst()).
					AddErrorFn(func() error { return tree.addNode(pid, child) }).
					AddErrorFn(func() error { tree.addWatcher(child, deathWatch); return nil }).
					AddErrorFn(func() error { return actorSystem.broadcastActor(child) }).
					Error(); err != nil {
					return err
				}
			}
			return nil
		})
	}

	// wait for the child actor to spawn
	if err := eg.Wait(); err != nil {
		// disable messages processing
		pid.processState.ClearRunning()
		pid.processState.SetStopping()
		return fmt.Errorf("actor=(%s) failed to restart: %w", pid.Name(), err)
	}

	pid.processing.Store(idle)
	pid.processState.ClearSuspended()
	pid.supervisionLoop()
	if pid.passivationStrategy != nil {
		go pid.passivationLoop()
	}

	pid.restartCount.Inc()
	pid.fireSystemMessage(ctx, new(goaktpb.PostStart))
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

	spawnConfig := newSpawnConfig(opts...)
	if err := spawnConfig.Validate(); err != nil {
		return nil, err
	}

	if !spawnConfig.isSystem {
		// you should not create a system-based actor or
		// use the system actor naming convention pattern
		if isReservedName(name) {
			return nil, ErrReservedName
		}
	}

	childAddress := pid.childAddress(name)
	tree := pid.system.tree()
	if cnode, ok := tree.node(childAddress.String()); ok {
		cid := cnode.value()
		if cid.IsRunning() {
			return cid, nil
		}
	}

	// create the child actor options child inherits parent's options
	pidOptions := []pidOption{
		withInitMaxRetries(int(pid.initMaxRetries.Load())),
		withCustomLogger(pid.logger),
		withActorSystem(pid.system),
		withEventsStream(pid.eventsStream),
		withInitTimeout(pid.initTimeout.Load()),
		withRemoting(pid.remoting),
		withWorkerPool(pid.workerPool),
	}

	if spawnConfig.mailbox != nil {
		pidOptions = append(pidOptions, withMailbox(spawnConfig.mailbox))
	}

	// set the supervisor strategies when defined
	if spawnConfig.supervisor != nil {
		pidOptions = append(pidOptions, withSupervisor(spawnConfig.supervisor))
	}

	// set the relocation flag
	if !spawnConfig.relocatable {
		pidOptions = append(pidOptions, withRelocationDisabled())
	}

	// enable stash
	if spawnConfig.enableStash {
		pidOptions = append(pidOptions, withStash())
	}

	// set the dependencies when defined
	if spawnConfig.dependencies != nil {
		_ = pid.ActorSystem().Inject(spawnConfig.dependencies...)
		pidOptions = append(pidOptions, withDependencies(spawnConfig.dependencies...))
	}

	pidOptions = append(pidOptions, withPassivationStrategy(spawnConfig.passivationStrategy))

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
	_ = tree.addNode(pid, cid)
	tree.addWatcher(cid, pid.ActorSystem().getDeathWatch())

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
	return cid, pid.ActorSystem().broadcastActor(cid)
}

// Reinstate brings a previously suspended actor back into an active state.
//
// This method is used to reinstate an actor identified by its PID (`cid`)—for example,
// one that was suspended due to a fault, error, or supervision policy. Once reinstated,
// the actor resumes processing messages from its mailbox, retaining its internal state
// as it was prior to suspension.
//
// This can be invoked by a supervisor, system actor, or administrative service to
// recover from transient failures or to manually resume paused actors.
//
// Parameters:
//   - cid: The PID of the actor to be reinstated.
//
// Returns:
//   - error: If the actor does not exist, or cannot be reinstated due to internal errors.
//
// Example usage:
//
//	err := supervisorPID.Reinstate(suspendedActorPID)
//	if err != nil {
//	    log.Printf("Reinstate failed: %v", err)
//	}
//
// See also: PID.ReinstateNamed for name-based reinstatement.
func (pid *PID) Reinstate(cid *PID) error {
	if !pid.IsRunning() {
		return ErrDead
	}

	if cid.Equals(NoSender) {
		return ErrUndefinedActor
	}

	// this call is necessary because the reference to the actor may have been
	// kept elsewhere and the actor may have been stopped and removed from the system
	actual, err := pid.ActorSystem().LocalActor(cid.Name())
	if err != nil {
		return err
	}

	// this is a rare case when the local actor is not the same as the one
	if !actual.Equals(cid) {
		return NewErrActorNotFound(cid.Name())
	}

	if !cid.IsSuspended() || cid.IsRunning() {
		return nil
	}

	cid.doReinstate()
	return nil
}

// ReinstateNamed attempts to reinstate a previously suspended actor by its registered name.
//
// This is useful when the actor is addressed through a global or system-wide registry
// and its PID is not directly available. The method looks up the actor by name and,
// if found in a suspended state, transitions it back to active, allowing it to resume
// processing messages from its mailbox as if it had never been suspended. This method should be used
// when cluster mode is enabled.
//
// Typical use cases include supervisory systems, administrative tooling, or
// health-check-driven recovery mechanisms.
//
// Parameters:
//   - ctx: Standard context for cancellation, timeout, or deadlines.
//   - actorName: The globally or locally registered name of the actor to reinstate.
//
// Returns:
//   - error: If the actor does not exist, or cannot be reinstated due to internal errors.
//
// Example usage:
//
//	err := supervisorPID.ReinstateNamed(ctx, "payment-processor-42")
//	if err != nil {
//	    log.Printf("Failed to reinstate actor: %v", err)
//	}
//
// See also: PID.Reinstate for direct PID-based reinstatement.
func (pid *PID) ReinstateNamed(ctx context.Context, actorName string) error {
	if !pid.IsRunning() {
		return ErrDead
	}

	addr, cid, err := pid.ActorSystem().ActorOf(ctx, actorName)
	if err != nil {
		return err
	}

	if !cid.Equals(NoSender) {
		if !cid.IsSuspended() || cid.IsRunning() {
			return nil
		}

		cid.doReinstate()
		return nil
	}

	return pid.remoting.RemoteReinstate(ctx, addr.Host(), addr.Port(), actorName)
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
func (pid *PID) PipeTo(ctx context.Context, to *PID, task func() (proto.Message, error)) error {
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

	remoteClient := pid.remoting.remotingServiceClient(host, port)
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

	remoteService := pid.remoting.remotingServiceClient(to.GetHost(), int(to.GetPort()))
	request := connect.NewRequest(&internalpb.RemoteTellRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{
				Sender:   pid.Address().Address,
				Receiver: to.Address,
				Message:  marshaled,
			},
		},
	})

	pid.logger.Debugf("sending a message to remote=(%s:%d)", to.GetHost(), to.GetPort())
	_, err = remoteService.RemoteTell(ctx, request)
	if err != nil {
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

	remoteService := pid.remoting.remotingServiceClient(to.GetHost(), int(to.GetPort()))
	request := connect.NewRequest(&internalpb.RemoteAskRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{
				Sender:   pid.Address().Address,
				Receiver: to.Address,
				Message:  marshaled,
			},
		},
		Timeout: durationpb.New(timeout),
	})

	resp, err := remoteService.RemoteAsk(ctx, request)
	if err != nil {
		return nil, err
	}

	if resp != nil {
		for _, msg := range resp.Msg.GetMessages() {
			response = msg
			break
		}
	}

	return
}

// RemoteBatchTell sends a batch of messages to a remote actor in a way fire-and-forget manner
// Messages are processed one after the other in the order they are sent.
func (pid *PID) RemoteBatchTell(ctx context.Context, to *address.Address, messages []proto.Message) error {
	if pid.remoting == nil {
		return ErrRemotingDisabled
	}

	remoteMessages := make([]*internalpb.RemoteMessage, 0, len(messages))
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return NewErrInvalidRemoteMessage(err)
		}

		remoteMessages = append(remoteMessages, &internalpb.RemoteMessage{
			Sender:   pid.Address().Address,
			Receiver: to.Address,
			Message:  packed,
		})
	}

	remoteService := pid.remoting.remotingServiceClient(to.GetHost(), int(to.GetPort()))
	_, err := remoteService.RemoteTell(ctx, connect.NewRequest(&internalpb.RemoteTellRequest{
		RemoteMessages: remoteMessages,
	}))
	return err
}

// RemoteBatchAsk sends a synchronous bunch of messages to a remote actor and expect responses in the same order as the messages.
// Messages are processed one after the other in the order they are sent.
// This can hinder performance if it is not properly used.
func (pid *PID) RemoteBatchAsk(ctx context.Context, to *address.Address, messages []proto.Message, timeout time.Duration) (responses []*anypb.Any, err error) {
	if pid.remoting == nil {
		return nil, ErrRemotingDisabled
	}

	remoteMessages := make([]*internalpb.RemoteMessage, 0, len(messages))
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return nil, NewErrInvalidRemoteMessage(err)
		}

		remoteMessages = append(
			remoteMessages, &internalpb.RemoteMessage{
				Sender:   pid.Address().Address,
				Receiver: to.Address,
				Message:  packed,
			})
	}

	remoteService := pid.remoting.remotingServiceClient(to.GetHost(), int(to.GetPort()))
	resp, err := remoteService.RemoteAsk(ctx, connect.NewRequest(&internalpb.RemoteAskRequest{
		RemoteMessages: remoteMessages,
		Timeout:        durationpb.New(timeout),
	}))

	if err != nil {
		return nil, err
	}

	if resp != nil {
		responses = append(responses, resp.Msg.GetMessages()...)
	}

	return
}

// RemoteStop stops an actor on a remote node
func (pid *PID) RemoteStop(ctx context.Context, host string, port int, name string) error {
	if pid.remoting == nil {
		return ErrRemotingDisabled
	}

	remoteService := pid.remoting.remotingServiceClient(host, port)
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

	remoteService := pid.remoting.remotingServiceClient(host, port)
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

	remoteService := pid.remoting.remotingServiceClient(host, port)
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

	if !pid.processState.IsRunning() {
		pid.logger.Infof("actor=%s is offline. Maybe it has been passivated or stopped already", pid.Name())
		pid.stopLocker.Unlock()
		return nil
	}

	pid.processState.SetStopping()
	if pid.passivationStrategy != nil {
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
	pid.ActorSystem().tree().addWatcher(cid, pid)
}

// UnWatch stops watching a given actor
func (pid *PID) UnWatch(cid *PID) {
	tree := pid.ActorSystem().tree()
	pnode, ok := tree.node(pid.ID())
	if !ok {
		return
	}

	cnode, ok := tree.node(cid.ID())
	if !ok {
		return
	}

	for index, watchee := range pnode.Watchees.Items() {
		if watchee.value().Equals(cid) {
			pnode.Watchees.Delete(index)
			break
		}
	}

	for index, watcher := range cnode.Watchers.Items() {
		if watcher.value().Equals(pid) {
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
	if pid.IsRunning() {
		if err := pid.mailbox.Enqueue(receiveCtx); err != nil {
			pid.logger.Warn(err)
			pid.toDeadletters(receiveCtx, err)
		}
		pid.schedule()
	}
}

// schedule  schedules that a message has arrived and wake up the
// message processing loop
func (pid *PID) schedule() {
	// only signal if the actor is not already processing messages
	if pid.processing.CompareAndSwap(idle, busy) {
		pid.workerPool.SubmitWork(pid.receiveLoop)
	}
}

// receiveLoop extracts every message from the actor mailbox
// and pass it to the appropriate behavior for handling
func (pid *PID) receiveLoop() {
	var received *ReceiveContext
	for {
		if received != nil {
			releaseContext(received)
		}

		if received = pid.mailbox.Dequeue(); received != nil {
			// Process the message
			switch msg := received.Message().(type) {
			case *goaktpb.PoisonPill:
				_ = pid.Shutdown(received.Context())
			case *internalpb.Down:
				pid.handleFailure(received.Sender(), msg)
			case *goaktpb.PausePassivation:
				pid.pausePassivation()
			case *goaktpb.ResumePassivation:
				pid.resumePassivation()
			default:
				pid.handleReceived(received)
			}
		}

		// if no more messages, change busy state to idle
		if !pid.processing.CompareAndSwap(busy, idle) {
			return
		}

		// Check if new messages were added in the meantime and restart processing
		if !pid.mailbox.IsEmpty() && pid.processing.CompareAndSwap(idle, busy) {
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
				pid.supervisionChan <- newSupervisionSignal(pe, received.Message())
				return
			}

			// this is a normal error just wrap it with some stack trace
			// for rich logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			pid.supervisionChan <- newSupervisionSignal(
				NewPanicError(
					fmt.Errorf("%w at %s[%s:%d]", err, runtime.FuncForPC(pc).Name(), fn, line),
				), received.Message())

		default:
			// we have no idea what panic it is. Enrich it with some stack trace for rich
			// logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			pid.supervisionChan <- newSupervisionSignal(
				NewPanicError(
					fmt.Errorf("%#v at %s[%s:%d]", r, runtime.FuncForPC(pc).Name(), fn, line),
				), received.Message())
		}
		return
	}
	if err := received.getError(); err != nil {
		pid.supervisionChan <- newSupervisionSignal(err, received.Message())
	}
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor will not be started
func (pid *PID) init(ctx context.Context) error {
	pid.logger.Infof("%s starting...", pid.Name())

	initContext := newContext(ctx, pid.Name(), pid.system, pid.Dependencies()...)

	cctx, cancel := context.WithTimeout(ctx, pid.initTimeout.Load())
	retrier := retry.NewRetrier(int(pid.initMaxRetries.Load()), time.Millisecond, pid.initTimeout.Load())

	if err := retrier.RunContext(cctx, func(_ context.Context) error {
		return pid.actor.PreStart(initContext)
	}); err != nil {
		e := NewErrInitFailure(err)
		cancel()
		return e
	}

	pid.processState.SetRunning()
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
	pid.initMaxRetries.Store(DefaultInitMaxRetries)
	pid.latestReceiveDuration.Store(0)
	pid.initTimeout.Store(DefaultInitTimeout)
	pid.behaviorStack.Reset()
	pid.processedCount.Store(0)
	pid.restartCount.Store(0)
	pid.startedAt.Store(0)
	pid.processState.Reset()
	pid.supervisor.Reset()
	pid.mailbox.Dispose()
	pid.isSingleton.Store(false)
	pid.relocatable.Store(true)
	pid.dependencies.Reset()
}

// freeWatchers releases all the actors watching this actor
func (pid *PID) freeWatchers(ctx context.Context) error {
	logger := pid.logger
	logger.Debugf("%s freeing all watcher actors...", pid.Name())
	pnode, ok := pid.ActorSystem().tree().node(pid.ID())
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

				wid := watcher.value()
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
		// no need to handle the error because the go routines will always return nil
		_ = eg.Wait()
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
	pnode, ok := pid.ActorSystem().tree().node(pid.ID())
	if !ok {
		pid.logger.Debugf("%s node not found in the actors tree", pid.Name())
		return nil
	}

	size := pnode.Watchees.Len()
	if size > 0 {
		eg := new(errgroup.Group)
		for _, watched := range pnode.Watchees.Items() {
			watched := watched
			wid := watched.value()
			eg.Go(func() error {
				logger.Debugf("watcher=(%s) unwatching actor=(%s)", pid.Name(), wid.Name())
				pid.UnWatch(wid)
				logger.Debugf("watcher=(%s) successfully unwatch actor=(%s)", pid.Name(), wid.Name())
				return nil
			})
		}
		// no need to handle the error because the go routines will always return nil
		_ = eg.Wait()
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
	pnode, ok := tree.node(pid.ID())
	if !ok {
		pid.logger.Debugf("%s node not found in the actors tree", pid.Name())
		return nil
	}

	if descendants, ok := tree.descendants(pid); ok && len(descendants) > 0 {
		eg, ctx := errgroup.WithContext(ctx)
		for index, descendant := range descendants {
			descendant := descendant
			child := descendant.value()
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
	pid.logger.Info("start the passivation listener for ", pid.Name())
	pid.logger.Infof("%s passivation strategy %s", pid.Name(), pid.passivationStrategy.String())
	var clock *ticker.Ticker
	var exec func()
	tickerStopSig := make(chan types.Unit, 1)

	switch s := pid.passivationStrategy.(type) {
	case *passivation.TimeBasedStrategy:
		clock = ticker.New(s.Timeout())
		exec = func() {
			elapsed := time.Since(pid.latestReceiveTime.Load())
			if elapsed >= s.Timeout() {
				tickerStopSig <- types.Unit{}
			}
		}
	case *passivation.MessagesCountBasedStrategy:
		clock = ticker.New(100 * time.Millisecond) // TODO: revisit this number
		exec = func() {
			currentCount := pid.ProcessedCount() - 1
			if currentCount >= s.MaxMessages() {
				tickerStopSig <- types.Unit{}
			}
		}
	case *passivation.LongLivedStrategy:
		clock = ticker.New(longLived)
		exec = func() {
			elapsed := time.Since(pid.latestReceiveTime.Load())
			if elapsed >= longLived {
				tickerStopSig <- types.Unit{}
			}
		}
	}

	clock.Start()
	pid.passivationState.Started()

	go func() {
		for {
			select {
			case <-clock.Ticks:
				exec()
			case <-pid.haltPassivationLnr:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	clock.Stop()

	// normal shutdown set the passivation as stopped
	if pid.processState.IsStopping() {
		pid.passivationState.Stopped()
		pid.passivationState.Reset()
	}

	if pid.processState.IsStopping() ||
		pid.processState.IsSuspended() ||
		pid.passivationState.IsPaused() {
		pid.logger.Infof("No need to passivate actor=%s", pid.Name())
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
			pid.processState.ClearRunning()
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
		AddErrorFn(func() error { return pid.freeWatchees() }).
		AddErrorFn(func() error { return pid.freeChildren(ctx) }).
		Error(); err != nil {
		pid.processState.ClearRunning()
		pid.reset()
		return err
	}

	stopContext := newContext(ctx, pid.Name(), pid.system, pid.Dependencies()...)

	// run the PostStop hook and let watchers know
	// you are terminated
	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddErrorFn(func() error { return pid.actor.PostStop(stopContext) }).
		AddErrorFn(func() error { return pid.freeWatchers(ctx) }).
		Error(); err != nil {
		pid.processState.ClearRunning()
		pid.reset()
		return err
	}

	pid.processState.ClearRunning()
	pid.logger.Infof("shutdown process completed for actor=%s...", pid.Name())
	pid.reset()
	return nil
}

// supervisionLoop send error to watchers
func (pid *PID) supervisionLoop() {
	go func() {
		for {
			select {
			case signal := <-pid.supervisionChan:
				pid.notifyParent(signal)
			case <-pid.supervisionStopSignal:
				return
			}
		}
	}()
}

// notifyParent sends a notification to the parent actor
func (pid *PID) notifyParent(signal *supervisionSignal) {
	if signal == nil || errors.Is(signal.Err(), ErrDead) {
		return
	}

	// find a directive for the given error or check whether there
	// is a directive for any error type
	directive, ok := pid.supervisor.Directive(signal.Err())
	if !ok {
		// let us check whether we have all errors directive
		directive, ok = pid.supervisor.Directive(new(anyError))
		if !ok {
			pid.logger.Debugf("no supervisor directive found for error: %s", errorType(signal.Err()))
			pid.suspend(signal.Err().Error())
			return
		}
	}

	pid.logger.Debugf("%s supervisor directive %s", pid.Name(), directive.String())

	// create the message to send to the parent
	actual, _ := anypb.New(signal.Msg())
	msg := &internalpb.Down{
		ActorId:      pid.ID(),
		ErrorMessage: signal.Err().Error(),
		Message:      actual,
		Timestamp:    signal.Timestamp(),
	}

	switch directive {
	case StopDirective:
		msg.Directive = &internalpb.Down_Stop{
			Stop: new(internalpb.StopDirective),
		}
	case RestartDirective:
		msg.Directive = &internalpb.Down_Restart{
			Restart: &internalpb.RestartDirective{
				MaxRetries: pid.supervisor.MaxRetries(),
				Timeout:    int64(pid.supervisor.Timeout()),
			},
		}
	case ResumeDirective:
		msg.Directive = &internalpb.Down_Resume{
			Resume: &internalpb.ResumeDirective{},
		}
	case EscalateDirective:
		msg.Directive = &internalpb.Down_Escalate{
			Escalate: &internalpb.EscalateDirective{},
		}
	default:
		pid.logger.Debugf("unknown directive: %T found for error: %s", directive, errorType(signal.Err()))
		pid.suspend(signal.Err().Error())
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
		pid.logger.Warnf("%s child actor=(%s) is failing: Err=%s", pid.Name(), parent.Name(), msg.GetErrorMessage())
		pid.logger.Infof("%s activates [strategy=%s, directive=%s] for failing child actor=(%s)",
			parent.Name(),
			pid.supervisor.Strategy(),
			directive,
			pid.Name())

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

	sender := &address.Address{}
	if receiveCtx.Sender() != nil || receiveCtx.Sender() != NoSender {
		sender = receiveCtx.Sender().Address()
	}

	ctx := context.WithoutCancel(receiveCtx.Context())
	receiver := pid.Address()
	pid.sendToDeadletter(ctx, sender, receiver, receiveCtx.Message(), err)
}

// sendToDeadletter sends a message to the deadletter actor
func (pid *PID) sendToDeadletter(ctx context.Context, from, to *address.Address, message proto.Message, err error) {
	deadletter := pid.ActorSystem().getDeadletter()
	msg, _ := anypb.New(message)

	// send the message to the deadletter actor
	_ = pid.Tell(ctx,
		deadletter,
		&internalpb.EmitDeadletter{
			Deadletter: &goaktpb.Deadletter{
				Sender:   from.Address,
				Receiver: to.Address,
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
	fut := future.New(completion.Task)
	result, err := fut.Await(ctx)
	// logger the error when the task returns an error
	// and send the message to the deadletter actor
	if err != nil {
		pid.logger.Error(err)
		pid.sendToDeadletter(context.Background(), pid.Address(), pid.Address(), new(goaktpb.NoMessage), err)
		return
	}

	// make sure that the receiver is still alive
	to := completion.Receiver
	if !to.IsRunning() {
		pid.logger.Errorf("unable to pipe message to actor=(%s): not started", to.Name())
		return
	}

	messageContext := newReceiveContext(ctx, pid, to, result)
	to.doReceive(messageContext)
}

// handleFailure watches for child actor's failure and act based upon the supervisory strategy
func (pid *PID) handleFailure(cid *PID, msg *internalpb.Down) {
	if cid.ID() == msg.GetActorId() {
		directive := msg.GetDirective()
		includeSiblings := msg.GetStrategy() == internalpb.Strategy_STRATEGY_ONE_FOR_ALL

		switch d := directive.(type) {
		case *internalpb.Down_Stop:
			pid.handleStopDirective(cid, includeSiblings)
		case *internalpb.Down_Restart:
			pid.handleRestartDirective(cid,
				d.Restart.GetMaxRetries(),
				time.Duration(d.Restart.GetTimeout()),
				includeSiblings)
		case *internalpb.Down_Resume:
		// pass
		case *internalpb.Down_Escalate:
			// forward the message to the parent and suspend the actor
			_ = cid.Tell(context.Background(), pid, &goaktpb.Mayday{
				Reason:    msg.GetErrorMessage(),
				Message:   msg.GetMessage(),
				Timestamp: msg.GetTimestamp(),
			})
			cid.suspend(msg.GetErrorMessage())
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
		if siblings, ok := tree.siblings(cid); ok {
			for _, sibling := range siblings {
				pids = append(pids, sibling.value())
			}
		}
	}

	eg, ctx := errgroup.WithContext(ctx)
	for _, spid := range pids {
		spid := spid
		eg.Go(func() error {
			// TODO: revisit this
			//pid.UnWatch(spid)
			if err := spid.Shutdown(ctx); err != nil {
				pid.logger.Error(fmt.Errorf("failed to shutdown actor=(%s): %w", spid.Name(), err))
				// we need to suspend the actor since its shutdown is the result of
				// one of its faulty siblings
				spid.suspend(err.Error())
				return nil
			}
			tree.deleteNode(spid)
			return nil
		})
	}
	_ = eg.Wait()
}

// handleRestartDirective handles the Behavior restart directive
func (pid *PID) handleRestartDirective(cid *PID, maxRetries uint32, timeout time.Duration, includeSiblings bool) {
	ctx := context.Background()
	tree := pid.ActorSystem().tree()
	pids := []*PID{cid}

	if includeSiblings {
		if siblings, ok := tree.siblings(cid); ok {
			for _, sibling := range siblings {
				pids = append(pids, sibling.value())
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
	pid.processState.SetSuspended()
	// pause passivation loop
	pid.pausePassivation()
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

// fireSystemMessage sends a system-level message to the specified PID by creating a receive context and invoking the message handling logic.
func (pid *PID) fireSystemMessage(ctx context.Context, message proto.Message) {
	receiveContext := getContext()
	receiveContext.build(ctx, NoSender, pid, message, true)
	pid.doReceive(receiveContext)
}

func (pid *PID) doReinstate() {
	pid.logger.Infof("%s has been reinstated", pid.Name())
	pid.processState.ClearSuspended()

	// resume the supervisor loop
	pid.supervisionLoop()
	// resume passivation loop
	pid.resumePassivation()

	// publish an event to the events stream
	pid.eventsStream.Publish(eventsTopic, &goaktpb.ActorReinstated{
		Address:      pid.Address().Address,
		ReinstatedAt: timestamppb.Now(),
	})
}

// pausePassivation pauses the passivation loop
func (pid *PID) pausePassivation() {
	if pid.passivationState.IsRunning() && pid.passivationStrategy != nil {
		pid.haltPassivationLnr <- types.Unit{}
		pid.passivationState.Pause()
	}
}

// resumePassivation resumes a paused passivation
func (pid *PID) resumePassivation() {
	if pid.passivationState.IsPaused() && pid.passivationStrategy != nil {
		pid.passivationState.Resume()
		go pid.passivationLoop()
	}
}
