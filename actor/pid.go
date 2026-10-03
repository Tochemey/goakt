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
	"unsafe"

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
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/pointer"
	"github.com/tochemey/goakt/v4/internal/refusal"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/retry"
	"github.com/tochemey/goakt/v4/internal/ticker"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/supervisor"
)

// passivationTouchInterval controls how frequently Touch is called on the
// passivation manager. For a 2-minute default timeout, refreshing every
// 100ms means the deadline is at most 0.08% stale — well within tolerance.
// This avoids acquiring the passivation manager's mutex on every message.
const passivationTouchInterval = int64(100 * time.Millisecond)

// defaultSupervisor is the shared supervisor assigned to PIDs constructed
// without a supervisor option. A supervisor holds no per-actor state, so a
// single instance serves every such PID; Reset and SetDirectiveByType would
// change it for all of them.
var defaultSupervisor = supervisor.NewSupervisor()

// defaultPassivationStrategy is the shared passivation strategy assigned to
// PIDs constructed without a passivation strategy option.
var defaultPassivationStrategy = passivation.NewTimeBasedStrategy(DefaultPassivationTimeout)

// defaultLogger is the shared logger assigned to PIDs constructed without
// a logger option.
var defaultLogger = log.NewZap(log.ErrorLevel, os.Stderr)

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
// # Operations forwarded to the remote node
//
// Lifecycle: Stop, Restart, Shutdown, SpawnChild, Reinstate
// Tree navigation: Child, Children, ChildrenCount, Parent
//
// # Local-only operations (return ErrNotLocal for remote PIDs)
//
// These are ReinstateNamed, SendAsync, SendSync, PipeTo, PipeToName and
// DiscoverActor.
//
// Watch and UnWatch called on a remote PID do nothing; call them on a local
// PID with the remote PID as argument to watch a remote actor.
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

	// initTimeout holds an explicit WithInitTimeout override for the actor's
	// PreStart deadline. A nil value means the actor inherits the actor system's
	// configured init timeout, resolved at initialization via effectiveInitTimeout.
	// A set override is what gets carried through relocation.
	initTimeout atomic.Pointer[time.Duration]

	// mailbox holds user messages in FIFO order, drained by runTurn up
	// to the dispatcher's per-turn throughput budget.
	mailbox Mailbox

	// remoteHolds tracks the flow-control credit shares of resident remote
	// messages independently of the mailbox implementation, so teardown can
	// repay peers whatever the mailbox abandons. Lazily created on the first
	// remote hold; local-only actors never allocate one.
	remoteHolds atomic.Pointer[remoteHoldRegistry]

	// systemQueue holds the control-plane messages (PoisonPill, Panicking,
	// Pause/ResumePassivation, Terminated, PanicSignal, SendDeadletter).
	// runTurn drains it before the user mailbox so shutdown and supervision
	// signals never queue behind a user backlog. It is embedded, two words
	// and no sentinel, so an idle actor pays nothing for it; see systemQueue.
	systemQueue systemQueue

	// postStart holds the PostStart of the current incarnation until the
	// actor's next turn handles it. runTurn handles it before the system queue
	// and the mailbox, so PostStart is always the first message an
	// incarnation processes, even when a child's Terminated or PanicSignal, or
	// a user message, reached the actor first.
	postStart atomic.Pointer[ReceiveContext]

	// the actor actorSystem
	actorSystem ActorSystem

	// various lockers to protect the PID fields
	// in a concurrent environment
	fieldsLocker sync.RWMutex
	stopLocker   sync.Mutex

	// specifies the actor behavior stack
	behaviorStack *behaviorStack

	// stashState is the stash buffer, nil until WithStash or a stash-mode
	// request creates it. An atomic pointer because that request creates it on
	// the processing turn while StashSize reads it from other goroutines.
	stashState atomic.Pointer[stashState]
	// reentrancy holds the async request state. An atomic pointer because
	// EnableReentrancy installs it at runtime from the processing turn while
	// off-turn readers (shutdown cancellation, wire snapshots) observe it. It
	// transitions nil to non-nil at most once and is never removed; disabling
	// flips the state's default mode to Off instead.
	reentrancy atomic.Pointer[reentrancyState]

	// mailboxHead and mailboxTail are the two ends of the default user
	// mailbox's lock-free MPSC list, embedded in the PID so an ordinary actor
	// holds no separate UnboundedMailbox object. Both are *ReceiveContext held
	// as unsafe.Pointer so the same sync/atomic pointer operations the standalone
	// mailbox uses apply unchanged; see embedded_mailbox.go.
	//
	// The consumer, the processing turn, advances mailboxHead on every dequeue
	// and no producer ever reads it, so it sits here at the head of the
	// consumer-written line beside processedCount and mailboxDequeued, a line no
	// producer reads per message. The mailbox interface field above points at
	// (*embeddedMailbox)(pid), so the hot path stays an ordinary interface call
	// into these words. A PID given a custom mailbox leaves mailboxHead and
	// mailboxTail nil and never touches them.
	mailboxHead unsafe.Pointer // *ReceiveContext

	// set the metrics settings
	restartCount   atomic.Int64
	processedCount atomic.Int64
	// mailboxDequeued counts the user messages the processing turn has taken out
	// of the mailbox. It is the consumer half of actor.mailbox.size, which is
	// reported as mailboxEnqueued minus mailboxDequeued rather than as a single
	// counter both sides write: one shared counter makes every message bounce a
	// cache line between the producer and the consumer.
	//
	// Its position is deliberate. It sits immediately next to processedCount,
	// which the turn already writes for every message it processes, so the
	// increment lands on a line the consumer effectively owns and adds no
	// coherence traffic of its own. Keep the two adjacent.
	//
	// It is maintained only when metricsEnabled is set, and zeroed by reset().
	mailboxDequeued atomic.Int64

	// passivationManager, msgCountPassivation and metricsEnabled are read by
	// the processing turn for every message and written only on lifecycle
	// paths. They sit on the line the consumer already writes for every
	// message (processedCount, mailboxDequeued), which no producer reads, so
	// the reads always hit and never interact with the producer's per-message
	// reads of schedState and ctxShard or with its writes to mailboxEnqueued.
	passivationManager  *passivationManager
	msgCountPassivation atomic.Bool // true when passivationStrategy is MessagesCountBasedStrategy; set once at init
	// metricsEnabled records whether a metric provider is wired to this actor.
	// It is written once in newPID and only read afterwards, which is what lets
	// the message path gate the mailbox counters on a plain field read instead
	// of on a provider lookup.
	metricsEnabled bool

	// consecutiveFaults counts the faults handled by the parent's restart
	// directive without a fault-free period in between; lastFaultAtNano records
	// when the latest one occurred. Together they drive the restart budget
	// (supervisor.WithRetry) and the exponential backoff delays
	// (supervisor.WithExponentialBackoff). Both are deliberately left out of
	// reset() so they survive the shutdown embedded in a restart; a fresh spawn
	// allocates a new PID and naturally starts at zero.
	consecutiveFaults atomic.Int64
	lastFaultAtNano   atomic.Int64

	// supervisor strategy
	supervisor *supervisor.Supervisor

	// schedState drives the dispatcher-pool ready-queue membership for
	// this actor.
	schedState dispatchState

	// ctxShard selects the context-pool shard used for messages delivered
	// to this actor. Assigned round-robin at construction so concurrently
	// active actors draw from distinct pool shards instead of contending
	// on one.
	ctxShard uint32

	// dispatcher is cached from the actor system at construction time so
	// the hot-path doReceive can schedule this actor without an interface
	// assertion on every message.
	dispatcher *dispatcher

	remoting remoteclient.Client

	// failureCount, reinstateCount and unhandledCount are written only on
	// failures and explicit rejections, so they share the read-mostly line of
	// schedState, ctxShard and dispatcher without disturbing it per message.
	failureCount   atomic.Int64
	reinstateCount atomic.Int64
	// unhandledCount counts the messages the actor explicitly rejected through
	// ReceiveContext.Unhandled. It accumulates for the lifetime of one actor
	// incarnation and is zeroed by reset(), exactly like processedCount.
	unhandledCount atomic.Int64

	startedAt atomic.Int64
	state     atomic.Uint32

	// the list of dependencies
	dependencies *xsync.Map[string, extension.Dependency]

	// mailboxTail is the producer end of the embedded user mailbox's MPSC list;
	// see mailboxHead. Producers swap it in on every enqueue and the consumer
	// never reads it, so it is parked here beside mailboxEnqueued on the
	// producer-written line, away from every field the processing turn touches
	// per message, exactly as the standalone mailbox padded its tail onto its
	// own cache line. It is a *ReceiveContext, and nil for a custom mailbox.
	mailboxTail unsafe.Pointer // *ReceiveContext

	// mailboxEnqueued counts the user messages producers have put into the
	// mailbox. It is the producer half of actor.mailbox.size, the counterpart of
	// mailboxDequeued.
	//
	// Its position is deliberate, and it is the reason the pair exists. It is
	// parked here among the cold configuration fields, away from every field the
	// processing turn touches per message (processedCount, mailboxDequeued,
	// latestReceiveTimeNano, schedState, metricsEnabled, and the mailbox and
	// dispatcher pointers), so at runtime its cache line is written by producers
	// only and the consumer never has to reload it. Moving it next to the other
	// counters would put it back on the line holding schedState, which both
	// sides write for every message, and hand back the per-message cache line
	// bounce the split removes. Keep it away from the hot fields.
	//
	// It is maintained only when metricsEnabled is set, and zeroed by reset().
	mailboxEnqueued atomic.Int64

	passivationStrategy passivation.Strategy

	// companion holds the spawn-time settings most actors never set: reliable
	// delivery, durable queues, metrics identity, singleton specification and
	// placement role. It stays nil for an ordinary actor, which keeps those
	// fields off the idle footprint (see pidCompanion), and it is read only on
	// lifecycle and scrape paths, so it can share a cache line with
	// mailboxEnqueued.
	companion *pidCompanion
}

var (
	_ passivationParticipant = (*PID)(nil)
	_ schedulable            = (*PID)(nil)
)

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
		address:               address,
		path:                  newPath(address),
	}

	pid.ctxShard = nextContextShard()
	pid.initMaxRetries.Store(DefaultInitMaxRetries)
	pid.latestReceiveDuration.Store(0)
	pid.processedCount.Store(0)
	pid.startedAt.Store(0)
	pid.restartCount.Store(0)
	pid.failureCount.Store(0)
	pid.reinstateCount.Store(0)
	pid.unhandledCount.Store(0)
	pid.mailboxEnqueued.Store(0)
	pid.mailboxDequeued.Store(0)
	pid.setState(relocationState, true)

	for _, opt := range opts {
		opt(pid)
	}

	// Install the default user mailbox unless a spawn option supplied a custom
	// one. The default runs on the PID's own mailboxHead and mailboxTail words
	// through (*embeddedMailbox)(pid), so no separate mailbox object is
	// allocated; one shared sentinel node seeds both ends exactly as
	// NewUnboundedMailbox does. A custom mailbox leaves the two words nil.
	if pid.mailbox == nil {
		sentinel := new(ReceiveContext)
		pid.mailboxHead = unsafe.Pointer(sentinel)
		pid.mailboxTail = unsafe.Pointer(sentinel)
		pid.mailbox = (*embeddedMailbox)(pid)
	}

	// resolved once, right after the options wired the provider, so the message
	// path never inspects the provider itself.
	provider := pid.metricProvider()
	pid.metricsEnabled = provider != nil && provider.Meter() != nil

	if pid.supervisor == nil {
		pid.supervisor = defaultSupervisor
	}

	if pid.passivationStrategy == nil {
		pid.passivationStrategy = defaultPassivationStrategy
	}

	behaviorStack := newBehaviorStack()
	behaviorStack.Push(pid.actor.Receive)
	pid.behaviorStack = behaviorStack

	if err := pid.init(ctx); err != nil {
		return nil, err
	}

	pid.startPassivation()
	pid.buildObserveOptions()
	pid.firePostStart()

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
	if pid.IsRemote() {
		role, err := pid.remoting.RemoteRole(context.Background(), pid.address.Host(), pid.address.Port(), pid.Name())
		if err == nil {
			return new(role)
		}
		return nil
	}

	pid.fieldsLocker.RLock()
	role := pid.placementRole()
	pid.fieldsLocker.RUnlock()
	return role
}

// Dependencies returns all dependencies registered with this actor.
// Dependencies are injected at spawn time via SpawnOptions and remain accessible
// for the lifetime of the actor.
func (pid *PID) Dependencies() []extension.Dependency {
	if pid.IsRemote() {
		if dependencies, err := pid.remoting.RemoteDependencies(context.Background(), pid.address.Host(), pid.address.Port(), pid.Name()); err == nil {
			return dependencies
		}
		return nil
	}

	if pid.dependencies == nil {
		return nil
	}
	return pid.dependencies.Values()
}

// Dependency returns the registered dependency with the given identifier, or nil if not found.
func (pid *PID) Dependency(dependencyID string) extension.Dependency {
	dependencies := pid.Dependencies()
	for _, dep := range dependencies {
		if dep.ID() == dependencyID {
			return dep
		}
	}
	return nil
}

// Metric returns a snapshot of the actor's runtime metrics.
// Returns nil when the actor is not running. Cluster data is not included.
func (pid *PID) Metric(ctx context.Context) *ActorMetric {
	if pid.IsRemote() {
		metric, err := pid.remoting.RemoteMetric(ctx, pid.address.Host(), pid.address.Port(), pid.Name())
		if err != nil {
			return nil
		}

		return &ActorMetric{
			deadlettersCount:        metric.GetDeadlettersCount(),
			childrenCount:           metric.GetChildrenCount(),
			uptime:                  metric.GetUptime(),
			latestProcessedDuration: metric.GetLatestProcessedDuration().AsDuration(),
			restartCount:            metric.GetRestartCount(),
			processedCount:          metric.GetProcessedCount(),
			stashSize:               metric.GetStashSize(),
			failureCount:            metric.GetFailureCount(),
			reinstateCount:          metric.GetReinstateCount(),
			unhandledCount:          metric.GetUnhandledCount(),
		}
	}

	if pid.IsRunning() {
		var (
			uptime                  = pid.Uptime()
			latestProcessedDuration = pid.LatestProcessedDuration()
			childrenCount           = pid.ChildrenCount()
			deadlettersCount        = pid.getDeadlettersCount(ctx)
			restartCount            = pid.RestartCount()
			processedCount          = pid.ProcessedCount()
			stashSize               = pid.StashSize()
		)

		// PostStart is not a processed message; until the first turn has
		// handled it there is nothing to subtract
		if processedCount > 0 {
			processedCount--
		}

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
			unhandledCount:          uint64(pid.unhandledCount.Load()),
		}
	}
	return nil
}

// Uptime returns the number of seconds elapsed since the actor started.
// Returns zero when the actor is not running.
func (pid *PID) Uptime() int64 {
	if pid.IsRemote() {
		if metric, err := pid.remoting.RemoteMetric(
			context.Background(),
			pid.address.Host(),
			pid.address.Port(),
			pid.Name()); err == nil {
			return metric.GetUptime()
		}
		return 0
	}

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

// Equals reports whether pid and to refer to the same actor. Actor names are
// case-sensitive, so the comparison is exact, as it is for Path.Equals.
func (pid *PID) Equals(to *PID) bool {
	if pid == nil && to == nil {
		return true
	}

	if pid == nil || to == nil {
		return false
	}

	return pid.ID() == to.ID()
}

// Actor returns the underlying Actor implementation.
// Returns nil for remote PIDs.
func (pid *PID) Actor() Actor {
	if pid.IsRemote() {
		return nil
	}
	return pid.actor
}

// Kind returns the reflected type name of the underlying Actor implementation.
// Returns an empty string for remote PIDs.
func (pid *PID) Kind() string {
	if pid.IsRemote() {
		if kind, err := pid.remoting.RemoteKind(context.Background(), pid.address.Host(), pid.address.Port(), pid.Name()); err == nil {
			return kind
		}
		return ""
	}

	if pid.actor == nil {
		return ""
	}
	return types.Name(pid.actor)
}

// Child returns the running child PID with the given name. For a remote PID it
// asks the actor's node for its children and returns a remote PID. It returns
// ErrDead if a local actor is not running, or ErrActorNotFound when no such
// child exists or the child is stopped.
func (pid *PID) Child(name string) (*PID, error) {
	if pid.IsRemote() {
		addresses, err := pid.remoting.RemoteChildren(context.Background(), pid.address.Host(), pid.address.Port(), pid.Name())
		if err != nil {
			return nil, err
		}

		for _, address := range addresses {
			if address.Name() == name {
				return newRemotePID(address, pid.remoting), nil
			}
		}

		return nil, gerrors.NewErrActorNotFound(name)
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
		address, err := pid.remoting.RemoteParent(context.Background(), pid.address.Host(), pid.address.Port(), pid.Name())
		if err != nil {
			return nil
		}
		return newRemotePID(address, pid.remoting)
	}

	tree := pid.ActorSystem().tree()
	parent, ok := tree.parent(pid)
	if !ok {
		return nil
	}
	return parent
}

// Children returns all direct child PIDs that are currently running.
func (pid *PID) Children() []*PID {
	if pid.IsRemote() {
		addresses, err := pid.remoting.RemoteChildren(context.Background(), pid.address.Host(), pid.address.Port(), pid.Name())
		if err != nil {
			return nil
		}

		children := make([]*PID, 0, len(addresses))
		for _, address := range addresses {
			children = append(children, newRemotePID(address, pid.remoting))
		}
		return children
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

// Stop stops the given actor at once, as Shutdown does: it does not wait for the message the actor is processing.
// When cid is remote, Stop delegates to RemoteStop via the remoting layer.
// Returns ErrRemotingDisabled when cid is remote but remoting is not configured,
// ErrDead if this actor is not running, and ErrActorNotFound when cid is not known to the actor
// system. Any local actor may be stopped this way, not only a child.
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
	if pid.IsRemote() {
		if state, err := pid.remoting.RemoteState(context.Background(),
			pid.address.Host(),
			pid.address.Port(),
			pid.Name(),
			remote.ActorStateRunning); err == nil {
			return state
		}
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
	if pid.IsRemote() {
		if state, err := pid.remoting.RemoteState(
			context.Background(),
			pid.address.Host(),
			pid.address.Port(),
			pid.Name(),
			remote.ActorStateSuspended); err == nil {
			return state
		}
		return false
	}

	return pid.isStateSet(suspendedState)
}

// IsSingleton reports whether the actor was spawned as a cluster singleton.
// A singleton exists at most once across the entire cluster and is always hosted on the oldest node.
// When that node leaves unexpectedly the singleton is restarted on the new oldest node.
func (pid *PID) IsSingleton() bool {
	if pid.IsRemote() {
		if state, err := pid.remoting.RemoteState(
			context.Background(),
			pid.address.Host(),
			pid.address.Port(),
			pid.Name(),
			remote.ActorStateSingleton); err == nil {
			return state
		}
		return false
	}

	return pid.isStateSet(singletonState)
}

// IsRelocatable reports whether the actor may be relocated to another node if its host node shuts down unexpectedly.
// Actors are relocatable by default; pass WithRelocationDisabled at spawn time to opt out.
func (pid *PID) IsRelocatable() bool {
	if pid.IsRemote() {
		if state, err := pid.remoting.RemoteState(
			context.Background(),
			pid.address.Host(),
			pid.address.Port(),
			pid.Name(),
			remote.ActorStateRelocatable); err == nil {
			return state
		}
		return false
	}
	return pid.isStateSet(relocationState)
}

// IsStopping reports whether the actor has begun stopping, either explicitly or via passivation.
func (pid *PID) IsStopping() bool {
	if pid.IsRemote() {
		if state, err := pid.remoting.RemoteState(
			context.Background(),
			pid.address.Host(),
			pid.address.Port(),
			pid.Name(),
			remote.ActorStateStopping); err == nil {
			return state
		}
		return false
	}
	return pid.isStateSet(stoppingState) || pid.isStateSet(passivatingState)
}

// PassivationStrategy returns the passivation strategy configured for this actor.
func (pid *PID) PassivationStrategy() passivation.Strategy {
	if pid.IsRemote() {
		if strategy, err := pid.remoting.RemotePassivationStrategy(
			context.Background(),
			pid.address.Host(),
			pid.address.Port(),
			pid.Name()); err == nil {
			return strategy
		}
		return nil
	}

	pid.fieldsLocker.RLock()
	strategy := pid.passivationStrategy
	pid.fieldsLocker.RUnlock()
	return strategy
}

// ActorSystem returns the actor system this PID belongs to.
// Returns nil for remote PIDs — check pid.IsLocal() before use.
func (pid *PID) ActorSystem() ActorSystem {
	if pid.IsRemote() {
		return nil
	}
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
// Each actor keeps its mailbox: a message being handled when the restart begins
// finishes on the old incarnation, and the messages queued behind it are handled
// by the new one, after its PostStart. Messages sent while the restart is under
// way are refused.
//
// If the target or any descendant fails to restart, Restart returns that error
// and the whole subtree is stopped: no actor of it is left running, their names
// are free and their watchers are told.
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

	pid.getLogger().Debugf("restarting actor=%s", pid.Name())
	actorSystem := pid.ActorSystem()
	tree := actorSystem.tree()
	deathWatch := actorSystem.getDeathWatch()

	// snapshot all alive descendants before shutdown so we can rebuild the full subtree
	// after the teardown has detached the children from their parents.
	subtree := buildRestartSubtree(pid, tree)

	parent, err := pid.restartParent(tree)
	if err != nil {
		return err
	}

	// The teardown of the target stops its descendants too. Marking the whole
	// subtree up front makes each of those stops part of the restart, so every
	// actor keeps its name and its registry record while it is down.
	subtree.setRestarting(true)
	defer subtree.setRestarting(false)

	if err := restartSubtree(ctx, subtree, parent, tree, deathWatch, actorSystem); err != nil {
		subtree.terminate(ctx)
		return err
	}

	return nil
}

// restartParent returns the parent the restarted actor is attached under. An
// actor that is running or suspended is in the tree, which knows its parent. A
// stopped actor has left the tree, so its parent is found from its address: its
// parent actor, which must still be in the tree, or the user guardian for a
// top-level actor. The restart then puts the actor back into the tree, under
// supervision and death watch, instead of reviving it outside the tree.
func (pid *PID) restartParent(tree *tree) (*PID, error) {
	if parent, ok := tree.parent(pid); ok {
		return parent, nil
	}

	parentAddress := pid.getAddress().Parent()
	if parentAddress == nil || parentAddress.Equals(address.NoSender()) {
		return pid.ActorSystem().getUserGuardian(), nil
	}

	node, ok := tree.node(parentAddress.String())
	if !ok || node.value() == nil {
		return nil, gerrors.ErrDead
	}

	return node.value(), nil
}

// RestartCount returns the total number of times this actor has been restarted.
func (pid *PID) RestartCount() int {
	if pid.IsRemote() {
		if metric, err := pid.remoting.RemoteMetric(
			context.Background(),
			pid.address.Host(),
			pid.address.Port(),
			pid.Name()); err == nil {
			return int(metric.GetRestartCount())
		}
		return 0
	}
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
	if pid.IsRemote() {
		if metric, err := pid.remoting.RemoteMetric(
			context.Background(),
			pid.address.Host(),
			pid.address.Port(),
			pid.Name()); err == nil {
			return int(metric.GetProcessedCount())
		}
		return 0
	}

	count := pid.processedCount.Load()
	return int(count)
}

// LatestProcessedDuration returns the elapsed time since the most recent message was processed.
func (pid *PID) LatestProcessedDuration() time.Duration {
	if pid.IsRemote() {
		if metric, err := pid.remoting.RemoteMetric(
			context.Background(),
			pid.address.Host(),
			pid.address.Port(),
			pid.Name()); err == nil {
			return metric.GetLatestProcessedDuration().AsDuration()
		}
		return 0
	}

	nanos := pid.latestReceiveTimeNano.Load()
	if nanos == 0 {
		return 0
	}
	pid.latestReceiveDuration.Store(time.Since(time.Unix(0, nanos)))
	return pid.latestReceiveDuration.Load()
}

// SpawnChild creates, starts, and supervises a child actor with the given name.
// If a running child with the same name already exists, its PID is returned without creating a new one.
// If that child is not running, because it is suspended, stopping or restarting, ErrActorAlreadyExists is returned.
// Returns ErrNotLocal for remote PIDs and ErrDead if this actor is not running.
//
// A child without WithSupervisor gets the actor system's default supervisor (see
// WithDefaultSupervisor). A child is placed on its parent's node, so WithRole, which
// only constrains placement, has no effect, and a child cannot be a reliable-delivery
// endpoint: AsReliableProducer and AsReliableConsumer are rejected with an error.
func (pid *PID) SpawnChild(ctx context.Context, name string, actor Actor, opts ...SpawnOption) (*PID, error) {
	config := newSpawnConfig(opts...)
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// A reliable endpoint needs a controller that only a top-level spawn
	// creates, and the remote child spawn request cannot carry its settings:
	// reject instead of silently spawning an endpoint without its controller.
	if config.reliableDelivery != nil {
		return nil, gerrors.ErrReliableChildSpawnUnsupported
	}

	if pid.IsRemote() {
		return pid.spawnChildRemote(ctx, name, actor, config)
	}

	return pid.spawnChildLocal(ctx, name, actor, config)
}

// Reinstate resumes a suspended actor, allowing it to process messages again.
// The actor's internal state is preserved across the suspension. It is a no-op when cid
// is already running or not suspended.
// When pid is remote, the call is forwarded over remoting to cid's node; it then returns
// ErrRemotingDisabled when remoting is not configured and ErrUndefinedActor for a nil cid.
// Returns ErrDead if this actor is not running, and ErrActorNotFound when cid is not known
// to the actor system.
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

		return pid.remoting.RemoteReinstate(ctx, cid.Path().Host(), cid.Path().Port(), cid.Path().QualifiedName())
	}

	if !pid.IsRunning() {
		return gerrors.ErrDead
	}

	if cid.Equals(pid.ActorSystem().NoSender()) {
		return gerrors.ErrUndefinedActor
	}

	// this call is necessary because the reference to the actor may have been
	// kept elsewhere and the actor may have been stopped and removed from the system.
	// The qualified name resolves exactly this actor, even when a sibling of
	// another parent shares its name.
	actual, err := pid.ActorSystem().ActorOf(ctx, cid.getAddress().QualifiedName())
	if err != nil {
		return err
	}

	// the actor lives on another node: reinstate it there
	if actual.IsRemote() {
		if !pid.remotingEnabled() {
			return gerrors.ErrRemotingDisabled
		}

		addr := actual.getAddress()
		return pid.remoting.RemoteReinstate(ctx, addr.Host(), addr.Port(), addr.QualifiedName())
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
	state := pid.stashState.Load()
	if state == nil || state.box == nil {
		return 0
	}
	return uint64(state.box.Len())
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
// The name is the actor's name for a top-level actor; a child is found by its qualified name
// (e.g. "parent/child") from any node, and by its bare name on its own node.
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
			pid.getLogger().Errorf("request/request-name task failed: %v", err)
			pid.toDeadletter(ctx, pid.address, pid.address, new(NoMessage), err)
			return
		}

		// send the result to the actor identified by its name
		actorSystem := pid.ActorSystem()
		if err := actorSystem.NoSender().SendAsync(ctx, actorName, result); err != nil {
			pid.getLogger().Errorf("request/request-name send async failed: %v", err)
			pid.toDeadletter(ctx, pid.address, pid.address, result, err)
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

	receiveContext := getContext(to.ctxShard)
	receiveContext.build(ctx, pid, to, message, false)
	receiveContext.deadline = askDeadline(ctx, timeout)
	responseCh := receiveContext.response

	// doReceive hands the context to the mailbox; it can be recycled and
	// rebuilt for an unrelated message at any point after, so only the
	// per-request response channel captured above may be touched from here
	// on. A reply arriving after this Ask gives up lands in that
	// unreachable channel and is dropped with it.
	to.doReceive(receiveContext)
	timer := timers.Get(timeout)

	select {
	case result := <-responseCh:
		timers.Put(timer)
		return result, nil
	case <-ctx.Done():
		err = errors.Join(ctx.Err(), gerrors.ErrRequestTimeout)
		pid.handleReceivedErrorWithMessage(pid, message, err)
		timers.Put(timer)
		return nil, err
	case <-timer.C:
		err = gerrors.ErrRequestTimeout
		pid.handleReceivedErrorWithMessage(pid, message, err)
		timers.Put(timer)
		return nil, err
	}
}

// Tell sends a message asynchronously to the target PID.
// Routing is location-transparent: remote PIDs are handled via the remoting layer.
func (pid *PID) Tell(ctx context.Context, to *PID, message any) error {
	state := to.state.Load()
	if state&uint32(remoteState) != 0 {
		return pid.remoteTell(ctx, to.getAddress(), message)
	}

	if state&uint32(runningState) == 0 ||
		state&uint32(stoppingState|passivatingState|suspendedState) != 0 {
		return gerrors.ErrDead
	}

	receiveContext := getContext(to.ctxShard)
	receiveContext.build(ctx, pid, to, message, true)

	to.doReceive(receiveContext)
	return nil
}

// SendAsync sends a message asynchronously to the named actor.
// The actor is resolved locally first; if not found, all active datacenters are queried.
// It never blocks on a relocation handoff: when the target's host has just left
// the cluster and its actors are being recreated elsewhere, SendAsync fails
// fast with ErrRelocationInProgress instead of buffering the send.
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

	// Resolve in the local datacenter and deliver exactly once. SendAsync is a
	// non-blocking fire-and-forget API (it also backs rctx.SendAsync inside
	// actor receive loops), so it never enters the handoff retry loop: a target
	// mid-relocation fails fast with ErrRelocationInProgress instead of being
	// buffered (see deliverBypassingHandoff).
	_, err := pid.deliverBypassingHandoff(ctx, actorName, func(ctx context.Context, to *PID) (any, error) {
		return nil, pid.Tell(ctx, to, message)
	})
	if err == nil {
		return nil
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
	cid, err := pid.DiscoverActor(ctx, actorName, timeout)
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

	// Resolve in the local datacenter and ask, masking the brief window in
	// which the target's host has left the cluster and its actor is being
	// recreated on a survivor (see deliverAcrossHandoff). In steady state this
	// resolves and asks exactly once.
	response, err = pid.deliverAcrossHandoff(ctx, actorName, timeout, func(ctx context.Context, to *PID) (any, error) {
		return pid.Ask(ctx, to, message, timeout)
	})
	if err == nil {
		return response, nil
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
	cid, err := pid.DiscoverActor(ctx, actorName, lookupTimeout)
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
		pid.getLogger().Warn("DC cache is stale, proceeding with best-effort cross-DC routing")
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

// Shutdown stops this actor and all its children, and returns once PostStop has run.
// When pid is remote, Shutdown delegates to RemoteStop via the remoting layer.
// The stop is immediate: Shutdown does not wait for the message the actor is
// processing, so PostStop may run while Receive is still handling it, and the
// messages still queued in the mailbox are dropped. To stop the actor between
// two messages instead, send it a PoisonPill; to stop it after everything
// already queued, send it a message of your own and call ReceiveContext.Shutdown
// when it arrives.
// Returns ErrRemotingDisabled when pid is remote but remoting is not configured,
// and ErrShutdownForbidden when called on a system actor while the actor system is still running.
func (pid *PID) Shutdown(ctx context.Context) error {
	if pid.IsRemote() {
		if pid.remoting == nil {
			return gerrors.ErrRemotingDisabled
		}
		return pid.remoting.RemoteStop(ctx, pid.Path().Host(), pid.Path().Port(), pid.Name())
	}

	// we should never shutdown system actors unless the whole system is
	// terminating. Endpoint-owned reliable-delivery controllers are the one
	// exception: their reserved identity hides them from every public API, yet
	// spawn rollback, endpoint subtree shutdown, and their own terminal
	// self-stop must all be able to stop them while the system keeps running.
	if actoryStem := pid.ActorSystem(); actoryStem != nil {
		if !actoryStem.isStopping() && isSystemName(pid.Name()) && pid.reliableCompanion() == nil {
			pid.getLogger().Warnf("attempt to shutdown system actor=%s", pid.Name())
			return gerrors.ErrShutdownForbidden
		}
	}

	return pid.stop(ctx)
}

// stop stops a local actor: it is the shutdown without the checks Shutdown
// makes on who may stop what, for the paths that may stop any actor, such as
// the teardown of a restart. It returns once PostStop has run, and does
// nothing for an actor that is not running or suspended.
func (pid *PID) stop(ctx context.Context) error {
	pid.stopLocker.Lock()
	pid.getLogger().Debugf("shutdown started for actor=%s", pid.Name())

	if !pid.isStateSet(runningState) {
		pid.getLogger().Debugf("actor=%s is offline, maybe passivated or stopped already", pid.Name())
		pid.stopLocker.Unlock()
		return nil
	}

	pid.setState(stoppingState, true)
	pid.unregisterPassivation()

	if err := pid.doStop(ctx); err != nil {
		pid.getLogger().Errorf("actor=%s failed to cleanly stop (hint: check PostStop cleanup)", pid.Name())
		pid.stopLocker.Unlock()
		return err
	}

	if pid.getEventsStream() != nil {
		pid.getEventsStream().Publish(eventsTopic, NewActorStopped(pid.Path()))
	}

	if actorSystem := pid.ActorSystem(); actorSystem != nil {
		actorSystem.recordActorStopped(pid)
	}

	pid.stopLocker.Unlock()
	pid.getLogger().Debugf("actor=%s successfully shutdown", pid.Name())
	return nil
}

// Watch registers pid to receive a Terminated message when cid shuts down.
// It is a no-op when pid itself is a remote handle or cid is nil.
// When cid is remote, the watch is registered on cid's host via the remoting
// layer and tracked locally in the remote watch registry so freeWatchees can
// release it on shutdown. Failures of the remote RPC are logged and the
// local registration is skipped. The remote call blocks until the RPC
// returns; callers should account for the round-trip cost.
func (pid *PID) Watch(cid *PID) {
	if pid.IsRemote() || cid == nil {
		return
	}

	if cid.IsRemote() {
		cidAddr := cid.getAddress()

		// Prefer the remoting client embedded in the cid handle (the one that
		// resolved cid in the first place); fall back to the watcher's own
		// remoting client to mirror PID.Stop's selection policy.
		r := cid.remoting
		if r == nil {
			r = pid.remoting
		}

		if r == nil {
			pid.getLogger().Debugf("watch: cannot register remote watch for %s: remoting disabled", cidAddr)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), pid.ActorSystem().getRemoteWatchTimeout())
		defer cancel()

		if err := r.RemoteWatch(ctx, cidAddr.Host(), cidAddr.Port(), cidAddr.QualifiedName(), pid.getAddress()); err != nil {
			pid.getLogger().Debugf("watch: RemoteWatch to %s failed: %v", cidAddr, err)
			return
		}

		// Local registration happens only after the remote acknowledged, so
		// freeWatchees never tries to unwatch something the remote does not
		// actually know about.
		pid.ActorSystem().getRemoteWatchRegistry().addWatchee(pid.ID(), cidAddr)
		return
	}

	pid.ActorSystem().tree().addWatcher(cid, pid)
}

// UnWatch cancels the watch previously registered by Watch for cid.
// It is a no-op when pid itself is a remote handle or cid is nil.
// When cid is remote, the local registration is dropped first so the local
// state is always clean even if the best-effort RemoteUnWatch fails;
// transport failures are logged but not surfaced.
func (pid *PID) UnWatch(cid *PID) {
	if pid.IsRemote() || cid == nil {
		return
	}

	if cid.IsRemote() {
		cidAddr := cid.getAddress()

		// Drop local state first so a failing RPC cannot leave a stale entry.
		pid.ActorSystem().getRemoteWatchRegistry().removeWatchee(pid.ID(), cidAddr)

		// Same remoting selection as PID.Watch / PID.Stop: prefer cid's own
		// client, fall back to the watcher's.
		r := cid.remoting
		if r == nil {
			r = pid.remoting
		}

		if r == nil {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), pid.ActorSystem().getRemoteWatchTimeout())
		defer cancel()

		if err := r.RemoteUnWatch(ctx, cidAddr.Host(), cidAddr.Port(), cidAddr.QualifiedName(), pid.getAddress()); err != nil {
			pid.getLogger().Debugf("unwatch: RemoteUnWatch to %s failed: %v", cidAddr, err)
		}
		return
	}

	pid.ActorSystem().tree().removeWatcher(cid, pid)
}

// getEventsStream returns the actor system's event stream, or nil for a PID
// that has no system. Publishers check for nil before emitting. The system
// pointer is read under the field lock for the same reason as in getLogger.
func (pid *PID) getEventsStream() eventstream.Stream {
	pid.fieldsLocker.RLock()
	system := pid.actorSystem
	pid.fieldsLocker.RUnlock()

	if system == nil {
		return nil
	}

	return system.getEventsStream()
}

// observedMailboxSize returns the number of user messages currently waiting in
// this actor's mailbox, the value reported as actor.mailbox.size.
//
// The queue depth is the difference between the two counters the producers and
// the processing turn keep on their own cache lines. Neither is read on the
// message path, so the subtraction happens here, once per scrape. The counts are
// maintained only when metrics are enabled, so the difference reads zero
// otherwise. The two loads are not atomic with respect to each other and the
// consumer can advance between them, so the result is clamped at zero rather
// than surfacing a transient negative value.
func (pid *PID) observedMailboxSize() int64 {
	return max(pid.mailboxEnqueued.Load()-pid.mailboxDequeued.Load(), 0)
}

// getLogger returns the logger the actor reports through: the actor system's
// logger, or the package default for a PID that has no system, such as a
// remote handle. It is a lookup rather than a field so the PID does not carry
// a copy of what the system already holds. The system pointer is read under
// the field lock, like every other reader of it, so a caller that swaps the
// system under that lock never races with an actor that is logging.
func (pid *PID) getLogger() log.Logger {
	pid.fieldsLocker.RLock()
	system := pid.actorSystem
	pid.fieldsLocker.RUnlock()

	if system == nil {
		return defaultLogger
	}

	return system.getLogger()
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

	reentrant := pid.reentrancy.Load()
	if reentrant == nil {
		return nil, gerrors.ErrReentrancyDisabled
	}

	config := newRequestConfig(opts...)
	mode := reentrant.getMode()
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

	reentrant := pid.reentrancy.Load()
	if reentrant == nil {
		return nil, gerrors.ErrReentrancyDisabled
	}

	config := newRequestConfig(opts...)
	mode := reentrant.getMode()
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

	// Tell routes to the network itself when ActorOf resolved a remote handle.
	if err := pid.Tell(ctx, cid, req); err != nil {
		pid.deregisterRequestState(state)
		return nil, err
	}

	return &requestHandle{state: state}, nil
}

// requestGrain sends an asynchronous request to a Grain.
//
// It follows the same admission flow as request/requestName; only the
// delivery differs: the envelope goes through the grain runtime, which
// activates the target or forwards to its owning node. The reply comes back
// addressed to this actor and completes through the mailbox like any other
// async response.
func (pid *PID) requestGrain(ctx context.Context, to *GrainIdentity, message any, opts ...RequestOption) (RequestCall, error) {
	if !pid.IsRunning() {
		return nil, gerrors.ErrDead
	}

	if message == nil {
		return nil, gerrors.ErrInvalidMessage
	}

	reentrant := pid.reentrancy.Load()
	if reentrant == nil {
		return nil, gerrors.ErrReentrancyDisabled
	}

	if to == nil {
		return nil, gerrors.ErrInvalidGrainIdentity
	}

	if err := to.Validate(); err != nil {
		return nil, gerrors.NewErrInvalidGrainIdentity(err)
	}

	config := newRequestConfig(opts...)
	mode := reentrant.getMode()
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

	if err := pid.ActorSystem().deliverAsyncEnvelope(ctx, to, req); err != nil {
		pid.deregisterRequestState(state)
		// the error goes to the actor, so it carries no node refusal mark
		return nil, refusal.Unmark(err)
	}

	return &requestHandle{state: state}, nil
}

// buildAsyncRequest wraps the payload with correlation and reply metadata.
//
// Design decision: the reply target is carried typed so that replying never
// re-parses an address; it is rendered to a string only when the envelope is
// serialized for another node.
func (pid *PID) buildAsyncRequest(message any, correlationID string) (*commands.AsyncRequest, error) {
	if message == nil {
		return nil, gerrors.ErrInvalidMessage
	}

	return &commands.AsyncRequest{
		CorrelationID: correlationID,
		ReplyTo:       &commands.AsyncReplyTo{Kind: commands.ReplyToActor, Actor: pathToAddress(pid.Path())},
		Message:       message,
	}, nil
}

// trackRemoteHold registers a remote message's credit share in the actor's
// hold registry, creating the registry on first use. Tracking must happen
// before the message enters the mailbox so no window exists in which a
// queued share is invisible to teardown.
func (pid *PID) trackRemoteHold(share *inet.CreditShare) {
	registry := pid.remoteHolds.Load()

	if registry == nil {
		fresh := newRemoteHoldRegistry()

		if pid.remoteHolds.CompareAndSwap(nil, fresh) {
			registry = fresh
		} else {
			// A concurrent first tracker won the race; hand the loser's
			// sentinel back to the pool and use the winner's registry.
			remoteHoldNodePool.Put((*remoteHoldNode)(fresh.head))
			registry = pid.remoteHolds.Load()
		}
	}

	registry.track(share)

	if pid.isStateSet(remoteHoldsClosedState) {
		// A terminal stop completed between the caller's liveness check and
		// this track: the teardown drain has already walked the registry and
		// nothing will ever walk it again. Observing the closed bit after
		// the publish proves either the drain saw this entry or this
		// producer must repay it itself; the drain is idempotent and
		// serialized by consumerMu, so racing the teardown or another late
		// tracker is safe. This also covers a first-ever track that created
		// the registry after teardown: the fresh registry was never drained,
		// so the producer drains it here.
		registry.releaseAll()
	}
}

// doReceive enqueues a message onto the actor's user mailbox or system queue
// and schedules the actor onto the dispatcher if it is not already in
// flight. This is the entry point for internal message delivery that
// may carry control-plane messages (PoisonPill, Panicking, etc.).
func (pid *PID) doReceive(receiveCtx *ReceiveContext) {
	msg := receiveCtx.Message()

	if system := pid.actorSystem; system != nil && system.isStopping() {
		if !isSystemMessage(msg) {
			pid.handleReceivedError(receiveCtx, gerrors.ErrSystemShuttingDown)
			// The refused context never enters the mailbox: repay its remote
			// credit share now instead of pinning the peer's window until
			// this actor's own teardown runs releaseAll.
			receiveCtx.releaseRemoteHold()
			return
		}
	}

	if isControlMessage(msg) {
		pid.systemQueue.push(receiveCtx)
	} else {
		if err := pid.mailbox.Enqueue(receiveCtx); err != nil {
			pid.getLogger().Warn(err)
			pid.handleReceivedError(receiveCtx, err)
			// The refused context never enters the mailbox, so no dequeue or
			// recycle will ever release its remote credit share.
			receiveCtx.releaseRemoteHold()
			return
		}

		if pid.metricsEnabled {
			// the message is queued and waiting: count it for actor.mailbox.size.
			// The system queue is deliberately outside the reported size.
			pid.mailboxEnqueued.Add(1)
		}
	}

	if pid.schedState.TrySchedule() {
		pid.dispatcher.schedule(pid)
	}
}

// runTurn implements schedulable. A dispatcher worker calls this after
// pulling the actor off the ready queue. The method takes exclusive
// ownership via the Scheduled -> Processing CAS, drains up to the
// dispatcher's throughput budget by interleaving system and user
// mailbox messages (system messages always win), then either yields
// back to Scheduled (and re-pushes onto the worker's local queue) or
// transitions to Idle with a race-safe reclaim if a concurrent enqueue
// slipped in.
func (pid *PID) runTurn(w *worker) {
	if !pid.schedState.TakeForProcessing() {
		return
	}

	now := time.Now()
	budget := w.dispatcher.throughput
	for range budget {
		// PostStart goes first at every message boundary, not only at the
		// start of the turn: a restart can arm it while this turn is already
		// running, and the messages still queued must not run ahead of it.
		pid.runPendingPostStart(now)

		if sysMsg := pid.systemQueue.pop(); sysMsg != nil {
			pid.dispatchOne(sysMsg, now)
			continue
		}

		// User messages wait while the actor cannot handle them: it is
		// suspended, stopping or restarting, or a failure awaits a supervision
		// decision. They stay queued for the incarnation that resumes, as in
		// any actor model, instead of running on a failed or torn-down actor.
		if !pid.handlesUserMessages() {
			if pid.releaseWithheldTurn() {
				return
			}

			continue
		}

		received := pid.mailbox.Dequeue()
		if received == nil {
			if pid.finishOrReclaim() {
				return
			}
			continue
		}

		// the message has left the queue: it no longer counts towards
		// actor.mailbox.size, which excludes the in-flight message.
		if pid.metricsEnabled {
			pid.mailboxDequeued.Add(1)
		}

		pid.dispatchOne(received, now)
	}
	pid.schedState.YieldToScheduled()
	w.reschedule(pid)
}

// finishOrReclaim attempts the Processing -> Idle transition. Returns
// true when the caller must exit the turn (no work remains and ownership
// is fully released). Returns false when a concurrent enqueue raced the
// transition, ownership was reclaimed, and the caller must continue
// draining within the same budget.
//
// The check is inlined (rather than passed as a closure) so the hot-path
// does not allocate a method-bound closure on every turn end.
func (pid *PID) finishOrReclaim() bool {
	pid.schedState.reset()
	if pid.mailbox.IsEmpty() && pid.systemQueue.isEmpty() && pid.postStart.Load() == nil {
		// The actor is idle: raise the message-count trigger again, so a
		// passivation that was refused while this turn ran is retried.
		if pid.passivationManager != nil && pid.msgCountPassivation.Load() {
			pid.passivationManager.MessageProcessed(pid)
		}

		return true
	}

	if !pid.schedState.TrySchedule() {
		return true
	}
	return !pid.schedState.TakeForProcessing()
}

// handlesUserMessages reports whether the turn may hand the actor a user
// message: it is running, not stopping or restarting, not suspended, and no
// failure is waiting for a supervision decision. Control messages are handled
// regardless.
func (pid *PID) handlesUserMessages() bool {
	state := pid.state.Load()
	return state&uint32(runningState) != 0 &&
		state&uint32(stoppingState|suspendedState|supervisionPendingState) == 0
}

// releaseWithheldTurn gives up the actor while its user messages wait (see
// handlesUserMessages). It returns false when the turn took the actor back
// because there is work it may do: a control message or a PostStart that
// arrived meanwhile, or user messages the actor can handle again.
//
// The state is reset before that check, and a resumer (a supervision decision
// or a reinstate) clears its flag before it tries to schedule the actor, so a
// resume that races with this release is seen by one of the two: either the
// check below finds the actor able to handle its messages, or the resumer's
// TrySchedule finds the actor idle.
func (pid *PID) releaseWithheldTurn() bool {
	pid.schedState.reset()
	if !pid.hasRunnableWork() {
		return true
	}

	if !pid.schedState.TrySchedule() {
		return true
	}
	return !pid.schedState.TakeForProcessing()
}

// hasRunnableWork reports whether a turn would find something to do: a
// control message, a pending PostStart, or user messages the actor may handle.
func (pid *PID) hasRunnableWork() bool {
	if !pid.systemQueue.isEmpty() || pid.postStart.Load() != nil {
		return true
	}

	return pid.handlesUserMessages() && !pid.mailbox.IsEmpty()
}

// scheduleWithheldWork schedules a turn for the messages that waited while the
// actor could not handle them. Called when it can again: after a supervision
// decision and after a reinstate.
func (pid *PID) scheduleWithheldWork() {
	if pid.dispatcher == nil || pid.mailbox == nil {
		return
	}

	if pid.hasRunnableWork() && pid.schedState.TrySchedule() {
		pid.dispatcher.schedule(pid)
	}
}

// resumeAfterSupervision lifts the pause a failure put on the user messages
// once supervision has decided, and schedules a turn for those that waited. A
// decision that suspended the actor keeps them waiting through the suspension.
func (pid *PID) resumeAfterSupervision() {
	pid.setState(supervisionPendingState, false)
	pid.scheduleWithheldWork()
}

// dispatchOne routes a single message to its handler. Release is owned by
// the queues: UnboundedMailbox.Dequeue reclaims the previous sentinel and
// systemQueue.pop the previously popped message. dispatchOne must not return
// the context here or the mailbox would hand out an in-use head.
func (pid *PID) dispatchOne(received *ReceiveContext, now time.Time) {
	// The message has left the mailbox: grant back any remote credit share
	// now, so flow control tracks mailbox residency rather than processing
	// time, and let the hold registry retire spent tracking entries. Local
	// messages skip both on a single nil check.
	if received.remoteHold != nil {
		received.releaseRemoteHold()

		if registry := pid.remoteHolds.Load(); registry != nil {
			registry.compact()
		}
	}

	// An Ask whose sender stopped waiting while the message sat in the
	// mailbox or in the stash is not handled: nobody reads the answer, and
	// under load that work is what keeps the actor behind. The sender
	// recorded the timeout as a deadletter when it gave up. An Ask already
	// in Receive when its sender gives up still runs to the end.
	if askExpired(received.deadline) {
		return
	}

	if pid.enableReentrancyStash(received) {
		if err := pid.stash(received); err != nil {
			pid.getLogger().Warn(err)
			pid.handleReceivedError(received, err)
		}
		return
	}
	switch msg := received.Message().(type) {
	case *PoisonPill:
		_ = pid.Shutdown(received.Context())
	case *commands.Panicking:
		pid.handlePanicking(received.Sender(), msg)
	case *PausePassivation:
		pid.pausePassivation()
	case *ResumePassivation:
		pid.resumePassivation()
	case *commands.AsyncRequest:
		pid.handleAsyncRequest(received, msg, now)
	case *commands.AsyncResponse:
		pid.handleAsyncResponse(received, msg)
	default:
		pid.handleReceived(received, now)
	}
}

// handleReceived picks the right behavior and processes the message
func (pid *PID) handleReceived(received *ReceiveContext, now time.Time) {
	defer pid.recovery(received)

	// only an Ask can have a derived context to release
	if received.deadline != 0 {
		defer received.releaseDeadlineContext()
	}

	if behavior := pid.behaviorStack.Peek(); behavior != nil {
		pid.markActivity(now)
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
	reentrant := pid.reentrancy.Load()
	if reentrant == nil || reentrant.blockingCount.Load() <= 0 {
		return false
	}

	switch received.Message().(type) {
	case *commands.AsyncResponse,
		*PoisonPill,
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
func (pid *PID) handleAsyncRequest(received *ReceiveContext, req *commands.AsyncRequest, now time.Time) {
	if received == nil || req == nil {
		pid.handleReceivedError(received, gerrors.ErrInvalidMessage)
		return
	}

	if req.CorrelationID == "" || !req.ReplyTo.Valid() || req.Message == nil {
		pid.handleReceivedError(received, gerrors.ErrInvalidMessage)
		return
	}

	received.message = req.Message
	received.response = nil
	received.withRequestMeta(req.CorrelationID, req.ReplyTo)
	pid.handleReceived(received, now)
}

// handleAsyncResponse resolves an AsyncResponse and completes the tracked call.
// An unknown correlation ID is a normal race with timeout or cancellation: the
// reply arrived after its request was already completed. It is dropped and
// reported at debug level only, as on the grain side.
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
		if !pid.completeRequest(correlationID, nil, asyncErrorFromString(resp.Error)) && pid.getLogger().Enabled(log.DebugLevel) {
			pid.getLogger().Debugf("async response dropped: unknown correlation id=%s", correlationID)
		}
		return
	}

	// A response without a payload and without an error is a successful reply
	// with nothing to return: a grain answered the request with NoErr.
	if !pid.completeRequest(correlationID, resp.Message, nil) && pid.getLogger().Enabled(log.DebugLevel) {
		pid.getLogger().Debugf("async response dropped: unknown correlation id=%s", correlationID)
	}
}

// enableReentrancy installs or retunes the actor's async request policy at
// runtime. In-flight requests keep the mode they were admitted with.
func (pid *PID) enableReentrancy(config *reentrancy.Reentrancy) error {
	return installReentrancy(&pid.reentrancy, config)
}

// disableReentrancy turns off async requests without disturbing in-flight
// ones: they complete normally while new Request calls are rejected until a
// later enableReentrancy. A no-op when reentrancy was never enabled.
func (pid *PID) disableReentrancy() {
	if reentrant := pid.reentrancy.Load(); reentrant != nil {
		reentrant.disable()
	}
}

// registerRequestState tracks an in-flight async request and enforces limits.
//
// Design decision: blockingCount reflects stash-mode requests so stashing can
// release only when the last blocking call completes.
func (pid *PID) registerRequestState(state *requestState) error {
	reentrant := pid.reentrancy.Load()
	if reentrant == nil {
		return gerrors.ErrReentrancyDisabled
	}

	if state == nil {
		return gerrors.ErrInvalidMessage
	}

	if maxInFlight := reentrant.maxInFlight.Load(); maxInFlight > 0 {
		for {
			current := reentrant.inFlightCount.Load()
			if current >= maxInFlight {
				return gerrors.ErrReentrancyInFlightLimit
			}

			if reentrant.inFlightCount.CompareAndSwap(current, current+1) {
				break
			}
		}
	} else {
		reentrant.inFlightCount.Inc()
	}

	if state.mode == reentrancy.StashNonReentrant {
		if pid.stashState.Load() == nil {
			pid.stashState.Store(&stashState{box: NewUnboundedMailbox()})
		}
		reentrant.blockingCount.Inc()
	}

	reentrant.requestStates.Set(state.id, state)
	return nil
}

// deregisterRequestState removes an in-flight async request and releases stashed messages.
//
// Design decision: when the last blocking request completes, unstash all messages.
// They are appended to the mailbox (see unstashAll), behind the messages that
// arrived after the response.
func (pid *PID) deregisterRequestState(state *requestState) {
	reentrant := pid.reentrancy.Load()
	if reentrant == nil || state == nil {
		return
	}

	if _, ok := reentrant.requestStates.Get(state.id); !ok {
		return
	}

	reentrant.requestStates.Delete(state.id)
	reentrant.inFlightCount.Dec()
	if state.mode == reentrancy.StashNonReentrant {
		remaining := reentrant.blockingCount.Dec()
		if remaining == 0 {
			if err := pid.unstashAll(); err != nil {
				pid.getLogger().Warn(err)
			}
		}
	}

	state.stopTimeoutIfSet()
}

// completeRequest marks an async request as completed and runs its callback.
//
// Design decision: completion is idempotent; only the first result wins.
func (pid *PID) completeRequest(correlationID string, result any, err error) bool {
	reentrant := pid.reentrancy.Load()
	if reentrant == nil {
		return false
	}

	state, ok := reentrant.requestStates.Get(correlationID)
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

	receiveContext := getContext(pid.ctxShard)
	receiveContext.build(ctx, pid, pid, response, true)
	pid.doReceive(receiveContext)
	return nil
}

// cancelInFlightRequests completes all in-flight async calls with the given reason.
//
// Design decision: cancellations are local-only to keep shutdown fast and avoid
// cross-node coordination.
func (pid *PID) cancelInFlightRequests(reason error) {
	reentrant := pid.reentrancy.Load()
	if reentrant == nil {
		return
	}

	keys := reentrant.requestStates.Keys()
	for _, key := range keys {
		state, ok := reentrant.requestStates.Get(key)
		if !ok || state == nil {
			continue
		}
		if _, completed := state.complete(nil, reason); !completed {
			continue
		}
		reentrant.requestStates.Delete(key)
		reentrant.inFlightCount.Dec()
		if state.mode == reentrancy.StashNonReentrant {
			reentrant.blockingCount.Dec()
		}
		state.stopTimeoutIfSet()
	}

	reentrant.inFlightCount.Store(0)
	reentrant.blockingCount.Store(0)
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
				pid.submitSupervision(newSupervisionSignal(pe, received.Message()))
				return
			}

			// this is a normal error just wrap it with some stack trace
			// for rich logging purpose; the error itself stays the cause, so
			// the supervisor's rule for its type applies (see directiveFor)
			pc, fn, line, _ := runtime.Caller(2)
			pid.submitSupervision(newPanicSupervisionSignal(
				gerrors.NewPanicError(
					fmt.Errorf("%w at %s[%s:%d]", err, runtime.FuncForPC(pc).Name(), fn, line),
				), err, received.Message()))

		default:
			// we have no idea what panic it is. Enrich it with some stack trace for rich
			// logging purpose
			pc, fn, line, _ := runtime.Caller(2)
			pid.submitSupervision(newSupervisionSignal(
				gerrors.NewPanicError(
					fmt.Errorf("%#v at %s[%s:%d]", r, runtime.FuncForPC(pc).Name(), fn, line),
				), received.Message()))
		}
		return
	}
	if err := received.getError(); err != nil {
		pid.submitSupervision(newSupervisionSignal(err, received.Message()))
	}
}

// submitSupervision routes a failure signal to the shared supervision consumer
// owned by the dispatcher. It falls back to handling the signal inline for PIDs
// constructed without a dispatcher (edge construction paths and tests).
//
// From the failure until the decision the actor handles no user message (see
// handlesUserMessages): the messages queued behind a failure must not run on
// the failed actor. ErrDead is never supervised (see notifyParent), so it
// pauses nothing.
func (pid *PID) submitSupervision(signal *supervisionSignal) {
	if signal == nil || errors.Is(signal.Err(), gerrors.ErrDead) {
		return
	}

	pid.setState(supervisionPendingState, true)
	if pid.dispatcher != nil && pid.dispatcher.submitSupervision(pid, signal) {
		return
	}

	// no consumer took the signal: decide inline when there is no dispatcher,
	// and lift the pause in every case so the actor is not held forever
	if pid.dispatcher == nil {
		pid.notifyParent(signal)
	}

	pid.resumeAfterSupervision()
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

// effectiveInitTimeout resolves the actor's init timeout: the explicit
// WithInitTimeout override when set, otherwise the actor system's default.
func (pid *PID) effectiveInitTimeout() time.Duration {
	if override := pid.initTimeout.Load(); override != nil {
		return *override
	}

	return pid.actorSystem.getInitTimeout()
}

// init initializes the given actor and init processing messages
// when the initialization failed the actor will not be started
func (pid *PID) init(ctx context.Context) error {
	pid.getLogger().Debugf("initialization process started for actor %s", pid.Name())

	initContext := newContext(ctx, pid.Name(), pid.actorSystem, pid.Dependencies()...)

	initTimeout := pid.effectiveInitTimeout()
	cctx, cancel := context.WithTimeout(ctx, initTimeout)
	retrier := retry.NewRetrier(int(pid.initMaxRetries.Load()), time.Millisecond, initTimeout)

	if err := retrier.RunContext(cctx, func(_ context.Context) error {
		return pid.actor.PreStart(initContext)
	}); err != nil {
		e := gerrors.NewErrInitFailure(err)
		cancel()
		pid.getLogger().Errorf("failed to initialize actor %s: %v (hint: check PreStart, verify dependencies)", pid.Name(), err)
		return e
	}

	// Reopen remote hold tracking before the actor is announced as running:
	// reset() closed it for the teardown drain embedded in a restart, and a
	// live actor parks inbound remote credit again.
	pid.setState(remoteHoldsClosedState, false)
	// Arm PostStart before the actor is announced as running: from here on a
	// sender can reach it, and the turn its message triggers must handle
	// PostStart first.
	pid.armPostStart(ctx)
	pid.setState(runningState, true)
	pid.getLogger().Debugf("actor=%s initialization successful", pid.Name())

	if pid.getEventsStream() != nil {
		pid.getEventsStream().Publish(eventsTopic, NewActorStarted(pid.Path()))
	}

	cancel()
	return nil
}

// reset re-initializes the actor PID
func (pid *PID) reset() {
	pid.latestReceiveTimeNano.Store(0)
	pid.latestReceiveDuration.Store(0)
	// initMaxRetries and initTimeout are deliberately left untouched: they are
	// spawn-time configuration set from options (including a WithInitTimeout
	// override), not runtime state. reset() runs in the shutdown embedded in a
	// restart, which re-runs init() without re-applying options, so wiping them
	// here would silently drop the configured init budget on restart. A fresh
	// spawn allocates a new PID and receives its defaults from newPID.
	pid.behaviorStack.Reset()
	pid.processedCount.Store(0)
	pid.reinstateCount.Store(0)
	pid.unhandledCount.Store(0)
	// A restart keeps the mailbox and the messages still in it, so the two
	// counts behind actor.mailbox.size keep describing them. A terminal stop
	// abandons the mailbox, and the counts restart from zero.
	restarting := pid.isStateSet(restartingState)
	if !restarting {
		pid.mailboxEnqueued.Store(0)
		pid.mailboxDequeued.Store(0)
	}

	// the restart and failure counts are cumulative across restarts, which a
	// failure causes; a terminal stop ends them
	if !restarting {
		pid.restartCount.Store(0)
		pid.failureCount.Store(0)
	}

	pid.startedAt.Store(0)
	pid.setState(runningState, false)
	pid.setState(stoppingState, false)
	pid.setState(suspendedState, false)
	pid.setState(supervisionPendingState, false)
	// the supervisor is deliberately left untouched: it is the object the user
	// passed to WithSupervisor and may be shared across actors. Wiping it here
	// erased the directive rules of every actor spawned with the same instance
	// and of the actor itself after a restart while running (#1269).
	// Queued messages survive a restart: the turn stops handing out user
	// messages once the actor is stopping (see handlesUserMessages), and the
	// restart reuses the mailbox. A terminal stop abandons them to the garbage
	// collector, which would strand their credit shares and permanently shrink
	// the peer connection's flow-control window. The hold registry tracks
	// every share independently of the mailbox, so this grants them all back
	// regardless of the mailbox implementation: messages that survive a
	// restart later release as no-ops (idempotent), and a terminal stop repays
	// the peers immediately.
	//
	// The closed bit must be set before the drain: a remote delivery that
	// passed its liveness check before this teardown can still track its
	// share afterwards, and nothing ever walks the registry again after a
	// terminal stop. Atomics are sequentially consistent, so a track whose
	// publish lands after the drain finished is guaranteed to observe the
	// bit and repay its own share (see trackRemoteHold); init() clears the
	// bit when a restart brings the actor back.
	pid.setState(remoteHoldsClosedState, true)

	if registry := pid.remoteHolds.Load(); registry != nil {
		registry.releaseAll()
	}

	// A restart reuses the mailbox, so it must not be disposed of: a
	// BoundedMailbox's Dispose is final, and every later Enqueue would fail.
	if !restarting {
		pid.mailbox.Dispose()
	}

	// Note: singletonState and relocationState are deliberately left untouched as
	// well: both are spawn-time configuration applied once by newPID through
	// asSingleton and withRelocationDisabled. The restart path re-runs init()
	// on the same PID without re-applying options, so flipping them back to
	// their defaults here turned a WithRelocationDisabled actor into a
	// relocatable one after Restart, and a stopped PID awaiting DeathWatch
	// cleanup reported the wrong configuration to IsRelocatable, IsSingleton,
	// toSerialize and the remote state endpoint (#1349).

	// The dependencies are spawn-time configuration, like the settings above:
	// the restart re-runs PreStart on the same PID with the same dependencies.
	if pid.dependencies != nil && !restarting {
		pid.dependencies.Reset()
	}

	pid.setState(passivationPausedState, false)
	pid.setState(passivatingState, false)
	pid.setState(passivationSkipNextState, false)
	pid.reentrancy.Load().reset()
}

// freeWatchers tells the given watchers of this actor that it terminated and
// releases them. The watchers are those recorded in the tree before the name was
// released (see doStop).
// Local watchers receive a Terminated message via the regular mailbox path; a
// watcher that is suspended receives it too, since control messages are handled
// while an actor is suspended, so it is not lost to the suspension.
// Remote watchers receive a Terminated message via fire-and-forget RemoteTell;
// its wire encoding is provided by terminatedSerializer and decoded back to
// *Terminated on the receiving node, so user actors observe a single message
// type regardless of locality. No synchronous liveness probe is issued, so
// the shutdown path is not gated on the reachability of any remote peer.
// An actor torn down by a restart is not dead: nobody is told and every watch
// is kept, the death watch's included, since the restart updates the actor's
// registry record in place.
func (pid *PID) freeWatchers(ctx context.Context, watchers []*PID) {
	logger := pid.getLogger()
	if pid.isStateSet(restartingState) {
		logger.Debugf("actor=%s is restarting: its watchers are kept", pid.Name())
		return
	}

	logger.Debugf("freeing all actor %s's watchers", pid.Name())

	for _, watcher := range watchers {
		terminated := NewTerminated(pid.Path())

		switch {
		case watcher.IsRunning():
			logger.Debugf("watcher %s releasing watched %s", watcher.Name(), pid.Name())
			// ignore error here because the watcher is running
			_ = pid.Tell(ctx, watcher, terminated)
		case watcher.IsSuspended():
			logger.Debugf("suspended watcher %s releasing watched %s", watcher.Name(), pid.Name())
			pid.tellSuspended(ctx, watcher, terminated)
		default:
			continue
		}

		watcher.UnWatch(pid)
		logger.Debugf("watcher %s released watched %s", watcher.Name(), pid.Name())
	}

	remoteWatchRegistry := pid.ActorSystem().getRemoteWatchRegistry()
	remoteWatchers := remoteWatchRegistry.watchersFor(pid.ID())
	if len(remoteWatchers) > 0 && pid.remoting != nil {
		terminated := NewTerminated(pid.Path())
		from := pid.getAddress()

		for _, watcherAddr := range remoteWatchers {
			if err := pid.remoting.RemoteTell(ctx, from, watcherAddr, terminated); err != nil {
				logger.Debugf("freeWatchers: RemoteTell Terminated to %s failed: %v", watcherAddr, err)
			}
		}
	}

	// Clear both watcher and watchee entries for this pid, now that the remote
	// watchers have been told, so a shutdown followed by re-use of the same id
	// does not see stale state.
	remoteWatchRegistry.dropPID(pid.ID())

	if len(watchers) == 0 && len(remoteWatchers) == 0 {
		logger.Debugf("actor=%s has no watchers, maybe already freed", pid.Name())
		return
	}

	logger.Debugf("all actor %s's watchers freed", pid.Name())
}

// tellSuspended delivers a control message to a suspended actor. Tell refuses a
// suspended target, but control messages are handled while an actor is
// suspended, so a Terminated must not be lost to the suspension.
func (pid *PID) tellSuspended(ctx context.Context, to *PID, message any) {
	receiveContext := getContext(to.ctxShard)
	receiveContext.build(ctx, pid, to, message, true)
	to.doReceive(receiveContext)
}

// freeWatchees releases all actors that have been watched by this actor.
// Local watchees are released via pid.UnWatch on the existing tree path.
// Remote watchees are released by sending a best-effort RemoteUnWatch to each
// peer; failures are logged at debug and do not block shutdown. Each remote
// watchee entry is dropped from the registry once its peer has been told; the
// entries for this actor's own remote watchers are kept for freeWatchers, so
// neither direction keeps stale entries after the actor has terminated.
func (pid *PID) freeWatchees(ctx context.Context) error {
	logger := pid.getLogger()
	logger.Debugf("freeing all actor %s's watched actors", pid.Name())

	tree := pid.ActorSystem().tree()
	watchees := tree.watchees(pid)
	for _, watched := range watchees {
		logger.Debugf("watcher %s unwatching actor %s", pid.Name(), watched.Name())
		pid.UnWatch(watched)
		logger.Debugf("watcher=%s unwatch actor=%s", pid.Name(), watched.Name())
	}

	remoteWatchRegistry := pid.ActorSystem().getRemoteWatchRegistry()
	remoteWatchees := remoteWatchRegistry.watcheesFor(pid.ID())
	if len(remoteWatchees) > 0 && pid.remoting != nil {
		from := pid.getAddress()
		timeout := pid.ActorSystem().getRemoteWatchTimeout()

		for _, watcheeAddr := range remoteWatchees {
			rpcCtx, cancel := context.WithTimeout(ctx, timeout)
			err := pid.remoting.RemoteUnWatch(rpcCtx, watcheeAddr.Host(), watcheeAddr.Port(), watcheeAddr.QualifiedName(), from)
			cancel()
			if err != nil {
				logger.Debugf("freeWatchees: RemoteUnWatch to %s failed: %v", watcheeAddr, err)
			}

			// the watch is gone whatever the peer answered; the remote watchers
			// of this actor are kept for freeWatchers to tell
			remoteWatchRegistry.removeWatchee(pid.ID(), watcheeAddr)
		}
	}

	if len(watchees) == 0 && len(remoteWatchees) == 0 {
		logger.Debugf("actor=%s has no watched actors, maybe already freed", pid.Name())
		return nil
	}

	logger.Debugf("actor=%s successfully unwatch all watched actors", pid.Name())
	return nil
}

// freeChildren releases all child actors
func (pid *PID) freeChildren(ctx context.Context) error {
	logger := pid.getLogger()
	logger.Debugf("actor=%s freeing all descendant actors", pid.Name())

	tree := pid.ActorSystem().tree()
	node, ok := tree.node(pid.ID())
	if !ok {
		pid.getLogger().Debugf("actor=%s node not found in actors tree", pid.Name())
		return nil
	}

	children := tree.children(pid)
	if len(children) > 0 {
		eg, ctx := errgroup.WithContext(ctx)
		for _, child := range children {
			eg.Go(func() error {
				logger.Debugf("parent %s disowning descendant %s", pid.Name(), child.Name())
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
					logger.Debugf("parent %s successfully disown descendant %s", pid.Name(), child.Name())
				}
				return nil
			})
		}

		if err := eg.Wait(); err != nil {
			logger.Errorf("parent=%s failed to free all descendant actors: %v (hint: check child supervision, shutdown order)", pid.Name(), err)
			return err
		}

		logger.Debugf("actor=%s successfully freed all descendant actors", pid.Name())
		return nil
	}
	pid.getLogger().Debugf("actor=%s has no children, maybe already freed", pid.Name())
	return nil
}

// tryPassivation evaluates the current passivation strategy and, when conditions are met,
// stops the actor to free up resources.
//
// Only an idle actor is passivated. An actor with a message in flight or
// waiting is left alone, so PostStop never runs alongside Receive here, and
// the attempt is retried later: at the next deadline for a time-based
// strategy, when the actor goes idle for a message-count one.
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
		pid.getLogger().Debugf("passivation decision skipped once for actor %s due to recent reinstate", pid.Name())
		return false
	}

	if pid.isStateSet(stoppingState) ||
		pid.isStateSet(suspendedState) ||
		pid.isStateSet(passivationPausedState) {
		pid.getLogger().Debugf("no need to passivate actor=%s", pid.Name())
		return false
	}

	pid.getLogger().Debugf("passivation mode triggered for actor=%s reason=%s", pid.Name(), reason)
	pid.stopLocker.Lock()
	defer pid.stopLocker.Unlock()

	// a Shutdown that took the lock first has stopped the actor already
	if !pid.isStateSet(runningState) || pid.isStateSet(stoppingState) {
		pid.getLogger().Debugf("passivation of actor=%s abandoned: the actor is stopped or stopping", pid.Name())
		return false
	}

	if pid.compareAndSwapState(passivationSkipNextState, true, false) {
		pid.getLogger().Debugf("passivation decision aborted for %s due to reinstate observed during critical section", pid.Name())
		return false
	}

	// Owning the dispatch state keeps every worker off the actor until the
	// stop is over. It can only be taken from an idle actor.
	if !pid.schedState.TakeIdleForStop() {
		pid.getLogger().Debugf("passivation of actor=%s deferred: the actor is processing messages", pid.Name())
		if pid.passivationManager != nil {
			pid.passivationManager.Defer(pid)
		}

		return false
	}

	// A message accepted just before the state was taken is waiting: its
	// sender found the actor idle and lost the race to schedule it. Hand the
	// actor back with that work scheduled, and retry later.
	if pid.hasRunnableWork() {
		pid.getLogger().Debugf("passivation of actor=%s deferred: a message arrived meanwhile", pid.Name())
		pid.schedState.reset()
		pid.scheduleWithheldWork()
		if pid.passivationManager != nil {
			pid.passivationManager.Defer(pid)
		}

		return false
	}

	defer pid.schedState.reset()

	// The passivating bit makes the actor refuse messages and report itself
	// not running. It is raised only once the stop is certain: an attempt
	// refused above leaves a busy actor untouched, so its senders, its parent
	// and the actors it watches never see a live actor as stopping.
	pid.setState(passivatingState, true)
	defer pid.setState(passivatingState, false)

	pid.unregisterPassivation()

	ctx := context.Background()
	if err := pid.doStop(ctx); err != nil {
		pid.getLogger().Errorf("failed to passivate actor=%s: %v (hint: check OnPassivate implementation)", pid.Name(), err)
		return false
	}

	if pid.getEventsStream() != nil {
		pid.getEventsStream().Publish(eventsTopic, NewActorPassivated(pid.Path()))
	}

	if actorSystem := pid.ActorSystem(); actorSystem != nil {
		actorSystem.recordActorPassivated(pid)
	}

	pid.getLogger().Debugf("actor=%s successfully passivated", pid.Name())
	return true
}

// setBehavior is a utility function that helps set the actor behavior
func (pid *PID) setBehavior(behavior Behavior) {
	pid.fieldsLocker.Lock()
	pid.behaviorStack.Reset()
	pid.behaviorStack.Push(behavior)
	pid.fieldsLocker.Unlock()
}

// resetBehavior returns the actor to its default behavior, Receive, and
// clears every stacked or swapped behavior, so a later UnBecomeStacked has
// nothing to return to.
func (pid *PID) resetBehavior() {
	pid.fieldsLocker.Lock()
	pid.behaviorStack.Reset()
	pid.behaviorStack.Push(pid.actor.Receive)
	pid.fieldsLocker.Unlock()
}

// setBehaviorStacked adds a behavior to the actor's behaviorStack
func (pid *PID) setBehaviorStacked(behavior Behavior) {
	pid.fieldsLocker.Lock()
	pid.behaviorStack.Push(behavior)
	pid.fieldsLocker.Unlock()
}

// unsetBehaviorStacked returns the actor to the behavior that was active
// before the last setBehaviorStacked. The bottom behavior is never popped: with
// nothing stacked the call has no effect, so the actor always has a behavior
// to handle its messages with.
func (pid *PID) unsetBehaviorStacked() {
	pid.fieldsLocker.Lock()
	if pid.behaviorStack.Len() > 1 {
		pid.behaviorStack.Pop()
	}
	pid.fieldsLocker.Unlock()
}

// doStop stops the actor: it releases the actors it watches, stops its
// children and runs PostStop. Whatever the outcome, the actor is dead once
// doStop returns, so the watchers are always told and the actor gives its name
// back before it is marked as not running. A caller that sees the stop return
// can therefore spawn the name again, and a lookup never finds the dead actor.
func (pid *PID) doStop(ctx context.Context) error {
	pid.cancelInFlightRequests(gerrors.ErrRequestCanceled)

	defer func() {
		pid.setState(runningState, false)
		pid.reset()
	}()

	err := chain.
		New(chain.WithFailFast()).
		AddRunner(func() error {
			// An actor torn down by a restart is not dead: it keeps watching
			// what it watched, as it keeps its watchers (see freeWatchers).
			if pid.isStateSet(restartingState) {
				return nil
			}

			return pid.freeWatchees(ctx)
		}).
		AddRunner(func() error { return pid.freeChildren(ctx) }).
		AddRunner(func() error {
			stopContext := newContext(ctx, pid.Name(), pid.actorSystem, pid.Dependencies()...)
			return pid.actor.PostStop(stopContext)
		}).
		Run()

	// The name is released before the watchers are told, so a watcher that
	// spawns the same name again on Terminated finds it free. Releasing the
	// name removes the tree node and its watch edges, so the watchers are read
	// first.
	watchers := pid.ActorSystem().tree().watchers(pid)
	pid.releaseName()
	pid.freeWatchers(ctx, watchers)

	if err != nil {
		return err
	}

	pid.getLogger().Debugf("shutdown process completed for actor %s", pid.Name())
	return nil
}

// releaseName removes the stopped actor from the actor tree and from the live
// actors count, which frees its name on this node. The teardown inside a
// restart keeps both: the restart re-initializes the same PID in place, and
// releasing the name in between would let a concurrent spawn take it.
// Removing the actor's cluster registry record is left to the death watch,
// because it can fail and is retried.
func (pid *PID) releaseName() {
	if pid.isStateSet(restartingState) {
		return
	}

	system := pid.ActorSystem()
	if system == nil {
		return
	}

	if system.tree().deleteNode(pid) && !pid.isStateSet(systemState) {
		system.decreaseActorsCounter()
	}
}

// notifyParent sends a notification to the parent actor
func (pid *PID) notifyParent(signal *supervisionSignal) {
	if signal == nil || errors.Is(signal.Err(), gerrors.ErrDead) {
		return
	}

	directive, ok := pid.directiveFor(signal)
	if !ok {
		pid.getLogger().Debugf("no supervisor directive found for error: %s", errorType(signal.Err()))
		pid.suspend(signal.Err().Error())
		return
	}

	pid.getLogger().Debugf("actor=%s supervisor directive=%s", pid.Name(), directive.String())

	// create the message to send to the parent
	msg := &commands.Panicking{
		Address:    pid.address,
		Err:        signal.Err(),
		Message:    signal.Msg(),
		Timestamp:  signal.Timestamp(),
		Strategy:   pid.supervisor.Strategy(),
		Directive:  directive,
		Supervisor: pid.supervisor,
	}

	if parent := pid.Parent(); parent != nil && !parent.Equals(pid.ActorSystem().NoSender()) {
		pid.getLogger().Warnf("actor=%s child=%s failing: err=%s", parent.Name(), pid.Name(), msg.Err.Error())
		pid.getLogger().Debugf("actor=%s activates strategy=%s directive=%s for failing child actor=%s",
			parent.Name(),
			pid.supervisor.Strategy(),
			directive,
			pid.Name())

		// For ResumeDirective, avoid suspending to minimize timing windows where the child appears
		// temporarily "not running" to observers. For other directives, keep suspension semantics.
		if directive == supervisor.ResumeDirective {
			// a resumed failure is a failure the supervisor acted on, so it counts
			pid.failureCount.Inc()
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
	pid.getLogger().Warnf("actor=%s has no parent to notify about failure: err=%s", pid.Name(), msg.Err.Error())
	pid.suspend(msg.Err.Error())
}

// directiveFor finds the supervisor rule for a failure. A panic with an error
// value is supervised like ctx.Err with that error: a rule for the panicked
// error's type comes first, then the rule for the reported error (PanicError
// for a panic), then the rule for any error. So the default PanicNilError rule
// applies to panic(nil), and a rule for a user error type applies whether the
// handler records or panics with it.
func (pid *PID) directiveFor(signal *supervisionSignal) (supervisor.Directive, bool) {
	if cause := signal.Cause(); cause != nil {
		if directive, ok := pid.supervisor.Directive(cause); ok {
			return directive, true
		}
	}

	if directive, ok := pid.supervisor.Directive(signal.Err()); ok {
		return directive, true
	}

	return pid.supervisor.Directive(new(gerrors.AnyError))
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
	if pid.getEventsStream() == nil {
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
	var sender *address.Address
	if system != nil {
		sender = system.NoSender().address
	}

	if senderPID != nil {
		if system == nil || !senderPID.Equals(system.NoSender()) {
			sender = senderPID.address
		}
	}

	receiver := pid.address
	if receiver == nil {
		return
	}

	ctx := context.Background()
	pid.toDeadletter(ctx, sender, receiver, message, err)
}

// toDeadletter sends a message to the deadletter actor
func (pid *PID) toDeadletter(ctx context.Context, from, to *address.Address, message any, err error) {
	system := pid.ActorSystem()
	if system == nil {
		return
	}
	deadletter := system.getDeadletter()
	command := &commands.SendDeadletter{
		Deadletter: commands.Deadletter{
			Sender:   from,
			Receiver: to,
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
		pid.getLogger().Errorf("undefined task: %v", gerrors.ErrUndefinedTask)
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
		pid.getLogger().Errorf("pipe task failed: %v", err)
		pid.toDeadletter(ctx, pid.address, pid.address, new(NoMessage), err)
		return
	}

	// make sure that the receiver is still alive
	to := completion.Receiver
	if !to.IsRunning() {
		pid.getLogger().Errorf("unable to pipe message to actor=%s: not started", to.Name())
		pid.toDeadletter(ctx, pid.address, pid.address, result, gerrors.ErrDead)
		return
	}

	messageContext := newReceiveContext(ctx, pid, to, result)
	to.doReceive(messageContext)
}

// handlePanicking watches for child actor's failure and act based upon the supervisory strategy
func (pid *PID) handlePanicking(cid *PID, msg *commands.Panicking) {
	if cid.ID() == msg.Address.String() {
		directive := msg.Directive
		includeSiblings := msg.Strategy == supervisor.OneForAllStrategy

		switch directive {
		case supervisor.StopDirective:
			pid.handleStopDirective(cid, includeSiblings)
		case supervisor.RestartDirective:
			pid.handleRestartDirective(cid, msg.Supervisor, includeSiblings)
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
		eg.Go(func() error {
			// TODO: revisit this
			//pid.UnWatch(spid)
			// A failed shutdown still leaves the actor dead and out of the
			// tree (see PID.doStop), so there is nothing left to suspend.
			if err := spid.Shutdown(ctx); err != nil {
				pid.getLogger().Error(fmt.Errorf("failed to shutdown Actor (%s): %w", spid.Name(), err))
			}

			return nil
		})
	}
	_ = eg.Wait()
}

// handleRestartDirective handles the Behavior restart directive
// handleRestartDirective handles the Behavior restart directive.
//
// The faulty child cid arrives here already suspended by notifyParent. The
// directive applies to cid alone (one-for-one) or to cid and its siblings
// (one-for-all). Each fault bumps the consecutive fault counter of every
// group member, which drives the two safeguards configured on the supervisor:
//   - restart budget (WithRetry): more than MaxRetries consecutive faults
//     within a positive reset window leaves the group suspended instead of
//     restarting it.
//   - exponential backoff (WithExponentialBackoff): the nth consecutive
//     restart is delayed by min(InitialDelay << (n-1), MaxDelay).
//
// The restarts themselves are fire-and-forget: each group member restarts on
// its own goroutine while the parent keeps processing messages.
func (pid *PID) handleRestartDirective(cid *PID, sup *supervisor.Supervisor, includeSiblings bool) {
	pids := []*PID{cid}
	if includeSiblings {
		pids = append(pids, pid.ActorSystem().tree().siblings(cid)...)
	}

	// the reset window is backoff's resetAfter when configured, otherwise the
	// WithRetry timeout
	window := sup.BackoffResetAfter()
	if window <= 0 {
		window = sup.Timeout()
	}

	// bump every group member so a one-for-all group exhausts its budget even
	// when the faults alternate between siblings; the faulty child's count
	// drives the decisions below
	faults := int64(0)

	for _, spid := range pids {
		count := spid.recordFault(window)

		if spid.Equals(cid) {
			faults = count
		}
	}

	// the budget only applies within a positive window: without one the
	// counter never resets, and a handful of faults spread over days would
	// eventually suspend an actor that is otherwise healthy
	if maxRetries := sup.MaxRetries(); maxRetries > 0 && window > 0 && faults > int64(maxRetries) {
		pid.suspendGroup(cid, pids, faults, maxRetries)
		return
	}

	delay := backoffDelay(faults, sup.InitialDelay(), sup.MaxDelay())

	for _, spid := range pids {
		go pid.restartChild(spid, sup, delay)
	}
}

// suspendGroup finalizes an exhausted restart budget. The faulty child cid is
// already suspended by notifyParent and is left that way; with a one-for-all
// strategy its still-running siblings are suspended too so the group stops as
// one unit. Suspended actors can be revived with Reinstate.
func (pid *PID) suspendGroup(cid *PID, pids []*PID, faults int64, maxRetries uint32) {
	pid.getLogger().Warnf("restart budget exhausted for actor=%s: %d consecutive failures with maxRetries=%d", cid.Name(), faults, maxRetries)
	reason := fmt.Sprintf("restart budget exhausted: %d consecutive failures", faults)

	for _, spid := range pids {
		if !spid.Equals(cid) && spid.IsRunning() {
			spid.suspend(reason)
		}
	}
}

// restartChild restarts one member of a supervised group after waiting out the
// backoff delay. It runs on its own goroutine: the sleep only parks this
// goroutine, never the parent, and needs no timer or cancellation because
// there is nothing else to do until the delay elapses.
func (pid *PID) restartChild(spid *PID, sup *supervisor.Supervisor, delay time.Duration) {
	ctx := context.Background()

	if delay > 0 {
		pause.For(delay)
	}

	// the parent may have stopped meanwhile, or the whole actor system may be
	// stopping; restarting then would resurrect the child into a torn-down
	// hierarchy, so skip instead. The parent keeps watching the child: a
	// restart tells no watcher (see freeWatchers), and when the restart fails
	// for good the parent is told that the child died.
	if !pid.IsRunning() || pid.ActorSystem().isStopping() {
		return
	}

	maxRetries := sup.MaxRetries()
	timeout := sup.Timeout()

	var err error

	switch {
	case maxRetries == 0 || timeout <= 0:
		err = spid.Restart(ctx)
	default:
		// bound the attempts when the restart itself keeps failing (e.g. a
		// PreStart error); reuse the backoff bounds when configured, otherwise
		// retry at the constant WithRetry pace
		initial, maximum := timeout, timeout
		if sup.InitialDelay() > 0 {
			initial, maximum = sup.InitialDelay(), sup.MaxDelay()
		}

		retrier := retry.NewRetrier(int(maxRetries), initial, maximum)
		err = retrier.RunContext(ctx, spid.Restart)
	}

	if err != nil {
		pid.getLogger().Errorf("restart directive failed for actor=%s: %v (hint: check PreStart/Receive for panics)", spid.Name(), err)
		if err := spid.Shutdown(ctx); err != nil {
			pid.getLogger().Errorf("shutdown after restart failure: %v", err)
			// we need to suspend the actor since it is faulty
			spid.suspend(err.Error())
		}
	}
}

// recordFault updates the actor's consecutive fault counter and returns the
// updated count. A previous fault older than the reset window resets the
// counter first; a non-positive window means the counter never resets.
func (pid *PID) recordFault(window time.Duration) int64 {
	now := time.Now().UnixNano()
	if last := pid.lastFaultAtNano.Load(); window > 0 && last > 0 && now-last > window.Nanoseconds() {
		pid.consecutiveFaults.Store(0)
	}

	pid.lastFaultAtNano.Store(now)
	return pid.consecutiveFaults.Inc()
}

// backoffDelay computes the exponential backoff delay for the nth consecutive
// fault: min(initialDelay << (n-1), maxDelay). A non-positive
// initialDelay disables backoff.
func backoffDelay(faults int64, initialDelay, maxDelay time.Duration) time.Duration {
	if initialDelay <= 0 || faults < 1 {
		return 0
	}

	// time.Duration is an int64 nanosecond count, so 62 doublings of even the
	// smallest positive delay (1ns << 62 ≈ 146 years) exceed any sane maxDelay
	// and one more doubling overflows int64. Cap early rather than rely on the
	// wraparound check below.
	shift := faults - 1
	if shift >= 62 {
		return maxDelay
	}

	// a single shift can still wrap around for larger initial delays
	// (e.g. 100ms << 40); a wrapped value is negative or huge, both clamp
	delay := initialDelay << uint(shift)
	if delay <= 0 || delay > maxDelay {
		return maxDelay
	}

	return delay
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
	pid.getLogger().Debugf("actor=%s going into suspension mode", pid.Name())
	pid.setState(suspendedState, true)
	// increment suspension count
	pid.failureCount.Inc()
	// pause passivation loop
	pid.pausePassivation()
	// publish an event to the events stream
	if stream := pid.getEventsStream(); stream != nil {
		stream.Publish(eventsTopic, NewActorSuspended(pid.Path(), reason))
	}
}

// getDeadlettersCount gets deadletter
func (pid *PID) getDeadlettersCount(ctx context.Context) int64 {
	var (
		address = pid.address
		to      = pid.ActorSystem().getDeadletter()
		from    = pid.ActorSystem().getSystemGuardian()
		message = &commands.DeadlettersCountRequest{
			Address: address,
		}
	)
	if to.IsRunning() {
		// ask the deadletter actor for the count using the system-wide ask timeout.
		// IsRunning is not atomic with Ask: if the deadletter actor transitions
		// to stopping between the guard and the reply, Ask returns (nil, ErrDead).
		// Discard the count in that case instead of asserting on a nil reply.
		resp, err := from.Ask(ctx, to, message, pid.ActorSystem().getAskTimeout())
		if err != nil || resp == nil {
			return 0
		}
		deadlettersCount, ok := resp.(*commands.DeadlettersCountResponse)
		if !ok || deadlettersCount == nil {
			return 0
		}
		return deadlettersCount.TotalCount
	}
	return 0
}

// armPostStart stores the PostStart of a new incarnation for the actor's next
// turn. The message enters neither the system queue nor the mailbox: runTurn
// handles it before both, so no message can overtake it, and a full bounded
// mailbox cannot refuse it. init calls it once PreStart has succeeded and
// before the actor is marked running.
func (pid *PID) armPostStart(ctx context.Context) {
	receiveContext := getContext(pid.ctxShard)
	receiveContext.build(ctx, pid.ActorSystem().NoSender(), pid, new(PostStart), true)
	pid.postStart.Store(receiveContext)
}

// firePostStart schedules a turn so the armed PostStart runs even when no
// other message arrives. A turn that some other message already triggered
// handles PostStart first, so this only ensures that one happens.
func (pid *PID) firePostStart() {
	if pid.schedState.TrySchedule() {
		pid.dispatcher.schedule(pid)
	}
}

// runPendingPostStart runs the pending PostStart, if any, and releases its
// context. The turn owner calls it before every message, so the common case,
// nothing pending, costs one atomic load of a line the turn already has and
// no write; the processing benchmarks show no measurable cost. Only the turn
// owner clears the slot, so a pending message seen by the load is still
// there for the swap. PostStart goes straight to the behavior and is never
// stashed: no request can be in flight before an incarnation has started.
func (pid *PID) runPendingPostStart(now time.Time) {
	if pid.postStart.Load() == nil {
		return
	}

	received := pid.postStart.Swap(nil)
	pid.handleReceived(received, now)
	recycleContext(received)
}

func (pid *PID) doReinstate() {
	pid.getLogger().Debugf("actor=%s reinstated", pid.Name())
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

	// resume passivation loop
	pid.resumePassivation()

	// publish an event to the events stream
	if stream := pid.getEventsStream(); stream != nil {
		stream.Publish(eventsTopic, NewActorReinstated(pid.Path()))
	}

	// the messages queued while the actor was suspended are handled now
	pid.scheduleWithheldWork()
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
	if pid.IsSingleton() && pid.singletonSpec() != nil {
		singletonSpec = &internalpb.SingletonSpec{}
		singletonSpec.SetSpawnTimeout(durationpb.New(pid.singletonSpec().SpawnTimeout))
		singletonSpec.SetWaitInterval(durationpb.New(pid.singletonSpec().WaitInterval))
		singletonSpec.SetMaxRetries(pid.singletonSpec().MaxRetries)
	}

	var reentrancy *internalpb.ReentrancyConfig
	if reentrant := pid.reentrancy.Load(); reentrant != nil {
		reentrancy = reentrant.toProto()
	}

	// carry the init timeout through relocation only when it was an explicit
	// override, so a relocated actor keeps its intent while an actor that
	// inherited a node default picks up the target node's default instead.
	var initTimeout *durationpb.Duration
	if override := pid.initTimeout.Load(); override != nil {
		initTimeout = durationpb.New(*override)
	}

	actor := &internalpb.Actor{}
	actor.SetAddress(pid.ID())
	actor.SetType(types.Name(pid.Actor()))
	actor.SetSingleton(singletonSpec)
	actor.SetRelocatable(pid.IsRelocatable())
	actor.SetPassivationStrategy(codec.EncodePassivationStrategy(pid.PassivationStrategy()))
	actor.SetDependencies(dependencies)
	stash := pid.stashState.Load()
	actor.SetEnableStash(stash != nil && stash.box != nil)
	if x := pid.Role(); x != nil {
		actor.SetRole(*x)
	}
	actor.SetSupervisor(supervisorSpec)
	actor.SetReentrancy(reentrancy)
	actor.SetInitTimeout(initTimeout)
	actor.SetIncarnationId(pid.incarnationID())
	actor.SetReliableDelivery(pid.reliableDelivery().toProto())
	actor.SetReliableCompanion(pid.reliableCompanion().toProto())
	return actor, nil
}

// buildObserveOptions caches what the actor system's metrics callback needs to
// observe this actor, and does nothing when metrics are disabled.
//
// The actor kind is cached in both metric modes: the default mode carries it as
// an attribute, and the low cardinality mode groups live actors by it. The
// per-actor attribute set is built in the default mode only. The low cardinality
// mode reports one series per kind and never names an individual actor, so it
// leaves observeOptions nil.
func (pid *PID) buildObserveOptions() {
	provider := pid.metricProvider()
	if provider == nil || provider.Meter() == nil {
		return
	}

	// the provider lives in the companion, so it exists whenever it is set
	companion := pid.companion
	companion.metricKind = types.Name(pid.Actor())
	if pid.actorSystem.lowCardinalityMetrics() {
		return
	}

	companion.observeOptions = []otelmetric.ObserveOption{
		otelmetric.WithAttributes(attribute.String("actor.system", pid.actorSystem.Name())),
		otelmetric.WithAttributes(attribute.String("actor.name", pid.Name())),
		otelmetric.WithAttributes(attribute.String("actor.kind", companion.metricKind)),
		otelmetric.WithAttributes(attribute.String("actor.address", pid.ID())),
	}
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

// spawnChildRemote dispatches a SpawnChild request to the remote node owning this PID.
// The spawn config is cloned with relocation disabled before being serialized into
// the request so that the remote child inherits the parent-local non-relocatable
// default without mutating the caller's configuration.
func (pid *PID) spawnChildRemote(ctx context.Context, name string, actor Actor, config *spawnConfig) (*PID, error) {
	clone := config.clone(WithRelocationDisabled())
	address, err := pid.remoting.RemoteSpawnChild(ctx, pid.address.Host(), pid.address.Port(), &remote.SpawnChildRequest{
		Name:                name,
		Kind:                types.Name(actor),
		Parent:              pid.Name(),
		Relocatable:         clone.relocatable,
		PassivationStrategy: clone.passivationStrategy,
		Supervisor:          clone.supervisor,
		Dependencies:        clone.dependencies,
		EnableStashing:      clone.enableStash,
		Reentrancy:          clone.reentrancy,
		InitTimeout:         pointer.Deref(clone.initTimeout, 0),
	})
	if err != nil {
		return nil, err
	}
	return newRemotePID(address, pid.remoting), nil
}

// spawnChildLocal creates and registers a child actor on the local actor system.
// It enforces the live-parent and reserved-name preconditions, returns an existing
// running child when the name is already in use, and otherwise materializes a new
// PID, attaches it to the supervision tree, and publishes the child-created event.
func (pid *PID) spawnChildLocal(ctx context.Context, name string, actor Actor, config *spawnConfig) (*PID, error) {
	if !pid.IsRunning() {
		return nil, gerrors.ErrDead
	}

	if !config.isSystem {
		// you should not create a system-based actor or
		// use the system actor naming convention pattern
		if isSystemName(name) {
			return nil, gerrors.ErrReservedName
		}
	}

	childAddress := pid.childAddress(name)
	tree := pid.actorSystem.tree()
	if existing, err := pid.childNameResolver(tree, childAddress.String(), name); existing != nil || err != nil {
		return existing, err
	}

	// Serialize concurrent spawns of the same child so only one PID is created
	// and inserted; concurrent callers coalesce onto the winner and share it.
	return pid.actorSystem.runSpawnActivation(ctx, childAddress.String(), func() (*PID, error) {
		if existing, err := pid.childNameResolver(tree, childAddress.String(), name); existing != nil || err != nil {
			return existing, err
		}

		if config.dependencies != nil {
			_ = pid.ActorSystem().Inject(config.dependencies...)
		}

		cid, err := newPID(
			ctx,
			childAddress,
			actor,
			pid.buildChildOptions(config)...,
		)
		if err != nil {
			return nil, err
		}

		// attach the child to the tree, supervise it and publish it to the cluster.
		// The tree may already hold this child, in which case completeSpawn
		// returns that one and the caller must get it, not the duplicate.
		spawned, err := pid.ActorSystem().completeSpawn(ctx, pid, cid)
		if err != nil {
			return nil, err
		}

		if spawned != cid {
			return spawned, nil
		}

		// the event is published only after successful cluster publication, so it
		// means durable creation
		eventsStream := pid.getEventsStream()
		if eventsStream != nil {
			eventsStream.Publish(eventsTopic, NewActorChildCreated(cid.Path(), pid.Path()))
		}

		return cid, nil
	})
}

// childNameResolver resolves a child spawn against the child registered at
// childAddress in tree. It returns the child when it is running,
// ErrActorAlreadyExists when it holds the name without running, and a nil PID
// with a nil error when the name is free (see nameResolver).
func (pid *PID) childNameResolver(tree *tree, childAddress, name string) (*PID, error) {
	cnode, ok := tree.node(childAddress)
	if !ok {
		return nil, nil
	}

	return nameResolver(cnode, name)
}

// buildChildOptions translates a spawn config into the pidOption list used
// when materializing a child PID. The parent's runtime wiring (logger, actor
// system, remoting, passivation manager, etc.) is always inherited; child-specific
// overrides from config are appended only when set.
func (pid *PID) buildChildOptions(config *spawnConfig) []pidOption {
	pidOptions := []pidOption{
		withInitMaxRetries(int(pid.initMaxRetries.Load())),
		withActorSystem(pid.actorSystem),
		withInitTimeout(config.initTimeout),
		withRemoting(pid.remoting),
		withPassivationManager(pid.passivationManager),
		withMetricProvider(pid.metricProvider()),
		withRelocationDisabled(), // by default child is not relocatable
	}

	if config.mailbox != nil {
		pidOptions = append(pidOptions, withMailbox(config.mailbox))
	}

	// a child without a supervisor of its own gets the system's default, as a
	// top-level actor does
	supervisor := config.supervisor
	if supervisor == nil {
		supervisor = pid.actorSystem.getDefaultSupervisor()
	}

	if supervisor != nil {
		pidOptions = append(pidOptions, withSupervisor(supervisor))
	}

	if config.enableStash {
		pidOptions = append(pidOptions, withStash())
	}

	if config.reentrancy != nil {
		pidOptions = append(pidOptions, withReentrancy(config.reentrancy))
	}

	if config.dependencies != nil {
		pidOptions = append(pidOptions, withDependencies(config.dependencies...))
	}

	pidOptions = append(pidOptions, withPassivationStrategy(config.passivationStrategy))
	return pidOptions
}

func (pid *PID) incarnationID() string {
	if path := pid.Path(); path != nil {
		return path.incarnationID()
	}
	return ""
}

// qualifiedName returns the actor's name qualified by its ancestors, read
// through the path without taking fieldsLocker: the path and its address are
// written once at construction. The actor tree calls it while holding its own
// lock, and Children and Stop take the two locks in the opposite order.
func (pid *PID) qualifiedName() string {
	if path := pid.Path(); path != nil {
		return path.QualifiedName()
	}
	return ""
}

// setRestarting raises or clears the restarting marker on every actor of the
// subtree.
func (x *restartNode) setRestarting(enabled bool) {
	x.pid.setState(restartingState, enabled)

	for _, child := range x.children {
		child.setRestarting(enabled)
	}
}

// terminate ends every actor of a subtree whose restart failed. Nothing is
// rolled back: the actors cannot return to the state they had before the
// restart, so all of them end up dead with their names free. Children are
// ended before their parent.
//
// At this point an actor of the subtree is in one of two states:
//   - Not running: the restart tore it down, and it either failed to start
//     again or was never reached. It is already dead, so it only tells its
//     watchers and frees its name. Telling the death watch is what removes
//     its registry record.
//   - Running: it was started again before another actor of the subtree
//     failed. A child does not outlive its parent, so it is stopped like any
//     other actor.
func (x *restartNode) terminate(ctx context.Context) {
	for _, child := range x.children {
		child.terminate(ctx)
	}

	pid := x.pid
	pid.setState(restartingState, false)

	if pid.isStateSet(runningState) {
		if err := pid.Shutdown(ctx); err != nil {
			pid.getLogger().Errorf("actor=%s failed to stop after a failed restart: %v (hint: check PostStop cleanup)", pid.Name(), err)
		}

		return
	}

	watchers := pid.ActorSystem().tree().watchers(pid)
	pid.releaseName()
	pid.freeWatchers(ctx, watchers)
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

	// Mark the actor as restarting for the whole pass. The Shutdown below is the
	// teardown half of a restart, not a stop, and the marker is how the stop path
	// tells the two apart.
	pid.setState(restartingState, true)
	defer pid.setState(restartingState, false)

	pid.cancelInFlightRequests(gerrors.ErrRequestCanceled)
	_, wasInTree := tree.node(pid.ID())

	// A running or suspended actor is stopped before it is started again, so
	// PostStop releases what PreStart acquired, as for any restart. The stop
	// skips the system-actor check of Shutdown: a supervisor may restart a
	// system actor.
	if pid.IsRunning() || pid.IsSuspended() {
		if err := pid.stop(ctx); err != nil {
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

	// Wait until no worker holds the actor before re-initializing. The
	// MPSC mailbox is single-consumer; restarting while a worker is mid
	// Dequeue would be a data race.
	for pid.schedState.Load() == dispatchProcessing {
		runtime.Gosched()
	}

	// The dispatch state is forced to Idle here, while no worker holds the
	// actor and no sender can reach it: init marks it running, and a message
	// that starts a turn from then on owns the state. Forcing Idle any later
	// would hand the actor to a second worker while that turn still runs.
	pid.schedState.reset()
	pid.resetBehavior()
	if err := pid.init(ctx); err != nil {
		return err
	}

	// the uptime starts again with the new incarnation
	pid.startedAt.Store(time.Now().Unix())

	// re-add the actor back to the actor tree and cluster
	if err := chain.New(chain.WithFailFast()).
		AddRunner(func() error { return tree.addOrAttachNode(parent, pid) }).
		AddRunner(func() error { tree.addWatcher(pid, deathWatch); return nil }).
		AddRunner(func() error { return actorSystem.putActorOnCluster(ctx, pid) }).
		Run(); err != nil {
		// disable messages processing so a failed restart does not leave a
		// re-inited but unpublished actor running
		pid.setState(stoppingState, true)
		pid.setState(runningState, false)
		return err
	}

	eg, gctx := errgroup.WithContext(ctx)
	for _, child := range node.children {
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

	pid.setState(suspendedState, false)
	pid.startPassivation()

	pid.restartCount.Inc()
	pid.firePostStart()
	if pid.getEventsStream() != nil {
		pid.getEventsStream().Publish(eventsTopic, NewActorRestarted(pid.Path()))
	}

	// the teardown of a restart keeps the actor in the tree and in the live
	// actors count (see PID.releaseName), so only an actor that was not in the
	// tree is counted again
	if actorSystem != nil && !pid.isStateSet(systemState) && !wasInTree {
		actorSystem.increaseActorsCounter()
	}

	pid.getLogger().Debugf("actor=%s restarted", pid.Name())
	return nil
}

// isLongLivedStrategy checks whether the given strategy is a long-lived strategy
// This is used to determine if the actor should be treated as a long-lived actor
// and not passivated automatically.
func isLongLivedPassivationStrategy(strategy passivation.Strategy) bool {
	_, ok := strategy.(*passivation.LongLivedStrategy)
	return ok
}

// isControlMessage identifies messages that bypass the user-message
// backlog and route to the actor's system queue. Shutdown,
// supervision, lifecycle and observability signals must not queue
// behind a user-message burst.
//
// Narrower than isSystemMessage by design: asyncRequest and
// asyncResponse participate in the reentrancy stash protocol and must
// keep FIFO ordering with user messages, so they are not control plane.
func isControlMessage(message any) bool {
	switch message.(type) {
	case *PoisonPill,
		*commands.Panicking,
		*PausePassivation,
		*ResumePassivation,
		*PanicSignal,
		*Terminated,
		*commands.SendDeadletter:
		return true
	}
	return false
}

func isSystemMessage(message any) bool {
	switch message.(type) {
	case *commands.AsyncResponse,
		*commands.AsyncRequest,
		*PoisonPill,
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
	case gerrors.ErrUnhanledMessage.Error():
		return gerrors.ErrUnhanledMessage
	case gerrors.ErrDead.Error():
		return gerrors.ErrDead
	case gerrors.ErrSystemShuttingDown.Error():
		return gerrors.ErrSystemShuttingDown
	default:
		// Unhandled replies carry the offending message type behind the
		// sentinel; restore the identity so errors.Is keeps working across the
		// envelope path exactly as it does on the channel path.
		if detail, ok := strings.CutPrefix(err, gerrors.ErrUnhanledMessage.Error()+"\n"); ok {
			return gerrors.NewErrUnhandledMessage(errors.New(detail))
		}
		return errors.New(err)
	}
}
