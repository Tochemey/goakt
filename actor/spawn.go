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
	"math/rand/v2"
	"net"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/flowchartsman/retry"
	"github.com/google/uuid"

	"github.com/tochemey/goakt/v3/datacenter"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/pointer"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/internal/strconvx"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/supervisor"
)

// Spawn creates and starts a new actor in the local actor system.
//
// The actor will be registered under the given `name`, allowing other actors
// or components to send messages to it using the returned *PID. If an actor
// with the same name already exists in the local system, an error will be returned.
//
// Parameters:
//   - ctx: A context used to control cancellation and timeouts during the spawn process.
//   - name: A unique identifier for the actor within the local actor system.
//   - actor: An instance implementing the Actor interface, representing the behavior and lifecycle of the actor.
//   - opts: Optional SpawnOptions to customize the actor's behavior (e.g., dependency, mailbox, supervisor strategy).
//
// Returns:
//   - *PID: A pointer to the Process ID of the spawned actor, used for message passing.
//   - error: An error if the actor could not be spawned (e.g., name conflict, invalid configuration).
//
// Example:
//
//	pid, err := system.Spawn(ctx, "user-service", NewUserActor())
//	if err != nil {
//	    log.Fatalf("Failed to spawn actor: %v", err)
//	}
//
// Note: Actors spawned using this method are confined to the local actor system.
// For distributed scenarios, use a SpawnOn method.
func (x *actorSystem) Spawn(ctx context.Context, name string, actor Actor, opts ...SpawnOption) (*PID, error) {
	if !x.Running() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	// check some preconditions
	if err := x.checkSpawnPreconditions(ctx, name, actor, false, nil); err != nil {
		return nil, err
	}

	pidNode, exist := x.actors.nodeByName(name)
	if exist {
		pid := pidNode.value()
		if pid.IsRunning() {
			return pid, nil
		}
	}

	pid, err := x.configPID(ctx, name, actor, opts...)
	if err != nil {
		return nil, err
	}

	if !pid.isStateSet(systemState) {
		x.increaseActorsCounter()
	}

	// add the given actor to the tree and supervise it
	guardian := x.getUserGuardian()
	_ = x.actors.addNode(guardian, pid)
	x.actors.addWatcher(pid, x.deathWatch)
	return pid, x.putActorOnCluster(pid)
}

// SpawnNamedFromFunc creates an actor with the given receive function and provided name. One can set the PreStart and PostStop lifecycle hooks
// in the given optional options
func (x *actorSystem) SpawnNamedFromFunc(ctx context.Context, name string, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error) {
	if !x.Running() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	config := newFuncConfig(opts...)
	actor := newFuncActor(name, receiveFunc, config)

	// check some preconditions
	if err := x.checkSpawnPreconditions(ctx, name, actor, false, nil); err != nil {
		return nil, err
	}

	pidNode, exist := x.actors.nodeByName(name)
	if exist {
		pid := pidNode.value()
		if pid.IsRunning() {
			return pid, nil
		}
	}

	pid, err := x.configPID(ctx, name, actor, WithMailbox(config.mailbox), WithRelocationDisabled())
	if err != nil {
		return nil, err
	}

	if !pid.isStateSet(systemState) {
		x.increaseActorsCounter()
	}

	_ = x.actors.addNode(x.userGuardian, pid)
	x.actors.addWatcher(pid, x.deathWatch)
	return pid, x.putActorOnCluster(pid)
}

// SpawnOn creates and starts an actor locally, on another node in the current cluster,
// or on a node in a different data center, depending on options and actor system configuration.
//
// # Cross–data center placement
//
// When opts include WithDataCenter, the actor is spawned on a node in that data center.
// Placement is a random node among the target DC's advertised remoting endpoints: SpawnOn
// calls spawnOnDatacenter, which requires DataCenterReady(), looks up the target DC by the
// given datacenter.DataCenter's ID() in the controller's active records, then sends a
// RemoteSpawn to one of that record's Endpoints chosen at random. There is no leader
// selection—which node runs the actor depends entirely on which addresses the target DC
// registered (via datacenter.Config.Endpoints). If that DC advertises only its leader, every
// cross-DC spawn goes to the leader; if it advertises all nodes, the actor is placed on a
// random node in that DC. The actor kind must be registered on the target data center's
// actor systems. See WithDataCenter and spawnOnDatacenter for details and errors
// (e.g. ErrDataCenterNotReady, ErrDataCenterStaleRecords, ErrDataCenterRecordNotFound).
//
// # Same–data center placement
//
// When no target data center is specified, behavior depends on cluster mode:
//
//   - In cluster mode, the actor may be placed on any node in the local cluster according to
//     the placement strategy and role filter. Supported strategies:
//     RoundRobin, Random, Local, LeastLoad. Placement uses cluster.Members and thus stays
//     within the current data center when the architecture is one-cluster-per-DC.
//   - In non-cluster mode, the actor is created on the local actor system, like Spawn.
//
// Unlike Spawn, SpawnOn does not return a PID. Use ActorOf to resolve the actor's PID or
// Address after it has been successfully created.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts during the spawn process.
//   - name: A globally unique name for the actor in the cluster or across data centers.
//   - actor: An instance implementing the Actor interface.
//   - opts: Optional SpawnOptions: placement strategy, WithDataCenter for cross-DC spawn,
//     role, dependencies, mailbox, supervisor strategy, etc.
//
// Returns:
//   - error: An error if the actor could not be spawned (e.g., name conflict, network or
//     remoting failure, misconfiguration; or, when using WithDataCenter, data center
//     not ready, stale records, or target DC not found).
//
// Example (same-DC, cluster placement):
//
//	err := system.SpawnOn(ctx, "actor-1", NewCartActor(), WithPlacement(Random))
//	if err != nil {
//	    log.Fatalf("Failed to spawn actor: %v", err)
//	}
//
// Example (cross-DC):
//
//	dc := &datacenter.DataCenter{Name: "dc-west", Region: "us-west-2"}
//	err := system.SpawnOn(ctx, "actor-1", NewCartActor(), WithDataCenter(dc))
//	if err != nil {
//	    log.Fatalf("Failed to spawn actor in dc-west: %v", err)
//	}
//
// ⚠️ Note: The created actor uses the default mailbox from the actor system unless overridden in opts.
func (x *actorSystem) SpawnOn(ctx context.Context, name string, actor Actor, opts ...SpawnOption) error {
	if !x.Running() {
		return gerrors.ErrActorSystemNotStarted
	}

	config := newSpawnConfig(opts...)
	// here we are sending the message to a datacenter
	if config.dataCenter != nil {
		return x.spawnOnDatacenter(ctx, name, actor, config)
	}

	// check some preconditions
	if err := x.checkSpawnPreconditions(ctx, name, actor, false, nil); err != nil {
		return err
	}

	if !x.InCluster() {
		_, err := x.Spawn(ctx, name, actor, opts...)
		return err
	}

	peers, err := x.cluster.Members(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch cluster nodes: %w", err)
	}

	if len(peers) == 0 {
		_, err := x.Spawn(ctx, name, actor, opts...)
		return err
	}

	peers, err = x.filterPeersByRole(peers, config.role)
	if err != nil {
		return err
	}

	peer, err := x.selectPlacementPeer(ctx, peers, config.placement)
	if err != nil {
		return err
	}

	// spawn the actor on the local node
	if peer == nil {
		_, err := x.Spawn(ctx, name, actor, opts...)
		return err
	}

	request := &remote.SpawnRequest{
		Name:                name,
		Kind:                registry.Name(actor),
		Singleton:           nil,
		Relocatable:         config.relocatable,
		PassivationStrategy: config.passivationStrategy,
		Dependencies:        config.dependencies,
		EnableStashing:      config.enableStash,
		Reentrancy:          config.reentrancy,
	}

	if config.supervisor != nil {
		request.Supervisor = config.supervisor
	}

	return x.remoting.RemoteSpawn(ctx, peer.Host, peer.RemotingPort, request)
}

// SpawnFromFunc creates an actor with the given receive function.
func (x *actorSystem) SpawnFromFunc(ctx context.Context, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error) {
	return x.SpawnNamedFromFunc(ctx, uuid.NewString(), receiveFunc, opts...)
}

// SpawnRouter creates and initializes a new router actor with the specified options.
//
// A router is a special type of actor designed to distribute messages of the same type
// across a group of routee actors. This enables concurrent message processing and improves
// throughput by leveraging multiple actors in parallel.
//
// Each individual actor in the group (a "routee") processes only one message at a time,
// preserving the actor model’s single-threaded execution semantics.
//
// Note: Routers are **not** redeployable. If the host node of a router leaves the cluster
// or crashes, the router and its routees will not be automatically re-spawned elsewhere.
//
// Use routers when you need to fan out work across multiple workers while preserving
// the isolation and safety guarantees of the actor model.
func (x *actorSystem) SpawnRouter(ctx context.Context, name string, poolSize int, routeesKind Actor, opts ...RouterOption) (*PID, error) {
	router := newRouter(poolSize, routeesKind, x.logger, opts...)
	return x.Spawn(ctx, name, router,
		WithRelocationDisabled(),
		asSystem(),
		WithSupervisor(
			supervisor.NewSupervisor(supervisor.WithAnyErrorDirective(supervisor.ResumeDirective)),
		))
}

// SpawnSingleton creates (or ensures the existence of) a cluster-wide singleton actor.
//
// A singleton actor exists at most once in the cluster for a given actor "kind" and optional
// role. The kind is derived from the concrete actor type (see registry.Name) and, when
// WithSingletonRole is used, the uniqueness key becomes "kind::role".
//
// Preconditions:
//   - The actor system must be running (ErrActorSystemNotStarted).
//   - Clustering must be enabled (ErrClusterDisabled).
//
// Placement:
//   - Without a role: the singleton is hosted on the cluster coordinator (leader). If the caller
//     is not the leader, it delegates by issuing a RemoteSpawn to the leader.
//   - With a role: the singleton is hosted on the oldest member that advertises the role. If the
//     caller is not the chosen host, it delegates by issuing a RemoteSpawn to that member.
//
// Idempotency and collisions:
//   - If a singleton for the same kind/role already exists, SpawnSingleton returns ErrSingletonAlreadyExists.
//   - If the requested name is already taken:
//   - If the existing actor bound to that name is the same singleton (same kind/role and marked as singleton
//     in cluster metadata), the call is treated as a no-op and succeeds. This makes concurrent callers more
//     resilient under cluster state propagation delays.
//   - Otherwise, SpawnSingleton returns ErrActorAlreadyExists.
//
// Retries:
// When spawn retries are configured, transient conditions (e.g. quorum errors, leader/engine
// unavailability, temporary lack of eligible role members, or Connect Unavailable/DeadlineExceeded)
// are retried until the retry budget is exhausted or the context is done.
//
// Operational guidance:
// SpawnSingleton is safe to call from any cluster member; it will resolve the correct host and
// delegate as needed. For operational simplicity, you may still prefer invoking it from a single
// control-plane location to reduce contention and make failure modes easier to reason about.
//
// Errors:
//   - ErrActorSystemNotStarted, ErrClusterDisabled when the system is not ready.
//   - ErrSingletonAlreadyExists when a singleton for the same kind/role already exists.
//   - ErrActorAlreadyExists when the requested name is already used by another actor.
//   - ErrWriteQuorum, ErrReadQuorum, ErrClusterQuorum when a quorum-related failure occurs.
//   - Other errors are returned as-is (e.g., no eligible members for a role, remoting failures).
func (x *actorSystem) SpawnSingleton(ctx context.Context, name string, actor Actor, opts ...ClusterSingletonOption) error {
	if !x.Running() {
		return gerrors.ErrActorSystemNotStarted
	}

	if !x.InCluster() {
		return gerrors.ErrClusterDisabled
	}

	cl := x.getCluster()
	cfg := newClusterSingletonConfig(opts...)
	role := strings.TrimSpace(pointer.Deref(cfg.Role(), ""))
	retries, err := strconvx.Int2Int32(cfg.numberOfRetries)
	if err != nil {
		return err
	}

	// singletonKey is the cluster registration key: kind or kind::role.
	singletonKey := registry.Name(actor)
	if role != "" {
		singletonKey = kindRole(singletonKey, role)
	}

	return x.retrySpawnSingleton(ctx, cfg, singletonKey, name, func(ctx context.Context) error {
		if role != "" {
			return x.spawnSingletonWithRole(ctx, cl, name, actor, role, cfg.spawnTimeout, cfg.waitInterval, retries)
		}

		// Resolve the cluster coordinator and spawn locally if it's us; otherwise delegate via RemoteSpawn.
		return x.spawnSingletonOnLeader(ctx, cl, name, actor, cfg.spawnTimeout, cfg.waitInterval, retries)
	})
}

// retrySpawnSingleton runs spawnFn with retries according to cfg.
//
// SpawnSingleton always builds a non-nil cfg via newClusterSingletonConfig, so cfg is assumed non-nil.
// This helper exists to keep the SpawnSingleton happy-path readable and to centralize retry semantics.
func (x *actorSystem) retrySpawnSingleton(ctx context.Context, cfg *clusterSingletonConfig, singletonKey, actorName string, spawnFn func(context.Context) error) error {
	retryCtx := ctx
	if cfg.spawnTimeout > 0 {
		var cancel context.CancelFunc
		retryCtx, cancel = context.WithTimeout(ctx, cfg.spawnTimeout)
		defer cancel()
	}

	retrier := retry.NewRetrier(cfg.numberOfRetries, cfg.waitInterval, cfg.spawnTimeout)
	if err := retrier.RunContext(retryCtx, func(ctx context.Context) error {
		if err := spawnFn(ctx); err != nil {
			return x.spawnSingletonRetryError(ctx, err, singletonKey, actorName)
		}
		return nil
	}); err != nil {
		return cluster.NormalizeQuorumError(err)
	}
	return nil
}

// spawnSingletonRetryError decides how a singleton spawn failure should be handled by the retrier.
//
// It maps `err` to one of the following outcomes:
//
//   - Success (return nil):
//   - When the spawn failed with ErrActorAlreadyExists, but the singleton kind is already
//     registered in the cluster (idempotent behavior: the singleton exists, so spawning is effectively done).
//   - Terminal failure (return retry.Stop(err)):
//   - ErrSingletonAlreadyExists (explicit singleton collision; do not retry).
//   - Context cancellation/deadline (stop with ctx.Err()).
//   - Any non-transient error.
//   - Retry (return err):
//   - Transient cluster/remoting conditions such as quorum errors, leader/engine unavailability,
//     temporary absence of eligible role members, or Connect Unavailable/DeadlineExceeded.
//
// `singletonKey` is the cluster registration key for the singleton (kind or kind::role). It is used
// to disambiguate name collisions (ErrActorAlreadyExists) and determine whether the singleton is
// already present in the cluster.
func (x *actorSystem) spawnSingletonRetryError(ctx context.Context, err error, singletonKey, actorName string) error {
	if errors.Is(err, gerrors.ErrSingletonAlreadyExists) {
		// Terminal: singleton already exists; don't retry.
		return retry.Stop(err)
	}

	if errors.Is(err, gerrors.ErrActorAlreadyExists) {
		return x.handleSingletonNameConflict(ctx, err, singletonKey, actorName)
	}

	if isContextDone(ctx) {
		return retry.Stop(ctx.Err())
	}

	if shouldRetrySpawnSingleton(err) {
		return err
	}

	return retry.Stop(err)
}

// handleSingletonNameConflict resolves ErrActorAlreadyExists during singleton spawns.
//
// The error can be caused by:
//   - A true name collision (the name is already used by a different actor), or
//   - A propagation race where the singleton was created elsewhere and cluster metadata is not yet visible.
//
// This handler disambiguates those cases by:
//  1. Checking cluster actor metadata by name (strong signal; avoids false success).
//  2. Falling back to checking whether the singleton kind/role key is registered (eventual-consistency tolerant).
//
// It returns:
//   - nil: treat as success (idempotent)
//   - retry.Stop(err): terminal failure
//   - err: retryable (within the configured retry budget)
func (x *actorSystem) handleSingletonNameConflict(ctx context.Context, err error, singletonKey, actorName string) error {
	if singletonKey == "" {
		return retry.Stop(err)
	}

	// Defensive: avoid doing lookups when the caller context is already done.
	if isContextDone(ctx) {
		return retry.Stop(ctx.Err())
	}

	// First, try to disambiguate by name: if the actor name exists, confirm it's the singleton we
	// intended (same kind/role + singleton flag). This turns eventual-consistency races into a
	// deterministic outcome and avoids treating unrelated name collisions as success.
	if strings.TrimSpace(actorName) != "" {
		existing, gerr := x.cluster.GetActor(ctx, actorName)
		if gerr == nil && existing != nil {
			expectedKind, expectedRole := splitSingletonKind(singletonKey)
			actualRole := strings.TrimSpace(pointer.Deref(existing.Role, ""))
			if existing.GetSingleton() != nil && existing.GetType() == expectedKind && actualRole == expectedRole {
				// The name is already bound to the singleton we wanted: treat as success/idempotent.
				return nil
			}
			// Name is taken by another actor (or a different singleton): stop.
			return retry.Stop(err)
		}

		if gerr != nil && !errors.Is(gerr, cluster.ErrActorNotFound) {
			if shouldRetrySpawnSingleton(gerr) {
				return gerr
			}
			return retry.Stop(gerr)
		}
		// If actor is not found, fall through to kind-based lookup. This can happen under propagation delay.
	}

	registered, lerr := x.isSingletonKeyRegistered(ctx, singletonKey)
	if lerr != nil {
		if shouldRetrySpawnSingleton(lerr) {
			return lerr
		}
		return retry.Stop(lerr)
	}

	if registered {
		return nil
	}

	// Ambiguous: name exists (we got ErrActorAlreadyExists) but the kind is not visible yet.
	// Treat as retryable to allow cluster propagation to settle within the configured retry budget.
	return err
}

func isContextDone(ctx context.Context) bool {
	return ctx.Err() != nil
}

func shouldRetrySpawnSingleton(err error) bool {
	if cluster.IsQuorumError(err) {
		return true
	}

	if errors.Is(err, gerrors.ErrLeaderNotFound) || errors.Is(err, cluster.ErrEngineNotRunning) {
		return true
	}

	if isNoRoleMembersError(err) {
		return true
	}

	// Retry on non-Connect timeouts as well (can happen depending on call stack).
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Retry on network timeouts/temporary errors.
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}

	code := connect.CodeOf(err)
	return code == connect.CodeUnavailable || code == connect.CodeDeadlineExceeded
}

// isSingletonKeyRegistered reports whether singletonKey (kind or kind::role) is present in cluster state.
func (x *actorSystem) isSingletonKeyRegistered(ctx context.Context, singletonKey string) (bool, error) {
	id, err := x.cluster.LookupKind(ctx, singletonKey)
	if err != nil {
		return false, err
	}
	return id == singletonKey, nil
}

type errNoRoleMembers struct {
	role string
}

func (e errNoRoleMembers) Error() string {
	return fmt.Sprintf("no cluster members found with role %s", e.role)
}

func isNoRoleMembersError(err error) bool {
	var noRole errNoRoleMembers
	return errors.As(err, &noRole)
}

func (x *actorSystem) spawnSingletonWithRole(ctx context.Context, cl cluster.Cluster, name string, actor Actor, role string, spawnTimeout, waitInterval time.Duration, retries int32) error {
	// fetch all cluster members
	members, err := cl.Members(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch cluster members: %w", err)
	}

	filtered := make([]*cluster.Peer, 0, len(members))
	for _, peer := range members {
		if peer.HasRole(role) {
			filtered = append(filtered, peer)
		}
	}

	if len(filtered) == 0 {
		return errNoRoleMembers{role: role}
	}

	// find the oldest node in the filtered peers
	sort.Slice(filtered, func(i int, j int) bool {
		return filtered[i].CreatedAt < filtered[j].CreatedAt
	})

	leader := filtered[0]
	if leader.PeerAddress() != x.clusterNode.PeersAddress() {
		var (
			actorType = registry.Name(actor)
			host      = leader.Host
			port      = leader.RemotingPort
		)

		return x.remoting.RemoteSpawn(ctx, host, port, &remote.SpawnRequest{
			Name: name,
			Kind: actorType,
			Singleton: &remote.SingletonSpec{
				SpawnTimeout: spawnTimeout,
				WaitInterval: waitInterval,
				MaxRetries:   retries,
			},
			Role: pointer.To(role),
		})
	}

	return x.spawnSingletonOnLocal(ctx, name, actor, pointer.To(role), spawnTimeout, waitInterval, retries)
}

func (x *actorSystem) spawnSingletonOnLocal(ctx context.Context, name string, actor Actor, role *string, spawnTimeout, waitInterval time.Duration, retries int32) (err error) {
	// Normalize role once
	singletonRole := strings.TrimSpace(pointer.Deref(role, ""))

	// Compute singleton kind once (used for cleanup)
	singletonKind := registry.Name(actor)
	if singletonRole != "" {
		singletonKind = kindRole(singletonKind, singletonRole)
	}

	// check some preconditions
	if err := x.checkSpawnPreconditions(ctx, name, actor, true, pointer.To(singletonRole)); err != nil {
		return err
	}

	// If we fail after preconditions, cleanup kind registration best-effort.
	// Note: this now works reliably because we use a named return `err`.
	spawnSucceeded := false
	defer func() {
		if spawnSucceeded || err == nil {
			return
		}
		_ = x.cluster.RemoveKind(ctx, singletonKind)
	}()

	pid, err := x.configPID(ctx, name, actor,
		WithLongLived(),
		withSingleton(&singletonSpec{
			SpawnTimeout: spawnTimeout,
			WaitInterval: waitInterval,
			MaxRetries:   retries,
		}),
		WithRole(singletonRole),
		WithSupervisor(
			supervisor.NewSupervisor(
				supervisor.WithStrategy(supervisor.OneForOneStrategy),
				supervisor.WithDirective(&gerrors.PanicError{}, supervisor.StopDirective),
				supervisor.WithDirective(&gerrors.InternalError{}, supervisor.StopDirective),
				supervisor.WithDirective(&runtime.PanicNilError{}, supervisor.StopDirective),
			),
		),
	)
	if err != nil {
		return err
	}

	if !pid.isStateSet(systemState) {
		x.increaseActorsCounter()
	}

	// add the given actor to the tree and supervise it
	_ = x.actors.addNode(x.singletonManager, pid)
	x.actors.addWatcher(pid, x.deathWatch)

	if err := x.putActorOnCluster(pid); err != nil {
		return err
	}

	spawnSucceeded = true
	return nil
}

// spawnOnDatacenter spawns an actor on a remote data center by sending a RemoteSpawn
// request to one of that data center's advertised endpoints, chosen at random.
//
// It is used when SpawnOn is called with WithDataCenter; the target data center is
// identified by config.dataCenter (see datacenter.DataCenter and its ID method).
// The multi-DC control plane must be configured and the local data center controller
// must have a non-stale view of active data centers.
//
// Placement: The target is one of the target DC's DataCenterRecord.Endpoints, selected
// uniformly at random. There is no leader selection. Which node actually runs the actor
// is determined by what that DC registered: DataCenterRecord.Endpoints come from the
// target DC's datacenter.Config.Endpoints at registration time (typically by that DC's
// leader). If the target DC registered a single endpoint (e.g. its leader only), every
// spawn goes to that node; if it registered all nodes' remoting addresses, the actor
// is placed on a random node in that DC.
//
// Behavior:
//  1. Ensures the data center controller is ready (DataCenterReady); otherwise returns ErrDataCenterNotReady.
//  2. Loads active data center records from the controller; if the cache is stale, returns ErrDataCenterStaleRecords.
//  3. Looks up the target data center by config.dataCenter.ID() among records in DataCenterActive state.
//  4. Selects one remoting endpoint at random from that record's Endpoints (each element is "host:port").
//  5. Invokes RemoteSpawn to create the actor on the node listening at that endpoint.
//
// The actor kind must be registered on the target data center's actor systems; otherwise
// the remote node will return ErrTypeNotRegistered. The target data center runs its own
// cluster; this path performs cross-DC placement by design.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts during the spawn process.
//   - name: Globally unique name for the actor (used both for placement and on the remote node).
//   - actor: Instance implementing the Actor interface; its registered kind is sent to the remote node.
//   - config: Spawn configuration; config.dataCenter must be set and used to resolve the target DC.
//
// Returns:
//   - error: ErrDataCenterNotReady, ErrDataCenterStaleRecords, or ErrDataCenterRecordNotFound when
//     the controller is unavailable, cache is stale, or the target DC is not active in the cache;
//     or an error from RemoteSpawn (e.g. ErrTypeNotRegistered, transport failures).
func (x *actorSystem) spawnOnDatacenter(ctx context.Context, name string, actor Actor, config *spawnConfig) error {
	if !x.DataCenterReady() {
		return gerrors.ErrDataCenterNotReady
	}

	// get the datacenter controller
	controller := x.getDataCenterController()
	// grab the active datacenter using the controller
	activeRecords, stale := controller.ActiveRecords()
	if stale {
		return gerrors.ErrDataCenterStaleRecords
	}

	// locate the target datacenter
	var dcRecord *datacenter.DataCenterRecord
	for _, record := range activeRecords {
		if record.State == datacenter.DataCenterActive && record.ID == config.dataCenter.ID() {
			dcRecord = pointer.To(record)
			break
		}
	}

	if dcRecord == nil {
		return gerrors.ErrDataCenterRecordNotFound
	}

	// let us pick a random endpoint from the datacenter record endpoints
	// every datacenter record endpoints must be a valid TCP address
	endpoint := dcRecord.Endpoints[rand.IntN(len(dcRecord.Endpoints))] //nolint:gosec
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("failed to split host and port from endpoint: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("failed to convert port to int: %w", err)
	}

	return x.remoting.RemoteSpawn(ctx, host, port, &remote.SpawnRequest{
		Name:                name,
		Kind:                registry.Name(actor),
		Relocatable:         config.relocatable,
		PassivationStrategy: config.passivationStrategy,
		Dependencies:        config.dependencies,
		EnableStashing:      config.enableStash,
		Reentrancy:          config.reentrancy,
	})
}

func (x *actorSystem) selectPlacementPeer(ctx context.Context, peers []*cluster.Peer, placement SpawnPlacement) (*cluster.Peer, error) {
	switch placement {
	case Random:
		return peers[rand.IntN(len(peers))], nil //nolint:gosec
	case RoundRobin:
		return x.actorsRoundRobinPlacementPeer(ctx, peers)
	case LeastLoad:
		return x.leastLoadedPeer(ctx, peers)
	default:
		return nil, nil
	}
}

func kindRole(kind, role string) string {
	return fmt.Sprintf("%s%s%s", kind, kindRoleSeparator, role)
}

// splitSingletonKind parses the singleton cluster registration key into (kind, role).
// The key is either:
//   - "kind" (no role)
//   - "kind::role" (with role)
func splitSingletonKind(singletonKind string) (kind string, role string) {
	singletonKind = strings.TrimSpace(singletonKind)
	if singletonKind == "" {
		return "", ""
	}
	parts := strings.SplitN(singletonKind, kindRoleSeparator, 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

func (x *actorSystem) actorsRoundRobinPlacementPeer(ctx context.Context, peers []*cluster.Peer) (*cluster.Peer, error) {
	next, err := x.cluster.NextRoundRobinValue(ctx, cluster.ActorsRoundRobinKey)
	if err != nil {
		return nil, err
	}
	return peers[(next-1)%len(peers)], nil
}
