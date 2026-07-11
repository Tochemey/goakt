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
	"net"
	"syscall"
	"time"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/types"
)

const (
	// relocationHandoffWindow bounds how long a name-based send masks a
	// departed node's relocation. It is both the retention of a departed
	// endpoint in relocatingEndpoints and the deadline of the retry loop, so a
	// caller never waits longer than this for a target to reappear on a
	// survivor. It is deliberately short: masking trades a little latency for
	// availability during the brief handoff, not an unbounded queue.
	relocationHandoffWindow = 3 * time.Second

	// relocationHandoffMinBackoff and relocationHandoffMaxBackoff bound the
	// exponential wait between re-resolution attempts inside the handoff window.
	relocationHandoffMinBackoff = 50 * time.Millisecond
	relocationHandoffMaxBackoff = 300 * time.Millisecond

	// relocationNotFoundMaskWindow caps how long a *failed* (not-found)
	// resolution is masked, independently of the full handoff window that
	// applies to a target still resolvable to a departing endpoint. A name that
	// never existed can only ever reach the not-found branch, so a short cap
	// keeps such probes fail-fast during a handoff instead of stalling for the
	// multi-second window. A genuinely relocating actor is normally still
	// resolvable to its departing endpoint (masked with the full window) and
	// only briefly disappears between its entry's removal and its rewrite on a
	// survivor, which this cap comfortably covers.
	relocationNotFoundMaskWindow = 500 * time.Millisecond
)

// markEndpointRelocating opens a bounded handoff window for the remoting
// endpoint of the node that just departed at peerAddress (a host:peersPort
// address). It resolves the departed node's remoting port from the peer cache
// so the recorded endpoint matches the host:port a name lookup resolves to;
// when the port is unknown (the node was never observed alive by this node)
// there is nothing to mask and the call is a no-op.
func (x *actorSystem) markEndpointRelocating(peerAddress string) {
	host, _, err := net.SplitHostPort(peerAddress)
	if err != nil {
		return
	}

	remotingPort, ok := x.peerRemotingPort(peerAddress)
	if !ok {
		return
	}

	x.relocatingEndpoints.Set(address.FormatHostPort(host, remotingPort), types.Unit{})
}

// markEndpointRecovered closes the handoff window for the remoting endpoint of
// a node that rejoined the cluster at peerAddress. A member that restarts at
// the same host:port within its own handoff window is healthy and reachable
// again; leaving the mask in place would stall or refuse every name-based send
// that resolves to it for the remainder of the window. Unresolvable addresses
// and unknown ports mean nothing was masked, so the call is a no-op.
func (x *actorSystem) markEndpointRecovered(peerAddress string) {
	host, _, err := net.SplitHostPort(peerAddress)
	if err != nil {
		return
	}

	remotingPort, ok := x.peerRemotingPort(peerAddress)
	if !ok {
		return
	}

	x.relocatingEndpoints.Delete(address.FormatHostPort(host, remotingPort))
}

// isEndpointRelocating reports whether addr resolves to a remoting endpoint that
// is within its handoff window (its host recently left the cluster and its
// actors are being recreated elsewhere).
func (x *actorSystem) isEndpointRelocating(addr *address.Address) bool {
	if addr == nil {
		return false
	}
	_, ok := x.relocatingEndpoints.Get(address.FormatHostPort(addr.Host(), addr.Port()))
	return ok
}

// relocationInFlight reports whether any departed endpoint is still within its
// handoff window. It gates masking of a "not found" resolution, where the
// target endpoint is unknown because the registry entry was removed mid-handoff
// but its replacement has not been written yet.
//
// It uses Active (not Len) so it ignores endpoints whose handoff window has
// already elapsed but that have not yet been evicted: eviction only runs on
// Set, so a Len-based gate would read true for the life of the process after
// the first departure, spinning every unresolved send in the handoff retry loop
// for the full window.
func (x *actorSystem) relocationInFlight() bool {
	return x.relocatingEndpoints.Active()
}

// isHandoffRetryable reports whether err is a transient send failure that a
// relocation handoff can mask by re-resolving and retrying: a target still
// pinned to (or freshly removed from) a departed node surfaces as one of these
// while its replacement is written on a survivor.
func isHandoffRetryable(err error) bool {
	if err == nil {
		return false
	}

	switch {
	case errors.Is(err, gerrors.ErrActorNotFound),
		errors.Is(err, gerrors.ErrAddressNotFound),
		errors.Is(err, gerrors.ErrRemoteSendFailure),
		errors.Is(err, gerrors.ErrRequestTimeout),
		errors.Is(err, gerrors.ErrRelocationInProgress),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, syscall.ECONNREFUSED):
		return true
	}

	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// deliverAcrossHandoff resolves actorName in the local datacenter and invokes
// deliver on the resolved target, masking the brief window in which a departed
// node's actors are being relocated onto a survivor.
//
// In the common case (not clustered, or the target resolves to a live node) it
// resolves once and delivers once, adding no latency. It only enters its
// bounded retry loop when there is an active handoff that would otherwise turn
// into a spurious error:
//   - resolution succeeds but points at a departing endpoint: the target is
//     mid-relocation, so it waits for the record to be rewritten on a survivor
//     instead of dialing the dead host;
//   - resolution fails with a transient error while any relocation is in flight
//     (the record was removed but its replacement is not yet visible).
//
// A resolution to a live node delivers immediately and surfaces the delivery
// outcome as-is: a failure there is a genuine error against a healthy node, not
// a handoff to mask. Masking is bounded three ways: a target pinned to a
// departing endpoint waits the full relocationHandoffWindow; a merely
// not-yet-visible (not-found) resolution waits only relocationNotFoundMaskWindow
// so never-existed names stay fail-fast; and, when maxWait is positive, the
// whole operation (masking plus the final delivery) is capped by the caller's
// budget so a masked send still honors its timeout instead of adding a fresh
// one on top. When masking gives up it surfaces the error that stalled it,
// which is ErrRelocationInProgress when the target was left waiting on a
// departing endpoint (a retryable signal rather than a misleading "not found").
// A terminal resolution error short-circuits immediately.
func (pid *PID) deliverAcrossHandoff(ctx context.Context, actorName string, maxWait time.Duration, deliver func(context.Context, *PID) (any, error)) (any, error) {
	system := pid.actorSystem

	to, err := system.ActorOf(ctx, actorName)

	// Fast path: without clustering there is no relocation to mask.
	if !system.InCluster() {
		if err != nil {
			return nil, err
		}
		return deliver(ctx, to)
	}

	start := time.Now()

	// callerDeadline caps the entire operation (the masking retries plus the
	// final delivery) by the caller's own budget. Without it the retry loop can
	// consume the whole window and then the final deliver would start a fresh
	// full timeout on top, so a masked SendSync(ctx, name, msg, 2s) could block
	// ~4s. A non-positive maxWait means the caller imposed no bound.
	var callerDeadline time.Time
	if maxWait > 0 {
		callerDeadline = start.Add(maxWait)
	}

	// window bounds how long a target still resolvable to a departing endpoint
	// is masked: never longer than the handoff window, and never longer than the
	// caller's own timeout when that is shorter (a 100ms SendSync must not wait
	// out the full multi-second window).
	window := relocationHandoffWindow
	if maxWait > 0 && maxWait < window {
		window = maxWait
	}
	deadline := start.Add(window)

	// notFoundDeadline caps the shorter masking applied to a *failed*
	// resolution. It is deliberately much shorter than the full window so a name
	// that never existed (which can only reach the not-found branch) fails fast
	// during a handoff instead of stalling for the whole window. It is anchored
	// at the first not-found observation, not at the send's start: a send that
	// first masked the pinned-endpoint case and then sees the registry entry's
	// removal (the remove-then-respawn gap while the actor is recreated on a
	// survivor) must still get the full not-found budget, otherwise it would
	// surface a spurious not-found in exactly the scenario the mask exists for.
	// It never outlives the caller's own budget.
	var notFoundDeadline time.Time

	backoff := relocationHandoffMinBackoff
	buffered := false

	for {
		// retryErr is the error to surface if masking gives up before this
		// attempt succeeds; attemptDeadline is the cap that applies to it.
		var retryErr error
		var attemptDeadline time.Time
		switch {
		case err == nil && to.IsRemote() && system.isEndpointRelocating(to.getAddress()):
			// Target still pinned to a departing node: do not dial the dead
			// host, wait for its actor to re-register on a survivor. This is a
			// high-confidence handoff, so it earns the full window.
			retryErr = gerrors.ErrRelocationInProgress
			attemptDeadline = deadline

		case err == nil:
			// Resolved to a node this sender considers live. A departing target
			// was handled above, so deliver and surface the outcome directly: a
			// failure here is a genuine delivery error against a live node, not
			// a handoff to mask. Bound the delivery by the caller's remaining
			// budget so the time already spent masking is deducted from it.
			if callerDeadline.IsZero() {
				return deliver(ctx, to)
			}
			dctx, cancel := context.WithDeadline(ctx, callerDeadline)
			resp, derr := deliver(dctx, to)
			cancel()
			return resp, derr

		case isHandoffRetryable(err) && system.relocationInFlight():
			// Resolution failed transiently while a relocation is in flight (the
			// record was removed but its replacement is not visible yet). This is
			// speculative (a never-existed name looks identical), so it is masked
			// only briefly, starting from this first failed resolution.
			if notFoundDeadline.IsZero() {
				notFoundDeadline = time.Now().Add(relocationNotFoundMaskWindow)

				if !callerDeadline.IsZero() && callerDeadline.Before(notFoundDeadline) {
					notFoundDeadline = callerDeadline
				}
			}

			retryErr = err
			attemptDeadline = notFoundDeadline

		default:
			// Terminal resolution failure, or nothing is relocating: fail fast
			// (e.g. the actor never existed and no node is relocating).
			return nil, err
		}

		// Record the handoff once, the first time this send is buffered.
		if !buffered {
			buffered = true
			system.recordRelocationHandoff(ctx)
		}

		if !sleepWithinHandoff(ctx, backoff, attemptDeadline) {
			return nil, retryErr
		}

		backoff = min(backoff*2, relocationHandoffMaxBackoff)
		to, err = system.ActorOf(ctx, actorName)
	}
}

// deliverBypassingHandoff resolves actorName once and delivers immediately,
// never entering the handoff retry loop: it is the non-blocking counterpart of
// deliverAcrossHandoff for fire-and-forget sends (SendAsync, including its
// ReceiveContext wrapper, which runs inside actor receive loops and must never
// sleep out a relocation window). A target still pinned to a departing
// endpoint is not dialed (the host is gone); the send fails fast with
// ErrRelocationInProgress, a retryable signal that tells the caller the actor
// is being recreated rather than a misleading connection error.
func (pid *PID) deliverBypassingHandoff(ctx context.Context, actorName string, deliver func(context.Context, *PID) (any, error)) (any, error) {
	system := pid.actorSystem

	to, err := system.ActorOf(ctx, actorName)
	if err != nil {
		return nil, err
	}

	if system.InCluster() && to.IsRemote() && system.isEndpointRelocating(to.getAddress()) {
		return nil, gerrors.ErrRelocationInProgress
	}

	return deliver(ctx, to)
}

// sleepWithinHandoff blocks for up to d, clamped so it never sleeps past
// deadline, and reports whether the caller should attempt another retry. It
// returns false when the handoff window has closed or the context is done, in
// which case the caller surfaces its last error.
func sleepWithinHandoff(ctx context.Context, duration time.Duration, deadline time.Time) bool {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return false
	}

	if duration > remaining {
		duration = remaining
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
