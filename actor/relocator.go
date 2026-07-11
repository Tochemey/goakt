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
	"runtime"
	"strconv"
	"time"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/log"
	sup "github.com/tochemey/goakt/v4/supervisor"
)

// defaultRelocationConcurrency bounds the number of concurrent spawn and grain
// activation operations performed during a single cluster rebalance. It caps the
// fan-out of local spawns and per-peer batch RPCs so relocating a node that
// hosted a large number of actors does not trigger an unbounded burst of
// goroutines and peer connections.
const defaultRelocationConcurrency = 10

// relocator is a system actor that coordinates cluster rebalancing when nodes
// leave the cluster. It spawns one short-lived relocation worker per departed
// node, so relocations of distinct nodes proceed concurrently, and cleans up
// after any worker that dies abnormally.
type relocator struct {
	remoting remoteclient.Client
	pid      *PID
	logger   log.Logger
	// workers maps a live worker name to the relocation job it owns. Entries
	// are removed when the worker terminates.
	workers map[string]workerJob
	// sequence disambiguates worker names when the same address departs
	// again after a completed relocation.
	sequence uint64
}

// workerJob identifies the relocation job a worker owns: the departed peer
// address and the exact peer state snapshot registered for it. The snapshot
// pointer is the job's identity: local tells pass messages by reference, so
// the pointer registered by beginRelocation is the one that reaches the
// relocator inside the Rebalance message. handleTerminated compares it against
// the currently registered job to tell a stale Terminated (whose job was
// already released, and possibly re-registered by a newer departure of the
// same address) from an abnormal worker death.
type workerJob struct {
	address   string
	peerState *internalpb.PeerState
}

// enforce compilation error
var _ Actor = (*relocator)(nil)

// newRelocator creates an instance of relocator. The remoting client is owned
// by the actor system and shared with the relocation workers; it is never
// closed here.
func newRelocator(remoting remoteclient.Client) *relocator {
	return &relocator{
		remoting: remoting,
		workers:  make(map[string]workerJob),
	}
}

// PreStart pre-starts the actor.
func (r *relocator) PreStart(*Context) error {
	return nil
}

// Receive handles messages sent to the relocator
func (r *relocator) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
		r.pid = ctx.Self()
		r.logger = ctx.Logger()
		r.logger.Infof("%s started successfully", r.pid.Name())
	case *internalpb.Rebalance:
		r.startWorker(ctx, msg.GetPeerState())
	case *Terminated:
		r.handleTerminated(ctx, msg)
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (r *relocator) PostStop(ctx *Context) error {
	ctx.ActorSystem().Logger().Infof("actor=%s stopped successfully", ctx.ActorName())
	return nil
}

// startWorker spawns and watches a relocation worker for the departed node
// described by peerState, then hands it the rebalance order. If the worker
// cannot be spawned the relocation is aborted and reported.
//
// There is deliberately no per-address dedupe here: beginRelocation gates
// dispatch, so every Rebalance message corresponds to a freshly registered
// job that needs a worker. A tracked worker with the same address can only be
// one that already completed and released its job but whose Terminated has
// not been processed yet; skipping the new job because of it would orphan the
// registration.
func (r *relocator) startWorker(ctx *ReceiveContext, peerState *internalpb.PeerState) {
	address := net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetPeersPort())))

	r.sequence++
	name := fmt.Sprintf("%s-%d", reservedName(relocationWorkerType), r.sequence)

	// A worker that panics is stopped, never restarted: the Rebalance order it
	// was processing is gone with its mailbox, so a restart would sit idle
	// while the job map entry blocks any retry for that address. Stopping
	// triggers the Terminated cleanup below instead.
	supervisor := sup.NewSupervisor(
		sup.WithStrategy(sup.OneForOneStrategy),
		sup.WithDirective(&errors.PanicError{}, sup.StopDirective),
		sup.WithDirective(&runtime.PanicNilError{}, sup.StopDirective),
	)

	worker := ctx.Spawn(name, newRelocationWorker(r.remoting), asSystem(), WithLongLived(), WithSupervisor(supervisor))
	if worker == nil {
		r.abortRelocation(ctx, address, peerState, errors.NewSpawnError(fmt.Errorf("failed to spawn relocation worker for node=%s", address)))
		return
	}

	ctx.Watch(worker)
	r.workers[name] = workerJob{address: address, peerState: peerState}
	ctx.Tell(worker, &internalpb.Rebalance{PeerState: peerState})
}

// handleTerminated reconciles the death of a relocation worker. A worker that
// completed normally has already published its outcome, deleted the departed
// node's peer state and released the relocation job before stopping, so the
// registered job either misses or belongs to a newer relocation of the same
// address that departed again in the meantime; both leave nothing to do here.
// The registered job matching the dead worker's own snapshot can only mean
// the worker died abnormally (panic), in which case the relocation is aborted
// and reported.
func (r *relocator) handleTerminated(ctx *ReceiveContext, msg *Terminated) {
	name := msg.ActorPath().Name()
	job, ok := r.workers[name]
	if !ok {
		return
	}

	delete(r.workers, name)

	registered, ok := r.pid.ActorSystem().relocationJob(job.address)
	if !ok || registered != job.peerState {
		return
	}

	r.logger.Errorf("relocation worker=%s for node=%s terminated unexpectedly (hint: check logs for panics)", name, job.address)
	r.abortRelocation(ctx, job.address, registered, errors.NewRebalancingError(fmt.Errorf("relocation worker for node=%s terminated unexpectedly", job.address)))
}

// abortRelocation gives up on a relocation that could not run at all: it
// publishes a RelocationFailed event listing every actor and grain of the
// departed node, records the loss in the relocation metrics, removes the node's
// peer state snapshot and releases the relocation job so future departures of
// the same address can rebalance.
func (r *relocator) abortRelocation(ctx *ReceiveContext, address string, peerState *internalpb.PeerState, err error) {
	system := r.pid.ActorSystem()
	rctx := context.WithoutCancel(ctx.Context())

	// An aborted rebalance (worker spawn failure or abnormal worker death)
	// relocates nothing. Apply the shared abort accounting so this path agrees
	// with the worker's Peers() abort and the normal per-item path: every
	// relocatable actor and eager grain is reported as failed, lazy grain
	// directory entries are released locally (only a failed release is a
	// failure), and disabled grains are excluded rather than counted as losses.
	// This also records the loss in relocation.failed.count so alerting sees it
	// instead of a healthy-looking rebalance.
	system.reportAbortedRelocation(rctx, r.pid, address, peerState, 0, err)

	if derr := system.getClusterStore().DeletePeerState(rctx, address); derr != nil {
		r.logger.Errorf("failed to remove peer=%s state after failed relocation: %v (hint: check cluster store)", address, derr)
	}

	system.endRelocation(address)
}

// publishRelocationFailed emits a RelocationFailed event describing the
// departed node and the actors and grains that could not be relocated.
func publishRelocationFailed(pid *PID, address string, actors map[string]*internalpb.Actor, grains map[string]*internalpb.Grain, err error) {
	if pid.eventsStream == nil {
		return
	}

	actorNames := make([]string, 0, len(actors))
	for _, actor := range actors {
		actorNames = append(actorNames, actor.GetAddress())
	}

	grainIDs := make([]string, 0, len(grains))
	for _, grain := range grains {
		grainIDs = append(grainIDs, grain.GetGrainId().GetValue())
	}

	pid.eventsStream.Publish(eventsTopic, NewRelocationFailed(address, time.Now().UTC(), actorNames, grainIDs, err))
}
