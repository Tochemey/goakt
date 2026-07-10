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
	"fmt"
	"net"
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
	"github.com/tochemey/goakt/v4/internal/slices"
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
	address := net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetPeersPort())))
	departedNode := net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetRemotingPort())))
	rctx := context.WithoutCancel(ctx.Context())

	if system.isStopping() {
		system.endRelocation(address)
		return
	}

	peers, err := system.getCluster().Peers(rctx)
	if err != nil {
		w.logger.Errorf("cluster rebalancing failed for node=%s: %v (hint: check cluster quorum, peer connectivity)", address, err)
		publishRelocationFailed(w.pid, address, peerState.GetActors(), peerState.GetGrains(), errors.NewInternalError(err))
		w.finish(rctx, address)
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

	leaderActors, peerActors := allocateActors(len(peers)+1, peerState)
	leaderGrains, peerGrains := allocateGrains(len(peers)+1, grains)

	eg := new(errgroup.Group)
	eg.SetLimit(defaultRelocationConcurrency)

	// recreate the leader's share locally, one bounded goroutine per item;
	// a failing item records a failure instead of cancelling its siblings
	for _, wireActor := range leaderActors {
		eg.Go(func() error {
			var err error

			if wireActor.GetSingleton() != nil {
				err = recreateSingletonFromWire(rctx, system, wireActor)
			} else {
				err = system.recreateActorFromWire(rctx, wireActor, departedNode)
			}

			if err != nil {
				w.logger.Errorf("failed to relocate actor=%s locally: %v (hint: check actor type registered, cluster quorum)", wireActor.GetAddress(), err)
				failures.record(wireActor.GetAddress(), false, err)
			}
			return nil
		})
	}

	for _, wireGrain := range leaderGrains {
		eg.Go(func() error {
			// lazy grains: directory cleanup only. A failure here is not an
			// item loss (activation self-heals a stale entry on next use), so
			// it is logged, not recorded as a relocation failure.
			if !wireGrain.GetEagerRelocation() {
				if err := system.releaseGrainForLazyRelocation(rctx, wireGrain, departedNode); err != nil {
					w.logger.Warnf("failed to release lazy grain=%s directory entry: %v (hint: entry self-heals on next activation)", wireGrain.GetGrainId().GetValue(), err)
				}
				return nil
			}

			if err := system.recreateGrainFromWire(rctx, wireGrain, departedNode); err != nil {
				w.logger.Errorf("failed to relocate grain=%s locally: %v (hint: check grain OnActivate, grain kind registered)", wireGrain.GetGrainId().GetValue(), err)
				failures.record(wireGrain.GetGrainId().GetValue(), true, err)
			}
			return nil
		})
	}

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

	if failed := failures.items(); len(failed) > 0 {
		actors, grains := splitFailures(failed)
		err := errors.NewRebalancingError(fmt.Errorf("relocation of node=%s completed with %d failed actors and %d failed grains", address, len(actors), len(grains)))
		w.logger.Errorf("%v (hint: subscribe to RelocationFailed events for the affected actors and grains)", err)

		if w.pid.eventsStream != nil {
			w.pid.eventsStream.Publish(eventsTopic, NewRelocationFailed(address, time.Now().UTC(), actors, grains, err))
		}
	}

	w.finish(rctx, address)
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

// relocateShare delivers one peer's share, batch by batch, to its target. When
// the target becomes unreachable the unsent remainder is moved once to the next
// surviving peer; items that still cannot be delivered are recorded as failed.
func (w *relocationWorker) relocateShare(ctx context.Context, requests []*internalpb.RelocateBatchRequest, target *cluster.Peer, peers []*cluster.Peer, failures *relocationFailures) {
	remaining, err := w.sendBatches(ctx, target, requests, failures)
	if err == nil {
		return
	}

	w.logger.Warnf("peer=%s:%d unreachable during relocation: %v (hint: reassigning share to next peer)", target.Host, target.RemotingPort, err)

	fallback := nextSurvivingPeer(peers, target)
	if fallback == nil {
		recordUnsent(remaining, err, failures)
		return
	}

	remaining, err = w.sendBatches(ctx, fallback, remaining, failures)
	if err != nil {
		recordUnsent(remaining, err, failures)
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
		resp, err := w.remoting.RelocateBatch(ctx, target.Host, target.RemotingPort, request)
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
// path, restoring its singleton spawn configuration. The departed node's
// registry entry is removed unconditionally: singleton placement is arbitrated
// by the singleton spawn path itself, not by the relocation gating used for
// regular actors.
func recreateSingletonFromWire(ctx context.Context, system ActorSystem, props *internalpb.Actor) error {
	addr, err := address.Parse(props.GetAddress())
	if err != nil {
		return errors.NewInternalError(err)
	}

	if isSystemName(addr.Name()) {
		return nil
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

// nextSurvivingPeer returns the peer following target in the given list,
// wrapping around, or nil when target is the only peer.
func nextSurvivingPeer(peers []*cluster.Peer, target *cluster.Peer) *cluster.Peer {
	if len(peers) < 2 {
		return nil
	}

	for i, peer := range peers {
		if peer.Host == target.Host && peer.RemotingPort == target.RemotingPort {
			return peers[(i+1)%len(peers)]
		}
	}

	return peers[0]
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

// allocateActors builds the list of actors to recreate on the leader node and
// the shares to hand to the peers in the cluster. Singleton actors always land
// in the leader's share; the leader also takes the division remainder and the
// first chunk. Chunks 1..n map to peers 0..n-1.
func allocateActors(totalPeers int, nodeLeftState *internalpb.PeerState) (leaderShares []*internalpb.Actor, peersShares [][]*internalpb.Actor) {
	actors := nodeLeftState.GetActors()
	actorsCount := len(actors)

	// Collect all actors to be rebalanced
	toRebalances := make([]*internalpb.Actor, 0, actorsCount)
	for _, actorProps := range actors {
		toRebalances = append(toRebalances, actorProps)
	}

	// Separate singleton actors to be assigned to the leader
	leaderShares = slices.Filter(toRebalances, func(actor *internalpb.Actor) bool {
		return actor.GetSingleton() != nil
	})

	// Remove singleton actors from the list
	toRebalances = slices.Filter(toRebalances, func(actor *internalpb.Actor) bool {
		return actor.GetSingleton() == nil
	})

	// Distribute remaining actors among peers
	actorsCount = len(toRebalances)
	quotient := actorsCount / totalPeers
	remainder := actorsCount % totalPeers

	// Assign remainder actors to the leader
	leaderShares = append(leaderShares, toRebalances[:remainder]...)

	// Chunk the remaining actors for peers
	chunks := chunk.Chunkify(toRebalances[remainder:], quotient)

	// Ensure leader takes the first chunk
	if len(chunks) > 0 {
		leaderShares = append(leaderShares, chunks[0]...)
	}

	return leaderShares, chunks
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
