// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package actor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flowchartsman/retry"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/datacenter"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/chain"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/future"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/locker"
	"github.com/tochemey/goakt/v4/internal/metric"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/ticker"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/supervisor"
)

// specifies the state in which the PID is
// regarding message processing

const (
	// idle means there are no messages to process
	idle int32 = iota
	// busy means the PID is processing messages
	busy
)

// passivationTouchInterval controls how frequently Touch is called on the
// passivation manager. For a 2-minute default timeout, refreshing every
// 100ms means the deadline is at most 0.08% stale — well within tolerance.
// This avoids acquiring the passivation manager's mutex on every message.
const passivationTouchInterval = int64(100 * time.Millisecond)

// taskCompletion is used to track completions' taskCompletion
// to pipe the result to the appropriate PID
type taskCompletion struct {
	Receiver *PID
	Task     func() (any, error)
}

type restartNode struct {
	pid      *PID
	children []*restartNode
}

// PID is the sole actor reference in GoAkt. It is location-transparent:
// a PID may represent either a live local actor or a lightweight handle
// for an actor on a remote node. Use IsLocal / IsRemote to distinguish.
//
// # Location-transparent operations (work for both local and remote PIDs)
//
//   - Identity: Name, ID, Address, Kind, Role, Equals
//   - State queries: IsLocal, IsRunning, IsSuspended, IsSingleton, IsRelocatable, IsStopping
//   - Messaging: Tell, Ask, BatchTell, BatchAsk
//   - Remote helpers: RemoteLookup, RemoteStop, RemoteReSpawn
//
// # Local-only operations (return ErrNotLocal for remote PIDs)
//
// Lifecycle: Stop, Restart, Shutdown, SpawnChild, Reinstate, ReinstateNamed
// Tree navigation: Child, Children, ChildrenCount, Parent
// Watch: Watch, UnWatch
// Name-based messaging: SendAsync, SendSync, PipeTo, PipeToName, DiscoverActor
//
// # Query methods (safe for remote, return zero values)
//
// Actor, ProcessedCount, RestartCount, LatestProcessedDuration, LatestActivityTime,
// StashSize, PassivationStrategy, Dependencies, Dependency, Uptime, Metric
//
// # Nil for remote PIDs
//
// ActorSystem returns nil for remote PIDs — always guard with IsLocal before use.
type PID struct {
	_ locker.NoCopy
	// specifies the message processor
	actor Actor

	// specifies the actor address
	address *address.Address
	path    Path

	// latestReceiveTimeNano stores the latest receive timestamp as UnixNano (int64).
	// Using atomic.Int64 instead of atomic.Time avoids the interface-boxing
	// allocation that atomic.Time.Store incurs on every message (~24 bytes).
	latestReceiveTimeNano atomic.Int64
	lastPassivationTouch  atomic.Int64 // UnixNano of last passivation Touch call (for coalescing)
	latestReceiveDuration atomic.Duration

	// specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	initMaxRetries atomic.Int32

	// specifies the init timeout.
	// the default initialization timeout is 1s
	initTimeout atomic.Duration

	// specifies the actor mailbox
	mailbox Mailbox

	// the actor actorSystem
	actorSystem ActorSystem

	// specifies the logger to use
	logger log.Logger

	// various lockers to protect the PID fields
	// in a concurrent environment
	fieldsLocker sync.RWMutex
	stopLocker   sync.Mutex

	// specifies the actor behavior stack
	behaviorStack *behaviorStack

	// stash settings
	stashState *stashState
	// reentrancy settings
	reentrancy *reentrancyState

	// define an events stream
	eventsStream eventstream.Stream

	// set the metrics settings
	restartCount   atomic.Int64
	processedCount atomic.Int64
	failureCount   atomic.Int64
	reinstateCount atomic.Int64

	// supervisor strategy
	supervisor               *supervisor.Supervisor
	supervisionChan          chan *supervisionSignal
	supervisionStopSignal    chan types.Unit
	supervisionStopRequested atomic.Bool

	// atomic flag indicating whether the actor is processing messages
	processing atomic.Int32

	remoting remoteclient.Client

	startedAt atomic.Int64
	state     atomic.Uint32

	// the list of dependencies
	dependencies *xsync.Map[string, extension.Dependency]

	passivationStrategy passivation.Strategy
	passivationManager  *passivationManager
	msgCountPassivation atomic.Bool // true when passivationStrategy is MessagesCountBasedStrategy; set once at init

	// this is used to specify a role the actor belongs to
	// this field is optional and will be set when the actor is created with a given role
	role *string

	// only set this when the actor is a singleton
	singletonSpec *singletonSpec

	metricProvider *metric.Provider
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
		latestReceiveTimeNano: atomic.Int64{},
		logger:                log.New(log.ErrorLevel, os.Stderr),
		address:               address,
		mailbox:               NewUnboundedMailbox(),
		supervisionChan:       make(chan *supervisionSignal, 1),
		supervisionStopSignal: make(chan types.Unit, 1),
		path:                  newPath(address),
	}

	pid.initMaxRetries.Store(DefaultInitMaxRetries)
	pid.latestReceiveDuration.Store(0)
	pid.processedCount.Store(0)
	pid.startedAt.Store(0)
	pid.restartCount.Store(0)
	pid.failureCount.Store(0)
	pid.reinstateCount.Store(0)
	pid.initTimeout.Store(DefaultInitTimeout)
	pid.processing.Store(int32(idle))
	pid.setState(relocationState, true)

	for _, opt := range opts {
		opt(pid)
	}

	if pid.supervisor == nil {
		pid.supervisor = supervisor.NewSupervisor()
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

	if err := pid.registerMetrics(); err != nil {
		return nil, err
	}

	pid.fireSystemMessage(ctx, new(PostStart))

	pid.startedAt.Store(time.Now().Unix())
	return pid, nil
}

// newRemotePID creates a lightweight PID that represents an actor on a remote node.
//
// A remote PID holds only the actor address and a handle to the remoting layer.
// It carries no mailbox, no supervision state, no behavior stack, and no
// actor-system reference. All messaging operations on a remote PID must be
// dispatched through the remoting layer rather than the local mailbox.
//
// This constructor is intentionally lean: it performs no allocations beyond the
// PID struct itself and sets exactly the fields required for identity and routing.
func newRemotePID(addr *address.Address, remoting remoteclient.Client) *PID {
	pid := &PID{
		address:  addr,
		remoting: remoting,
		path:     newPath(addr),
	}
	pid.setState(remoteState, true)
	return pid
}

// IsLocal reports whether this PID represents an actor running in the local actor system.
//
// Local PIDs have a live mailbox, a behavior stack, and a full actor-system reference.
// Use IsLocal to distinguish in-process actors from remote handles before performing
// operations that are only meaningful locally (e.g. inspecting children, passivation, etc.).
func (pid *PID) IsLocal() bool {
	if pid == nil {
		return false
	}
	return !pid.isStateSet(remoteState)
}

// IsRemote reports whether this PID is a lightweight handle for an actor on a remote node.
//
// Remote PIDs hold only the actor's address and a remoting handle; they carry no
// mailbox, supervisor, or actor-system reference. Messaging through a remote PID
// is routed via the remoting layer rather than a local mailbox enqueue.
func (pid *PID) IsRemote() bool {
	if pid == nil {
		return false
	}
	return pid.isStateSet(remoteState)
}

// Role returns the cluster placement role assigned to this actor, or nil if none was set.
// Placement roles constrain on which nodes an actor may be started or relocated.
// This setting only affects SpawnOn and SpawnSingleton; local-only spawns ignore it.
func (pid *PID) Role() *string {
	pid.fieldsLocker.RLock()
	role := pid.role
	pid.fieldsLocker.RUnlock()
	return role
}

// Dependencies returns all dependencies registered with this actor.
// Dependencies are injected at spawn time via SpawnOptions and remain accessible
// for the lifetime of the actor.
func (pid *PID) Dependencies() []extension.Dependency {
	if pid.dependencies == nil {
		return nil
	}
	return pid.dependencies.Values()
}

// Dependency returns the registered dependency with the given identifier, or nil if not found.
func (pid *PID) Dependency(dependencyID string) extension.Dependency {
	if pid.dependencies == nil {
		return nil
	}
	if dependency, ok := pid.dependencies.Get(dependencyID); ok {
		return dependency
	}
	return nil
}

// Metric returns a snapshot of the actor's runtime metrics.
// Returns nil when the actor is not running. Cluster data is not included.
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
			failureCount:            uint64(pid.failureCount.Load()),
			reinstateCount:          uint64(pid.reinstateCount.Load()),
		}
	}
	return nil
}

// Uptime returns the number of seconds elapsed since the actor started.
// Returns zero when the actor is not running.
func (pid *PID) Uptime() int64 {
	if pid.IsRunning() {
		return time.Now().Unix() - pid.startedAt.Load()
	}
	return 0
}

// ID returns the actor unique identifier, which is its canonical address string.
func (pid *PID) ID() string {
	if path := pid.Path(); path != nil {
		return path.String()
	}
	return ""
}

// Name returns the actor name.
func (pid *PID) Name() string {
	if path := pid.Path(); path != nil {
		return path.Name()
	}
	return ""
}

// Equals reports whether pid and to refer to the same actor.
func (pid *PID) Equals(to *PID) bool {
	if pid == nil && to == nil {
		return true
	}

	if pid == nil || to == nil {
		return false
	}

	return strings.EqualFold(pid.ID(), to.ID())
}

// Actor returns the underlying Actor implementation.
// Returns nil for remote PIDs.
func (pid *PID) Actor() Actor {
	return pid.actor
}

// Kind returns the reflected type name of the underlying Actor implementation.
// Returns an empty string for remote PIDs.
func (pid *PID) Kind() string {
	if pid.actor == nil {
		return ""
	}
	return types.Name(pid.actor)
}

// Child returns the running child PID with the given name.
// Returns ErrNotLocal for remote PIDs, ErrDead if this actor is not running,
// or ErrActorNotFound when no such child exists or the child is stopped.
func (pid *PID) Child(name string) (*PID, error) {
	if err := pid.assertLocal(); err != nil {
		return nil, err
	}
	if !pid.IsRunning() {
		return nil, gerrors.ErrDead
	}

	childAddress := pid.childAddress(name)
	if cidNode, ok := pid.actorSystem.tree().node(childAddress.String()); ok {
		cid := cidNode.value()
		if cid.IsRunning() {
			return cid, nil
		}
	}
	return nil, gerrors.NewErrActorNotFound(childAddress.String())
}

// Parent returns the parent PID in the actor tree.
// Returns nil for root actors and remote PIDs.
func (pid *PID) Parent() *PID {
	if pid.IsRemote() {
		return nil
	}
	tree := pid.ActorSystem().tree()
	parent, ok := tree.parent(pid)
	if !ok {
		return nil
	}
	return parent
}

// Children returns all direct child PIDs that are currently running.
// Returns nil for remote PIDs.
func (pid *PID) Children() []*PID {
	if pid.IsRemote() {
		return nil
	}
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

// Stop signals the given child PID to shut down after it finishes processing its current message.
// When cid is remote, Stop delegates to RemoteStop via the remoting layer.
// Returns ErrRemotingDisabled when cid is remote but remoting is not configured,
// ErrDead if this actor is not running, and ErrActorNotFound when cid is not a child of this actor.
// It is a no-op when cid is already stopped.
func (pid *PID) Stop(ctx context.Context, cid *PID) error {
	if cid.IsRemote() {
		r := cid.remoting
		if r == nil {
			r = pid.remoting
		}
		if r == nil {
			return gerrors.ErrRemotingDisabled
		}
		return r.RemoteStop(ctx, cid.getAddress().Host(), cid.getAddress().Port(), cid.Name())
	}

	if !pid.IsRunning() {
		return gerrors.ErrDead
	}

	if cid == nil || cid == pid.ActorSystem().NoSender() {
		return gerrors.ErrUndefinedActor
	}

	// If the child is not running and not suspended, it's not found.
	if !cid.IsRunning() && !cid.IsSuspended() {
		return gerrors.NewErrActorNotFound(cid.Path().String())
	}

	// Check if the child exists in the actor tree.
	pid.fieldsLocker.RLock()
	tree := pid.actorSystem.tree()
	_, exists := tree.node(cid.Path().String())
	pid.fieldsLocker.RUnlock()

	if !exists {
		return gerrors.NewErrActorNotFound(cid.Path().String())
	}

	// Attempt to shutdown the child.
	if err := cid.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}

// IsRunning reports whether the actor is alive and ready to process messages.
// Returns false when the actor has not started, is stopping, passivating, or suspended.
func (pid *PID) IsRunning() bool {
	if pid == nil {
		return false
	}
	// Single atomic load, then check all flags with bitwise operations
	state := pid.state.Load()
	return state&uint32(runningState) != 0 &&
		state&uint32(stoppingState) == 0 &&
		state&uint32(passivatingState) == 0 &&
		state&uint32(suspendedState) == 0
}

// IsSuspended reports whether the actor is suspended due to a fault.
func (pid *PID) IsSuspended() bool {
	return pid.isStateSet(suspendedState)
}

// IsSingleton reports whether the actor was spawned as a cluster singleton.
// A singleton exists at most once across the entire cluster and is always hosted on the oldest node.
// When that node leaves unexpectedly the singleton is restarted on the new oldest node.
func (pid *PID) IsSingleton() bool {
	return pid.isStateSet(singletonState)
}

// IsRelocatable reports whether the actor may be relocated to another node if its host node shuts down unexpectedly.
// Actors are relocatable by default; pass WithRelocationDisabled at spawn time to opt out.
func (pid *PID) IsRelocatable() bool {
	return pid.isStateSet(relocationState)
}

// IsStopping reports whether the actor has begun stopping, either explicitly or via passivation.
func (pid *PID) IsStopping() bool {
	return pid.isStateSet(stoppingState) || pid.isStateSet(passivatingState)
}

// PassivationStrategy returns the passivation strategy configured for this actor.
func (pid *PID) PassivationStrategy() passivation.Strategy {
	pid.fieldsLocker.RLock()
	strategy := pid.passivationStrategy
	pid.fieldsLocker.RUnlock()
	return strategy
}

// ActorSystem returns the actor system this PID belongs to.
// Returns nil for remote PIDs — check pid.IsLocal() before use.
func (pid *PID) ActorSystem() ActorSystem {
	pid.fieldsLocker.RLock()
	sys := pid.actorSystem
	pid.fieldsLocker.RUnlock()
	return sys
}

// Path returns the actor path (location-transparent view of host, port, name, system, parent).
// Returns nil when called on a nil PID or when the address is nil.
// Use PathToAddress to convert to *address.Address when needed for RemoteTell, RemoteAsk, etc.
//
// No lock is taken: path is written exactly once during construction and never mutated.
func (pid *PID) Path() Path {
	if pid == nil {
		return nil
	}
	return pid.path
}

// Restart restarts this actor and all running or suspended descendants.
//
// The subtree is snapshotted, the same parent/child topology is rebuilt, and each
// actor is re-initialized via its PreStart hook. Suspended actors are reinitialized
// without a prior shutdown step; non-running descendants are skipped entirely.
// Mailboxes are not preserved — queued or in-flight messages may be dropped.
//
// If any descendant fails to restart, Restart returns that error and the subtree
// may be only partially recovered. The target actor is left non-running on failure.
//
// When pid is remote, Restart delegates to RemoteReSpawn via the remoting layer.
// Returns ErrRemotingDisabled when pid is remote but remoting is not configured,
// and ErrUndefinedActor for nil receivers.
func (pid *PID) Restart(ctx context.Context) error {
	if pid == nil || pid.Path() == nil {
		return gerrors.ErrUndefinedActor
	}

	if pid.IsRemote() {
		if pid.remoting == nil {
			return gerrors.ErrRemotingDisabled
		}
		if _, err := pid.remoting.RemoteReSpawn(ctx, pid.Path().Host(), pid.Path().Port(), pid.Name()); err != nil {
			return err
		}
		return nil
	}

	pid.logger.Debugf("Restarting Actor (%s)", pid.Name())
	actorSystem := pid.ActorSystem()
	tree := actorSystem.tree()
	deathWatch := actorSystem.getDeathWatch()

	// snapshot all alive descendants before shutdown so we can rebuild the full subtree
	// even after the death watch removes entries from the tree.
	subtree := buildRestartSubtree(pid, tree)
	// get the parent node of the actor
	parent := pid.ActorSystem().NoSender()
	if ppid, ok := tree.parent(pid); ok {
		parent = ppid
	}

	return restartSubtree(ctx, subtree, parent, tree, deathWatch, actorSystem)
}

// RestartCount returns the total number of times this actor has been restarted.
func (pid *PID) RestartCount() int {
	count := pid.restartCount.Load()
	return int(count)
}

// ChildrenCount returns the number of direct children currently running.
func (pid *PID) ChildrenCount() int {
	descendants := pid.Children()
	return len(descendants)
}

// ProcessedCount returns the total number of messages this actor has processed.
func (pid *PID) ProcessedCount() int {
	count := pid.processedCount.Load()
	return int(count)
}

// LatestProcessedDuration returns the elapsed time since the most recent message was processed.
func (pid *PID) LatestProcessedDuration() time.Duration {
	nanos := pid.latestReceiveTimeNano.Load()
	if nanos == 0 {
		return 0
	}
	pid.latestReceiveDuration.Store(time.Since(time.Unix(0, nanos)))
	return pid.latestReceiveDuration.Load()
}

// SpawnChild creates, starts, and supervises a child actor with the given name.
// If a running child with the same name already exists, its PID is returned without creating a new one.
// Returns ErrNotLocal for remote PIDs and ErrDead if this actor is not running.
func (pid *PID) SpawnChild(ctx context.Context, name string, actor Actor, opts ...SpawnOption) (*PID, error) {
	if err := pid.assertLocal(); err != nil {
		return nil, err
	}
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
	tree := pid.actorSystem.tree()
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
		withActorSystem(pid.actorSystem),
		withEventsStream(pid.eventsStream),
		withInitTimeout(pid.initTimeout.Load()),
		withRemoting(pid.remoting),
		withPassivationManager(pid.passivationManager),
		withMetricProvider(pid.metricProvider),
		withRelocationDisabled(), // by default child is not relocatable
	}

	if spawnConfig.mailbox != nil {
		pidOptions = append(pidOptions, withMailbox(spawnConfig.mailbox))
	}

	// set the supervisor strategies when defined
	if spawnConfig.supervisor != nil {
		pidOptions = append(pidOptions, withSupervisor(spawnConfig.supervisor))
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

	if !cid.isStateSet(systemState) {
		pid.ActorSystem().increaseActorsCounter()
	}

	// no need to handle the error because the given parent exist and running
	// that check was done in the above lines
	_ = tree.addNode(pid, cid)
	tree.addWatcher(cid, pid.ActorSystem().getDeathWatch())

	eventsStream := pid.eventsStream
	if eventsStream != nil {
		eventsStream.Publish(eventsTopic, NewActorChildCreated(cid.ID(), pid.ID()))
	}

	// set the actor in the given actor system registry
	return cid, pid.ActorSystem().putActorOnCluster(cid)
}

// Reinstate resumes a suspended actor, allowing it to process messages again.
// The actor's internal state is preserved across the suspension. It is a no-op when cid
// is already running or not suspended.
// Returns ErrNotLocal for remote PIDs, ErrDead if this actor is not running,
// and ErrActorNotFound when cid is not known to the actor system.
//
// See also: ReinstateNamed for name-based reinstatement.
func (pid *PID) Reinstate(cid *PID) error {
	ctx := context.Background()

	// When the caller is remote, delegate to RemoteReinstate via the remoting layer.
	if pid.IsRemote() {
		if pid.remoting == nil {
			return gerrors.ErrRemotingDisabled
		}

		if cid == nil || cid.Path() == nil {
			return gerrors.ErrUndefinedActor
		}

		return pid.remoting.RemoteReinstate(ctx, cid.Path().Host(), cid.Path().Port(), cid.Name())
	}

	if !pid.IsRunning() {
		return gerrors.ErrDead
	}

	if cid.Equals(pid.ActorSystem().NoSender()) {
		return gerrors.ErrUndefinedActor
	}

	// this call is necessary because the reference to the actor may have been
	// kept elsewhere and the actor may have been stopped and removed from the system
	actual, err := pid.ActorSystem().ActorOf(ctx, cid.Name())
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

// ReinstateNamed resumes a suspended actor identified by name.
// Unlike Reinstate, the actor is looked up by name, making this method suitable for
// cluster-wide recovery where the PID may not be available locally. It is a no-op when
// the actor is already running or not suspended.
// Returns ErrNotLocal for remote PIDs, ErrDead if this actor is not running,
// ErrActorNotFound when no actor with that name exists, and ErrRemotingDisabled
// when the actor is remote but remoting has not been configured.
//
// See also: Reinstate for direct PID-based reinstatement.
func (pid *PID) ReinstateNamed(ctx context.Context, actorName string) error {
	if err := pid.assertLocal(); err != nil {
		return err
	}

	if !pid.IsRunning() {
		return gerrors.ErrDead
	}

	cid, err := pid.ActorSystem().ActorOf(ctx, actorName)
	if err != nil {
		return err
	}

	if cid.IsLocal() {
		if !cid.IsSuspended() || cid.IsRunning() {
			return nil
		}
		cid.doReinstate()
		return nil
	}

	if !pid.remotingEnabled() {
		return gerrors.ErrRemotingDisabled
	}

	addr := cid.getAddress()
	return pid.remoting.RemoteReinstate(ctx, addr.Host(), addr.Port(), actorName)
}

// StashSize returns the number of messages currently held in the stash buffer.
func (pid *PID) StashSize() uint64 {
	if pid.stashState == nil || pid.stashState.box == nil {
		return 0
	}
	return uint64(pid.stashState.box.Len())
}

// PipeTo runs task asynchronously and, on success, delivers the result to to's mailbox.
// The calling actor is not blocked; it continues processing other messages while the task runs.
// On task failure the error is forwarded to the dead-letter queue.
// Returns ErrNotLocal for remote PIDs and ErrUndefinedTask when task is nil.
func (pid *PID) PipeTo(ctx context.Context, to *PID, task func() (any, error), opts ...PipeOption) error {
	if err := pid.assertLocal(); err != nil {
		return err
	}
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

// PipeToName runs task asynchronously and, on success, delivers the result to the named actor's mailbox.
// The actor is resolved by name, providing location transparency: the caller does not need a PID.
// On task failure the error is forwarded to the dead-letter queue.
// Returns ErrNotLocal for remote PIDs and ErrUndefinedTask when task is nil.
func (pid *PID) PipeToName(ctx context.Context, actorName string, task func() (any, error), opts ...PipeOption) error {
	if err := pid.assertLocal(); err != nil {
		return err
	}
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
		runTask := func() (any, error) {
			if config != nil && config.circuitBreaker != nil {
				outcome, oerr := config.circuitBreaker.Execute(ctx, func(ctx context.Context) (any, error) {
					return fut.Await(ctx)
				})

				if oerr != nil {
					return nil, oerr
				}

				// no need to check the type since the future.Await returns proto.Message
				// if there is no error
				return outcome, nil
			}
			return fut.Await(ctx)
		}

		result, err := runTask()
		if err != nil {
			pid.logger.Error(err)
			pid.toDeadletter(ctx, pid.Path(), pid.Path(), new(NoMessage), err)
			return
		}

		// send the result to the actor identified by its name
		actorSystem := pid.ActorSystem()
		if err := actorSystem.NoSender().SendAsync(ctx, actorName, result); err != nil {
			pid.logger.Error(err)
			pid.toDeadletter(ctx, pid.Path(), pid.Path(), result, err)
			return
		}
	}()

	return nil
}

// Ask sends a synchronous message to to and waits for a response.
// It blocks until a response is received, the context is cancelled, or timeout elapses.
func (pid *PID) Ask(ctx context.Context, to *PID, message any, timeout time.Duration) (response any, err error) {
	if to.IsRemote() {
		return pid.remoteAsk(ctx, to.getAddress(), message, timeout)
	}

	if !to.IsRunning() {
		return nil, gerrors.ErrDead
	}

	if timeout <= 0 {
		return nil, gerrors.ErrInvalidTimeout
	}

	receiveContext := getContext()
	receiveContext.build(ctx, pid, to, message, false)
	responseCh := receiveContext.response

	to.doReceive(receiveContext)
	timer := timers.Get(timeout)

	select {
	case result := <-responseCh:
		timers.Put(timer)
		receiveContext.responseClosed.Store(true)
		putResponseChannel(responseCh)
		return result, nil
	case <-ctx.Done():
		err = errors.Join(ctx.Err(), gerrors.ErrRequestTimeout)
		pid.handleReceivedErrorWithMessage(pid, message, err)
		timers.Put(timer)
		receiveContext.responseClosed.Store(true)
		putResponseChannel(responseCh)
		return nil, err
	case <-timer.C:
		err = gerrors.ErrRequestTimeout
		pid.handleReceivedErrorWithMessage(pid, message, err)
		timers.Put(timer)
		receiveContext.responseClosed.Store(true)
		putResponseChannel(responseCh)
		return nil, err
	}
}

// Tell sends a message asynchronously to the target PID.
// Routing is location-transparent: remote PIDs are handled via the remoting layer.
func (pid *PID) Tell(ctx context.Context, to *PID, message any) error {
	if to.IsRemote() {
		return pid.remoteTell(ctx, to.getAddress(), message)
	}

	if !to.IsRunning() {
		return gerrors.ErrDead
	}

	receiveContext := getContext()
	receiveContext.build(ctx, pid, to, message, true)

	to.doReceive(receiveContext)
	return nil
}

// SendAsync sends a message asynchronously to the named actor.
// The actor is resolved locally first; if not found, all active datacenters are queried.
func (pid *PID) SendAsync(ctx context.Context, actorName string, message any) error {
	if err := pid.assertLocal(); err != nil {
		return err
	}
	if !pid.IsRunning() {
		return gerrors.ErrDead
	}

	// Access actorSystem directly: it is immutable after PID construction,
	// so the fieldsLocker read-lock in the public ActorSystem() accessor
	// is unnecessary here and would add contention on the hot path.
	system := pid.actorSystem

	// try to find the actor in the local datacenter
	cid, err := system.ActorOf(ctx, actorName)
	if err == nil {
		return pid.Tell(ctx, cid, message)
	}

	// Actor not found in local datacenter - check if it's a "not found" error
	if !errors.Is(err, gerrors.ErrActorNotFound) {
		// Some other error occurred (e.g., system not started, network error)
		return err
	}

	dcConfig := system.getDataCenterConfig()
	timeout := datacenter.DefaultRequestTimeout
	if dcConfig != nil {
		timeout = dcConfig.RequestTimeout
	}

	// Try to find the actor in remote datacenters
	cid, err = pid.DiscoverActor(ctx, actorName, timeout)
	if err != nil {
		return err
	}

	// Send message to the actor in the remote datacenter
	return pid.Tell(ctx, cid, message)
}

// SendSync sends a synchronous message to the named actor and waits for a response.
// The actor is resolved locally first; if not found, all active datacenters are queried.
// It blocks until a response is received, the context is cancelled, or timeout elapses.
func (pid *PID) SendSync(ctx context.Context, actorName string, message any, timeout time.Duration) (response any, err error) {
	if err := pid.assertLocal(); err != nil {
		return nil, err
	}
	if !pid.IsRunning() {
		return nil, gerrors.ErrDead
	}

	// Access actorSystem directly: immutable after PID construction.
	system := pid.actorSystem

	// try to find the actor in the local datacenter
	cid, err := system.ActorOf(ctx, actorName)
	if err == nil {
		return pid.Ask(ctx, cid, message, timeout)
	}

	// Actor not found in local datacenter - check if it's a "not found" error
	if !errors.Is(err, gerrors.ErrActorNotFound) {
		// Some other error occurred (e.g., system not started, network error)
		return nil, err
	}

	// Cap lookup timeout at 5 seconds to ensure responsive discovery
	dcConfig := system.getDataCenterConfig()
	lookupTimeout := datacenter.DefaultRequestTimeout
	if dcConfig != nil {
		lookupTimeout = dcConfig.RequestTimeout
	}

	if timeout > 0 && timeout < lookupTimeout {
		lookupTimeout = timeout
	}

	// Try to find the actor in remote datacenters
	cid, err = pid.DiscoverActor(ctx, actorName, lookupTimeout)
	if err != nil {
		return nil, err
	}

	// Send message to the actor in the remote datacenter
	return pid.Ask(ctx, cid, message, timeout)
}

// DiscoverActor locates a named actor across all active datacenters using parallel discovery.
// All datacenter endpoints are queried concurrently; the first successful result is returned
// and remaining queries are cancelled. Discovery is best-effort: a stale cache is used with
// a warning rather than failing hard.
// Returns ErrNotLocal for remote PIDs, ErrDead if not running, and ErrActorNotFound when
// the actor does not exist in any active datacenter.
func (pid *PID) DiscoverActor(ctx context.Context, actorName string, timeout time.Duration) (*PID, error) {
	if err := pid.assertLocal(); err != nil {
		return nil, err
	}
	if !pid.IsRunning() {
		return nil, gerrors.ErrDead
	}

	actorSystem := pid.actorSystem
	if actorSystem == nil || actorSystem.getDataCenterController() == nil {
		return nil, gerrors.ErrActorNotFound
	}

	dataCenterController := actorSystem.getDataCenterController()
	dataCenterRecords, stale := dataCenterController.ActiveRecords()
	if stale {
		if dataCenterController.FailOnStaleCache() {
			return nil, gerrors.ErrDataCenterStaleRecords
		}
		// Best-effort routing: proceed with stale cache but log warning
		// Stale cache may miss newly registered DCs or include inactive ones
		pid.logger.Warn("DC cache is stale, proceeding with best-effort cross-DC routing")
	}

	if len(dataCenterRecords) == 0 {
		return nil, gerrors.ErrActorNotFound
	}

	// Count total endpoints for proper channel buffer sizing
	endpointCount := 0
	for _, dcRecord := range dataCenterRecords {
		if dcRecord.State == datacenter.DataCenterActive {
			endpointCount += len(dcRecord.Endpoints)
		}
	}

	if endpointCount == 0 {
		return nil, gerrors.ErrActorNotFound
	}

	// Query remote datacenters in parallel with timeout
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type result struct {
		cid *PID
		err error
	}

	// Buffer sized for all endpoints to prevent goroutine blocking
	results := make(chan result, endpointCount)
	var wg sync.WaitGroup

	// Query each active datacenter in parallel
	for _, dcRecord := range dataCenterRecords {
		if dcRecord.State != datacenter.DataCenterActive {
			continue
		}

		// Query each endpoint in the datacenter
		for _, endpoint := range dcRecord.Endpoints {
			host, portStr, err := net.SplitHostPort(endpoint)
			if err != nil {
				continue
			}

			port, err := strconv.Atoi(portStr)
			if err != nil {
				continue
			}

			wg.Add(1)
			go func(host string, port int) {
				defer wg.Done()
				cid, lookupErr := pid.RemoteLookup(queryCtx, host, port, actorName)
				results <- result{
					cid: cid,
					err: lookupErr,
				}
			}(host, port)
		}
	}

	// Wait for all goroutines to complete and close the channel
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect first successful result
	var cid *PID
	for result := range results {
		if result.err == nil && result.cid != nil {
			cid = result.cid
			cancel() // Cancel remaining lookups
			break
		}
	}

	if cid == nil {
		return nil, gerrors.ErrActorNotFound
	}

	return cid, nil
}

// BatchTell sends multiple messages asynchronously to the given PID, in order.
// When to is remote, a single RemoteBatchTell RPC is used for efficiency.
// When to is local, each message is delivered via Tell; processing order is guaranteed.
func (pid *PID) BatchTell(ctx context.Context, to *PID, messages ...any) error {
	if !pid.IsRunning() {
		return gerrors.ErrDead
	}
	if len(messages) == 0 {
		return nil
	}
	if to.IsRemote() {
		if !pid.remotingEnabled() {
			return gerrors.ErrRemotingDisabled
		}
		return pid.remoting.RemoteBatchTell(ctx, pid.getAddress(), to.getAddress(), messages)
	}
	for _, message := range messages {
		if err := pid.Tell(ctx, to, message); err != nil {
			return err
		}
	}
	return nil
}

// BatchAsk sends multiple messages synchronously to the given PID and returns responses in the same order.
// When to is remote, a single RemoteBatchAsk RPC is used for efficiency.
// When to is local, each message is delivered via Ask; the call blocks until all responses are received or any Ask fails.
func (pid *PID) BatchAsk(ctx context.Context, to *PID, messages []any, timeout time.Duration) (responses chan any, err error) {
	if !pid.IsRunning() {
		return nil, gerrors.ErrDead
	}

	if len(messages) == 0 {
		return emptyAnyCh, nil
	}

	if to.IsRemote() {
		if !pid.remotingEnabled() {
			return nil, gerrors.ErrRemotingDisabled
		}

		resp, err := pid.remoting.RemoteBatchAsk(ctx, pid.getAddress(), to.getAddress(), messages, timeout)
		if err != nil {
			return nil, err
		}

		ch := make(chan any, len(resp))
		for _, v := range resp {
			ch <- v
		}
		close(ch)
		return ch, nil
	}

	responses = make(chan any, len(messages))
	for i := range messages {
		response, err := pid.Ask(ctx, to, messages[i], timeout)
		if err != nil {
			return nil, err
		}
		responses <- response
	}
	close(responses)
	return
}

// RemoteLookup resolves a named actor on a specific remote node and returns it as a PID.
// Returns ErrRemotingDisabled when remoting is not configured, and ErrActorNotFound
// when no actor with that name exists on the target node.
func (pid *PID) RemoteLookup(ctx context.Context, host string, port int, name string) (*PID, error) {
	if !pid.remotingEnabled() {
		return nil, gerrors.ErrRemotingDisabled
	}

	addr, err := pid.remoting.RemoteLookup(ctx, host, port, name)
	if err != nil {
		return nil, err
	}

	if addr == nil || addr.Equals(address.NoSender()) {
		return nil, gerrors.NewErrActorNotFound(name)
	}

	return newRemotePID(addr, pid.remoting), nil
}

// RemoteStop stops a named actor on the specified remote node.
// Returns ErrRemotingDisabled when remoting is not configured.
func (pid *PID) RemoteStop(ctx context.Context, host string, port int, name string) error {
	if !pid.remotingEnabled() {
		return gerrors.ErrRemotingDisabled
	}

	return pid.remoting.RemoteStop(ctx, host, port, name)
}

// RemoteReSpawn restarts a named actor on the specified remote node.
// Returns ErrRemotingDisabled when remoting is not configured.
func (pid *PID) RemoteReSpawn(ctx context.Context, host string, port int, name string) (*PID, error) {
	if !pid.remotingEnabled() {
		return nil, gerrors.ErrRemotingDisabled
	}

	addr, err := pid.remoting.RemoteReSpawn(ctx, host, port, name)
	if err != nil {
		return nil, err
	}

	if addr == nil {
		return nil, gerrors.NewErrActorNotFound(name)
	}

	// parse the address string from the response
	address, err := address.Parse(*addr)
	if err != nil {
		return nil, err
	}

	return newRemotePID(address, pid.remoting), nil
}

// Shutdown gracefully stops this actor and all its children.
// When pid is remote, Shutdown delegates to RemoteStop via the remoting layer.
// Pending mailbox messages are processed before the actor terminates.
// Returns ErrRemotingDisabled when pid is remote but remoting is not configured,
// and ErrShutdownForbidden when called on a system actor while the actor system is still running.
func (pid *PID) Shutdown(ctx context.Context) error {
	if pid.IsRemote() {
		if pid.remoting == nil {
			return gerrors.ErrRemotingDisabled
		}
		return pid.remoting.RemoteStop(ctx, pid.Path().Host(), pid.Path().Port(), pid.Name())
	}

	// we should never shutdown system actors unless the whole system is terminating
	if actoryStem := pid.ActorSystem(); actoryStem != nil {
		if !actoryStem.isStopping() && isSystemName(pid.Name()) {
			pid.logger.Warnf("Attempt to shutdown a System Actor (%s)", pid.Name())
			return gerrors.ErrShutdownForbidden
		}
	}

	pid.stopLocker.Lock()
	pid.logger.Infof("Shutdown process has started for Actor (%s)...", pid.Name())

	if !pid.isStateSet(runningState) {
		pid.logger.Infof("Actor %s is offline. Maybe it has been passivated or stopped already", pid.Name())
		pid.stopLocker.Unlock()
		return nil
	}

	pid.setState(stoppingState, true)
	pid.unregisterPassivation()

	if err := pid.doStop(ctx); err != nil {
		pid.logger.Errorf("Actor (%s) failed to cleanly stop", pid.Name())
		pid.stopLocker.Unlock()
		return err
	}

	if pid.eventsStream != nil {
		pid.eventsStream.Publish(eventsTopic, NewActorStopped(pid.ID()))
	}

	pid.stopLocker.Unlock()
	pid.logger.Infof("Actor %s successfully shutdown", pid.Name())
	return nil
}

// Watch registers pid to receive a Terminated message when cid shuts down.
// It is a no-op for remote PIDs.
func (pid *PID) Watch(cid *PID) {
	if pid.IsRemote() {
		return
	}
	pid.ActorSystem().tree().addWatcher(cid, pid)
}

// UnWatch cancels the watch previously registered by Watch for cid.
// It is a no-op for remote PIDs.
func (pid *PID) UnWatch(cid *PID) {
	if pid.IsRemote() {
		return
	}
	pid.ActorSystem().tree().removeWatcher(cid, pid)
}

// logger returns the logger set when creating the PID.
func (pid *PID) getLogger() log.Logger {
	pid.fieldsLocker.RLock()
	l := pid.logger
	pid.fieldsLocker.RUnlock()
	return l
}

// LatestActivityTime returns the timestamp of the last message received by this actor.
// Returns the zero time when no message has been processed yet.
func (pid *PID) LatestActivityTime() time.Time {
	nanos := pid.latestReceiveTimeNano.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

// request sends an asynchronous request to another PID and returns a RequestCall.
//
// Design decision: async requests are opt-in per actor to preserve legacy semantics.
// The caller must have reentrancy enabled; otherwise ErrReentrancyDisabled is returned.
func (pid *PID) request(ctx context.Context, to *PID, message any, opts ...RequestOption) (RequestCall, error) {
	if !pid.IsRunning() {
		return nil, gerrors.ErrDead
	}

	if to == nil || !to.IsRunning() {
		return nil, gerrors.ErrDead
	}

	if message == nil {
		return nil, gerrors.ErrInvalidMessage
	}

	if pid.reentrancy == nil {
		return nil, gerrors.ErrReentrancyDisabled
	}

	config := newRequestConfig(opts...)
	mode := pid.reentrancy.mode
	if config.modeSet {
		mode = config.mode
	}

	if mode == reentrancy.Off {
		return nil, gerrors.ErrReentrancyDisabled
	}

	if !reentrancy.IsValidReentrancyMode(mode) {
		return nil, gerrors.ErrInvalidReentrancyMode
	}

	correlationID := uuid.NewString()
	state := newRequestState(correlationID, mode, pid)
	if err := pid.registerRequestState(state); err != nil {
		return nil, err
	}

	if config.timeout != nil {
		state.startTimeout(*config.timeout)
	}

	req, err := pid.buildAsyncRequest(message, correlationID)
	if err != nil {
		pid.deregisterRequestState(state)
		return nil, err
	}

	if err := pid.Tell(ctx, to, req); err != nil {
		pid.deregisterRequestState(state)
		return nil, err
	}

	return &requestHandle{state: state}, nil
}

// requestName sends an asynchronous request to a named actor.
//
// Design decision: name resolution is performed once to avoid an extra lookup and
// to keep control of the async envelope before sending. The caller must have
// reentrancy enabled; otherwise ErrReentrancyDisabled is returned.
func (pid *PID) requestName(ctx context.Context, actorName string, message any, opts ...RequestOption) (RequestCall, error) {
	if !pid.IsRunning() {
		return nil, gerrors.ErrDead
	}

	if message == nil {
		return nil, gerrors.ErrInvalidMessage
	}

	if pid.reentrancy == nil {
		return nil, gerrors.ErrReentrancyDisabled
	}

	config := newRequestConfig(opts...)
	mode := pid.reentrancy.mode
	if config.modeSet {
		mode = config.mode
	}

	if mode == reentrancy.Off {
		return nil, gerrors.ErrReentrancyDisabled
	}

	if !reentrancy.IsValidReentrancyMode(mode) {
		return nil, gerrors.ErrInvalidReentrancyMode
	}

	cid, err := pid.ActorSystem().ActorOf(ctx, actorName)
	if err != nil {
		return nil, err
	}

	correlationID := uuid.NewString()
	state := newRequestState(correlationID, mode, pid)
	if err := pid.registerRequestState(state); err != nil {
		return nil, err
	}

	if config.timeout != nil {
		state.startTimeout(*config.timeout)
	}

	req, err := pid.buildAsyncRequest(message, correlationID)
	if err != nil {
		pid.deregisterRequestState(state)
		return nil, err
	}

	if cid.IsLocal() {
		if err := pid.Tell(ctx, cid, req); err != nil {
			pid.deregisterRequestState(state)
			return nil, err
		}
	} else if err := pid.remoteTell(ctx, cid.getAddress(), req); err != nil {
		pid.deregisterRequestState(state)
		return nil, err
	}

	return &requestHandle{state: state}, nil
}

// buildAsyncRequest wraps the payload with correlation and reply metadata.
//
// Design decision: AsyncRequest uses Any to preserve the protobuf-only message
// contract while keeping the async envelope stable across message types.
func (pid *PID) buildAsyncRequest(message any, correlationID string) (*commands.AsyncRequest, error) {
	if message == nil {
		return nil, gerrors.ErrInvalidMessage
	}

	return &commands.AsyncRequest{
		CorrelationID: correlationID,
		ReplyTo:       pathString(pid.Path()),
		Message:       message,
	}, nil
}

// doReceive pushes a given message to the actor mailbox
// and signals the receiveLoop to process it
func (pid *PID) doReceive(receiveCtx *ReceiveContext) {
	// fast path: check if system is shutting down
	if system := pid.actorSystem; system != nil && system.isStopping() {
		// slow path: only check message type if shutting down
		// system messages must be allowed through for proper shutdown/supervision
		if !isSystemMessage(receiveCtx.Message()) {
			pid.handleReceivedError(receiveCtx, gerrors.ErrSystemShuttingDown)
			return
		}
	}

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
				if pid.enableReentrancyStash(received) {
					if err := pid.stash(received); err != nil {
						pid.logger.Warn(err)
						pid.handleReceivedError(received, err)
					}
					received = nil
					continue
				}
				// Process the message
				switch msg := received.Message().(type) {
				case *PoisonPill:
					_ = pid.Shutdown(received.Context())
				case *commands.HealthCheckRequest:
					pid.handleHealthcheck(received)
				case *commands.Panicking:
					pid.handlePanicking(received.Sender(), msg)
				case *PausePassivation:
					pid.pausePassivation()
				case *ResumePassivation:
					pid.resumePassivation()
				case *commands.AsyncRequest:
					pid.handleAsyncRequest(received, msg)
				case *commands.AsyncResponse:
					pid.handleAsyncResponse(received, msg)
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

			// Release the last processed context before exiting so it is
			// returned to the pool instead of leaking until the next GC cycle.
			if received != nil {
				releaseContext(received)
			}
			return
		}
	}()
}

// handleHealthcheck is used to handle the readiness probe messages
func (pid *PID) handleHealthcheck(received *ReceiveContext) {
	pid.markActivity(time.Now())
	received.Response(new(commands.HealthCheckResponse))
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

// enableReentrancyStash decides whether to stash the current message due to
// reentrancy blocking.
//
// Design decision: async responses and critical system/control messages bypass
// stashing to avoid deadlocks and preserve liveness.
func (pid *PID) enableReentrancyStash(received *ReceiveContext) bool {
	if pid.reentrancy == nil || pid.reentrancy.blockingCount.Load() <= 0 {
		return false
	}

	switch received.Message().(type) {
	case *commands.AsyncResponse,
		*PoisonPill,
		*commands.HealthCheckRequest,
		*commands.Panicking,
		*PausePassivation,
		*ResumePassivation:
		return false
	default:
		return true
	}
}

// handleAsyncRequest unwraps an AsyncRequest and dispatches the inner message.
//
// Design decision: async metadata is carried on ReceiveContext to enable Response
// to send AsyncResponse without changing the user-facing API.
func (pid *PID) handleAsyncRequest(received *ReceiveContext, req *commands.AsyncRequest) {
	if received == nil || req == nil {
		pid.handleReceivedError(received, gerrors.ErrInvalidMessage)
		return
	}

	if req.CorrelationID == "" || req.ReplyTo == "" || req.Message == nil {
		pid.handleReceivedError(received, gerrors.ErrInvalidMessage)
		return
	}

	received.message = req.Message
	received.response = nil
	received.withRequestMeta(req.CorrelationID, req.ReplyTo)
	pid.handleReceived(received)
}

// handleAsyncResponse resolves an AsyncResponse and completes the tracked call.
//
// Design decision: errors are encoded as strings on the wire to keep the response
// envelope stable and avoid cross-version type coupling.
func (pid *PID) handleAsyncResponse(received *ReceiveContext, resp *commands.AsyncResponse) {
	if resp == nil {
		pid.handleReceivedError(received, gerrors.ErrInvalidMessage)
		return
	}

	correlationID := strings.TrimSpace(resp.CorrelationID)
	if correlationID == "" {
		pid.handleReceivedError(received, gerrors.ErrInvalidMessage)
		return
	}

	if resp.Error != "" {
		if !pid.completeRequest(correlationID, nil, asyncErrorFromString(resp.Error)) {
			pid.logger.Warnf("Async response dropped: unknown correlation id=%s", correlationID)
		}
		return
	}

	if resp.Message == nil {
		if !pid.completeRequest(correlationID, nil, gerrors.ErrInvalidMessage) {
			pid.logger.Warnf("Async response dropped: unknown correlation id=%s", correlationID)
		}
		return
	}

	if !pid.completeRequest(correlationID, resp.Message, nil) {
		pid.logger.Warnf("Async response dropped: unknown correlation id=%s", correlationID)
	}
}

// registerRequestState tracks an in-flight async request and enforces limits.
//
// Design decision: blockingCount reflects stash-mode requests so stashing can
// release only when the last blocking call completes.
func (pid *PID) registerRequestState(state *requestState) error {
	if pid.reentrancy == nil {
		return gerrors.ErrReentrancyDisabled
	}

	if state == nil {
		return gerrors.ErrInvalidMessage
	}

	if pid.reentrancy.maxInFlight > 0 {
		maxInFlight := int64(pid.reentrancy.maxInFlight)
		for {
			current := pid.reentrancy.inFlightCount.Load()
			if current >= maxInFlight {
				return gerrors.ErrReentrancyInFlightLimit
			}

			if pid.reentrancy.inFlightCount.CompareAndSwap(current, current+1) {
				break
			}
		}
	} else {
		pid.reentrancy.inFlightCount.Inc()
	}

	if state.mode == reentrancy.StashNonReentrant {
		if pid.stashState == nil {
			pid.stashState = &stashState{box: NewUnboundedMailbox()}
		}
		pid.reentrancy.blockingCount.Inc()
	}

	pid.reentrancy.requestStates.Set(state.id, state)
	return nil
}

// deregisterRequestState removes an in-flight async request and releases stashed messages.
//
// Design decision: when the last blocking request completes, unstash all messages
// to preserve original mailbox order.
func (pid *PID) deregisterRequestState(state *requestState) {
	if pid.reentrancy == nil || state == nil {
		return
	}

	if _, ok := pid.reentrancy.requestStates.Get(state.id); !ok {
		return
	}

	pid.reentrancy.requestStates.Delete(state.id)
	pid.reentrancy.inFlightCount.Dec()
	if state.mode == reentrancy.StashNonReentrant {
		remaining := pid.reentrancy.blockingCount.Dec()
		if remaining == 0 {
			if err := pid.unstashAll(); err != nil {
				pid.logger.Warn(err)
			}
		}
	}

	state.stopTimeoutIfSet()
}

// completeRequest marks an async request as completed and runs its callback.
//
// Design decision: completion is idempotent; only the first result wins.
func (pid *PID) completeRequest(correlationID string, result any, err error) bool {
	if pid.reentrancy == nil {
		return false
	}

	state, ok := pid.reentrancy.requestStates.Get(correlationID)
	if !ok {
		return false
	}

	callback, completed := state.complete(result, err)
	if !completed {
		return true
	}

	pid.deregisterRequestState(state)
	if callback != nil {
		callback(result, err)
	}

	return true
}

// enqueueAsyncError injects an AsyncResponse error into the actor's mailbox.
//
// Design decision: errors are funneled through the mailbox to keep callbacks on
// the actor's processing thread.
func (pid *PID) enqueueAsyncError(ctx context.Context, correlationID string, err error) error {
	if correlationID == "" {
		return gerrors.ErrInvalidMessage
	}
	if err == nil {
		return nil
	}

	response := &commands.AsyncResponse{
		CorrelationID: correlationID,
		Error:         err.Error(),
	}

	receiveContext := getContext()
	receiveContext.build(ctx, pid, pid, response, true)
	pid.doReceive(receiveContext)
	return nil
}

// sendAsyncResponse delivers an AsyncResponse to the original requester.
//
// Design decision: reply routing uses the string address embedded in AsyncRequest
// to support local and remote responders with the same flow.
func (pid *PID) sendAsyncResponse(ctx context.Context, replyTo, correlationID string, message any, err error) error {
	if correlationID == "" || replyTo == "" {
		return gerrors.ErrInvalidMessage
	}

	response := &commands.AsyncResponse{
		CorrelationID: correlationID,
	}

	if err != nil {
		response.Error = err.Error()
	} else {
		if message == nil {
			return gerrors.ErrInvalidMessage
		}
		response.Message = message
	}

	addr, err := address.Parse(replyTo)
	if err != nil {
		return err
	}

	system := pid.ActorSystem()
	if system == nil {
		return gerrors.ErrActorSystemNotStarted
	}

	isLocal := addr.System() == system.Name() && addr.Host() == system.Host() && addr.Port() == system.Port()
	if isLocal {
		target, err := system.ActorOf(ctx, addr.Name())
		if err != nil {
			return err
		}
		return pid.Tell(ctx, target, response)
	}

	return pid.remoteTell(ctx, addr, response)
}

// cancelInFlightRequests completes all in-flight async calls with the given reason.
//
// Design decision: cancellations are local-only to keep shutdown fast and avoid
// cross-node coordination.
func (pid *PID) cancelInFlightRequests(reason error) {
	if pid.reentrancy == nil {
		return
	}

	keys := pid.reentrancy.requestStates.Keys()
	for _, key := range keys {
		state, ok := pid.reentrancy.requestStates.Get(key)
		if !ok || state == nil {
			continue
		}
		if _, completed := state.complete(nil, reason); !completed {
			continue
		}
		pid.reentrancy.requestStates.Delete(key)
		pid.reentrancy.inFlightCount.Dec()
		if state.mode == reentrancy.StashNonReentrant {
			pid.reentrancy.blockingCount.Dec()
		}
		state.stopTimeoutIfSet()
	}

	pid.reentrancy.inFlightCount.Store(0)
	pid.reentrancy.blockingCount.Store(0)
}

// markActivity updates the last receive timestamp and notifies the shared passivation manager.
func (pid *PID) markActivity(at time.Time) {
	nanos := at.UnixNano()
	pid.latestReceiveTimeNano.Store(nanos)
	if pid.passivationManager != nil {
		// Coalesce Touch calls: only acquire the passivation manager's mutex
		// when at least passivationTouchInterval has elapsed since the last
		// Touch. The CAS ensures exactly one goroutine wins per interval.
		last := pid.lastPassivationTouch.Load()
		if nanos-last >= passivationTouchInterval {
			if pid.lastPassivationTouch.CompareAndSwap(last, nanos) {
				pid.passivationManager.Touch(pid)
			}
		}
	}
}

// recordProcessedMessage increments the processed message count and notifies the passivation manager.
func (pid *PID) recordProcessedMessage() {
	pid.processedCount.Inc()
	// Only call MessageProcessed for message-count-based strategies.
	// For time-based strategies (the common case), MessageProcessed
	// acquires the passivation manager's mutex only to check the strategy
	// type and return immediately. Skipping it avoids one mutex lock
	// per message on the hot path.
	//
	// We use the atomic flag (set once at init) instead of a type assertion
	// on pid.passivationStrategy to avoid racing with any code that modifies
	// the strategy field under fieldsLocker.
	if pid.passivationManager != nil && pid.msgCountPassivation.Load() {
		pid.passivationManager.MessageProcessed(pid)
	}
}

// passivationID returns the unique identifier of the actor for passivation tracking
func (pid *PID) passivationID() string {
	return pid.ID()
}

// passivationLatestActivity returns the latest activity time of the actor for passivation tracking
func (pid *PID) passivationLatestActivity() time.Time {
	nanos := pid.latestReceiveTimeNano.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
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

// getAddress returns the internal address for use with APIs that require *address.Address
// (e.g., RemoteTell, RemoteAsk, childAddress). Caller must hold fieldsLocker or ensure
// the PID is not being mutated.
func (pid *PID) getAddress() *address.Address {
	if pid == nil {
		return address.NoSender()
	}
	pid.fieldsLocker.RLock()
	addr := pid.address
	pid.fieldsLocker.RUnlock()
	return addr
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor will not be started
func (pid *PID) init(ctx context.Context) error {
	pid.logger.Infof("Initialization process started for Actor %s ...", pid.Name())

	initContext := newContext(ctx, pid.Name(), pid.actorSystem, pid.Dependencies()...)

	cctx, cancel := context.WithTimeout(ctx, pid.initTimeout.Load())
	retrier := retry.NewRetrier(int(pid.initMaxRetries.Load()), time.Millisecond, pid.initTimeout.Load())

	if err := retrier.RunContext(cctx, func(_ context.Context) error {
		return pid.actor.PreStart(initContext)
	}); err != nil {
		e := gerrors.NewErrInitFailure(err)
		cancel()
		pid.logger.Errorf("Failed to initialize Actor %s: %v", pid.Name(), err)
		return e
	}

	pid.setState(runningState, true)
	pid.logger.Infof("Actor %s initialization is successful.", pid.Name())

	if pid.eventsStream != nil {
		pid.eventsStream.Publish(eventsTopic, NewActorStarted(pid.ID()))
	}

	cancel()
	return nil
}

// reset re-initializes the actor PID
func (pid *PID) reset() {
	pid.latestReceiveTimeNano.Store(0)
	pid.initMaxRetries.Store(DefaultInitMaxRetries)
	pid.latestReceiveDuration.Store(0)
	pid.initTimeout.Store(DefaultInitTimeout)
	pid.behaviorStack.Reset()
	pid.processedCount.Store(0)
	pid.failureCount.Store(0)
	pid.reinstateCount.Store(0)
	pid.restartCount.Store(0)
	pid.startedAt.Store(0)
	pid.setState(runningState, false)
	pid.setState(stoppingState, false)
	pid.setState(suspendedState, false)
	if pid.supervisor != nil {
		if sys, ok := pid.actorSystem.(*actorSystem); !ok || pid.supervisor != sys.defaultSupervisor {
			pid.supervisor.Reset()
		}
	}
	pid.mailbox.Dispose()
	pid.setState(singletonState, false)
	pid.setState(relocationState, true)
	if pid.dependencies != nil {
		pid.dependencies.Reset()
	}
	pid.setState(passivationPausedState, false)
	pid.setState(passivatingState, false)
	pid.setState(passivationSkipNextState, false)
	if pid.reentrancy != nil {
		pid.reentrancy.reset()
	}
}

// freeWatchers releases all the actors watching this actor
func (pid *PID) freeWatchers(ctx context.Context) {
	logger := pid.logger
	logger.Debugf("Freeing all Actor %s's watchers...", pid.Name())
	tree := pid.ActorSystem().tree()
	watchers := tree.watchers(pid)
	if len(watchers) > 0 {
		// this call will be fast no need of parallel processing
		for _, watcher := range watchers {
			watcher := watcher
			terminated := NewTerminated(pid.ID())

			if watcher.IsRunning() {
				logger.Debugf("Watcher %s releasing watched %s", watcher.Name(), pid.Name())
				// ignore error here because the watcher is running
				_ = pid.Tell(ctx, watcher, terminated)
				watcher.UnWatch(pid)
				logger.Debugf("Watcher %s released watched %s", watcher.Name(), pid.Name())
			}
		}

		logger.Debugf("All Actor %s's watchers freed...", pid.Name())
		return
	}
	logger.Debugf("Actor %s does not have any watchers. Maybe already freed.", pid.Name())
}

// freeWatchees releases all actors that have been watched by this actor
func (pid *PID) freeWatchees() error {
	logger := pid.logger
	logger.Debugf("Freeing all Actor %s's watched actors...", pid.Name())

	tree := pid.ActorSystem().tree()
	watchees := tree.watchees(pid)
	if len(watchees) > 0 {
		// this call will be fast no need of parallel processing
		for _, watched := range watchees {
			logger.Debugf("Watcher %s unwatching Actor %s", pid.Name(), watched.Name())
			pid.UnWatch(watched)
			logger.Debugf("Watcher %s successfully unwatch Actor %s", pid.Name(), watched.Name())
		}

		logger.Debugf("Actor %s successfully unwatch all watched actors...", pid.Name())
		return nil
	}
	logger.Debugf("Actor %s does not have any watched actors. Maybe already freed.", pid.Name())
	return nil
}

// freeChildren releases all child actors
func (pid *PID) freeChildren(ctx context.Context) error {
	logger := pid.logger
	logger.Debugf("Actor %s freeing all descendant actors...", pid.Name())

	tree := pid.ActorSystem().tree()
	node, ok := tree.node(pid.ID())
	if !ok {
		pid.logger.Debugf("Actor %s node not found in the actors tree", pid.Name())
		return nil
	}

	children := tree.children(pid)
	if len(children) > 0 {
		eg, ctx := errgroup.WithContext(ctx)
		for _, child := range children {
			eg.Go(func() error {
				logger.Debugf("Parent %s disowning descendant %s", pid.Name(), child.Name())
				pid.UnWatch(child)
				tree.removeDescendant(node.id, child.ID())
				if child.IsSuspended() || child.IsRunning() {
					if err := child.Shutdown(ctx); err != nil {
						// only return error when the actor is not dead
						// because if the actor is dead it means that
						// it has been stopped or passivated already
						// this can happen due to timing issue
						if !errors.Is(err, gerrors.ErrDead) {
							return fmt.Errorf("Parent %s failed to disown descendant %s: %w", pid.Name(), child.Name(), err)
						}
					}
					logger.Debugf("Parent %s successfully disown descendant %s", pid.Name(), child.Name())
				}
				return nil
			})
		}

		if err := eg.Wait(); err != nil {
			logger.Errorf("Parent (%s) failed to free all descendant actors: %v", pid.Name(), err)
			return err
		}

		logger.Debugf("Actor %s successfully free all descendant actors...", pid.Name())
		return nil
	}
	pid.logger.Debugf("Actor %s does not have any children. Maybe already freed.", pid.Name())
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

	if pid.compareAndSwapState(passivationSkipNextState, true, false) {
		pid.logger.Debugf("Passivation decision skipped once for Actor %s due to recent reinstate", pid.Name())
		return false
	}

	if pid.isStateSet(stoppingState) ||
		pid.isStateSet(suspendedState) ||
		pid.isStateSet(passivationPausedState) {
		pid.logger.Infof("No need to passivate actor=%s", pid.Name())
		return false
	}

	pid.logger.Infof("Passivation mode has been triggered for Actor %s (%s)...", pid.Name(), reason)
	pid.setState(passivatingState, true)
	defer pid.setState(passivatingState, false)

	pid.stopLocker.Lock()
	defer pid.stopLocker.Unlock()

	if pid.compareAndSwapState(passivationSkipNextState, true, false) {
		pid.logger.Debugf("Passivation decision aborted for %s due to reinstate observed during critical section", pid.Name())
		return false
	}

	pid.unregisterPassivation()

	ctx := context.Background()
	if err := pid.doStop(ctx); err != nil {
		pid.logger.Errorf("Failed to passivate Actor (%s): Reason=(%v)", pid.Name(), err)
		return false
	}

	if pid.eventsStream != nil {
		pid.eventsStream.Publish(eventsTopic, NewActorPassivated(pid.ID()))
	}

	pid.logger.Infof("Actor %s successfully passivated", pid.Name())
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
	pid.cancelInFlightRequests(gerrors.ErrRequestCanceled)

	defer func() {
		pid.setState(runningState, false)
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

	stopContext := newContext(ctx, pid.Name(), pid.actorSystem, pid.Dependencies()...)

	// run the PostStop hook and let watchers know
	// you are terminated
	if err := chain.
		New(chain.WithFailFast()).
		AddRunner(func() error { return pid.actor.PostStop(stopContext) }).
		AddRunner(func() error { pid.freeWatchers(ctx); return nil }).
		Run(); err != nil {
		return err
	}

	pid.logger.Infof("Shutdown process completed for Actor %s...", pid.Name())
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
		case pid.supervisionStopSignal <- types.Unit{}:
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

	pid.logger.Debugf("Actor %s supervisor directive %s", pid.Name(), directive.String())

	// create the message to send to the parent
	msg := &commands.Panicking{
		ActorID:    pid.ID(),
		Err:        signal.Err(),
		Message:    signal.Msg(),
		Timestamp:  signal.Timestamp(),
		Strategy:   pid.supervisor.Strategy(),
		Directive:  directive,
		Supervisor: pid.supervisor,
	}

	if parent := pid.Parent(); parent != nil && !parent.Equals(pid.ActorSystem().NoSender()) {
		pid.logger.Warnf("Actor %s's child Actor (%s) is failing: Err=%s", parent.Name(), pid.Name(), msg.Err.Error())
		pid.logger.Infof("Actor %s activates [strategy=%s, directive=%s] for failing child actor=(%s)",
			parent.Name(),
			pid.supervisor.Strategy(),
			directive,
			pid.Name())

		// For ResumeDirective, avoid suspending to minimize timing windows where the child appears
		// temporarily "not running" to observers. For other directives, keep suspension semantics.
		if directive == supervisor.ResumeDirective {
			// Always skip the next passivation decision once to avoid immediate stop after resume.
			pid.setState(passivationSkipNextState, true)
			// If the actor was already suspended due to a prior signal, reinstate immediately.
			if pid.IsSuspended() {
				pid.doReinstate()
			}
			return
		}

		// suspend the actor until the parent takes an action based on strategy/directive
		pid.suspend(msg.Err.Error())

		// notify parent about the failure
		_ = pid.Tell(context.Background(), parent, msg)
		return
	}

	// no parent found, just suspend the actor
	pid.logger.Warnf("Actor %s has no parent to notify about its failure: Err=%s", pid.Name(), msg.Err.Error())
	pid.suspend(msg.Err.Error())
}

// handleReceivedError sends message to deadletter synthetic actor
func (pid *PID) handleReceivedError(receiveCtx *ReceiveContext, err error) {
	if receiveCtx == nil {
		return
	}
	pid.handleReceivedErrorWithMessage(receiveCtx.Sender(), receiveCtx.Message(), err)
}

func (pid *PID) handleReceivedErrorWithMessage(senderPID *PID, message any, err error) {
	// the message is lost
	if pid.eventsStream == nil {
		return
	}

	// skip system messages and deadletter commands to prevent infinite recursion
	switch message.(type) {
	case *PostStart, *Terminated, *commands.SendDeadletter:
		return
	default:
		// pass through
	}

	system := pid.ActorSystem()
	var sender Path
	if system != nil {
		sender = system.NoSender().Path()
	}

	if senderPID != nil {
		if system == nil || !senderPID.Equals(system.NoSender()) {
			sender = senderPID.Path()
		}
	}

	receiver := pid.Path()
	if receiver == nil {
		return
	}

	ctx := context.Background()
	pid.toDeadletter(ctx, sender, receiver, message, err)
}

// toDeadletter sends a message to the deadletter actor
func (pid *PID) toDeadletter(ctx context.Context, from, to Path, message any, err error) {
	system := pid.ActorSystem()
	if system == nil {
		return
	}
	deadletter := system.getDeadletter()
	command := &commands.SendDeadletter{
		Deadletter: commands.Deadletter{
			Sender:   pathString(from),
			Receiver: pathString(to),
			Message:  message,
			SendTime: time.Now().UTC(),
			Reason:   err.Error(),
		},
	}

	// send the message to the deadletter actor
	_ = pid.Tell(ctx, deadletter, command)
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
	runTask := func() (any, error) {
		if config != nil && config.circuitBreaker != nil {
			outcome, oerr := config.circuitBreaker.Execute(ctx, func(ctx context.Context) (any, error) {
				return fut.Await(ctx)
			})

			if oerr != nil {
				return nil, oerr
			}

			// no need to check the type since the future.Await returns proto.Message
			// if there is no error
			return outcome, nil
		}
		return fut.Await(ctx)
	}

	result, err := runTask()
	if err != nil {
		pid.logger.Error(err)
		pid.toDeadletter(ctx, pid.Path(), pid.Path(), new(NoMessage), err)
		return
	}

	// make sure that the receiver is still alive
	to := completion.Receiver
	if !to.IsRunning() {
		pid.logger.Errorf("Unable to pipe message to Actor (%s): not started", to.Name())
		pid.toDeadletter(ctx, pid.Path(), pid.Path(), result, gerrors.ErrDead)
		return
	}

	messageContext := newReceiveContext(ctx, pid, to, result)
	to.doReceive(messageContext)
}

// handlePanicking watches for child actor's failure and act based upon the supervisory strategy
func (pid *PID) handlePanicking(cid *PID, msg *commands.Panicking) {
	if cid.ID() == msg.ActorID {
		directive := msg.Directive
		includeSiblings := msg.Strategy == supervisor.OneForAllStrategy

		switch directive {
		case supervisor.StopDirective:
			pid.handleStopDirective(cid, includeSiblings)
		case supervisor.RestartDirective:
			pid.handleRestartDirective(cid,
				msg.Supervisor.MaxRetries(),
				msg.Supervisor.Timeout(),
				includeSiblings)
		case supervisor.ResumeDirective:
			// simply reinstate the actor
			cid.doReinstate()
		case supervisor.EscalateDirective:
			// forward the message to the parent and suspend the actor
			_ = cid.Tell(context.Background(), pid, NewPanicSignal(msg.Message, msg.Err.Error(), msg.Timestamp))
		default:
			cid.suspend(msg.Err.Error())
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
				pid.logger.Error(fmt.Errorf("failed to shutdown Actor (%s): %w", spid.Name(), err))
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
	addr := pid.getAddress()
	if addr == nil || addr.Equals(address.NoSender()) {
		return nil
	}
	return address.NewWithParent(name, addr.System(), addr.Host(), addr.Port(), addr)
}

// suspend puts the actor in a suspension mode.
func (pid *PID) suspend(reason string) {
	pid.logger.Infof("Actor %s going into suspension mode", pid.Name())
	pid.setState(suspendedState, true)
	// increment suspension count
	pid.failureCount.Inc()
	// pause passivation loop
	pid.pausePassivation()
	// stop the supervisor loop
	pid.stopSupervisionLoop()
	// publish an event to the events stream
	pid.eventsStream.Publish(eventsTopic, NewActorSuspended(pid.ID(), reason))
}

// getDeadlettersCount gets deadletter
func (pid *PID) getDeadlettersCount(ctx context.Context) int64 {
	var (
		name    = pid.ID()
		to      = pid.ActorSystem().getDeadletter()
		from    = pid.ActorSystem().getSystemGuardian()
		message = &commands.DeadlettersCountRequest{
			ActorID: &name,
		}
	)
	if to.IsRunning() {
		// ask the deadletter actor for the count
		// using the default ask timeout
		// note: no need to check for error because this call is internal
		message, _ := from.Ask(ctx, to, message, DefaultAskTimeout)
		// cast the response received from the deadletter
		deadlettersCount := message.(*commands.DeadlettersCountResponse)
		return deadlettersCount.TotalCount
	}
	return 0
}

// fireSystemMessage sends a system-level message to the specified PID by creating a receive context and invoking the message handling logic.
func (pid *PID) fireSystemMessage(ctx context.Context, message any) {
	receiveContext := getContext()
	noSender := pid.ActorSystem().NoSender()
	receiveContext.build(ctx, noSender, pid, message, true)
	pid.doReceive(receiveContext)
}

func (pid *PID) doReinstate() {
	pid.logger.Infof("Actor %s has been reinstated", pid.Name())
	// if we're already running and not suspended, nothing to do
	if pid.IsRunning() && !pid.IsSuspended() {
		return
	}
	pid.setState(suspendedState, false)
	// increment reinstate count
	pid.reinstateCount.Inc()
	// Guard against a pending passivation path that might have just crossed the threshold
	// but hasn't yet checked suspension state. Skip the next passivation decision once.
	pid.setState(passivationSkipNextState, true)
	// Treat reinstate as activity so any freshly registered passivation deadline
	// doesn't immediately fire before the skip guard can cancel the in-flight attempt.
	pid.markActivity(time.Now())

	// resume the supervisor loop
	pid.startSupervision()
	// resume passivation loop
	pid.resumePassivation()

	// publish an event to the events stream
	pid.eventsStream.Publish(eventsTopic, NewActorReinstated(pid.ID()))
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
	pid.setState(passivationPausedState, true)
}

// resumePassivation resumes a paused passivation
func (pid *PID) resumePassivation() {
	if pid.passivationStrategy == nil {
		return
	}

	if pid.isStateSet(passivationPausedState) {
		pid.setState(passivationPausedState, false)
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
	logger.Infof("Actor %s readiness probe...", pid.Name())

	if pid.isStateSet(systemState) {
		logger.Debugf("Actor %s is a system actor. No need for readiness probe.", pid.Name())
		return nil
	}

	message := new(commands.HealthCheckRequest)
	timeout := pid.initTimeout.Load()
	numretries := pid.initMaxRetries.Load()
	noSender := pid.ActorSystem().NoSender()

	retrier := retry.NewRetrier(int(numretries), timeout, timeout)
	err := retrier.RunContext(ctx, func(ctx context.Context) error {
		_, err := noSender.Ask(ctx, pid, message, DefaultAskTimeout)
		return err
	})

	if err != nil {
		logger.Errorf("Actor %s readiness probe failed: %v", pid.Name(), err)
		// attempt to shut down the actor to free pre-allocated resources
		return errors.Join(err, pid.Shutdown(ctx))
	}

	logger.Infof("Actor %s readiness probe successfully completed.", pid.Name())
	return nil
}

func (pid *PID) remotingEnabled() bool {
	if pid == nil || pid.remoting == nil {
		return false
	}

	if pid.IsRemote() {
		return true
	}

	system := pid.ActorSystem()
	if system == nil {
		return false
	}

	if sys, ok := system.(*actorSystem); ok {
		return sys.remotingEnabled.Load()
	}

	return true
}

func (pid *PID) toSerialize() (*internalpb.Actor, error) {
	dependencies, err := codec.EncodeDependencies(pid.Dependencies()...)
	if err != nil {
		return nil, err
	}

	var supervisorSpec *internalpb.SupervisorSpec
	if pid.supervisor != nil {
		supervisorSpec = codec.EncodeSupervisor(pid.supervisor)
	}

	var singletonSpec *internalpb.SingletonSpec
	if pid.IsSingleton() && pid.singletonSpec != nil {
		singletonSpec = &internalpb.SingletonSpec{
			SpawnTimeout: durationpb.New(pid.singletonSpec.SpawnTimeout),
			WaitInterval: durationpb.New(pid.singletonSpec.WaitInterval),
			MaxRetries:   pid.singletonSpec.MaxRetries,
		}
	}

	var reentrancy *internalpb.ReentrancyConfig
	if pid.reentrancy != nil {
		reentrancy = pid.reentrancy.toProto()
	}

	return &internalpb.Actor{
		Address:             pid.ID(),
		Type:                types.Name(pid.Actor()),
		Singleton:           singletonSpec,
		Relocatable:         pid.IsRelocatable(),
		PassivationStrategy: codec.EncodePassivationStrategy(pid.PassivationStrategy()),
		Dependencies:        dependencies,
		EnableStash:         pid.stashState != nil && pid.stashState.box != nil,
		Role:                pid.Role(),
		Supervisor:          supervisorSpec,
		Reentrancy:          reentrancy,
	}, nil
}

func (pid *PID) registerMetrics() error {
	if pid.metricProvider != nil && pid.metricProvider.Meter() != nil {
		meter := pid.metricProvider.Meter()
		metrics, err := metric.NewActorMetric(meter)
		if err != nil {
			return err
		}

		observeOptions := []otelmetric.ObserveOption{
			otelmetric.WithAttributes(attribute.String("actor.system", pid.actorSystem.Name())),
			otelmetric.WithAttributes(attribute.String("actor.name", pid.Name())),
			otelmetric.WithAttributes(attribute.String("actor.kind", types.Name(pid.Actor()))),
			otelmetric.WithAttributes(attribute.String("actor.address", pid.ID())),
		}

		_, err = meter.RegisterCallback(func(ctx context.Context, observer otelmetric.Observer) error {
			observer.ObserveInt64(metrics.ChildrenCount(), int64(pid.ChildrenCount()), observeOptions...)
			observer.ObserveInt64(metrics.StashSize(), int64(pid.StashSize()), observeOptions...)
			observer.ObserveInt64(metrics.RestartCount(), int64(pid.RestartCount()), observeOptions...)
			observer.ObserveInt64(metrics.ProcessedCount(), int64(pid.ProcessedCount()-1), observeOptions...)
			observer.ObserveInt64(metrics.LastReceivedDuration(), pid.LatestProcessedDuration().Milliseconds(), observeOptions...)
			observer.ObserveInt64(metrics.Uptime(), pid.Uptime(), observeOptions...)
			observer.ObserveInt64(metrics.DeadlettersCount(), pid.getDeadlettersCount(ctx), observeOptions...)
			observer.ObserveInt64(metrics.FailureCount(), int64(pid.failureCount.Load()), observeOptions...)
			observer.ObserveInt64(metrics.ReinstateCount(), int64(pid.reinstateCount.Load()), observeOptions...)
			return nil
		}, metrics.ChildrenCount(),
			metrics.StashSize(),
			metrics.RestartCount(),
			metrics.ProcessedCount(),
			metrics.LastReceivedDuration(),
			metrics.Uptime(),
			metrics.DeadlettersCount(),
			metrics.FailureCount(),
			metrics.ReinstateCount(),
		)

		return err
	}
	return nil
}

// remoteTell sends a message to an actor remotely without expecting any reply
func (pid *PID) remoteTell(ctx context.Context, to *address.Address, message any) error {
	if !pid.remotingEnabled() {
		return gerrors.ErrRemotingDisabled
	}

	return pid.remoting.RemoteTell(ctx, pid.getAddress(), to, message)
}

// remoteAsk sends a synchronous message to another actor remotely and expect a response.
func (pid *PID) remoteAsk(ctx context.Context, to *address.Address, message any, timeout time.Duration) (response any, err error) {
	if !pid.remotingEnabled() {
		return nil, gerrors.ErrRemotingDisabled
	}

	if timeout <= 0 {
		return nil, gerrors.ErrInvalidTimeout
	}

	return pid.remoting.RemoteAsk(ctx, pid.getAddress(), to, message, timeout)
}

// assertLocal returns ErrNotLocal when called on a remote PID.
// Used as a zero-allocation first-line guard in every method that requires
// a live local actor (lifecycle ops, tree navigation, etc.).
func (pid *PID) assertLocal() error {
	if pid.IsRemote() {
		return gerrors.ErrNotLocal
	}
	return nil
}

func buildRestartSubtree(root *PID, tree *tree) *restartNode {
	rootNode := &restartNode{pid: root}
	descendants := tree.descendants(root)
	if len(descendants) == 0 {
		return rootNode
	}

	nodes := make(map[string]*restartNode, len(descendants))
	for _, descendant := range descendants {
		if descendant.IsRunning() || descendant.IsSuspended() {
			nodes[descendant.ID()] = &restartNode{pid: descendant}
		}
	}
	if len(nodes) == 0 {
		return rootNode
	}

	for _, node := range nodes {
		parent, ok := tree.parent(node.pid)
		if !ok || parent == nil {
			continue
		}
		if parent.Equals(root) {
			rootNode.children = append(rootNode.children, node)
			continue
		}
		if parentNode, ok := nodes[parent.ID()]; ok {
			parentNode.children = append(parentNode.children, node)
		}
	}

	return rootNode
}

func restartSubtree(ctx context.Context, node *restartNode, parent *PID, tree *tree, deathWatch *PID, actorSystem ActorSystem) error {
	if node == nil || node.pid == nil {
		return nil
	}

	pid := node.pid
	pid.cancelInFlightRequests(gerrors.ErrRequestCanceled)
	_, wasInTree := tree.node(pid.ID())
	didShutdown := false
	if pid.IsRunning() {
		if err := pid.Shutdown(ctx); err != nil {
			return err
		}
		didShutdown = true
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

	// Wait for any in-flight processing goroutine to fully drain before
	// reinitializing. The MPSC mailbox is single-consumer; starting a new
	// consumer while the old one is still calling Dequeue is a data race.
	for pid.processing.Load() != idle {
		runtime.Gosched()
	}

	pid.resetBehavior()
	if err := pid.init(ctx); err != nil {
		return err
	}

	// re-add the actor back to the actor tree and cluster
	if err := chain.New(chain.WithFailFast()).
		AddRunner(func() error { return tree.addOrAttachNode(parent, pid) }).
		AddRunner(func() error { tree.addWatcher(pid, deathWatch); return nil }).
		AddRunner(func() error { return actorSystem.putActorOnCluster(pid) }).
		Run(); err != nil {
		return err
	}

	eg, gctx := errgroup.WithContext(ctx)
	for _, child := range node.children {
		child := child
		eg.Go(func() error {
			return restartSubtree(gctx, child, pid, tree, deathWatch, actorSystem)
		})
	}

	// wait for descendant actors to restart
	if err := eg.Wait(); err != nil {
		// disable messages processing
		pid.setState(stoppingState, true)
		pid.setState(runningState, false)
		return fmt.Errorf("actor=(%s) failed to restart: %w", pid.Name(), err)
	}

	pid.processing.Store(idle)
	pid.setState(suspendedState, false)
	pid.startSupervision()
	pid.startPassivation()

	if err := pid.healthCheck(ctx); err != nil {
		return err
	}

	pid.restartCount.Inc()
	pid.fireSystemMessage(ctx, new(PostStart))
	if pid.eventsStream != nil {
		pid.eventsStream.Publish(eventsTopic, NewActorRestarted(pid.ID()))
	}

	if actorSystem != nil && !pid.isStateSet(systemState) && (didShutdown || !wasInTree) {
		actorSystem.increaseActorsCounter()
	}

	if err := pid.registerMetrics(); err != nil {
		return err
	}

	pid.logger.Debugf("Actor (%s) successfully restarted..:)", pid.Name())
	return nil
}

// isLongLivedStrategy checks whether the given strategy is a long-lived strategy
// This is used to determine if the actor should be treated as a long-lived actor
// and not passivated automatically.
func isLongLivedPassivationStrategy(strategy passivation.Strategy) bool {
	_, ok := strategy.(*passivation.LongLivedStrategy)
	return ok
}

// isSystemMessage checks if a message is a system message that must be allowed
// through even during system shutdown (e.g., for proper shutdown, supervision, lifecycle).
//
// This is a zero-allocation type switch that identifies critical system messages.
func isSystemMessage(message any) bool {
	switch message.(type) {
	case *commands.AsyncResponse,
		*commands.AsyncRequest,
		*PoisonPill,
		*commands.HealthCheckRequest,
		*commands.Panicking,
		*commands.SendDeadletter,
		*PausePassivation,
		*ResumePassivation,
		*PostStart,
		*Terminated,
		*PanicSignal:
		return true
	default:
		return false
	}
}

// asyncErrorFromString maps well-known async error strings back to typed errors.
//
// Design decision: preserve known error identities when possible while tolerating
// opaque error strings from other nodes or versions.
func asyncErrorFromString(err string) error {
	switch err {
	case gerrors.ErrRequestTimeout.Error():
		return gerrors.ErrRequestTimeout
	case gerrors.ErrRequestCanceled.Error():
		return gerrors.ErrRequestCanceled
	default:
		return errors.New(err)
	}
}
