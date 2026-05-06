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
	"context"

	"github.com/tochemey/goakt/v4/actor"
)

// feedSourceActor is the head of a per-substream sub-pipeline. The splitter
// pushes elements into it via *subPush messages and signals exhaustion with
// *subFeedDone. Each time it dispatches an element to its own downstream the
// actor accumulates an ack and periodically reports consumption to the
// splitter via *subFeedAck so the splitter can decrement its per-key
// in-flight counter.
type feedSourceActor struct {
	splitter   *actor.PID
	key        any
	downstream *actor.PID
	subID      string
	seqNo      uint64
	buf        queue
	demand     int64
	feedDone   bool
	// unackedDispatches is the number of elements dispatched downstream
	// since the last subFeedAck was sent to the splitter. Acks are batched
	// at ackThreshold to avoid one-message-per-element overhead.
	unackedDispatches int64
	ackThreshold      int64
	config            StageConfig
}

func newFeedSourceActor(splitter *actor.PID, key any, ackThreshold int64, config StageConfig) *feedSourceActor {
	if ackThreshold < 1 {
		ackThreshold = 1
	}

	return &feedSourceActor{
		splitter:     splitter,
		key:          key,
		ackThreshold: ackThreshold,
		config:       config,
	}
}

func (a *feedSourceActor) PreStart(_ *actor.Context) error { return nil }

func (a *feedSourceActor) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID

	case *streamRequest:
		a.demand += msg.n
		a.tryFlush(rctx)

	case *subPush:
		a.buf.push(msg.value)
		a.tryFlush(rctx)

	case *subFeedDone:
		a.feedDone = true
		a.tryFlush(rctx)

	case *streamCancel:
		// Drop any unflushed acks — splitter is tearing down anyway.
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// tryFlush forwards buffered elements while demand remains; emits batched
// acks back to the splitter; completes the substream once the feed is done
// and the buffer has drained.
func (a *feedSourceActor) tryFlush(rctx *actor.ReceiveContext) {
	for a.demand > 0 && !a.buf.empty() {
		a.seqNo++
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: a.buf.pop(),
			seqNo: a.seqNo,
		})
		a.demand--
		a.unackedDispatches++

		if a.unackedDispatches >= a.ackThreshold {
			a.flushAck(rctx)
		}
	}

	if a.feedDone && a.buf.empty() {
		// Emit any remaining acks before tearing down so the splitter's
		// per-key in-flight counter returns to zero.
		a.flushAck(rctx)
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()
	}
}

func (a *feedSourceActor) flushAck(rctx *actor.ReceiveContext) {
	if a.unackedDispatches == 0 || a.splitter == nil {
		return
	}

	rctx.Tell(a.splitter, &subFeedAck{key: a.key, n: a.unackedDispatches})
	a.unackedDispatches = 0
}

func (a *feedSourceActor) PostStop(_ *actor.Context) error { return nil }

// makeFeedSourceDesc returns a stageDesc whose actor is a feedSourceActor.
// The descriptor is a regular sourceKind stage so it slots into materialize().
func makeFeedSourceDesc(splitter *actor.PID, key any, ackThreshold int64) *stageDesc {
	return &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newFeedSourceActor(splitter, key, ackThreshold, cfg)
		},
		config: defaultStageConfig(),
	}
}

// makeUpstreamFeederSinkDesc returns a sink that forwards each element of the
// original source pipeline to the splitter as *subUpstreamElem and reports
// completion with *subUpstreamDone. It is appended to the upstream pipeline
// when the splitter materializes itself.
func makeUpstreamFeederSinkDesc(splitter *actor.PID) *stageDesc {
	config := defaultStageConfig()
	return &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSinkActor(func(v any) error {
				return actor.Tell(context.Background(), splitter, &subUpstreamElem{value: v})
			}, func() {
				_ = actor.Tell(context.Background(), splitter, &subUpstreamDone{})
			}, cfg)
		},
		config: config,
	}
}

// subMergeSinkActor is the terminal sink of a per-substream pipeline.
// Elements are forwarded to the splitter as *subOut, normal completion as
// *subDone, and pipeline failures as *subErr. Replacing the standard
// newSinkActor lets the splitter distinguish "substream finished" from
// "substream errored" — which the SubstreamErrorStrategy needs to act on.
type subMergeSinkActor struct {
	splitter *actor.PID
	key      any
	upstream *actor.PID
	subID    string
	credit   int64
}

func newSubMergeSinkActor(splitter *actor.PID, key any) *subMergeSinkActor {
	return &subMergeSinkActor{splitter: splitter, key: key}
}

func (a *subMergeSinkActor) PreStart(_ *actor.Context) error { return nil }

func (a *subMergeSinkActor) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.subID = msg.subID
		a.upstream = msg.upstream
		a.credit = defaultInitialDemand
		if a.upstream != nil {
			rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: a.credit})
		}

	case *streamElement:
		rctx.Tell(a.splitter, &subOut{value: msg.value})
		a.credit--

		if a.credit <= defaultRefillThreshold && a.upstream != nil {
			refill := int64(defaultInitialDemand) - a.credit
			a.credit += refill
			rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: refill})
		}

	case *streamComplete:
		rctx.Tell(a.splitter, &subDone{key: a.key})
		rctx.Shutdown()

	case *streamError:
		rctx.Tell(a.splitter, &subErr{key: a.key, err: msg.err})
		rctx.Shutdown()

	case *streamCancel:
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

func (a *subMergeSinkActor) PostStop(_ *actor.Context) error { return nil }

// makeSubMergeSinkDesc returns the terminal sink of a per-substream pipeline.
func makeSubMergeSinkDesc(splitter *actor.PID, key any) *stageDesc {
	config := defaultStageConfig()
	return &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSubMergeSinkActor(splitter, key)
		},
		config: config,
	}
}

// substreamState holds per-key bookkeeping inside the splitter.
type substreamState struct {
	head     *actor.PID
	inFlight int64 // pushed via subPush but not yet acked via subFeedAck
}

// subFlowSourceActor backs MergeSubstreams. It is the source of the new
// pipeline produced by collapsing a SubFlow, and is responsible for:
//
//  1. Spawning the upstream pipeline (everything before GroupBy) terminated
//     by an upstream feeder sink that forwards elements back as *subUpstreamElem.
//  2. Routing each upstream element to the per-substream sub-pipeline keyed
//     by keyFn(elem). New keys spawn a fresh sub-pipeline (subject to the
//     maxSubs cap); known keys reuse the existing feedSourceActor.
//  3. Enforcing per-key backpressure via an in-flight counter and the
//     OverflowStrategy (DropTail by default; FailSource fails the stream).
//  4. Collecting *subOut messages from every per-substream merge sink and
//     forwarding them to its own downstream as demand permits.
//  5. Dispatching substream-level errors per the SubstreamErrorStrategy:
//     FailAll terminates the whole stream, Drop blocklists the key, and
//     Restart simply forgets the failed pipeline so the next element with
//     the same key spawns a fresh substream.
type subFlowSourceActor[K comparable] struct {
	upstreamStages []*stageDesc
	subStages      []*stageDesc
	mode           splitMode
	keyFn          func(any) K    // GroupBy only
	splitPred      func(any) bool // SplitWhen / SplitAfter only
	maxSubs        int
	system         actor.ActorSystem

	perKeyBuffer  int64
	overflow      OverflowStrategy
	errorStrategy SubstreamErrorStrategy

	downstream *actor.PID
	subID      string
	seqNo      uint64

	buf    queue
	demand int64

	children       map[K]*substreamState
	activeChildren int
	blocklist      map[K]struct{}

	// splitCounter is the synthetic substream key for SplitWhen / SplitAfter
	// modes; incremented each time the splitter rotates to a new substream.
	// splitHasElements tracks whether the current SplitWhen substream has
	// already received at least one element — needed because the very first
	// element must land in substream 0 regardless of the predicate's value.
	splitCounter     int
	splitHasElements bool

	upstreamDone bool
	failed       bool
	completed    bool

	config  StageConfig
	metrics *stageMetrics
}

func newSubFlowSourceActor[K comparable](
	upstream []*stageDesc,
	sub []*stageDesc,
	mode splitMode,
	keyFn func(any) K,
	splitPred func(any) bool,
	maxSubs int,
	perKeyBuffer int,
	overflow OverflowStrategy,
	errorStrategy SubstreamErrorStrategy,
	config StageConfig,
) *subFlowSourceActor[K] {
	metrics := config.Metrics
	if metrics == nil {
		metrics = &stageMetrics{}
	}

	if perKeyBuffer < 1 {
		perKeyBuffer = defaultBufferSize
	}

	return &subFlowSourceActor[K]{
		upstreamStages: upstream,
		subStages:      sub,
		mode:           mode,
		keyFn:          keyFn,
		splitPred:      splitPred,
		maxSubs:        maxSubs,
		system:         config.System,
		perKeyBuffer:   int64(perKeyBuffer),
		overflow:       overflow,
		errorStrategy:  errorStrategy,
		children:       make(map[K]*substreamState),
		blocklist:      make(map[K]struct{}),
		config:         config,
		metrics:        metrics,
	}
}

func (a *subFlowSourceActor[K]) PreStart(_ *actor.Context) error { return nil }

func (a *subFlowSourceActor[K]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		a.spawnUpstream(rctx)

	case *streamRequest:
		a.demand += msg.n
		a.tryFlush(rctx)

	case *subUpstreamElem:
		a.metrics.elementsIn.Add(1)
		a.routeElement(rctx, msg.value)

	case *subUpstreamDone:
		a.upstreamDone = true
		// Tell every active substream that no more elements will arrive.
		for _, state := range a.children {
			rctx.Tell(state.head, &subFeedDone{})
		}

		a.maybeComplete(rctx)

	case *subOut:
		a.buf.push(msg.value)
		a.tryFlush(rctx)

	case *subDone:
		a.handleDone(rctx, msg.key)

	case *subErr:
		a.handleErr(rctx, msg.key, msg.err)

	case *subFeedAck:
		a.handleAck(msg.key, msg.n)

	case *streamCancel:
		// Tear down: ask every substream to stop, then complete downstream.
		for _, state := range a.children {
			rctx.Tell(state.head, &streamCancel{})
		}

		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

func (a *subFlowSourceActor[K]) PostStop(_ *actor.Context) error { return nil }

func (a *subFlowSourceActor[K]) spawnUpstream(rctx *actor.ReceiveContext) {
	feeder := makeUpstreamFeederSinkDesc(rctx.Self())
	all := make([]*stageDesc, len(a.upstreamStages)+1)
	copy(all, a.upstreamStages)
	all[len(a.upstreamStages)] = feeder
	spawnSubPipeline(rctx.Context(), a.system, all)
}

// routeElement applies the per-substream in-flight cap before pushing. When
// a substream is at capacity the configured OverflowStrategy decides whether
// to drop the element or terminate the stream. The routing key comes from
// keyFn (GroupBy mode) or from a monotonic counter rotated on a predicate-true
// element (SplitWhen / SplitAfter modes).
func (a *subFlowSourceActor[K]) routeElement(rctx *actor.ReceiveContext, value any) {
	if a.failed {
		return
	}

	// SplitWhen rotates BEFORE the push so a predicate-true element starts
	// the new substream. The very first element of the source must land in
	// substream 0 regardless of the predicate's value, so we require that
	// the current substream has already received at least one element.
	if a.mode == splitModeWhen && a.splitHasElements && a.splitPred(value) {
		a.rotateSplitSubstream(rctx)
	}

	key := a.deriveKey(value)

	if _, blocked := a.blocklist[key]; blocked {
		// SubstreamDrop: silently discard further elements for this key.
		a.metrics.droppedElements.Add(1)
		if a.config.OnDrop != nil {
			a.config.OnDrop(value, "substream-drop: key blocklisted after error")
		}

		return
	}

	state, exists := a.children[key]
	if !exists {
		if a.maxSubs > 0 && len(a.children) >= a.maxSubs {
			a.fail(rctx, ErrTooManySubstreams)
			return
		}

		feedHead := a.spawnSubstream(rctx, key)
		if feedHead == nil {
			return
		}

		state = &substreamState{head: feedHead}
		a.children[key] = state
		a.activeChildren++
	}

	if state.inFlight >= a.perKeyBuffer {
		switch a.overflow {
		case FailSource:
			a.fail(rctx, ErrSubstreamOverflow)
			return
		default:
			// DropTail / DropHead / BackpressureSource: drop the new element.
			// DropHead would require a pending queue inside the splitter; for
			// v2 we treat all non-FailSource strategies as drop-newest.
			a.metrics.droppedElements.Add(1)
			if a.config.OnDrop != nil {
				a.config.OnDrop(value, "substream-overflow: per-key in-flight cap reached")
			}

			return
		}
	}

	state.inFlight++
	rctx.Tell(state.head, &subPush{value: value})

	// Track that the current SplitWhen substream is no longer empty so the
	// next predicate-true element can trigger a rotation.
	if a.mode == splitModeWhen {
		a.splitHasElements = true
	}

	// SplitAfter rotates AFTER the push so the predicate-true element is the
	// LAST element of the substream it terminates.
	if a.mode == splitModeAfter && a.splitPred(value) {
		a.rotateSplitSubstream(rctx)
	}
}

// deriveKey returns the routing key for value under the active split mode.
// In SplitWhen / SplitAfter modes the key is the synthetic counter cast to K
// — these modes only ever instantiate with K=int (see SplitWhen / SplitAfter
// constructors), so the assertion is safe by construction.
func (a *subFlowSourceActor[K]) deriveKey(value any) K {
	if a.mode == splitModeGroupBy {
		return a.keyFn(value)
	}

	key, _ := any(a.splitCounter).(K)
	return key
}

// rotateSplitSubstream closes the current SplitWhen / SplitAfter substream
// and advances the counter so subsequent elements land in a fresh substream.
// Closing means sending subFeedDone — the substream's pipeline drains and
// reports completion via subDone in the normal way.
func (a *subFlowSourceActor[K]) rotateSplitSubstream(rctx *actor.ReceiveContext) {
	currentKey, _ := any(a.splitCounter).(K)
	if state, exists := a.children[currentKey]; exists {
		rctx.Tell(state.head, &subFeedDone{})
	}

	a.splitCounter++
	a.splitHasElements = false
}

// spawnSubstream materializes a fresh per-substream pipeline:
// feedSource → subStages → subMergeSink.
func (a *subFlowSourceActor[K]) spawnSubstream(rctx *actor.ReceiveContext, key K) *actor.PID {
	ackThreshold := a.perKeyBuffer / 4
	if ackThreshold < 1 {
		ackThreshold = 1
	}

	stages := make([]*stageDesc, 0, len(a.subStages)+2)
	stages = append(stages, makeFeedSourceDesc(rctx.Self(), key, ackThreshold))
	stages = append(stages, a.subStages...)
	stages = append(stages, makeSubMergeSinkDesc(rctx.Self(), key))

	_, feedHead, err := materializeWithHead(rctx.Context(), a.system, stages)
	if err != nil {
		a.fail(rctx, err)
		return nil
	}

	return feedHead
}

func (a *subFlowSourceActor[K]) handleAck(rawKey any, ackedCount int64) {
	key, ok := rawKey.(K)
	if !ok {
		return
	}

	state, exists := a.children[key]
	if !exists {
		return
	}

	state.inFlight -= ackedCount
	if state.inFlight < 0 {
		state.inFlight = 0
	}
}

func (a *subFlowSourceActor[K]) handleDone(rctx *actor.ReceiveContext, rawKey any) {
	if key, ok := rawKey.(K); ok {
		delete(a.children, key)
	}

	a.activeChildren--
	a.maybeComplete(rctx)
}

func (a *subFlowSourceActor[K]) handleErr(rctx *actor.ReceiveContext, rawKey any, err error) {
	key, ok := rawKey.(K)
	if !ok {
		// Should not happen — sink captures the key at construction. Fall back
		// to FailAll so the error is at least surfaced.
		a.fail(rctx, err)
		return
	}

	switch a.errorStrategy {
	case SubstreamDrop:
		a.blocklist[key] = struct{}{}
		delete(a.children, key)
		a.activeChildren--
		a.maybeComplete(rctx)
	case SubstreamRestart:
		delete(a.children, key)
		a.activeChildren--
		a.maybeComplete(rctx)
	default: // SubstreamFailAll
		a.fail(rctx, err)
	}
}

// tryFlush forwards merged-buffer elements downstream while demand remains.
func (a *subFlowSourceActor[K]) tryFlush(rctx *actor.ReceiveContext) {
	for a.demand > 0 && !a.buf.empty() {
		a.seqNo++
		a.metrics.elementsOut.Add(1)
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: a.buf.pop(),
			seqNo: a.seqNo,
		})
		a.demand--
	}

	a.maybeComplete(rctx)
}

// maybeComplete emits streamComplete downstream once the upstream pipeline
// has finished, every substream has reported done, and the merged buffer is
// drained.
func (a *subFlowSourceActor[K]) maybeComplete(rctx *actor.ReceiveContext) {
	if a.completed || a.failed {
		return
	}

	if !a.upstreamDone || a.activeChildren > 0 || !a.buf.empty() {
		return
	}

	a.completed = true
	rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
	rctx.Shutdown()
}

func (a *subFlowSourceActor[K]) fail(rctx *actor.ReceiveContext, err error) {
	if a.failed {
		return
	}

	a.failed = true
	for _, state := range a.children {
		rctx.Tell(state.head, &streamCancel{})
	}

	rctx.Tell(a.downstream, &streamError{subID: a.subID, err: err})
	rctx.Shutdown()
}
