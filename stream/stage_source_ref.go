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

package stream

import (
	"errors"
	"fmt"
	"time"

	"github.com/tochemey/goakt/v4/actor"
)

// sourceRefBufferLimit caps the number of elements held in the endpoint's
// pending queue while waiting for wire credit. The sub-pipeline's mergeSink
// requests its own demand independently of wire credit, so the endpoint must
// absorb the gap; bounding it converts an OOM risk into a visible stream
// error. Sized to comfortably exceed the default sink InitialDemand (224)
// plus a refill so a normal producer/consumer pair never trips it.
const sourceRefBufferLimit = 1024

// sourceRefShutdownGrace is how long the endpoint actor stays alive after
// the stream has finished (success or error). During the grace window any
// late streamSubscribeWire arriving from a slow bridge gets a deterministic
// streamErrorWire instead of telling a dead actor and hanging. The window
// is sized to comfortably cover both in-process scheduling jitter and
// cross-cluster message latency without leaking the actor longer than
// needed.
const sourceRefShutdownGrace = 30 * time.Second

// sourceRefShutdown is the self-message scheduled at stream termination
// that triggers the endpoint actor's final teardown. Defined as a typed
// struct (not a sentinel value) so the actor's switch handles it via the
// type system rather than magic-value comparison.
type sourceRefShutdown struct{}

// sourceRefEndpointActor is the producer-side endpoint of a SourceRef. It
// owns a Source[T] stage list and, on receipt of streamSubscribeWire,
// materialises the underlying source as a child sub-pipeline whose terminal
// sink forwards every element to this actor via mergeSubValue. Elements are
// shipped to the subscriber captured from rctx.Sender() as ordinary remote
// messages — the remoting layer handles their serialization. Delivery is
// gated on wire credit granted by the consumer via streamRequestWire.
//
// One subscription per endpoint: subsequent subscribes (whether the stream
// is still active or has already finished) receive streamErrorWire. The
// actor stays alive after natural completion so a late subscriber gets a
// deterministic rejection instead of telling a dead actor and hanging — it
// is reaped only on explicit cancel (streamCancelWire) or actor-system
// shutdown. This matches Akka StreamRefs' single-use semantics.
type sourceRefEndpointActor[T any] struct {
	srcStages  []*stage
	system     actor.ActorSystem
	subscriber *actor.PID
	streamID   string

	// Wire-level flow control. wireCredit is granted by the consumer via
	// streamRequestWire; pending buffers elements emitted by the local
	// sub-pipeline before credit is available. Both are owned by this actor
	// goroutine — no synchronization needed.
	wireCredit int64
	pending    []any
	completed  bool // set on mergeSubDone; final completeWire flushed after pending drains
	terminated bool // set after the stream has finished — late subscribes get an error
}

// newSourceRefEndpointActor constructs an endpoint actor over the given
// source stage list. The endpoint does nothing until a subscriber arrives.
func newSourceRefEndpointActor[T any](srcStages []*stage) *sourceRefEndpointActor[T] {
	return &sourceRefEndpointActor[T]{srcStages: srcStages}
}

func (a *sourceRefEndpointActor[T]) PreStart(ctx *actor.Context) error {
	a.system = ctx.ActorSystem()
	return nil
}

// Receive handles streamSubscribeWire, streamRequestWire, mergeSubValue,
// mergeSubDone, and streamCancelWire.
func (a *sourceRefEndpointActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *streamSubscribeWire:
		if a.subscriber != nil || a.terminated {
			// Either a current subscriber is active or the stream has already
			// run to completion. Reject the late subscribe with the rejection
			// streamID matching the caller's request so the bridge's
			// streamID filter routes it correctly.
			reason := "stream: source ref already subscribed"
			if a.terminated {
				reason = "stream: source ref already consumed"
			}
			rctx.Tell(rctx.Sender(), &streamErrorWire{
				StreamID: msg.StreamID,
				Err:      reason,
			})
			return
		}
		a.subscriber = rctx.Sender()
		a.streamID = msg.StreamID

		// Spawn the underlying source as a child sub-pipeline whose terminal
		// sink forwards each element back to this actor via mergeSubValue.
		// The slot value is unused for source refs (only one subscriber).
		self := rctx.Self()
		sink := makeMergeSinkDesc(self, 0)
		all := make([]*stage, len(a.srcStages)+1)
		copy(all, a.srcStages)
		all[len(a.srcStages)] = sink
		spawnSubPipeline(rctx.Context(), a.system, all)

	case *streamRequestWire:
		if msg.StreamID != a.streamID || a.terminated {
			return
		}
		a.wireCredit += msg.N
		if !a.drainPending(rctx) {
			return
		}
		a.maybeFinish(rctx)

	case *mergeSubValue:
		if a.subscriber == nil || a.terminated {
			return
		}

		// Fast path: credit available and no backlog — serialize and ship inline.
		if a.wireCredit > 0 && len(a.pending) == 0 {
			a.shipElement(rctx, msg.value)
			return
		}

		// Backpressure path: buffer until the consumer grants credit. A bounded
		// queue converts an unbounded mailbox into a visible stream-level error
		// when a slow consumer can't keep up. The endpoint stays alive through
		// the grace window so late subscribes still get a deterministic
		// rejection rather than telling a dead actor.
		if len(a.pending) >= sourceRefBufferLimit {
			rctx.Tell(a.subscriber, &streamErrorWire{
				StreamID: a.streamID,
				Err:      fmt.Sprintf("stream: source ref backpressure overflow (>%d elements buffered)", sourceRefBufferLimit),
			})
			a.pending = nil
			a.scheduleTermination(rctx)
			return
		}
		a.pending = append(a.pending, msg.value)

	case *mergeSubDone:
		if a.terminated {
			return
		}
		// Defer the wire-level completion until any buffered elements have
		// drained, otherwise the consumer would see "complete" while pending
		// values are still queued in this actor.
		a.completed = true
		a.maybeFinish(rctx)

	case *streamCancelWire:
		// Subscriber cancelled — shut down immediately. The sub-pipeline
		// becomes unreferenced and is GC'd when its own coordinator stops.
		rctx.Shutdown()

	case *sourceRefShutdown:
		// Grace window elapsed after stream termination — release the actor.
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

func (a *sourceRefEndpointActor[T]) PostStop(_ *actor.Context) error { return nil }

// shipElement sends value to the subscriber as a plain remote message,
// decrementing wire credit. The remoting layer serializes the value using
// the registry shared with the rest of the actor system; no per-stream
// serializer is needed. wireForm normalises to a pointer so the registry's
// exact-type lookup matches the registered *T entry.
func (a *sourceRefEndpointActor[T]) shipElement(rctx *actor.ReceiveContext, value any) {
	a.wireCredit--
	rctx.Tell(a.subscriber, wireForm(value))
}

// drainPending ships buffered elements while wire credit is available.
// Returns false when shipElement triggered an error path (actor is being
// shut down) so the caller stops processing.
func (a *sourceRefEndpointActor[T]) drainPending(rctx *actor.ReceiveContext) bool {
	for a.wireCredit > 0 && len(a.pending) > 0 {
		v := a.pending[0]
		// Avoid leaking references to drained values via the slice's backing
		// array — without this the GC keeps them alive until the slice header
		// is reallocated.
		a.pending[0] = nil
		a.pending = a.pending[1:]
		a.shipElement(rctx, v)
		// shipElement may have shut us down on serializer failure; bail.
		if a.subscriber == nil {
			return false
		}
	}
	// Reset the slice header once fully drained so the backing array is
	// eligible for GC instead of growing unboundedly across stream lifetimes.
	if len(a.pending) == 0 {
		a.pending = nil
	}
	return true
}

// maybeFinish flushes the wire-level completion once the upstream is done
// AND every buffered element has been shipped. The actor stays alive through
// the grace window so a racing second subscriber gets a deterministic
// "already consumed" rejection rather than telling a dead actor; after the
// window the actor schedules its own teardown so refs don't leak when the
// caller forgot to cancel.
func (a *sourceRefEndpointActor[T]) maybeFinish(rctx *actor.ReceiveContext) {
	if !a.completed || a.terminated || len(a.pending) > 0 {
		return
	}
	if a.subscriber != nil {
		rctx.Tell(a.subscriber, &streamCompleteWire{StreamID: a.streamID})
	}
	a.scheduleTermination(rctx)
}

// scheduleTermination flips the endpoint into the post-stream state and
// arranges its eventual cleanup. Late streamSubscribeWire arrivals during
// the grace window get the "already consumed" rejection; once the
// scheduled sourceRefShutdown fires the actor stops and any further
// arrivals fall through to the actor system's dead-letter handling.
//
// A failed schedule is non-fatal: the actor stays alive and is reaped at
// system shutdown. Logging the error is left to the actor system's
// scheduler — we don't want to escalate a best-effort cleanup into a
// stream-level failure.
func (a *sourceRefEndpointActor[T]) scheduleTermination(rctx *actor.ReceiveContext) {
	if a.terminated {
		return
	}
	a.terminated = true
	if a.system == nil {
		return
	}
	_ = a.system.ScheduleOnce(rctx.Context(), &sourceRefShutdown{}, rctx.Self(), sourceRefShutdownGrace)
}

// remoteSourceBridgeActor is the consumer-side source stage that adapts a
// SourceRef back into a Source[T] in the local graph. It resolves the
// endpoint by name (with brief retries because cluster registration on
// the producer side is asynchronous), sends streamSubscribeWire, forwards
// downstream demand as streamRequestWire, and converts incoming user values
// (delivered as ordinary remote messages) into the internal streamElement
// protocol.
type remoteSourceBridgeActor[T any] struct {
	endpointName string
	endpointHost string
	endpointPort int
	system       actor.ActorSystem
	endpoint     *actor.PID
	downstream   *actor.PID
	subID        string
	streamID     string
	// seqNo is the local-pipeline element sequence emitted downstream.
	// Required by ordering-sensitive stages like Parallel.
	seqNo uint64
	// completed flips when a control wire (complete / error / cancel) has
	// been processed. It guards the *actor.Terminated handler against a
	// spurious error when the endpoint dies normally right after sending its
	// final wire message — an unrelated death-watch notification arriving
	// after a clean stream must not be mistranslated into a stream error.
	completed bool
	config    StageConfig
}

func newRemoteSourceBridgeActor[T any](endpointName, endpointHost string, endpointPort int, config StageConfig) *remoteSourceBridgeActor[T] {
	return &remoteSourceBridgeActor[T]{
		endpointName: endpointName,
		endpointHost: endpointHost,
		endpointPort: endpointPort,
		system:       config.System,
		config:       config,
	}
}

func (a *remoteSourceBridgeActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, control wires, streamCancel,
// and (default) user element values arriving from the endpoint.
func (a *remoteSourceBridgeActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID

		endpoint, err := resolveEndpoint(rctx.Context(), a.system, rctx.Self(), a.endpointHost, a.endpointPort, a.endpointName)
		if err != nil {
			rctx.Tell(a.downstream, &streamError{
				subID: a.subID,
				err:   fmt.Errorf("stream: resolve source ref %q: %w", a.endpointName, err),
			})
			rctx.Shutdown()
			return
		}

		a.endpoint = endpoint
		a.streamID = newStageID()

		// Watch the endpoint as defense-in-depth against an unexpected death
		// (panic, system shutdown, grace-period expiry on a stale ref) — if
		// the endpoint goes away before answering, Terminated surfaces the
		// problem instead of leaving us hung. Watch BEFORE Tell so a fast
		// endpoint can't reply-and-die before we register.
		rctx.Watch(a.endpoint)

		// Self().Tell so an enqueue-time failure (endpoint already in
		// stopping state) returns an error here. rctx.Tell records errors
		// only on rctx.Err which the bridge would never observe.
		if err := rctx.Self().Tell(rctx.Context(), a.endpoint, &streamSubscribeWire{StreamID: a.streamID}); err != nil {
			a.completed = true
			rctx.Tell(a.downstream, &streamError{
				subID: a.subID,
				err:   fmt.Errorf("stream: source ref subscribe %q: %w", a.endpointName, err),
			})
			rctx.Shutdown()
			return
		}

	case *streamRequest:
		if a.endpoint != nil {
			rctx.Tell(a.endpoint, &streamRequestWire{StreamID: a.streamID, N: msg.n})
		}

	case *streamCompleteWire:
		if msg.StreamID != a.streamID {
			return
		}
		a.completed = true
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	case *streamErrorWire:
		if msg.StreamID != a.streamID {
			return
		}
		a.completed = true
		rctx.Tell(a.downstream, &streamError{
			subID: a.subID,
			err:   errors.New(msg.Err),
		})
		rctx.Shutdown()

	case *streamCancel:
		a.completed = true
		if a.endpoint != nil {
			rctx.Tell(a.endpoint, &streamCancelWire{StreamID: a.streamID})
		}
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	case *actor.Terminated:
		if a.completed {
			// Normal completion already handled — endpoint dying naturally
			// (e.g., cancelled or grace-period elapsed) is expected.
			return
		}
		a.completed = true
		rctx.Tell(a.downstream, &streamError{
			subID: a.subID,
			err:   fmt.Errorf("stream: source ref %q endpoint terminated before completion", a.endpointName),
		})
		rctx.Shutdown()

	default:
		// Anything that isn't a known control wire is a user element pushed
		// from the endpoint. fromWire handles both T-direct (primitive types
		// deserialized by CBOR as values) and *T (struct pointers); drop
		// everything else to keep the pipeline type-safe against cross-talk.
		v, ok := fromWire[T](rctx.Message())
		if !ok {
			rctx.Unhandled()
			return
		}
		if a.downstream == nil {
			return
		}
		a.seqNo++
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: v,
			seqNo: a.seqNo,
		})
	}
}

func (a *remoteSourceBridgeActor[T]) PostStop(_ *actor.Context) error { return nil }
