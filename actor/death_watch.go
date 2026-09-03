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
	"errors"
	"fmt"
	"time"

	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/log"
)

const (
	// deathWatchRemovalMaxRetries bounds the removal retries scheduled after a
	// failed cluster registry cleanup. Together with the doubling delay below,
	// the budget spans roughly fifteen seconds, comfortably covering the
	// routing table convergence window during which such failures are
	// typically transient.
	deathWatchRemovalMaxRetries = 5
	// deathWatchRemovalRetryDelay is the delay before the first removal
	// retry; it doubles on every subsequent attempt.
	deathWatchRemovalRetryDelay = 500 * time.Millisecond
)

// retryDeadActorRemoval asks DeathWatch to retry the cluster registry removal
// of a dead actor whose record could not be removed when it terminated. It is
// self-scheduled through the system scheduler, so retries ride the existing
// scheduler machinery instead of a dedicated goroutine, and each message
// carries its own state so DeathWatch itself stays stateless.
type retryDeadActorRemoval struct {
	actorName string
	attempt   int
}

// clusterCleanupError signals a failed removal of a dead actor's cluster
// registry record. The removal often runs while the cluster is still digesting
// a membership change, so the failure is usually transient and never means
// DeathWatch itself is broken: the DeathWatch supervisor resumes on this type
// (see spawnDeathWatch) instead of escalating it. The type is internal to the
// runtime by design — it only travels between DeathWatch and its supervisor
// and is never surfaced to users.
type clusterCleanupError struct {
	err error
}

// enforce compilation error
var _ error = (*clusterCleanupError)(nil)

// newClusterCleanupError returns an instance of clusterCleanupError
func newClusterCleanupError(err error) *clusterCleanupError {
	return &clusterCleanupError{err: err}
}

// Error implements the standard error interface
func (e *clusterCleanupError) Error() string {
	return fmt.Sprintf("cluster cleanup error: %v", e.err)
}

func (e *clusterCleanupError) Unwrap() error {
	return e.err
}

// deathWatch removes dead actors from the system
// that helps free non-utilized resources
type deathWatch struct{}

// enforce compilation error
var _ Actor = (*deathWatch)(nil)

// newDeathWatch creates an instance of the system deathWatch
func newDeathWatch() *deathWatch {
	return &deathWatch{}
}

// PreStart is the pre-start hook
func (x *deathWatch) PreStart(*Context) error {
	return nil
}

// Receive a handle message received
func (x *deathWatch) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
		x.handlePostStart(ctx)
	case *Terminated:
		ctx.Err(x.handleTerminated(ctx))
	case *retryDeadActorRemoval:
		x.handleRetryDeadActorRemoval(ctx)
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (x *deathWatch) PostStop(ctx *Context) error {
	logger := ctx.ActorSystem().Logger()
	if logger.Enabled(log.InfoLevel) {
		logger.Infof("actor=%s stopped successfully", ctx.ActorName())
	}
	return nil
}

// handlePostStart handles PostStart message
func (x *deathWatch) handlePostStart(ctx *ReceiveContext) {
	logger := ctx.Logger()
	if logger.Enabled(log.InfoLevel) {
		logger.Infof("actor=%s started successfully", ctx.Self().Name())
	}
}

// handleTerminated handles Terminated message
func (x *deathWatch) handleTerminated(ctx *ReceiveContext) error {
	msg := ctx.Message().(*Terminated)

	logger := ctx.Logger()
	actorSys := ctx.ActorSystem()

	path := msg.ActorPath()
	if logger.Enabled(log.DebugLevel) {
		logger.Debugf("actor=%s removing dead actor resource from system", path)
	}

	actorTree := actorSys.tree()
	if node, ok := actorTree.node(path.String()); ok {
		pid := node.value()

		if !pid.isStateSet(systemState) {
			actorSys.decreaseActorsCounter()
		}

		actorName := pid.Name()
		actorTree.deleteNode(pid)
		// system actors never publish registry records, with one exception:
		// reliable-delivery controller companions do through their private
		// publication path, so their records must leave the registry with them
		removable := !pid.isStateSet(systemState) || pid.reliableCompanion != nil
		removeFromCluster := actorSys.InCluster() && removable && !actorSys.isStopping()

		if removeFromCluster {
			cctx := ctx.withoutCancel()
			cl := actorSys.getCluster()

			if err := cl.RemoveActor(cctx, actorName); err != nil {
				if logger.Enabled(log.ErrorLevel) {
					logger.Errorf("actor=%s failed to remove dead actor from cluster: %v", path, err)
				}
				// a failed registry cleanup is not a DeathWatch failure: the
				// removal often runs while the cluster is still digesting the
				// membership change that terminated the actor, so the error is
				// usually transient. Report it as clusterCleanupError, which the
				// DeathWatch supervisor resumes on (see spawnDeathWatch) instead
				// of escalating to the system guardian and stopping an otherwise
				// healthy node, and schedule a bounded removal retry so the
				// stale record does not keep the actor name reserved once the
				// cluster settles. A stopped engine is the one failure no retry
				// can outlive (it means the system began stopping after the
				// gate above), so only that skips the retry outright.
				if !errors.Is(err, cluster.ErrEngineNotRunning) {
					x.scheduleRemovalRetry(ctx, actorName, 1)
				}
				return newClusterCleanupError(err)
			}
		}

		if logger.Enabled(log.DebugLevel) {
			logger.Debugf("actor=%s removed dead actor resource from system", path)
		}
		return nil
	}
	if logger.Enabled(log.DebugLevel) {
		logger.Debugf("actor=%s addr=%s unable to locate dead actor resource, maybe already freed", ctx.Self().Name(), path)
	}
	return nil
}

// handleRetryDeadActorRemoval retries the cluster registry removal of a dead
// actor. The retry is skipped when the system is stopping (the shutdown path
// and the surviving nodes' relocation cleanup reconcile the node's registry
// records) or no longer in cluster mode. A failed retry within the budget
// reschedules itself with a doubled delay; once the budget is exhausted the
// record is abandoned with an error log, since retrying forever against a
// persistently failing registry would only mask a real cluster problem.
func (x *deathWatch) handleRetryDeadActorRemoval(ctx *ReceiveContext) {
	msg := ctx.Message().(*retryDeadActorRemoval)

	logger := ctx.Logger()
	actorSys := ctx.ActorSystem()

	if !actorSys.InCluster() || actorSys.isStopping() {
		return
	}

	cl := actorSys.getCluster()
	if err := cl.RemoveActor(ctx.withoutCancel(), msg.actorName); err != nil {
		// a stopped engine cannot recover within the retry budget: the system
		// is going down and its registry records are reconciled elsewhere
		if errors.Is(err, cluster.ErrEngineNotRunning) {
			return
		}

		if msg.attempt >= deathWatchRemovalMaxRetries {
			if logger.Enabled(log.ErrorLevel) {
				logger.Errorf("actor=%s failed to remove dead actor from cluster after %d retries: %v (hint: check cluster health; the stale record keeps the actor name reserved)", msg.actorName, msg.attempt, err)
			}
			return
		}

		if logger.Enabled(log.WarningLevel) {
			logger.Warnf("actor=%s removal retry=%d/%d failed: %v (retrying)", msg.actorName, msg.attempt, deathWatchRemovalMaxRetries, err)
		}

		x.scheduleRemovalRetry(ctx, msg.actorName, msg.attempt+1)
		return
	}

	if logger.Enabled(log.DebugLevel) {
		logger.Debugf("actor=%s removed dead actor resource from cluster on retry=%d", msg.actorName, msg.attempt)
	}
}

// scheduleRemovalRetry books the attempt-th removal retry for actorName with
// the system scheduler, doubling the delay on each attempt. Scheduling rides
// the scheduler's own machinery, so no goroutine is spawned and DeathWatch's
// mailbox is never blocked waiting out a backoff. A scheduling failure is only
// logged: it means the scheduler is no longer running, which only happens when
// the actor system itself is going down.
func (x *deathWatch) scheduleRemovalRetry(ctx *ReceiveContext, actorName string, attempt int) {
	delay := deathWatchRemovalRetryDelay << (attempt - 1)
	message := &retryDeadActorRemoval{actorName: actorName, attempt: attempt}

	if err := ctx.ActorSystem().ScheduleOnce(ctx.withoutCancel(), message, ctx.Self(), delay); err != nil {
		logger := ctx.Logger()
		if logger.Enabled(log.ErrorLevel) {
			logger.Errorf("actor=%s failed to schedule removal retry=%d: %v", actorName, attempt, err)
		}
	}
}
