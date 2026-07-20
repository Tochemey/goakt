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
	stderrors "errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/flowchartsman/retry"
	"golang.org/x/sync/errgroup"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/chunk"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/log"
)

const (
	// defaultRelocationBatchSize caps the number of actors and grains carried by a
	// single RelocateBatch request so a batch stays far below the transport's max
	// frame size (an oversized frame closes the connection instead of returning an
	// error).
	defaultRelocationBatchSize = 500
	// relocationBatchMaxAttempts is the total number of tries for a single
	// RelocateBatch request before its target peer is considered unreachable.
	relocationBatchMaxAttempts = 2
	// relocationRetryMinBackoff is the initial backoff between RelocateBatch attempts.
	relocationRetryMinBackoff = 100 * time.Millisecond
	// relocationRetryMaxBackoff is the backoff ceiling between RelocateBatch attempts.
	relocationRetryMaxBackoff = time.Second
	// relocationBatchSendTimeout bounds a single RelocateBatch RPC attempt. The
	// relocation context is detached with context.WithoutCancel and carries no
	// deadline of its own, and the transport only imposes a connection deadline
	// when the context has one. Without this a target that black-holes after the
	// TCP connect (plausible during the same network event that caused the
	// departure) would block the batch send - and thus the whole rebalance -
	// indefinitely: the errgroup never completes, no failure event or metric is
	// emitted, the peer-state snapshot is never deleted, and the relocation job
	// stays registered so future departures of that address are skipped.
	relocationBatchSendTimeout = 30 * time.Second
	// relocationLoadScanTimeout bounds the registry scan that seeds load-aware
	// placement when no cluster read timeout is configured (see
	// clusterReadTimeout). The scan is best-effort: on timeout or error it is
	// skipped and placement falls back to an even split of the departed node's
	// actors.
	relocationLoadScanTimeout = 2 * time.Second
	// relocationSpawnMaxAttempts and relocationSpawnRetryBackoff bound the
	// respawn retry inside recreateActorFromWire. The registry delete of the
	// departed node's record and the duplicate-name check inside Spawn
	// (cluster.ActorExists) are not atomic across replicas: with a replica
	// count above 1 and a read quorum of 1, the check can hit a backup replica
	// that has not yet applied the delete and spuriously report the crashed
	// actor as already existing. The retry gives the delete time to propagate;
	// a name genuinely owned by a live node is never retried.
	relocationSpawnMaxAttempts  = 5
	relocationSpawnRetryBackoff = 100 * time.Millisecond
	// relocationQuiescenceWindow, relocationQuiescencePoll and
	// relocationQuiescenceMaxWait govern the gate on crash recovery (see
	// gateCrashRecovery): the registry is only scanned once no olric rebalance
	// event has been observed for the window, polling at the given cadence,
	// and never waiting longer than the max before proceeding anyway.
	relocationQuiescenceWindow  = 3 * time.Second
	relocationQuiescencePoll    = 200 * time.Millisecond
	relocationQuiescenceMaxWait = 30 * time.Second
	// relocationDeriveScanTimeout floors the registry scans that reconstruct a
	// crashed node's relocation set (deriveRelocationSetFromRegistry). Crash
	// recovery is a correctness path that must cover the whole registry: the
	// interactive cluster read timeout (default one second, tuned for
	// single-record lookups) cannot scan a registry of thousands of records,
	// and a timed-out scan abandons the entire rebalance, losing every actor
	// of the crashed node.
	relocationDeriveScanTimeout = time.Minute
	// abortedRelocationCleanupTimeout bounds the lazy-grain directory cleanup an
	// aborted relocation performs (see reportAbortedRelocation). The abort runs
	// precisely when the cluster is already unhealthy, so the cleanup must not
	// hold the relocator's mailbox on unbounded round trips; a grain whose
	// release cannot complete within the budget is reported as failed instead.
	abortedRelocationCleanupTimeout = 30 * time.Second
	// relocationItemMaxAttempts and relocationItemRetryBackoff bound the
	// per-item retry in enqueueRelocation (see retryRelocationItem): relocation
	// runs while the cluster is digesting a node loss, so registry operations
	// transiently time out and a single-attempt failure would permanently lose
	// the item.
	relocationItemMaxAttempts  = 3
	relocationItemRetryBackoff = 500 * time.Millisecond
	// relocationDeriveMaxAttempts and relocationDeriveRetryBackoff bound the
	// quiesce-then-derive retry in gateCrashRecovery: the derivation's registry
	// scan can transiently fail on an unreachable member (a replacement node
	// joining or flapping), and a single-attempt failure would permanently skip
	// the crashed node's rebalance.
	relocationDeriveMaxAttempts  = 4
	relocationDeriveRetryBackoff = 5 * time.Second
)

// relocationWorker relocates the actors and grains of a single departed node.
// It is spawned by the relocator for each rebalance, performs the relocation,
// publishes the outcome, releases the relocation job and stops itself.
//
// Failures are isolated per item: a failing actor, grain, or peer never aborts
// the rest of the rebalance. A peer-level failure moves the unsent remainder of
// that peer's share to the next surviving peer once before the remaining items
// are reported as failed. The RelocationFailed event lists exactly the items
// that could not be relocated.
type relocationWorker struct {
	remoting remoteclient.Client
	pid      *PID
	logger   log.Logger
}

// enforce compilation error
var _ Actor = (*relocationWorker)(nil)

// newRelocationWorker creates a relocation worker. The remoting client is
// owned by the actor system and shared with the relocator; it is never closed
// here.
func newRelocationWorker(remoting remoteclient.Client) *relocationWorker {
	return &relocationWorker{
		remoting: remoting,
	}
}

// PreStart pre-starts the actor.
func (w *relocationWorker) PreStart(*Context) error {
	return nil
}

// Receive handles messages sent to the relocation worker
func (w *relocationWorker) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
		w.pid = ctx.Self()
		w.logger = ctx.Logger()
	case *internalpb.Rebalance:
		w.relocate(ctx, msg.GetPeerState())
		ctx.Shutdown()
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (w *relocationWorker) PostStop(*Context) error {
	return nil
}

// relocate rebalances the departed node described by peerState across the
// surviving nodes, then performs all completion bookkeeping (outcome event,
// peer state removal, relocation job release) before returning. Doing the
// bookkeeping here, before the worker stops, guarantees the relocator's
// Terminated handler only ever finds a registered job when the worker died
// abnormally.
func (w *relocationWorker) relocate(ctx *ReceiveContext, peerState *internalpb.PeerState) {
	system := w.pid.ActorSystem()
	// peersAddress is the relocation bookkeeping key. It must stay byte-identical
	// to the key used by handleNodeLeftEvent (the NodeLeft peers address) and by
	// relocator.startWorker, so it keeps the exact net.JoinHostPort form both use.
	peersAddress := net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetPeersPort())))
	// departedNode is the remoting endpoint compared against endpoints sliced out
	// of an actor/grain address (address.HostPort() / FormatHostPort), which never
	// bracket IPv6 hosts. Build it with the same canonical form; net.JoinHostPort
	// would bracket IPv6 hosts and silently break every comparison on an IPv6 cluster.
	departedNode := address.FormatHostPort(peerState.GetHost(), int(peerState.GetRemotingPort()))
	rctx := context.WithoutCancel(ctx.Context())
	start := time.Now()

	if system.isStopping() {
		system.endRelocation(peersAddress)
		return
	}

	peers, err := system.getCluster().Peers(rctx)
	if err != nil {
		w.logger.Errorf("cluster rebalancing failed for node=%s: %v (hint: check cluster quorum, peer connectivity)", peersAddress, err)
		// Nothing could be relocated. Apply the shared abort accounting so this
		// path matches the relocator's abort and the normal per-item path: every
		// relocatable actor and eager grain is a failure, lazy grain directory
		// entries are released locally (only a failed release is a failure), and
		// disabled grains are excluded. This also records the loss in
		// relocation.failed.count so alerting sees it instead of a silent gap.
		system.reportAbortedRelocation(rctx, w.pid, peersAddress, peerState, time.Since(start), errors.NewInternalError(err))
		w.finish(rctx, peersAddress)
		return
	}

	failures := &relocationFailures{}

	// Grains relocate lazily by default: eager grains are reactivated upfront,
	// lazy grains only have their directory entry cleaned so the next
	// TellGrain/AskGrain re-activates them on a live node. Both kinds are
	// allocated across the leader and the peers, each target dispatching on the
	// grain's own eager_relocation flag, so the cleanup fan-out scales out
	// instead of being issued entirely by this node.
	grains := relocatableGrains(peerState.GetGrains())

	// The load scan only seeds actor placement, so a departed node with no
	// actors to place skips the registry scan entirely.
	var loads []int
	if len(peerState.GetActors()) > 0 {
		loads = w.targetLoads(rctx, system, peers)
	}

	leaderActors, peerActors, unplaceableActors := allocateActors(system.getNodeRoles(), peers, peerState, loads)
	leaderGrains, peerGrains := allocateGrains(len(peers)+1, grains)

	// Role-constrained actors with no surviving eligible node cannot be
	// relocated; record them so subscribers observe the loss via RelocationFailed
	// instead of the actors silently disappearing.
	for _, wireActor := range unplaceableActors {
		rerr := errors.NewRebalancingError(fmt.Errorf("no surviving node advertises role %q required by actor %s", wireActor.GetRole(), wireActor.GetAddress()))
		w.logger.Errorf("cannot relocate actor=%s: %v (hint: add a node advertising the required role, or remove the role constraint)", wireActor.GetAddress(), rerr)
		failures.record(wireActor.GetAddress(), false, rerr)
	}

	eg := new(errgroup.Group)
	eg.SetLimit(defaultRelocationConcurrency)

	// recreate the leader's share locally through the shared dispatch so the
	// leader-side and peer-side (RelocateBatch handler) paths cannot drift in
	// how items are recreated, which grains are released versus reactivated, and
	// what counts as a failure.
	enqueueRelocation(rctx, eg, system, w.logger, departedNode, leaderActors, leaderGrains, failures.record)

	// hand each peer its share with batched round trips, one goroutine per peer
	shares := max(len(peerActors), len(peerGrains))

	for i := 1; i < shares; i++ {
		var shareActors []*internalpb.Actor
		var shareGrains []*internalpb.Grain

		if i < len(peerActors) {
			shareActors = peerActors[i]
		}

		if i < len(peerGrains) {
			shareGrains = peerGrains[i]
		}

		peer := peers[i-1]
		requests := buildRelocateBatchRequests(departedNode, shareActors, shareGrains)

		eg.Go(func() error {
			w.relocateShare(rctx, requests, peer, peers, failures)
			return nil
		})
	}

	_ = eg.Wait()

	failed := failures.items()

	// relocated = every relocatable item minus the ones that failed. Lazy
	// grains that self-heal are counted as relocated (they were handled and are
	// not reported as failures), matching the failure-reporting semantics.
	relocatedCount := max((len(peerState.GetActors())+len(grains))-len(failed), 0)
	system.recordRelocationMetrics(rctx, peersAddress, time.Since(start), relocatedCount, len(failed))

	if len(failed) > 0 {
		failedActors, failedGrains := splitFailures(failed)
		err := errors.NewRebalancingError(fmt.Errorf("relocation of node=%s completed with %d failed actors and %d failed grains", peersAddress, len(failedActors), len(failedGrains)))
		w.logger.Errorf("%v (hint: subscribe to RelocationFailed events for the affected actors and grains)", err)

		if w.pid.eventsStream != nil {
			w.pid.eventsStream.Publish(eventsTopic, NewRelocationFailed(peersAddress, time.Now().UTC(), failedActors, failedGrains, err))
		}
	}

	w.finish(rctx, peersAddress)
}

// targetLoads returns the current actor occupancy of each relocation target,
// aligned to allocateActors' indexing (index 0 = leader, index i = peers[i-1]).
//
// It is a best-effort placement signal: the leader asks the cluster for a
// per-node actor tally so the departed node's actors land on the least-loaded
// surviving nodes rather than being split evenly among targets that started out
// unevenly loaded. CountActorsByHost streams the registry and returns only the
// small per-node map, so the leader never materializes the full actor set to
// seed placement. The departed node's own entries are keyed to its (now dead)
// address, so they match no target and never inflate a survivor's load. A scan
// failure returns nil and allocateActors falls back to balancing only the
// departed node's actors.
func (w *relocationWorker) targetLoads(ctx context.Context, system ActorSystem, peers []*cluster.Peer) []int {
	// CountActorsByHost applies the timeout to ctx internally, so the scan is
	// already bounded; no extra context wrapper is needed here. The budget
	// honors the user-configured cluster read timeout, like the sibling scan in
	// deriveRelocationSetFromRegistry, falling back to relocationLoadScanTimeout.
	counts, err := system.getCluster().CountActorsByHost(ctx, system.clusterReadTimeout(relocationLoadScanTimeout))
	if err != nil {
		w.logger.Warnf("load-aware relocation disabled for this rebalance: registry scan failed: %v (hint: placement falls back to an even split of the departed node's actors)", err)
		return nil
	}

	loads := make([]int, len(peers)+1)
	loads[0] = counts[address.FormatHostPort(system.Host(), system.Port())]
	for i, peer := range peers {
		loads[i+1] = counts[address.FormatHostPort(peer.Host, peer.RemotingPort)]
	}

	return loads
}

// finish removes the departed node's peer state snapshot and releases the
// relocation job so a future departure of the same address can rebalance again.
func (w *relocationWorker) finish(ctx context.Context, address string) {
	system := w.pid.ActorSystem()

	if err := system.getClusterStore().DeletePeerState(ctx, address); err != nil {
		w.logger.Errorf("failed to remove peer=%s state after relocation: %v (hint: check cluster store)", address, err)
	}

	system.endRelocation(address)
}

// enqueueRelocation schedules recreation of the given actors and grains on eg,
// one bounded goroutine per item, and records a per-item failure via record for
// anything that cannot be recovered. It is the single dispatch rule shared by
// the leader's local share and the peer-side RelocateBatch handler so the two
// cannot drift:
//
//   - a singleton actor is re-established through the cluster singleton path
//     (recreateSingletonFromWire), any other actor through recreateActorFromWire;
//     a failure is recorded against the actor address;
//   - an eager grain is reactivated up front (recreateGrainFromWire); a lazy
//     grain (the default) only has its stale directory entry released so the
//     next TellGrain/AskGrain re-activates it on a survivor. A lazy grain is a
//     failure only when its release fails, because the entry then still points
//     at the dead node and the fast path does not self-heal a stale owner.
//
// A failing item records a failure instead of cancelling its siblings, so every
// goroutine returns nil. record must be safe for concurrent use.
func enqueueRelocation(ctx context.Context, eg *errgroup.Group, system ActorSystem, logger log.Logger, departedNode string, actors []*internalpb.Actor, grains []*internalpb.Grain, record func(id string, grain bool, err error)) {
	for _, wireActor := range actors {
		eg.Go(func() error {
			err := retryRelocationItem(ctx, func() error {
				if wireActor.GetSingleton() != nil {
					return recreateSingletonFromWire(ctx, system, wireActor, departedNode)
				}
				return system.recreateActorFromWire(ctx, wireActor, departedNode)
			})

			if err != nil {
				logger.Errorf("failed to relocate actor=%s: %v (hint: check actor type registered, cluster quorum)", wireActor.GetAddress(), err)
				record(wireActor.GetAddress(), false, err)
			}
			return nil
		})
	}

	for _, wireGrain := range grains {
		eg.Go(func() error {
			if !wireGrain.GetEagerRelocation() {
				err := retryRelocationItem(ctx, func() error {
					return system.releaseGrainForLazyRelocation(ctx, wireGrain, departedNode)
				})
				if err != nil {
					logger.Errorf("failed to release lazy grain=%s directory entry: %v (hint: grain may be unreachable until re-registered; check cluster quorum)", wireGrain.GetGrainId().GetValue(), err)
					record(wireGrain.GetGrainId().GetValue(), true, err)
				}
				return nil
			}

			err := retryRelocationItem(ctx, func() error {
				return system.recreateGrainFromWire(ctx, wireGrain, departedNode)
			})
			if err != nil {
				logger.Errorf("failed to relocate grain=%s: %v (hint: check grain OnActivate, grain kind registered)", wireGrain.GetGrainId().GetValue(), err)
				record(wireGrain.GetGrainId().GetValue(), true, err)
			}
			return nil
		})
	}
}

// retryRelocationItem runs recreate up to relocationItemMaxAttempts times with
// a linear backoff between attempts. Relocation runs while the cluster is still
// digesting a node loss, so registry reads and writes transiently time out;
// giving up on the first error would turn those into permanently lost items,
// because the failure report is terminal and relocation is never re-run.
// Non-transient errors (unregistered kind, invalid record) fail on every
// attempt and only cost two extra rounds on this cold path.
func retryRelocationItem(ctx context.Context, recreate func() error) error {
	var err error

	// backoff waits draw from the shared timer pool: relocation runs thousands
	// of items concurrently and most succeed on the first attempt, so the
	// common path touches no timer at all, while a mass-retry storm reuses a
	// handful of pooled timers instead of allocating one per retrying item
	for attempt := 1; ; attempt++ {
		err = recreate()
		if err == nil || attempt >= relocationItemMaxAttempts {
			return err
		}

		backoff := time.Duration(attempt) * relocationItemRetryBackoff
		waiter := timers.Get(backoff)

		select {
		case <-ctx.Done():
			timers.Put(waiter)
			return err
		case <-waiter.C:
			timers.Put(waiter)
		}
	}
}

// relocateShare delivers one peer's share, batch by batch, to its target. When
// the target becomes unreachable the unsent remainder is redistributed across
// the remaining survivors: each actor moves to a survivor that advertises its
// required role and grains (which carry no role) move to a survivor, so a
// role-constrained actor is not dropped merely because one ring-neighbor lacks
// its role. The leader (this node) counts as a surviving host too: an actor
// only it can host by role is recreated locally, and when no peer survives the
// whole remainder is taken locally instead of being reported lost. An actor
// that no surviving node (leader included) can host is recorded once with an
// accurate message; items a chosen survivor also fails to accept are recorded
// as failed (eager grains and actors) or have their lazy directory entry
// released.
func (w *relocationWorker) relocateShare(ctx context.Context, requests []*internalpb.RelocateBatchRequest, target *cluster.Peer, peers []*cluster.Peer, failures *relocationFailures) {
	remaining, err := w.sendBatches(ctx, target, requests, failures)
	if err == nil {
		return
	}

	w.logger.Warnf("peer=%s:%d unreachable during relocation: %v (hint: redistributing its share among the remaining survivors)", target.Host, target.RemotingPort, err)

	survivors := survivingPeersExcept(peers, target)

	// Without a system there is no local (leader) fallback: record the unsent
	// share as failed. Only test doubles construct the worker without a pid.
	if len(survivors) == 0 && w.pid == nil {
		recordUnsent(remaining, err, failures)
		w.releaseUndeliverableLazyGrains(ctx, remaining, failures)
		return
	}

	var leaderRoles []string
	if w.pid != nil {
		leaderRoles = w.pid.ActorSystem().getNodeRoles()
	}

	departedNode := departedNodeOf(remaining)
	actorShares, leaderActors, grains := reassignByRole(remaining, survivors, leaderRoles, failures)

	// With no peer left standing the leader is the last surviving node, so it
	// also takes the grains (eager reactivated locally, lazy released).
	var leaderGrains []*internalpb.Grain
	if len(survivors) == 0 {
		leaderGrains = grains
		grains = nil
	}

	if len(leaderActors) > 0 || len(leaderGrains) > 0 {
		system := w.pid.ActorSystem()
		eg := new(errgroup.Group)
		eg.SetLimit(defaultRelocationConcurrency)
		enqueueRelocation(ctx, eg, system, w.logger, departedNode, leaderActors, leaderGrains, failures.record)
		_ = eg.Wait()
	}

	// Grains carry no role, so they spread round-robin across the survivors
	// instead of piling onto one ring-neighbor; a share whose target is
	// unreachable is released (lazy) or recorded (eager) below.
	grainShares := make([][]*internalpb.Grain, len(survivors))
	for i, grain := range grains {
		grainShares[i%len(survivors)] = append(grainShares[i%len(survivors)], grain)
	}

	for i, survivor := range survivors {
		if len(actorShares[i]) == 0 && len(grainShares[i]) == 0 {
			continue
		}

		reqs := buildRelocateBatchRequests(departedNode, actorShares[i], grainShares[i])
		unsent, serr := w.sendBatches(ctx, survivor, reqs, failures)
		if serr != nil {
			recordUnsent(unsent, serr, failures)
			w.releaseUndeliverableLazyGrains(ctx, unsent, failures)
		}
	}
}

// survivingPeersExcept returns every peer other than target (matched on
// host:remoting-port), preserving order.
func survivingPeersExcept(peers []*cluster.Peer, target *cluster.Peer) []*cluster.Peer {
	survivors := make([]*cluster.Peer, 0, len(peers))
	for _, peer := range peers {
		if peer.Host == target.Host && peer.RemotingPort == target.RemotingPort {
			continue
		}
		survivors = append(survivors, peer)
	}
	return survivors
}

// departedNodeOf returns the departed-node marker carried by a batch of
// relocation requests (they all share the same value).
func departedNodeOf(requests []*internalpb.RelocateBatchRequest) string {
	for _, request := range requests {
		if request.GetDepartedNode() != "" {
			return request.GetDepartedNode()
		}
	}
	return ""
}

// reassignByRole distributes the unsent actors across survivors, assigning each
// actor to the least-loaded survivor advertising its required role (ties break
// on the lower index) so the redistributed share spreads out instead of piling
// onto one ring-neighbor. An actor no peer can host falls back to the leader
// (this node) when leaderRoles qualify it, so an actor whose only remaining
// eligible host is the leader is recovered locally instead of being dropped;
// only an actor that no surviving node (leader included) can host is recorded
// as failed. It returns the per-survivor actor shares (aligned to survivors),
// the leader's local share, and the flattened grains, which carry no role and
// are placed by the caller.
func reassignByRole(requests []*internalpb.RelocateBatchRequest, survivors []*cluster.Peer, leaderRoles []string, failures *relocationFailures) (actorShares [][]*internalpb.Actor, leaderActors []*internalpb.Actor, grains []*internalpb.Grain) {
	actorShares = make([][]*internalpb.Actor, len(survivors))

	for _, request := range requests {
		grains = append(grains, request.GetGrains()...)

		for _, wireActor := range request.GetActors() {
			if idx := leastLoadedEligibleSurvivor(survivors, actorShares, wireActor.GetRole()); idx >= 0 {
				actorShares[idx] = append(actorShares[idx], wireActor)
				continue
			}

			if eligibleForRole(leaderRoles, wireActor.GetRole()) {
				leaderActors = append(leaderActors, wireActor)
				continue
			}

			rerr := errors.NewRebalancingError(fmt.Errorf("no surviving node advertises role %q required by actor %s", wireActor.GetRole(), wireActor.GetAddress()))
			failures.record(wireActor.GetAddress(), false, rerr)
		}
	}

	return actorShares, leaderActors, grains
}

// leastLoadedEligibleSurvivor returns the index of the eligible survivor with
// the smallest share assigned so far (ties break on the lower index), or -1
// when none advertises the given role. A role-less actor is eligible everywhere.
func leastLoadedEligibleSurvivor(survivors []*cluster.Peer, shares [][]*internalpb.Actor, role string) int {
	best := -1

	for i, survivor := range survivors {
		if !eligibleForRole(survivor.Roles, role) {
			continue
		}

		if best < 0 || len(shares[i]) < len(shares[best]) {
			best = i
		}
	}

	return best
}

// releaseUndeliverableLazyGrains removes, leader-side, the directory entries of
// lazy grains that could not be handed off to any peer. Without this their
// entries keep pointing at the departed (dead) node, and the TellGrain/AskGrain
// fast path does not self-heal a stale owner (only the GrainIdentity activation
// path does), so the grain would be permanently unreachable. recordUnsent
// deliberately does not report lazy grains as failures, so this best-effort
// cleanup is what makes them provably self-healing; eager grains are already
// recorded as failures by recordUnsent.
//
// When the cleanup itself fails the entry stays pinned to the dead node with no
// self-heal path, so the grain is recorded as a relocation failure to preserve
// the guarantee that every relocated grain is either reachable again or listed
// in RelocationFailed.
func (w *relocationWorker) releaseUndeliverableLazyGrains(ctx context.Context, requests []*internalpb.RelocateBatchRequest, failures *relocationFailures) {
	if w.pid == nil {
		return
	}

	system := w.pid.ActorSystem()

	for _, request := range requests {
		for _, wireGrain := range request.GetGrains() {
			if wireGrain.GetEagerRelocation() {
				continue
			}

			if err := system.releaseGrainForLazyRelocation(ctx, wireGrain, request.GetDepartedNode()); err != nil {
				w.logger.Errorf("failed to release undeliverable lazy grain=%s directory entry: %v (hint: grain may be unreachable until re-registered; check cluster quorum)", wireGrain.GetGrainId().GetValue(), err)
				failures.record(wireGrain.GetGrainId().GetValue(), true, err)
			}
		}
	}
}

// sendBatches sends the given batch requests to the target in order and
// records the per-item failures reported by the target. It stops at the first
// peer-level error and returns the unsent remainder (including the failed
// batch) along with the error.
func (w *relocationWorker) sendBatches(ctx context.Context, target *cluster.Peer, requests []*internalpb.RelocateBatchRequest, failures *relocationFailures) ([]*internalpb.RelocateBatchRequest, error) {
	for i, request := range requests {
		response, err := w.sendBatch(ctx, target, request)
		if err != nil {
			return requests[i:], err
		}

		failures.merge(response.GetFailures())
	}

	return nil, nil
}

// sendBatch sends a single RelocateBatch request to the target with bounded
// retries and backoff.
func (w *relocationWorker) sendBatch(ctx context.Context, target *cluster.Peer, request *internalpb.RelocateBatchRequest) (*internalpb.RelocateBatchResponse, error) {
	var response *internalpb.RelocateBatchResponse

	retrier := retry.NewRetrier(relocationBatchMaxAttempts, relocationRetryMinBackoff, relocationRetryMaxBackoff)
	err := retrier.RunContext(ctx, func(ctx context.Context) error {
		// Bound each attempt so a black-holed target cannot wedge the send
		// forever; the detached relocation context carries no deadline of its own.
		attemptCtx, cancel := context.WithTimeout(ctx, relocationBatchSendTimeout)
		defer cancel()

		resp, err := w.remoting.RelocateBatch(attemptCtx, target.Host, target.RemotingPort, request)
		if err != nil {
			return err
		}

		response = resp
		return nil
	})

	return response, err
}

// recreateSingletonFromWire respawns a singleton actor from its serialized wire
// representation on this node (the leader) through the cluster singleton spawn
// path, restoring its singleton spawn configuration.
//
// It is gated on the departed node exactly like recreateActorFromWire: the
// singleton is only re-established when its registry entry still points at the
// departed node (or is missing). An entry pointing anywhere else means the
// singleton was already recreated on a survivor, so this is a stale re-run (a
// duplicate NodeLeft against a peer-state snapshot that a failed DeletePeerState
// left behind, or a leadership change) and the unconditional RemoveActor +
// SpawnSingleton below would tear down and double-spawn the live singleton.
// Skipping in that case keeps the relocation idempotent.
func recreateSingletonFromWire(ctx context.Context, system ActorSystem, props *internalpb.Actor, departedNode string) error {
	addr, err := address.Parse(props.GetAddress())
	if err != nil {
		return errors.NewInternalError(err)
	}

	if isSystemName(addr.Name()) {
		return nil
	}

	existing, gerr := system.getCluster().GetActor(ctx, addr.Name())
	switch {
	case gerr == nil:
		if entry, perr := address.Parse(existing.GetAddress()); perr == nil && entry.HostPort() != departedNode {
			// already recreated on a survivor; do not tear it down and respawn
			return nil
		}
	case stderrors.Is(gerr, cluster.ErrActorNotFound):
		// no registry entry; fall through and (re-)establish the singleton
	default:
		return errors.NewInternalError(gerr)
	}

	if err := system.getCluster().RemoveActor(ctx, addr.Name()); err != nil {
		return errors.NewInternalError(err)
	}

	actor, err := system.getReflection().instantiateActor(props.GetType())
	if err != nil {
		return err
	}

	singletonOpts := []ClusterSingletonOption{
		WithSingletonSpawnTimeout(props.GetSingleton().GetSpawnTimeout().AsDuration()),
		WithSingletonSpawnWaitInterval(props.GetSingleton().GetWaitInterval().AsDuration()),
		WithSingletonSpawnRetries(int(props.GetSingleton().GetMaxRetries())),
	}

	if props.GetRole() != "" {
		singletonOpts = append(singletonOpts, WithSingletonRole(props.GetRole()))
	}

	_, err = system.SpawnSingleton(ctx, addr.Name(), actor, singletonOpts...)
	return err
}

// buildRelocateBatchRequests splits one peer's share into RelocateBatch
// requests of at most defaultRelocationBatchSize items each.
func buildRelocateBatchRequests(departedNode string, actors []*internalpb.Actor, grains []*internalpb.Grain) []*internalpb.RelocateBatchRequest {
	actorBatches := chunk.Chunkify(actors, defaultRelocationBatchSize)
	grainBatches := chunk.Chunkify(grains, defaultRelocationBatchSize)

	total := len(actorBatches) + len(grainBatches)
	requests := make([]*internalpb.RelocateBatchRequest, 0, total)

	for _, batch := range actorBatches {
		requests = append(requests, &internalpb.RelocateBatchRequest{
			DepartedNode: departedNode,
			Actors:       batch,
		})
	}

	for _, batch := range grainBatches {
		requests = append(requests, &internalpb.RelocateBatchRequest{
			DepartedNode: departedNode,
			Grains:       batch,
		})
	}

	return requests
}

// recordUnsent marks every item of the given unsent batch requests as failed.
// Unsent lazy grains are deliberately not recorded: their release is a cleanup
// optimization, not an item move, and the directory entry self-heals on the
// grain's next activation.
func recordUnsent(requests []*internalpb.RelocateBatchRequest, err error, failures *relocationFailures) {
	for _, request := range requests {
		for _, wireActor := range request.GetActors() {
			failures.record(wireActor.GetAddress(), false, err)
		}

		for _, wireGrain := range request.GetGrains() {
			if !wireGrain.GetEagerRelocation() {
				continue
			}

			failures.record(wireGrain.GetGrainId().GetValue(), true, err)
		}
	}
}

// splitFailures partitions per-item failures into actor addresses and grain
// identities for the RelocationFailed event.
func splitFailures(failed []*internalpb.RelocationFailure) (actors []string, grains []string) {
	for _, failure := range failed {
		if failure.GetGrain() {
			grains = append(grains, failure.GetId())
			continue
		}

		actors = append(actors, failure.GetId())
	}

	return actors, grains
}

// relocationFailures collects per-item relocation failures from concurrent
// goroutines.
type relocationFailures struct {
	mu       sync.Mutex
	failures []*internalpb.RelocationFailure
}

// record adds a single item failure.
func (r *relocationFailures) record(id string, grain bool, err error) {
	r.mu.Lock()
	r.failures = append(r.failures, &internalpb.RelocationFailure{Id: id, Grain: grain, Message: err.Error()})
	r.mu.Unlock()
}

// merge adds item failures reported by a remote peer.
func (r *relocationFailures) merge(failures []*internalpb.RelocationFailure) {
	if len(failures) == 0 {
		return
	}

	r.mu.Lock()
	r.failures = append(r.failures, failures...)
	r.mu.Unlock()
}

// items returns the collected failures.
func (r *relocationFailures) items() []*internalpb.RelocationFailure {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failures
}

// eligibleForRole reports whether a target advertising targetRoles may host an
// actor requiring role. A role-less actor (empty role) is eligible everywhere.
// It scans the role slice directly rather than building a lookup set per target:
// role lists are tiny (usually zero to a few entries), so a linear scan is both
// faster and allocation-free, which keeps the per-rebalance work GC-friendly.
func eligibleForRole(targetRoles []string, role string) bool {
	return role == "" || slices.Contains(targetRoles, role)
}

// allocateActors distributes the departed node's actors across the leader
// (index 0) and the surviving peers (indexes 1..n, where index i maps to
// peers[i-1]), honoring each actor's required role.
//
// The returned values are:
//   - leaderShares: actors to recreate on the leader (singletons plus the
//     leader's own balanced share at index 0 of peersShares)
//   - peersShares: per-target shares aligned to the worker's fan-out indexing
//     (index 0 is the leader, index i maps to peers[i-1])
//   - unplaceable: role-constrained actors for which no surviving target
//     advertises the required role; the caller records these as relocation
//     failures instead of silently dropping them
//
// Singleton actors always go to the leader share: their placement (including
// any role constraint) is re-arbitrated by the cluster singleton spawn path.
// Non-singleton actors are assigned to the least-loaded eligible target so the
// load spreads evenly while respecting role constraints.
//
// baseLoads carries each target's current actor occupancy (index 0 = leader,
// index i = peers[i-1]) so placement accounts for how loaded a surviving node
// already is, not just how many departed actors it has been handed. This stops
// the leader (or a single peer) from being over-assigned when the survivors
// started out unevenly loaded. A nil or mis-sized baseLoads falls back to
// balancing only the departed node's actors (all targets start at zero).
func allocateActors(leaderRoles []string, peers []*cluster.Peer, nodeLeftState *internalpb.PeerState, baseLoads []int) (leaderShares []*internalpb.Actor, peersShares [][]*internalpb.Actor, unplaceable []*internalpb.Actor) {
	// targetRoles[0] is the leader; targetRoles[i] maps to peers[i-1]. The peer
	// role slices are referenced, not copied, so no per-target allocation.
	targetRoles := make([][]string, 0, len(peers)+1)
	targetRoles = append(targetRoles, leaderRoles)

	for _, peer := range peers {
		targetRoles = append(targetRoles, peer.Roles)
	}

	peersShares = make([][]*internalpb.Actor, len(targetRoles))
	loads := make([]int, len(targetRoles))
	if len(baseLoads) == len(loads) {
		copy(loads, baseLoads)
	}

	for _, actor := range nodeLeftState.GetActors() {
		// Singletons are re-arbitrated by the singleton spawn path, so they
		// always land in the leader's share regardless of role.
		if actor.GetSingleton() != nil {
			leaderShares = append(leaderShares, actor)
			continue
		}

		role := actor.GetRole()
		best := -1

		for idx := range targetRoles {
			if !eligibleForRole(targetRoles[idx], role) {
				continue
			}

			if best == -1 || loads[idx] < loads[best] {
				best = idx
			}
		}

		if best == -1 {
			// no surviving node advertises the required role
			unplaceable = append(unplaceable, actor)
			continue
		}

		peersShares[best] = append(peersShares[best], actor)
		loads[best]++
	}

	// the leader recreates its own balanced share (index 0) locally
	leaderShares = append(leaderShares, peersShares[0]...)

	return leaderShares, peersShares, unplaceable
}

// relocatableGrains returns the departed node's grains that participate in
// relocation. Grains that opted out entirely (WithGrainDisableRelocation) are
// skipped: they are lost with the node and re-addressing makes a fresh
// instance, so relocation never touches them.
//
// Whether a returned grain is reactivated upfront (eager,
// WithGrainEagerRelocation) or only has its directory entry released so it
// re-activates on next use (lazy, the default) is decided by each relocation
// target from the grain's own eager_relocation flag.
func relocatableGrains(grains map[string]*internalpb.Grain) []*internalpb.Grain {
	relocatable := make([]*internalpb.Grain, 0, len(grains))

	for _, grain := range grains {
		if grain.GetDisableRelocation() {
			continue
		}

		relocatable = append(relocatable, grain)
	}

	return relocatable
}

// allocateGrains distributes grains among the leader and peers for rebalancing.
//
// It returns two values:
//   - leaderShares: grains to be created on the leader node
//   - peersShares: a slice of grain slices; the first entry belongs to the
//     leader and entries 1..n map to peers 0..n-1
func allocateGrains(totalPeers int, grains []*internalpb.Grain) (leaderShares []*internalpb.Grain, peersShares [][]*internalpb.Grain) {
	grainCount := len(grains)

	// Collect all grains to be rebalanced
	toRebalances := make([]*internalpb.Grain, 0, grainCount)
	toRebalances = append(toRebalances, grains...)

	quotient := grainCount / totalPeers
	remainder := grainCount % totalPeers

	// Assign the remainder grains to the leader
	leaderShares = append(leaderShares, toRebalances[:remainder]...)

	// Chunk the remaining grains for peers
	peersShares = chunk.Chunkify(toRebalances[remainder:], quotient)

	// Ensure leader takes the first chunk
	if len(peersShares) > 0 {
		leaderShares = append(leaderShares, peersShares[0]...)
	}

	return leaderShares, peersShares
}
