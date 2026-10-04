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
	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/types"
)

const (
	// upstreamSlot is the input slot of the pipeline that feeds the splitter.
	// The substream pipelines take the slots from 1 up.
	upstreamSlot = 0
	// mergedWindow is the number of substream elements the splitter's merged
	// buffer holds before it stops delivering upstream elements, so a slow
	// downstream holds the upstream back instead of growing the buffer.
	mergedWindow = defaultInitialDemand
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

// substreamState holds per-key bookkeeping inside the splitter.
type substreamState struct {
	head *actor.PID
	// slot is the input slot of the substream's sink at the splitter. It
	// identifies the substream in the sink's mergeSubDone and mergeSubErr.
	slot     int
	inFlight int64 // pushed via subPush but not yet acked via subFeedAck
}

// routedElement is an upstream element whose substream is decided. It waits
// in the splitter while it cannot be delivered.
type routedElement[K comparable] struct {
	key   K
	value any
	// last marks a SplitAfter element whose predicate held: its substream
	// is closed once the element has been delivered.
	last bool
}

// subFlowSourceActor backs MergeSubstreams. It is the source of the new
// pipeline produced by collapsing a SubFlow, and is responsible for:
//
//  1. Spawning the upstream pipeline (everything before GroupBy) terminated
//     by a mergeSinkActor on upstreamSlot.
//  2. Routing each upstream element to the per-substream sub-pipeline keyed
//     by keyFn(elem). New keys spawn a fresh sub-pipeline (subject to the
//     maxSubs cap); known keys reuse the existing feedSourceActor.
//  3. Holding the upstream back. An upstream element is acknowledged to the
//     upstream sink only once delivered, and it waits while the merged
//     buffer holds mergedWindow elements (the downstream is slow), or while
//     its substream is at its in-flight cap under BackpressureSource (the
//     substream is slow). Under the other OverflowStrategy values an element
//     for a substream at its cap is dropped (DropTail, DropHead) or fails the
//     stream (FailSource).
//  4. Collecting the output of every substream, each ending in a
//     mergeSinkActor on a slot of its own, and forwarding it to its own
//     downstream as demand permits. An element is acknowledged to its sink
//     when it leaves the merged buffer, so a substream runs at most one
//     window ahead of the downstream.
//  5. Dispatching substream-level errors per the SubstreamErrorStrategy:
//     FailAll terminates the whole stream, Drop blocklists the key, and
//     Restart simply forgets the failed pipeline so the next element with
//     the same key spawns a fresh substream.
type subFlowSourceActor[K comparable] struct {
	upstreamStages []*stage
	subStages      []*stage
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

	// buf is the merged buffer: the *mergeSubValue elements of every
	// substream that wait for downstream demand.
	buf    queue
	demand int64

	children  map[K]*substreamState
	blocklist map[K]types.Unit
	// slotKeys maps the input slot of each open substream to its key.
	slotKeys map[int]K
	// freeSlots holds the input slots of ended substreams, for reuse.
	freeSlots []int
	// nextSlot is the next never-used input slot.
	nextSlot int

	// pending holds, in upstream order, the routedElement values that wait
	// to be delivered. It holds at most one upstream window: an upstream
	// element is acknowledged only once delivered.
	pending queue

	// subs tracks the upstream pipeline and every substream pipeline the
	// splitter materialized; aborted in PostStop.
	subs inputPipelines

	// splitCounter is the synthetic substream key for SplitWhen / SplitAfter
	// modes; incremented each time the splitter rotates to a new substream.
	// splitHasElements tracks whether the current SplitWhen substream has
	// already been assigned at least one element — needed because the very
	// first element must land in substream 0 regardless of the predicate's
	// value.
	splitCounter     int
	splitHasElements bool
	// feeding is the key of the SplitWhen / SplitAfter substream that the
	// last delivered element went to; feedingOpen tells whether that
	// substream has not been told yet that no more elements will arrive.
	feeding     K
	feedingOpen bool

	upstreamDone bool
	// feedsClosed is set once the substreams have been told that no more
	// elements will arrive: the upstream ended and no element waits.
	feedsClosed bool
	failed      bool
	completed   bool

	config  StageConfig
	metrics *stageMetrics
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
func makeFeedSourceDesc(splitter *actor.PID, key any, ackThreshold int64) *stage {
	return &stage{
		id:   newStageID(),
		kind: sourceKind,
		actorFn: func(cfg StageConfig) actor.Actor {
			return newFeedSourceActor(splitter, key, ackThreshold, cfg)
		},
		config: defaultStageConfig(),
	}
}

func newSubFlowSourceActor[K comparable](
	upstream []*stage,
	sub []*stage,
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
		blocklist:      make(map[K]types.Unit),
		slotKeys:       make(map[int]K),
		nextSlot:       upstreamSlot + 1,
		config:         config,
		metrics:        metrics,
	}
}

// PreStart does nothing: the splitter starts on its stageWire.
func (x *subFlowSourceActor[K]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, mergeSubValue, mergeSubDone,
// mergeSubErr, subFeedAck, and streamCancel. The mergeSub messages of
// upstreamSlot come from the upstream pipeline, the others from a substream.
func (x *subFlowSourceActor[K]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		x.downstream = msg.downstream
		x.subID = msg.subID
		x.spawnUpstream(rctx)

	case *streamRequest:
		x.demand += msg.n
		x.tryFlush(rctx)

	case *mergeSubValue:
		x.subs.arrived(msg)

		if msg.slot == upstreamSlot {
			x.metrics.elementsIn.Add(1)
			x.routeElement(rctx, msg.value)
			return
		}

		x.buf.push(msg)
		x.tryFlush(rctx)

	case *mergeSubDone:
		if msg.slot == upstreamSlot {
			x.upstreamDone = true
			x.closeFeeds(rctx)
			x.maybeComplete(rctx)
			return
		}

		x.handleDone(rctx, msg.slot)

	case *mergeSubErr:
		if msg.slot == upstreamSlot {
			x.fail(rctx, msg.err)
			return
		}

		x.handleErr(rctx, msg.slot, msg.err)

	case *subFeedAck:
		x.handleAck(rctx.Sender(), msg.key, msg.n)
		x.drainPending(rctx)

	case *streamCancel:
		// Tear down: ask every substream to stop, then complete downstream.
		for _, state := range x.children {
			rctx.Tell(state.head, &streamCancel{})
		}

		rctx.Tell(x.downstream, &streamComplete{subID: x.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// PostStop aborts the upstream pipeline and the substream pipelines so they
// do not outlive the splitter.
func (x *subFlowSourceActor[K]) PostStop(_ *actor.Context) error {
	x.subs.abort()
	return nil
}

// spawnUpstream materializes the pipeline that feeds the splitter, ending in
// a mergeSinkActor on upstreamSlot. A failure to materialize it fails the
// stream.
func (x *subFlowSourceActor[K]) spawnUpstream(rctx *actor.ReceiveContext) {
	all := make([]*stage, len(x.upstreamStages)+1)
	copy(all, x.upstreamStages)
	all[len(x.upstreamStages)] = makeMergeSinkDesc(rctx.Self(), upstreamSlot)
	if err := x.subs.spawn(rctx.Context(), x.system, all); err != nil {
		x.fail(rctx, err)
	}
}

// routeElement decides the substream of an upstream element and delivers it.
// The element waits in pending when it cannot be delivered yet, or when
// earlier elements wait already, so upstream order is kept.
func (x *subFlowSourceActor[K]) routeElement(rctx *actor.ReceiveContext, value any) {
	if x.failed {
		return
	}

	element := x.assign(value)
	if !x.pending.empty() || !x.deliver(rctx, element) {
		x.pending.push(element)
	}
}

// assign returns value with the key of its substream, on arrival, so the key
// function and the predicate run once per element. GroupBy takes the key
// from keyFn. SplitWhen and SplitAfter use a counter: SplitWhen advances it
// before a predicate-true element, so that element starts the new substream,
// and SplitAfter after one, so that element ends its substream. The very
// first element of the source lands in substream 0 regardless of the
// predicate's value.
func (x *subFlowSourceActor[K]) assign(value any) routedElement[K] {
	switch x.mode {
	case splitModeGroupBy:
		return routedElement[K]{key: x.keyFn(value), value: value}
	case splitModeWhen:
		if x.splitHasElements && x.splitPred(value) {
			x.splitCounter++
		}

		x.splitHasElements = true
		return routedElement[K]{key: x.counterKey(), value: value}
	default: // splitModeAfter
		element := routedElement[K]{key: x.counterKey(), value: value}
		if x.splitPred(value) {
			element.last = true
			x.splitCounter++
		}

		return element
	}
}

// counterKey returns the split counter as a substream key. SplitWhen and
// SplitAfter only ever instantiate K=int (see their constructors), so the
// assertion is safe by construction.
func (x *subFlowSourceActor[K]) counterKey() K {
	key, _ := any(x.splitCounter).(K)
	return key
}

// deliver hands element to its substream and acknowledges it to the upstream
// sink, which may then request another. It reports false, and changes
// nothing, when the element must wait: the merged buffer holds mergedWindow
// elements, or the substream is at its in-flight cap and either the strategy
// is BackpressureSource or the downstream asks for nothing.
//
// The second case keeps a slow downstream from reaching the other
// strategies. One substream cannot fill the merged buffer on its own: up to
// inputAckBatch-1 of its acknowledgements wait to be batched, so it can stop
// with fewer than mergedWindow elements there. The strategy applies only
// while the downstream waits for elements, when the substream itself is the
// slow part.
func (x *subFlowSourceActor[K]) deliver(rctx *actor.ReceiveContext, element routedElement[K]) bool {
	if x.buf.len() >= mergedWindow {
		return false
	}

	state, open := x.children[element.key]
	if open && state.inFlight >= x.perKeyBuffer && (x.overflow == BackpressureSource || x.demand == 0) {
		return false
	}

	x.subs.release(rctx, upstreamSlot, 1)

	// A split element for another substream than the one being fed closes
	// that one: SplitWhen rotated before this element.
	if x.mode != splitModeGroupBy {
		if x.feedingOpen && x.feeding != element.key {
			x.closeFeeding(rctx)
		}

		x.feeding, x.feedingOpen = element.key, true
	}

	x.push(rctx, element.key, element.value)

	if element.last {
		x.closeFeeding(rctx)
	}

	return true
}

// push sends value to the substream of key, spawning the substream for a new
// key. An element of a blocklisted key is dropped. An element for a
// substream at its in-flight cap fails the stream under FailSource and is
// dropped otherwise; under BackpressureSource deliver holds it back before
// it gets here.
func (x *subFlowSourceActor[K]) push(rctx *actor.ReceiveContext, key K, value any) {
	if _, blocked := x.blocklist[key]; blocked {
		// SubstreamDrop: silently discard further elements for this key.
		x.metrics.droppedElements.Add(1)
		if x.config.OnDrop != nil {
			x.config.OnDrop(value, "substream-drop: key blocklisted after error")
		}

		return
	}

	state, exists := x.children[key]
	if !exists {
		if x.maxSubs > 0 && len(x.children) >= x.maxSubs {
			x.fail(rctx, ErrTooManySubstreams)
			return
		}

		state = x.spawnSubstream(rctx, key)
		if state == nil {
			return
		}
	}

	if state.inFlight >= x.perKeyBuffer {
		if x.overflow == FailSource {
			x.fail(rctx, ErrSubstreamOverflow)
			return
		}

		// DropTail / DropHead: drop the new element. DropHead would require
		// a queue per key; it is treated as drop-newest.
		x.metrics.droppedElements.Add(1)
		if x.config.OnDrop != nil {
			x.config.OnDrop(value, "substream-overflow: per-key in-flight cap reached")
		}

		return
	}

	state.inFlight++
	rctx.Tell(state.head, &subPush{value: value})
}

// closeFeeding tells the SplitWhen / SplitAfter substream being fed that no
// more elements will arrive. Its pipeline drains and completes as usual.
func (x *subFlowSourceActor[K]) closeFeeding(rctx *actor.ReceiveContext) {
	if !x.feedingOpen {
		return
	}

	x.feedingOpen = false

	if state, open := x.children[x.feeding]; open {
		rctx.Tell(state.head, &subFeedDone{})
	}
}

// closeFeeds tells the open substreams that no more elements will arrive,
// once the upstream has ended and no element waits. It runs once.
func (x *subFlowSourceActor[K]) closeFeeds(rctx *actor.ReceiveContext) {
	if x.feedsClosed || x.failed || !x.upstreamDone || !x.pending.empty() {
		return
	}

	x.feedsClosed = true

	// The earlier split substreams were closed as the next one started.
	if x.mode != splitModeGroupBy {
		x.closeFeeding(rctx)
		return
	}

	for _, state := range x.children {
		rctx.Tell(state.head, &subFeedDone{})
	}
}

// drainPending delivers the waiting elements in upstream order until one must
// wait again, then closes the substreams' feeds if the upstream has ended.
func (x *subFlowSourceActor[K]) drainPending(rctx *actor.ReceiveContext) {
	for !x.failed && !x.pending.empty() {
		if !x.deliver(rctx, x.pending.peek().(routedElement[K])) {
			return
		}

		x.pending.pop()
	}

	x.closeFeeds(rctx)
}

// spawnSubstream materializes a fresh per-substream pipeline,
// feedSource → subStages → mergeSink, on a free input slot and records it
// under key. It returns nil after failing the stream when the pipeline
// cannot be materialized.
func (x *subFlowSourceActor[K]) spawnSubstream(rctx *actor.ReceiveContext, key K) *substreamState {
	ackThreshold := x.perKeyBuffer / 4
	if ackThreshold < 1 {
		ackThreshold = 1
	}

	slot := x.nextSlot
	if n := len(x.freeSlots); n > 0 {
		slot = x.freeSlots[n-1]
		x.freeSlots = x.freeSlots[:n-1]
	} else {
		x.nextSlot++
	}

	stages := make([]*stage, 0, len(x.subStages)+2)
	stages = append(stages, makeFeedSourceDesc(rctx.Self(), key, ackThreshold))
	stages = append(stages, x.subStages...)
	stages = append(stages, makeMergeSinkDesc(rctx.Self(), slot))

	feedHead, err := x.subs.spawnWithHead(rctx.Context(), x.system, stages)
	if err != nil {
		x.fail(rctx, err)
		return nil
	}

	state := &substreamState{head: feedHead, slot: slot}
	x.children[key] = state
	x.slotKeys[slot] = key
	return state
}

// handleAck lowers the in-flight count of the substream of rawKey by the
// ackedCount elements its feed source has dispatched. An acknowledgement from
// another feed source than the substream's own is ignored: under
// SubstreamRestart the feed source of a failed substream can acknowledge
// after a new substream has taken its key.
func (x *subFlowSourceActor[K]) handleAck(sender *actor.PID, rawKey any, ackedCount int64) {
	key, ok := rawKey.(K)
	if !ok {
		return
	}

	state, exists := x.children[key]
	if !exists || !state.head.Equals(sender) {
		return
	}

	state.inFlight -= ackedCount
	if state.inFlight < 0 {
		state.inFlight = 0
	}
}

// handleDone forgets the substream that completed on slot.
func (x *subFlowSourceActor[K]) handleDone(rctx *actor.ReceiveContext, slot int) {
	x.forget(slot)
	x.maybeComplete(rctx)
}

// handleErr applies the SubstreamErrorStrategy to the substream that failed
// on slot. Under SubstreamDrop and SubstreamRestart a waiting element of its
// key can then be delivered: it is dropped, or starts a new substream.
func (x *subFlowSourceActor[K]) handleErr(rctx *actor.ReceiveContext, slot int, err error) {
	switch x.errorStrategy {
	case SubstreamDrop:
		x.blocklist[x.forget(slot)] = types.Unit{}
	case SubstreamRestart:
		x.forget(slot)
	default: // SubstreamFailAll
		x.fail(rctx, err)
		return
	}

	x.drainPending(rctx)
	x.maybeComplete(rctx)
}

// forget removes the substream of slot from the splitter's state, frees the
// slot for reuse and returns the substream's key.
func (x *subFlowSourceActor[K]) forget(slot int) K {
	key := x.slotKeys[slot]
	delete(x.slotKeys, slot)
	x.freeSlots = append(x.freeSlots, slot)

	if state, open := x.children[key]; open && state.slot == slot {
		delete(x.children, key)
	}

	return key
}

// tryFlush forwards merged-buffer elements downstream while demand remains,
// acknowledging each to the sink that sent it, then delivers the upstream
// elements the drained buffer has room for.
func (x *subFlowSourceActor[K]) tryFlush(rctx *actor.ReceiveContext) {
	for x.demand > 0 && !x.buf.empty() {
		element := x.buf.pop().(*mergeSubValue)
		x.seqNo++
		x.metrics.elementsOut.Add(1)
		rctx.Tell(x.downstream, &streamElement{
			subID: x.subID,
			value: element.value,
			seqNo: x.seqNo,
		})
		x.demand--
		x.subs.releaseValue(rctx, element)
	}

	x.drainPending(rctx)
	x.maybeComplete(rctx)
}

// maybeComplete emits streamComplete downstream once the upstream pipeline
// has finished, no upstream element waits, every substream has reported
// done, and the merged buffer is drained.
func (x *subFlowSourceActor[K]) maybeComplete(rctx *actor.ReceiveContext) {
	if x.completed || x.failed {
		return
	}

	if !x.upstreamDone || !x.pending.empty() || len(x.slotKeys) > 0 || !x.buf.empty() {
		return
	}

	x.completed = true
	rctx.Tell(x.downstream, &streamComplete{subID: x.subID})
	rctx.Shutdown()
}

// fail ends the merged stream with err: it cancels every open substream,
// sends err downstream and stops; PostStop then aborts the remaining
// pipelines. Later calls do nothing.
func (x *subFlowSourceActor[K]) fail(rctx *actor.ReceiveContext, err error) {
	if x.failed {
		return
	}

	x.failed = true
	for _, state := range x.children {
		rctx.Tell(state.head, &streamCancel{})
	}

	rctx.Tell(x.downstream, &streamError{subID: x.subID, err: err})
	rctx.Shutdown()
}
