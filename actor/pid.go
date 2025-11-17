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

	"github.com/flowchartsman/retry"
	"go.uber.org/atomic"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/chain"
	"github.com/tochemey/goakt/v3/internal/codec"
	"github.com/tochemey/goakt/v3/internal/collection"
	"github.com/tochemey/goakt/v3/internal/eventstream"
	"github.com/tochemey/goakt/v3/internal/future"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/locker"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/internal/ticker"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/passivation"
	"github.com/tochemey/goakt/v3/remote"
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
	_ locker.NoCopy
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

	// the actor system
	system ActorSystem

	// specifies the logger to use
	logger log.Logger

	// various lockers to protect the PID fields
	// in a concurrent environment
	fieldsLocker sync.RWMutex
	stopLocker   sync.Mutex

	// specifies the actor behavior stack
	behaviorStack *behaviorStack

	// stash settings
	stashBuffer *stashState

	// define an events stream
	eventsStream eventstream.Stream

	// set the metrics settings
	restartCount   atomic.Int64
	processedCount atomic.Int64

	// supervisor strategy
	supervisor               *Supervisor
	supervisionChan          chan *supervisionSignal
	supervisionStopSignal    chan registry.Unit
	supervisionStopRequested atomic.Bool

	// atomic flag indicating whether the actor is processing messages
	processing atomic.Int32

	remoting remote.Remoting

	startedAt  atomic.Int64
	stateFlags atomic.Uint32

	// the list of dependencies
	dependencies *collection.Map[string, extension.Dependency]

	passivationStrategy passivation.Strategy
	passivationManager  *passivationManager

	// this is used to specify a role the actor belongs to
	// this field is optional and will be set when the actor is created with a given role
	role *string
}

var _ passivationParticipant = (*PID)(nil)

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
		logger:                log.New(log.ErrorLevel, os.Stderr),
		address:               address,
		mailbox:               NewUnboundedMailbox(),
		supervisionChan:       make(chan *supervisionSignal, 1),
		supervisionStopSignal: make(chan registry.Unit, 1),
	}

	pid.initMaxRetries.Store(DefaultInitMaxRetries)
	pid.latestReceiveDuration.Store(0)
	pid.processedCount.Store(0)
	pid.startedAt.Store(0)
	pid.restartCount.Store(0)
	pid.initTimeout.Store(DefaultInitTimeout)
	pid.processing.Store(int32(idle))
	pid.toggleFlag(isRelocatableFlag, true)

	for _, opt := range opts {
		opt(pid)
	}

	if pid.supervisor == nil {
		pid.supervisor = NewSupervisor()
	}
	if pid.passivationStrategy == nil {
		pid.passivationStrategy = passivation.NewTimeBasedStrategy(DefaultPassivationTimeout)
	}

	behaviorStack := newBehaviorStack()
	behaviorStack.Push(pid.actor.Receive)
	pid.behaviorStack = behaviorStack

	if err := pid.init(ctx); err != nil {
		return nil, err
	}

	pid.startSupervision()
	pid.startPassivation()

	if err := pid.healthCheck(ctx); err != nil {
		return nil, err
	}

	pid.fireSystemMessage(ctx, new(goaktpb.PostStart))

	pid.startedAt.Store(time.Now().Unix())
	return pid, nil
}

// Role narrows placement to cluster members that advertise the given role.
//
// In a clustered deployment, GoAkt uses placement roles to constrain where actors may be
// started or relocated. When `Role` is non-nil the actor will only be considered for nodes
// that list the same role; clearing the field makes the actor eligible on any node.
//
// ⚠️ Note: This setting has effect only for `SpawnOn` and `SpawnSingleton` requests. Local-only
// spawns ignore it.
//
// Returns:
//   - *string: a pointer to the role name if one was assigned when the actor
//     was spawned; nil if the actor is not bound to any role.
//
// Notes:
//   - The value may be nil; callers should check for nil before dereferencing.
//   - Roles are typically provided via spawn options at creation time.
//   - The returned pointer should be treated as read-only; copy the value if you
//     need to store it elsewhere.
//
// Example:
//
//	if r := pid.Role(); r != nil {
//	    fmt.Printf("actor pinned to role %q\n", *r)
//	} else {
//	    fmt.Println("actor has no role")
//	}
func (pid *PID) Role() *string {
	pid.fieldsLocker.RLock()
	role := pid.role
	pid.fieldsLocker.RUnlock()
	return role
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
	if pid.dependencies == nil {
		return nil
	}
	return pid.dependencies.Values()
}

// Dependency retrieves a single dependency by its unique identifier from the PID's
// registered dependencies.
func (pid *PID) Dependency(dependencyID string) extension.Dependency {
	if pid.dependencies == nil {
		return nil
	}
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
		return nil, gerrors.ErrDead
	}

	childAddress := pid.childAddress(name)
	if cidNode, ok := pid.system.tree().node(childAddress.String()); ok {
		cid := cidNode.value()
		if cid.IsRunning() {
			return cid, nil
		}
	}
	return nil, gerrors.NewErrActorNotFound(childAddress.String())
}

// Parent returns the parent of this PID
func (pid *PID) Parent() *PID {
	tree := pid.ActorSystem().tree()
	parent, ok := tree.parent(pid)
	if !ok {
		return nil
	}
	return parent
}

// Children returns the list of all the direct descendants of the given actor
// Only alive actors are included in the list or an empty list is returned
func (pid *PID) Children() []*PID {
	pid.fieldsLocker.RLock()
	tree := pid.ActorSystem().tree()

	children := tree.children(pid)
	cids := make([]*PID, 0, len(children))
	for _, cid := range children {
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
		return gerrors.ErrDead
	}

	if cid == nil || cid == pid.ActorSystem().NoSender() {
		return gerrors.ErrUndefinedActor
	}

	// If the child is not running and not suspended, it's not found.
	if !cid.IsRunning() && !cid.IsSuspended() {
		return gerrors.NewErrActorNotFound(cid.Address().String())
	}

	// Check if the child exists in the actor tree.
	pid.fieldsLocker.RLock()
	tree := pid.system.tree()
	_, exists := tree.node(cid.Address().String())
	pid.fieldsLocker.RUnlock()

	if !exists {
		return gerrors.NewErrActorNotFound(cid.Address().String())
	}

	// Attempt to shutdown the child.
	if err := cid.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}

// IsRunning returns true when the actor is alive ready to process messages and false
// when the actor is stopped or not started at all
func (pid *PID) IsRunning() bool {
	return pid != nil &&
		pid.isFlagEnabled(runningFlag) &&
		!pid.isFlagEnabled(stoppingFlag) &&
		!pid.isFlagEnabled(passivatingFlag) &&
		!pid.isFlagEnabled(suspendedFlag)
}

// IsSuspended returns true when the actor is suspended
// A suspended actor is a faulty actor
func (pid *PID) IsSuspended() bool {
	return pid.isFlagEnabled(suspendedFlag)
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
	return pid.isFlagEnabled(isSingletonFlag)
}

// IsRelocatable determines whether the actor can be relocated to another node when its host node shuts down unexpectedly.
// By default, actors are relocatable to ensure system resilience and high availability.
// However, this behavior can be disabled during the actor's creation using the WithRelocationDisabled option.
//
// Returns true if relocation is allowed, and false if relocation is disabled.
func (pid *PID) IsRelocatable() bool {
	return pid.isFlagEnabled(isRelocatableFlag)
}

// IsStopping reports whether the actor is in the process of stopping.
// It returns true once a stop has been initiated—explicitly or via passivation—and false otherwise.
func (pid *PID) IsStopping() bool {
	return pid.isFlagEnabled(stoppingFlag) || pid.isFlagEnabled(passivatingFlag)
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
		return gerrors.ErrUndefinedActor
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
	parent := pid.ActorSystem().NoSender()
	if ppid, ok := tree.parent(pid); ok {
		parent = ppid
	}

	if pid.IsRunning() {
		if err := pid.Shutdown(ctx); err != nil {
			return err
		}
		tk := ticker.New(10 * time.Millisecond)
		tk.Start()
		tickerStopSig := make(chan registry.Unit, 1)
		go func() {
			for range tk.Ticks {
				if !pid.IsRunning() {
					tickerStopSig <- registry.Unit{}
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
		if err := chain.New(chain.WithFailFast()).
			AddRunner(func() error {
				if !parent.Equals(pid.ActorSystem().NoSender()) {
					return tree.addNode(parent, pid)
				}
				return nil
			}).
			AddRunner(func() error { tree.addWatcher(pid, deathWatch); return nil }).
			AddRunner(func() error { return actorSystem.putActorOnCluster(pid) }).
			Run(); err != nil {
			return err
		}
	}

	// restart all the previous children
	eg, gctx := errgroup.WithContext(ctx)
	for _, child := range children {
		child := child
		eg.Go(func() error {
			if err := child.Restart(gctx); err != nil {
				return err
			}

			if !child.IsSuspended() {
				// re-add the child back to the tree and cluster
				// since these calls are idempotent
				if err := chain.New(chain.WithFailFast()).
					AddRunner(func() error { return tree.addNode(pid, child) }).
					AddRunner(func() error { tree.addWatcher(child, deathWatch); return nil }).
					AddRunner(func() error { return actorSystem.putActorOnCluster(child) }).
					Run(); err != nil {
					return err
				}
			}
			return nil
		})
	}

	// wait for the child actor to spawn
	if err := eg.Wait(); err != nil {
		// disable messages processing
		pid.toggleFlag(stoppingFlag, true)
		pid.toggleFlag(runningFlag, false)
		return fmt.Errorf("actor=(%s) failed to restart: %w", pid.Name(), err)
	}

	pid.processing.Store(idle)
	pid.toggleFlag(suspendedFlag, false)
	pid.startSupervision()
	pid.startPassivation()

	if err := pid.healthCheck(ctx); err != nil {
		return err
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

// ChildrenCount returns the total number of children for the given PID
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
		return nil, gerrors.ErrDead
	}

	spawnConfig := newSpawnConfig(opts...)
	if err := spawnConfig.Validate(); err != nil {
		return nil, err
	}

	if !spawnConfig.isSystem {
		// you should not create a system-based actor or
		// use the system actor naming convention pattern
		if isSystemName(name) {
			return nil, gerrors.ErrReservedName
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
		withPassivationManager(pid.passivationManager),
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
	return cid, pid.ActorSystem().putActorOnCluster(cid)
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
		return gerrors.ErrDead
	}

	if cid.Equals(pid.ActorSystem().NoSender()) {
		return gerrors.ErrUndefinedActor
	}

	// this call is necessary because the reference to the actor may have been
	// kept elsewhere and the actor may have been stopped and removed from the system
	actual, err := pid.ActorSystem().LocalActor(cid.Name())
	if err != nil {
		return err
	}

	// this is a rare case when the local actor is not the same as the one
	if !actual.Equals(cid) {
		return gerrors.NewErrActorNotFound(cid.Name())
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
		return gerrors.ErrDead
	}

	addr, cid, err := pid.ActorSystem().ActorOf(ctx, actorName)
	if err != nil {
		return err
	}

	if cid != nil && !cid.Equals(pid.ActorSystem().NoSender()) {
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
	if pid.stashBuffer == nil || pid.stashBuffer.box == nil {
		return 0
	}
	return uint64(pid.stashBuffer.box.Len())
}

// PipeTo executes a long-running task asynchronously and delivers its result
// to the mailbox of the specified actor.
//
// While the task is executing, the calling actor is not blocked and can continue
// processing other messages. This enables efficient interaction with external
// services or computations without stalling the actor’s message loop.
//
// Once the task completes successfully, its result is sent as a message to the
// target actor’s mailbox. If the task fails, the failure is sent to the dead letter queue.
//
// This pattern is useful when:
//   - Calling external services (e.g., databases, APIs) from an actor.
//   - Performing background computations whose results are needed later.
//   - Offloading long tasks while keeping the actor responsive.
//
// Parameters:
//   - ctx: context for cancellation and timeouts.
//   - to: the target actor PID that will receive the result.
//   - task: a function that performs the work and returns a proto.Message
//     or an error.
//   - opts: optional PipeOptions. Check PipeOption to see the available options.
//
// Example:
//
//	pid.PipeTo(ctx, targetPID, func() (proto.Message, error) {
//	    resp, err := callExternalAPI()
//	    if err != nil {
//	        return nil, err
//	    }
//	    return &ApiResponse{Data: resp}, nil
//	})
func (pid *PID) PipeTo(ctx context.Context, to *PID, task func() (proto.Message, error), opts ...PipeOption) error {
	if task == nil {
		return gerrors.ErrUndefinedTask
	}

	if !to.IsRunning() {
		return gerrors.ErrDead
	}

	config := newPipeConfig(opts...)
	go pid.handleCompletion(
		ctx,
		config,
		&taskCompletion{
			Receiver: to,
			Task:     task,
		},
	)

	return nil
}

// PipeToName executes a long-running task asynchronously and delivers its result
// to the mailbox of the actor identified by its name.
//
// While the task is executing, the calling actor remains free to continue
// processing other messages without being blocked.
//
// Once the task completes successfully, its result is sent as a message
// to the target actor’s mailbox. If the task fails, the failure is sent to the dead letter queue.
//
// Compared to PipeTo, PipeToName provides location transparency: the destination
// actor is resolved by name, allowing messages to be delivered regardless of
// where the actor is running (e.g., local or remote).
//
// Parameters:
//   - ctx: context for cancellation and timeouts.
//   - actorName: the logical name of the target actor.
//   - task: a function that performs the work and returns a proto.Message
//     or an error.
//   - opts: optional PipeOptions. Check PipeOption to see the available options.
//
// Example:
//
//	pid.PipeToName(ctx, "worker-1", func() (proto.Message, error) {
//	    result, err := doWork()
//	    if err != nil {
//	        return nil, err
//	    }
//	    return &MyResult{Value: result}, nil
//	})
func (pid *PID) PipeToName(ctx context.Context, actorName string, task func() (proto.Message, error), opts ...PipeOption) error {
	if task == nil {
		return gerrors.ErrUndefinedTask
	}

	ok, err := pid.ActorSystem().ActorExists(ctx, actorName)
	if err != nil {
		return err
	}

	if !ok {
		return gerrors.NewErrActorNotFound(actorName)
	}

	go func() {
		config := newPipeConfig(opts...)

		// apply timeout if provided
		var cancel context.CancelFunc
		if config != nil && config.timeout != nil {
			ctx, cancel = context.WithTimeout(ctx, *config.timeout)
			defer cancel()
		}

		// wrap the provided completion task into a future
		fut := future.New(task)

		// execute the task, optionally via circuit breaker
		runTask := func() (proto.Message, error) {
			if config != nil && config.circuitBreaker != nil {
				outcome, oerr := config.circuitBreaker.Execute(ctx, func(ctx context.Context) (any, error) {
					return fut.Await(ctx)
				})

				if oerr != nil {
					return nil, oerr
				}

				// no need to check the type since the future.Await returns proto.Message
				// if there is no error
				return outcome.(proto.Message), nil
			}
			return fut.Await(ctx)
		}

		result, err := runTask()
		if err != nil {
			pid.logger.Error(err)
			pid.toDeadletter(ctx, pid.Address(), pid.Address(), new(goaktpb.NoMessage), err)
			return
		}

		// send the result to the actor identified by its name
		actorSystem := pid.ActorSystem()
		if err := actorSystem.NoSender().SendAsync(ctx, actorName, result); err != nil {
			pid.logger.Error(err)
			pid.toDeadletter(ctx, pid.Address(), pid.Address(), result, err)
			return
		}
	}()

	return nil
}

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (pid *PID) Ask(ctx context.Context, to *PID, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	if !to.IsRunning() {
		return nil, gerrors.ErrDead
	}

	if timeout <= 0 {
		return nil, gerrors.ErrInvalidTimeout
	}

	receiveContext := getContext()
	receiveContext.build(ctx, pid, to, message, false)
	responseCh := receiveContext.response
	if responseCh != nil {
		defer putResponseChannel(responseCh)
	}
	to.doReceive(receiveContext)

	timer := timers.Get(timeout)

	select {
	case result := <-responseCh:
		timers.Put(timer)
		return result, nil
	case <-ctx.Done():
		err = errors.Join(ctx.Err(), gerrors.ErrRequestTimeout)
		pid.handleReceivedError(receiveContext, err)
		timers.Put(timer)
		return nil, err
	case <-timer.C:
		err = gerrors.ErrRequestTimeout
		pid.handleReceivedError(receiveContext, err)
		timers.Put(timer)
		return nil, err
	}
}

// Tell sends an asynchronous message to another PID
func (pid *PID) Tell(ctx context.Context, to *PID, message proto.Message) error {
	if !to.IsRunning() {
		return gerrors.ErrDead
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
		return gerrors.ErrDead
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
		return nil, gerrors.ErrDead
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

	for i := range messages {
		response, err := pid.Ask(ctx, to, messages[i], timeout)
		if err != nil {
			return nil, err
		}
		responses <- response
	}
	return
}

// RemoteLookup look for an actor address on a remote node.
func (pid *PID) RemoteLookup(ctx context.Context, host string, port int, name string) (addr *address.Address, err error) {
	if pid.remoting == nil {
		return nil, gerrors.ErrRemotingDisabled
	}

	return pid.remoting.RemoteLookup(ctx, host, port, name)
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (pid *PID) RemoteTell(ctx context.Context, to *address.Address, message proto.Message) error {
	if pid.remoting == nil {
		return gerrors.ErrRemotingDisabled
	}

	return pid.remoting.RemoteTell(ctx, pid.Address(), to, message)
}

// RemoteAsk sends a synchronous message to another actor remotely and expect a response.
func (pid *PID) RemoteAsk(ctx context.Context, to *address.Address, message proto.Message, timeout time.Duration) (response *anypb.Any, err error) {
	if pid.remoting == nil {
		return nil, gerrors.ErrRemotingDisabled
	}

	if timeout <= 0 {
		return nil, gerrors.ErrInvalidTimeout
	}

	return pid.remoting.RemoteAsk(ctx, pid.Address(), to, message, timeout)
}

// RemoteBatchTell sends a batch of messages to a remote actor in a way fire-and-forget manner
// Messages are processed one after the other in the order they are sent.
func (pid *PID) RemoteBatchTell(ctx context.Context, to *address.Address, messages []proto.Message) error {
	if pid.remoting == nil {
		return gerrors.ErrRemotingDisabled
	}

	return pid.remoting.RemoteBatchTell(ctx, pid.Address(), to, messages)
}

// RemoteBatchAsk sends a synchronous bunch of messages to a remote actor and expect responses in the same order as the messages.
// Messages are processed one after the other in the order they are sent.
// This can hinder performance if it is not properly used.
func (pid *PID) RemoteBatchAsk(ctx context.Context, to *address.Address, messages []proto.Message, timeout time.Duration) (responses []*anypb.Any, err error) {
	if pid.remoting == nil {
		return nil, gerrors.ErrRemotingDisabled
	}

	return pid.remoting.RemoteBatchAsk(ctx, pid.Address(), to, messages, timeout)
}

// RemoteStop stops an actor on a remote node
func (pid *PID) RemoteStop(ctx context.Context, host string, port int, name string) error {
	if pid.remoting == nil {
		return gerrors.ErrRemotingDisabled
	}

	return pid.remoting.RemoteStop(ctx, host, port, name)
}

// RemoteSpawn creates an actor on a remote node. The given actor needs to be registered on the remote node using the Register method of ActorSystem
func (pid *PID) RemoteSpawn(ctx context.Context, host string, port int, actorName, actorType string, opts ...SpawnOption) error {
	if pid.remoting == nil {
		return gerrors.ErrRemotingDisabled
	}

	config := newSpawnConfig(opts...)
	if err := config.Validate(); err != nil {
		return err
	}

	request := &remote.SpawnRequest{
		Name:                actorName,
		Kind:                actorType,
		Singleton:           config.asSingleton,
		Relocatable:         config.relocatable,
		PassivationStrategy: config.passivationStrategy,
		Dependencies:        config.dependencies,
		EnableStashing:      config.enableStash,
	}

	return pid.remoting.RemoteSpawn(ctx, host, port, request)
}

// RemoteReSpawn restarts an actor on a remote node.
func (pid *PID) RemoteReSpawn(ctx context.Context, host string, port int, name string) error {
	if pid.remoting == nil {
		return gerrors.ErrRemotingDisabled
	}

	return pid.remoting.RemoteReSpawn(ctx, host, port, name)
}

// Shutdown gracefully shuts down the given actor
// All current messages in the mailbox will be processed before the actor shutdown after a period of time
// that can be configured. All child actors will be gracefully shutdown.
func (pid *PID) Shutdown(ctx context.Context) error {
	// we should never shutdown system actors unless the whole system is terminating
	if actoryStem := pid.ActorSystem(); actoryStem != nil {
		if !actoryStem.isStopping() && isSystemName(pid.Name()) {
			pid.logger.Warnf("attempt to shutdown a system actor (%s)", pid.Name())
			return gerrors.ErrShutdownForbidden
		}
	}

	pid.stopLocker.Lock()
	pid.logger.Infof("shutdown process has started for actor=(%s)...", pid.Name())

	if !pid.isFlagEnabled(runningFlag) {
		pid.logger.Infof("actor=%s is offline. Maybe it has been passivated or stopped already", pid.Name())
		pid.stopLocker.Unlock()
		return nil
	}

	pid.toggleFlag(stoppingFlag, true)
	pid.unregisterPassivation()

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

	pwatchees := tree.watchees(pid)
	for _, watchee := range pwatchees {
		if watchee.Equals(cid) {
			pnode.watchees.Delete(watchee.ID())
			break
		}
	}

	// get the watchers of the child actor
	cwatchers := tree.watchers(cid)
	for _, watcher := range cwatchers {
		if watcher.Equals(pid) {
			cnode.watchers.Delete(watcher.ID())
			break
		}
	}
}

// Logger returns the logger sets when creating the PID
func (pid *PID) Logger() log.Logger {
	pid.fieldsLocker.RLock()
	logger := pid.logger
	pid.fieldsLocker.RUnlock()
	return logger
}

// LatestActivityTime returns the timestamp of the last message received by the actor.
// This value can be used for monitoring and health-check purposes to determine
// if the actor is still active or has become idle.
//
// Note: The timestamp is updated whenever the actor receives a message.
func (pid *PID) LatestActivityTime() time.Time {
	return pid.latestReceiveTime.Load()
}

// doReceive pushes a given message to the actor mailbox
// and signals the receiveLoop to process it
func (pid *PID) doReceive(receiveCtx *ReceiveContext) {
	if err := pid.mailbox.Enqueue(receiveCtx); err != nil {
		pid.logger.Warn(err)
		pid.handleReceivedError(receiveCtx, err)
	}
	pid.process()
}

// process extracts every message from the actor mailbox
// and pass it to the appropriate behavior for handling
func (pid *PID) process() {
	// Only start a processing loop when transitioning from idle -> busy.
	// If another loop is already running (state is busy), exit early.
	if !pid.processing.CompareAndSwap(idle, busy) {
		return
	}

	go func() {
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
				case *internalpb.HealthCheckRequest:
					pid.handleHealthcheck(received)
				case *internalpb.Panicking:
					pid.handlePanicking(received.Sender(), msg)
				case *goaktpb.PausePassivation:
					pid.pausePassivation()
				case *goaktpb.ResumePassivation:
					pid.resumePassivation()
				default:
					pid.handleReceived(received)
				}
			}

			// if no more messages, change busy state to idle
			pid.processing.Store(idle)

			// Check if new messages were added in the meantime and restart processing
			if !pid.mailbox.IsEmpty() && pid.processing.CompareAndSwap(idle, busy) {
				continue
			}
			return
		}
	}()
}

// handleHealthcheck is used to handle the readiness probe messages
func (pid *PID) handleHealthcheck(received *ReceiveContext) {
	pid.markActivity(time.Now())
	received.Response(new(internalpb.HealthCheckResponse))
}

// handleReceived picks the right behavior and processes the message
func (pid *PID) handleReceived(received *ReceiveContext) {
	defer pid.recovery(received)
	if behavior := pid.behaviorStack.Peek(); behavior != nil {
		pid.markActivity(time.Now().UTC())
		pid.recordProcessedMessage()
		behavior(received)
	}
}

// markActivity updates the last receive timestamp and notifies the shared passivation manager.
func (pid *PID) markActivity(at time.Time) {
	pid.latestReceiveTime.Store(at)
	if pid.passivationManager != nil {
		pid.passivationManager.Touch(pid)
	}
}

// recordProcessedMessage increments the processed message count and notifies the passivation manager.
func (pid *PID) recordProcessedMessage() {
	pid.processedCount.Inc()
	if pid.passivationManager != nil {
		pid.passivationManager.MessageProcessed(pid)
	}
}

// passivationID returns the unique identifier of the actor for passivation tracking
func (pid *PID) passivationID() string {
	return pid.ID()
}

// passivationLatestActivity returns the latest activity time of the actor for passivation tracking
func (pid *PID) passivationLatestActivity() time.Time {
	return pid.latestReceiveTime.Load()
}

func (pid *PID) passivationTry(reason string) bool {
	return pid.tryPassivation(reason)
}

// recovery is called upon after message is processed
func (pid *PID) recovery(received *ReceiveContext) {
	if r := recover(); r != nil {
		switch err, ok := r.(error); {
		case ok:
			var pe *gerrors.PanicError
			if errors.As(err, &pe) {
				// in case PanicError is sent just forward it
				pid.supervisionChan <- newSupervisionSignal(pe, received.Message())
				return
			}

			// this is a normal error just wrap it with some stack trace
			// for rich logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			pid.supervisionChan <- newSupervisionSignal(
				gerrors.NewPanicError(
					fmt.Errorf("%w at %s[%s:%d]", err, runtime.FuncForPC(pc).Name(), fn, line),
				), received.Message())

		default:
			// we have no idea what panic it is. Enrich it with some stack trace for rich
			// logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			pid.supervisionChan <- newSupervisionSignal(
				gerrors.NewPanicError(
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
	pid.logger.Infof("%s initializing...", pid.Name())

	initContext := newContext(ctx, pid.Name(), pid.system, pid.Dependencies()...)

	cctx, cancel := context.WithTimeout(ctx, pid.initTimeout.Load())
	retrier := retry.NewRetrier(int(pid.initMaxRetries.Load()), time.Millisecond, pid.initTimeout.Load())

	if err := retrier.RunContext(cctx, func(_ context.Context) error {
		return pid.actor.PreStart(initContext)
	}); err != nil {
		e := gerrors.NewErrInitFailure(err)
		cancel()
		pid.logger.Errorf("%s failed to initialize: %v", pid.Name(), err)
		return e
	}

	pid.toggleFlag(runningFlag, true)
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
	pid.toggleFlag(runningFlag, false)
	pid.toggleFlag(stoppingFlag, false)
	pid.toggleFlag(suspendedFlag, false)
	if pid.supervisor != nil {
		if sys, ok := pid.system.(*actorSystem); !ok || pid.supervisor != sys.defaultSupervisor {
			pid.supervisor.Reset()
		}
	}
	pid.mailbox.Dispose()
	pid.toggleFlag(isSingletonFlag, false)
	pid.toggleFlag(isRelocatableFlag, true)
	if pid.dependencies != nil {
		pid.dependencies.Reset()
	}
	pid.toggleFlag(passivationPausedFlag, false)
	pid.toggleFlag(passivatingFlag, false)
	pid.toggleFlag(passivationSkipNextFlag, false)
}

// freeWatchers releases all the actors watching this actor
func (pid *PID) freeWatchers(ctx context.Context) {
	logger := pid.logger
	logger.Debugf("%s freeing all watcher actors...", pid.Name())
	tree := pid.ActorSystem().tree()

	watchers := tree.watchers(pid)
	if len(watchers) > 0 {
		// this call will be fast no need of parallel processing
		for _, watcher := range watchers {
			watcher := watcher
			terminated := &goaktpb.Terminated{
				Address:      pid.Address().Address,
				TerminatedAt: timestamppb.Now(),
			}

			if watcher.IsRunning() {
				logger.Debugf("watcher=(%s) releasing watched=(%s)", watcher.Name(), pid.Name())
				// ignore error here because the watcher is running
				_ = pid.Tell(ctx, watcher, terminated)
				watcher.UnWatch(pid)
				logger.Debugf("watcher=(%s) released watched=(%s)", watcher.Name(), pid.Name())
			}
		}

		logger.Debugf("%s successfully frees all watcher actors...", pid.Name())
		return
	}
	logger.Debugf("%s does not have any watcher actors. Maybe already freed.", pid.Name())
}

// freeWatchees releases all actors that have been watched by this actor
func (pid *PID) freeWatchees() error {
	logger := pid.logger
	logger.Debugf("%s freeing all watched actors...", pid.Name())

	tree := pid.ActorSystem().tree()
	watchees := tree.watchees(pid)
	if len(watchees) > 0 {
		// this call will be fast no need of parallel processing
		for _, watched := range watchees {
			logger.Debugf("watcher=(%s) unwatching actor=(%s)", pid.Name(), watched.Name())
			pid.UnWatch(watched)
			logger.Debugf("watcher=(%s) successfully unwatch actor=(%s)", pid.Name(), watched.Name())
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
	node, ok := tree.node(pid.ID())
	if !ok {
		pid.logger.Debugf("%s node not found in the actors tree", pid.Name())
		return nil
	}

	children := tree.children(pid)
	if len(children) > 0 {
		eg, ctx := errgroup.WithContext(ctx)
		for _, child := range children {
			eg.Go(func() error {
				logger.Debugf("parent=(%s) disowning descendant=(%s)", pid.Name(), child.Name())
				pid.UnWatch(child)
				node.descendants.Delete(child.ID())
				if child.IsSuspended() || child.IsRunning() {
					if err := child.Shutdown(ctx); err != nil {
						// only return error when the actor is not dead
						// because if the actor is dead it means that
						// it has been stopped or passivated already
						// this can happen due to timing issue
						if !errors.Is(err, gerrors.ErrDead) {
							return fmt.Errorf("parent=(%s) failed to disown descendant=(%s): %w", pid.Name(), child.Name(), err)
						}
					}
					logger.Debugf("parent=(%s) successfully disown descendant=(%s)", pid.Name(), child.Name())
				}
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

// tryPassivation evaluates the current passivation strategy and, when conditions are met,
// stops the actor to free up resources.
//
// Returns true when the actor was successfully passivated.
func (pid *PID) tryPassivation(reason string) bool {
	if pid.passivationStrategy == nil || isLongLivedPassivationStrategy(pid.passivationStrategy) {
		return false
	}

	if actoryStem := pid.ActorSystem(); actoryStem != nil {
		if actoryStem.isStopping() {
			return false
		}
	}

	if pid.compareAndSwapFlag(passivationSkipNextFlag, true, false) {
		pid.logger.Debugf("passivation decision skipped once for %s due to recent reinstate", pid.Name())
		return false
	}

	if pid.isFlagEnabled(stoppingFlag) ||
		pid.isFlagEnabled(suspendedFlag) ||
		pid.isFlagEnabled(passivationPausedFlag) {
		pid.logger.Infof("No need to passivate actor=%s", pid.Name())
		return false
	}

	pid.logger.Infof("passivation mode has been triggered for actor=%s (%s)...", pid.Name(), reason)
	pid.toggleFlag(passivatingFlag, true)
	defer pid.toggleFlag(passivatingFlag, false)

	pid.stopLocker.Lock()
	defer pid.stopLocker.Unlock()

	if pid.compareAndSwapFlag(passivationSkipNextFlag, true, false) {
		pid.logger.Debugf("passivation decision aborted for %s due to reinstate observed during critical section", pid.Name())
		return false
	}

	pid.unregisterPassivation()

	ctx := context.Background()
	if err := pid.doStop(ctx); err != nil {
		pid.logger.Errorf("failed to passivate actor=(%s): reason=(%v)", pid.Name(), err)
		return false
	}

	if pid.eventsStream != nil {
		event := &goaktpb.ActorPassivated{
			Address:      pid.Address().Address,
			PassivatedAt: timestamppb.Now(),
		}
		pid.eventsStream.Publish(eventsTopic, event)
	}

	pid.logger.Infof("actor=%s successfully passivated", pid.Name())
	return true
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
	defer func() {
		pid.toggleFlag(runningFlag, false)
		pid.reset()
	}()

	// stop supervisor loop
	pid.stopSupervisionLoop()

	if pid.remoting != nil {
		pid.remoting.Close()
	}

	if err := chain.
		New(chain.WithFailFast()).
		AddRunner(func() error { return pid.freeWatchees() }).
		AddRunner(func() error { return pid.freeChildren(ctx) }).
		Run(); err != nil {
		return err
	}

	stopContext := newContext(ctx, pid.Name(), pid.system, pid.Dependencies()...)

	// run the PostStop hook and let watchers know
	// you are terminated
	if err := chain.
		New(chain.WithFailFast()).
		AddRunner(func() error { return pid.actor.PostStop(stopContext) }).
		AddRunner(func() error { pid.freeWatchers(ctx); return nil }).
		Run(); err != nil {
		return err
	}

	pid.logger.Infof("shutdown process completed for actor=%s...", pid.Name())
	return nil
}

// startSupervision send error notifications to the parent actor
func (pid *PID) startSupervision() {
	pid.supervisionStopRequested.Store(false)
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

// stopSupervisionLoop stops the supervision loop
// it is safe to call this method multiple times
func (pid *PID) stopSupervisionLoop() {
	if pid.supervisionStopSignal == nil {
		return
	}

	if pid.supervisionStopRequested.Load() {
		return
	}

	if pid.supervisionStopRequested.CompareAndSwap(false, true) {
		select {
		case pid.supervisionStopSignal <- registry.Unit{}:
			return
		default:
			// channel already has a stop signal queued
			pid.supervisionStopRequested.Store(false)
		}
	}
}

// notifyParent sends a notification to the parent actor
func (pid *PID) notifyParent(signal *supervisionSignal) {
	if signal == nil || errors.Is(signal.Err(), gerrors.ErrDead) {
		return
	}

	// find a directive for the given error or check whether there
	// is a directive for any error type
	directive, ok := pid.supervisor.Directive(signal.Err())
	if !ok {
		// let us check whether we have all errors directive
		directive, ok = pid.supervisor.Directive(new(gerrors.AnyError))
		if !ok {
			pid.logger.Debugf("No supervisor directive found for error: %s", errorType(signal.Err()))
			pid.suspend(signal.Err().Error())
			return
		}
	}

	pid.logger.Debugf("%s supervisor directive %s", pid.Name(), directive.String())

	// create the message to send to the parent
	actual, _ := anypb.New(signal.Msg())
	msg := &internalpb.Panicking{
		ActorId:      pid.ID(),
		ErrorMessage: signal.Err().Error(),
		Message:      actual,
		Timestamp:    signal.Timestamp(),
	}

	switch directive {
	case StopDirective:
		msg.Directive = &internalpb.Panicking_Stop{
			Stop: new(internalpb.StopDirective),
		}
	case RestartDirective:
		msg.Directive = &internalpb.Panicking_Restart{
			Restart: &internalpb.RestartDirective{
				MaxRetries: pid.supervisor.MaxRetries(),
				Timeout:    int64(pid.supervisor.Timeout()),
			},
		}
	case ResumeDirective:
		msg.Directive = &internalpb.Panicking_Resume{
			Resume: &internalpb.ResumeDirective{},
		}
	case EscalateDirective:
		msg.Directive = &internalpb.Panicking_Escalate{
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

	if parent := pid.Parent(); parent != nil && !parent.Equals(pid.ActorSystem().NoSender()) {
		pid.logger.Warnf("%s's child actor=(%s) is failing: Err=%s", parent.Name(), pid.Name(), msg.GetErrorMessage())
		pid.logger.Infof("%s activates [strategy=%s, directive=%s] for failing child actor=(%s)",
			parent.Name(),
			pid.supervisor.Strategy(),
			directive,
			pid.Name())

		// For ResumeDirective, avoid suspending to minimize timing windows where the child appears
		// temporarily "not running" to observers. For other directives, keep suspension semantics.
		if directive == ResumeDirective {
			// Always skip the next passivation decision once to avoid immediate stop after resume.
			pid.toggleFlag(passivationSkipNextFlag, true)
			// If the actor was already suspended due to a prior signal, reinstate immediately.
			if pid.IsSuspended() {
				pid.doReinstate()
			}
			return
		}

		// suspend the actor until the parent takes an action based on strategy/directive
		pid.suspend(msg.GetErrorMessage())

		// notify parent about the failure
		_ = pid.Tell(context.Background(), parent, msg)
		return
	}

	// no parent found, just suspend the actor
	pid.logger.Warnf("%s has no parent to notify about its failure: Err=%s", pid.Name(), msg.GetErrorMessage())
	pid.suspend(msg.GetErrorMessage())
}

// handleReceivedError sends message to deadletter synthetic actor
func (pid *PID) handleReceivedError(receiveCtx *ReceiveContext, err error) {
	// the message is lost
	if pid.eventsStream == nil {
		return
	}

	// skip system messages
	switch receiveCtx.Message().(type) {
	case *goaktpb.PostStart, *goaktpb.Terminated:
		return
	default:
		// pass through
	}

	system := pid.ActorSystem()
	sender := address.NoSender()
	if system != nil {
		sender = system.NoSender().Address()
	}

	if senderPID := receiveCtx.Sender(); senderPID != nil {
		if system == nil || !senderPID.Equals(system.NoSender()) {
			sender = senderPID.Address()
		}
	}

	receiver := pid.Address()
	if receiver == nil {
		return
	}

	ctx := context.Background()
	pid.toDeadletter(ctx, sender, receiver, receiveCtx.Message(), err)
}

// toDeadletter sends a message to the deadletter actor
func (pid *PID) toDeadletter(ctx context.Context, from, to *address.Address, message proto.Message, err error) {
	deadletter := pid.ActorSystem().getDeadletter()
	msg, _ := anypb.New(message)

	// send the message to the deadletter actor
	_ = pid.Tell(ctx,
		deadletter,
		&internalpb.SendDeadletter{
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
func (pid *PID) handleCompletion(ctx context.Context, config *pipeConfig, completion *taskCompletion) {
	// defensive programming
	if completion == nil ||
		completion.Receiver == nil ||
		completion.Receiver == pid.ActorSystem().NoSender() ||
		completion.Task == nil {
		pid.logger.Error(gerrors.ErrUndefinedTask)
		return
	}

	// apply timeout if provided
	var cancel context.CancelFunc
	if config != nil && config.timeout != nil {
		ctx, cancel = context.WithTimeout(ctx, *config.timeout)
		defer cancel()
	}

	// wrap the provided completion task into a future
	fut := future.New(completion.Task)

	// execute the task, optionally via circuit breaker
	runTask := func() (proto.Message, error) {
		if config != nil && config.circuitBreaker != nil {
			outcome, oerr := config.circuitBreaker.Execute(ctx, func(ctx context.Context) (any, error) {
				return fut.Await(ctx)
			})

			if oerr != nil {
				return nil, oerr
			}

			// no need to check the type since the future.Await returns proto.Message
			// if there is no error
			return outcome.(proto.Message), nil
		}
		return fut.Await(ctx)
	}

	result, err := runTask()
	if err != nil {
		pid.logger.Error(err)
		pid.toDeadletter(ctx, pid.Address(), pid.Address(), new(goaktpb.NoMessage), err)
		return
	}

	// make sure that the receiver is still alive
	to := completion.Receiver
	if !to.IsRunning() {
		pid.logger.Errorf("unable to pipe message to actor=(%s): not started", to.Name())
		pid.toDeadletter(ctx, pid.Address(), pid.Address(), result, gerrors.ErrDead)
		return
	}

	messageContext := newReceiveContext(ctx, pid, to, result)
	to.doReceive(messageContext)
}

// handlePanicking watches for child actor's failure and act based upon the supervisory strategy
func (pid *PID) handlePanicking(cid *PID, msg *internalpb.Panicking) {
	if cid.ID() == msg.GetActorId() {
		directive := msg.GetDirective()
		includeSiblings := msg.GetStrategy() == internalpb.Strategy_STRATEGY_ONE_FOR_ALL

		switch d := directive.(type) {
		case *internalpb.Panicking_Stop:
			pid.handleStopDirective(cid, includeSiblings)
		case *internalpb.Panicking_Restart:
			pid.handleRestartDirective(cid,
				d.Restart.GetMaxRetries(),
				time.Duration(d.Restart.GetTimeout()),
				includeSiblings)
		case *internalpb.Panicking_Resume:
			// simply reinstate the actor
			cid.doReinstate()
		case *internalpb.Panicking_Escalate:
			// forward the message to the parent and suspend the actor
			_ = cid.Tell(context.Background(), pid, &goaktpb.PanicSignal{
				Reason:    msg.GetErrorMessage(),
				Message:   msg.GetMessage(),
				Timestamp: msg.GetTimestamp(),
			})
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
		siblings := tree.siblings(cid)
		if len(siblings) > 0 {
			// add siblings to the list of actors to stop
			pids = append(pids, siblings...)
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
		siblings := tree.siblings(cid)
		if len(siblings) > 0 {
			// add siblings to the list of actors to restart
			pids = append(pids, siblings...)
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
	return address.NewWithParent(name,
		pid.Address().System(),
		pid.Address().Host(),
		pid.Address().Port(),
		pid.Address())
}

// suspend puts the actor in a suspension mode.
func (pid *PID) suspend(reason string) {
	pid.logger.Infof("%s going into suspension mode", pid.Name())
	pid.toggleFlag(suspendedFlag, true)
	// pause passivation loop
	pid.pausePassivation()
	// stop the supervisor loop
	pid.stopSupervisionLoop()
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
		message = &internalpb.DeadlettersCountRequest{
			ActorId: &name,
		}
	)
	if to.IsRunning() {
		// ask the deadletter actor for the count
		// using the default ask timeout
		// note: no need to check for error because this call is internal
		message, _ := from.Ask(ctx, to, message, DefaultAskTimeout)
		// cast the response received from the deadletter
		deadlettersCount := message.(*internalpb.DeadlettersCountResponse)
		return deadlettersCount.GetTotalCount()
	}
	return 0
}

// fireSystemMessage sends a system-level message to the specified PID by creating a receive context and invoking the message handling logic.
func (pid *PID) fireSystemMessage(ctx context.Context, message proto.Message) {
	receiveContext := getContext()
	noSender := pid.ActorSystem().NoSender()
	receiveContext.build(ctx, noSender, pid, message, true)
	pid.doReceive(receiveContext)
}

func (pid *PID) doReinstate() {
	pid.logger.Infof("%s has been reinstated", pid.Name())
	// if we're already running and not suspended, nothing to do
	if pid.IsRunning() && !pid.IsSuspended() {
		return
	}
	pid.toggleFlag(suspendedFlag, false)

	// Guard against a pending passivation path that might have just crossed the threshold
	// but hasn't yet checked suspension state. Skip the next passivation decision once.
	pid.toggleFlag(passivationSkipNextFlag, true)
	// Treat reinstate as activity so any freshly registered passivation deadline
	// doesn't immediately fire before the skip guard can cancel the in-flight attempt.
	pid.markActivity(time.Now())

	// resume the supervisor loop
	pid.startSupervision()
	// resume passivation loop
	pid.resumePassivation()

	// publish an event to the events stream
	pid.eventsStream.Publish(eventsTopic, &goaktpb.ActorReinstated{
		Address:      pid.Address().Address,
		ReinstatedAt: timestamppb.Now(),
	})
}

func (pid *PID) shouldAutoPassivate() bool {
	return pid.passivationStrategy != nil && !isLongLivedPassivationStrategy(pid.passivationStrategy)
}

func (pid *PID) unregisterPassivation() {
	if pid.passivationManager != nil {
		pid.passivationManager.Unregister(pid)
	}
}

// pausePassivation pauses the passivation loop
func (pid *PID) pausePassivation() {
	if pid.passivationStrategy == nil {
		return
	}

	if pid.passivationManager != nil {
		pid.passivationManager.Pause(pid)
	}
	pid.toggleFlag(passivationPausedFlag, true)
}

// resumePassivation resumes a paused passivation
func (pid *PID) resumePassivation() {
	if pid.passivationStrategy == nil {
		return
	}

	if pid.isFlagEnabled(passivationPausedFlag) {
		pid.toggleFlag(passivationPausedFlag, false)
		if pid.passivationManager != nil {
			if pid.passivationManager.Resume(pid) {
				return
			}
		}
	}

	pid.startPassivation()
}

// startPassivation registers the passivation strategy with the shared scheduler when applicable.
// It is called when the actor is started, reinstated, or when passivation resumes.
// Long-lived strategies opt out of automatic passivation.
func (pid *PID) startPassivation() {
	if !pid.shouldAutoPassivate() || pid.passivationManager == nil {
		return
	}

	pid.passivationManager.Register(pid, pid.passivationStrategy)
}

// healthCheck is called whenever an actor is spawned to make sure it is ready and functional.
// It has to be very fast for a smooth operation.
func (pid *PID) healthCheck(ctx context.Context) error {
	logger := pid.logger
	logger.Infof("%s readiness probe...", pid.Name())

	if pid.isFlagEnabled(isSystemFlag) {
		logger.Debugf("%s is a system actor. No need for readiness probe.", pid.Name())
		return nil
	}

	message := new(internalpb.HealthCheckRequest)
	timeout := pid.initTimeout.Load()
	numretries := pid.initMaxRetries.Load()
	noSender := pid.ActorSystem().NoSender()

	retrier := retry.NewRetrier(int(numretries), timeout, timeout)
	err := retrier.RunContext(ctx, func(ctx context.Context) error {
		_, err := noSender.Ask(ctx, pid, message, DefaultAskTimeout)
		return err
	})

	if err != nil {
		logger.Errorf("%s readiness probe failed: %v", pid.Name(), err)
		// attempt to shut down the actor to free pre-allocated resources
		return errors.Join(err, pid.Shutdown(ctx))
	}

	logger.Infof("%s readiness probe successfully completed.", pid.Name())
	return nil
}

func (pid *PID) toWireActor() (*internalpb.Actor, error) {
	dependencies, err := codec.EncodeDependencies(pid.Dependencies()...)
	if err != nil {
		return nil, err
	}

	return &internalpb.Actor{
		Address:             pid.Address().Address,
		Type:                registry.Name(pid.Actor()),
		IsSingleton:         pid.IsSingleton(),
		Relocatable:         pid.IsRelocatable(),
		PassivationStrategy: codec.EncodePassivationStrategy(pid.PassivationStrategy()),
		Dependencies:        dependencies,
		EnableStash:         pid.stashBuffer != nil && pid.stashBuffer.box != nil,
		Role:                pid.Role(),
	}, nil
}

// isLongLivedStrategy checks whether the given strategy is a long-lived strategy
// This is used to determine if the actor should be treated as a long-lived actor
// and not passivated automatically.
func isLongLivedPassivationStrategy(strategy passivation.Strategy) bool {
	_, ok := strategy.(*passivation.LongLivedStrategy)
	return ok
}
