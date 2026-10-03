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
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
)

// inputAckBatch is the number of elements of one input that leave a fan-in
// buffer before the fan-in actor acknowledges them to that input's sink in a
// single mergeSubAck. Batching replaces one acknowledgement per element. The
// value makes the sink refill like any other sink: one request of 160 once
// its outstanding credit has fallen to RefillThreshold. Withheld
// acknowledgements only shrink the window, so the bound of one window per
// input holds for any batch size below InitialDemand.
const inputAckBatch = defaultInitialDemand - defaultRefillThreshold

// inputPrunesFrom is the smallest number of tracked sub-pipeline handles at
// which inputPipelines looks for ended ones to forget.
const inputPrunesFrom = 16

// chanBridgeBatchSize is the largest number of channel values the reader
// goroutine of a chanSourceActor hands to the actor in one chanBatch.
const chanBridgeBatchSize = 64

// inputPipelines tracks the sub-pipelines a composite stage has materialized:
// the inputs of a fan-in source, the nested streams of a FlatMap, the upstream
// and substreams of a splitter. Each runs under its own top-level coordinator,
// outside the stage's own actor subtree, so stopping the stage does not stop
// them. The stage keeps their handles here and aborts them when it stops.
type inputPipelines struct {
	// mu guards handles, aborted and pruneAt: spawn runs on the stage's
	// receive loop while abort runs from PostStop, which the runtime may call
	// from another goroutine, also while a spawn is in progress.
	mu sync.Mutex
	// handles holds the handles of the sub-pipelines that may still be running.
	handles []StreamHandle
	// aborted is set by abort. A sub-pipeline is never started, and never
	// kept, once it is set.
	aborted bool
	// pruneAt is the length of handles at which the ended sub-pipelines are
	// next removed from it.
	pruneAt int
	// sinks holds, per input slot, the internal sink of that input pipeline,
	// learned from the first element it forwards. Receive loop only.
	sinks []*actor.PID
	// unacked counts, per input slot, the elements that have left the fan-in
	// buffer and are not yet acknowledged to the sink. Receive loop only.
	unacked []int64
}

// mergeSinkActor is the internal sink at the end of every input pipeline of a
// fan-in source. It forwards each element to the fan-in actor as mergeSubValue
// and the end of the input as mergeSubDone or mergeSubErr.
//
// It does not refill its upstream demand on its own. It requests one window
// when it is wired and then only what the fan-in actor acknowledges with
// mergeSubAck, that is, what has left the fan-in buffer. The elements of one
// input held by the fan-in actor therefore never exceed one window, however
// little the fan-in's own downstream asks for.
type mergeSinkActor struct {
	target   *actor.PID // the fan-in actor
	slot     int        // index of this input at the fan-in actor
	upstream *actor.PID
	subID    string
	config   StageConfig
}

// pullSourceActor backs Of, Range, Unfold, and any synchronous pull-based source.
// On each streamRequest it calls pullFn to obtain a batch of elements and
// forwards them downstream. When pullFn signals no more elements the actor
// sends streamComplete and shuts down.
type pullSourceActor struct {
	pullFn     func(n int64) ([]any, bool)
	downstream *actor.PID
	subID      string
	seqNo      uint64
	metrics    *stageMetrics
	config     StageConfig
}

// newPullSourceActor creates a pullSourceActor backed by pullFn.
func newPullSourceActor(pullFn func(n int64) ([]any, bool), config StageConfig) *pullSourceActor {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &pullSourceActor{pullFn: pullFn, metrics: m, config: config}
}

func (a *pullSourceActor) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, and streamCancel.
func (a *pullSourceActor) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
	case *streamRequest:
		a.produce(rctx, msg.n)
	case *streamCancel:
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()
	default:
		rctx.Unhandled()
	}
}

// produce calls pullFn, forwards elements downstream, and completes when exhausted.
func (a *pullSourceActor) produce(rctx *actor.ReceiveContext, n int64) {
	elems, hasMore := a.pullFn(n)
	for _, v := range elems {
		a.seqNo++
		a.metrics.elementsIn.Add(1)
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: v,
			seqNo: a.seqNo,
		})
	}
	if !hasMore {
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()
	}
}

func (a *pullSourceActor) PostStop(_ *actor.Context) error { return nil }

// chanSourceActor backs FromChannel.
// A goroutine bridges the external channel into the actor mailbox via chanBatch
// and chanDone messages so the receive loop is never blocked on a channel read.
//
// The goroutine takes a value off the channel only against downstream demand:
// the actor adds every streamRequest to readCredit and the goroutine spends
// one unit per value it reads. With no demand the values stay in the channel
// and the producer blocks, which is the backpressure FromChannel promises. The
// goroutine exits when the channel closes or when the actor stops.
type chanSourceActor[T any] struct {
	ch          <-chan T
	downstream  *actor.PID
	subID       string
	seqNo       uint64
	buf         queue
	demand      int64
	channelDone bool
	config      StageConfig
	metrics     *stageMetrics

	readCredit atomic.Int64  // downstream demand the reader goroutine has not yet spent on channel reads
	wake       chan struct{} // capacity 1; signals the reader goroutine that readCredit was raised
	stop       chan struct{} // closed by PostStop to end the reader goroutine
	stopOnce   sync.Once     // guards stop against a double close
}

// newChanSourceActor creates a chanSourceActor that reads from ch.
func newChanSourceActor[T any](ch <-chan T, config StageConfig) *chanSourceActor[T] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &chanSourceActor[T]{
		ch:      ch,
		metrics: m,
		config:  config,
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
}

func (a *chanSourceActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, chanBatch, chanDone, and streamCancel.
func (a *chanSourceActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		go a.readLoop(rctx.Self())

	case *streamRequest:
		a.demand += msg.n
		a.readCredit.Add(msg.n)
		select {
		case a.wake <- struct{}{}:
		default:
			// A wake-up is already pending; the reader will see the new credit.
		}

		a.tryFlush(rctx)

	case *chanBatch:
		for _, v := range msg.values {
			a.buf.push(v)
			a.metrics.elementsIn.Add(1)
		}
		a.tryFlush(rctx)

	case *chanDone:
		a.channelDone = true
		a.tryFlush(rctx)

	case *streamCancel:
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// tryFlush forwards buffered elements to downstream while demand remains,
// then completes once the channel is closed and the buffer is empty.
func (a *chanSourceActor[T]) tryFlush(rctx *actor.ReceiveContext) {
	for a.demand > 0 && !a.buf.empty() {
		a.seqNo++
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: a.buf.pop(),
			seqNo: a.seqNo,
		})
		a.demand--
	}

	if a.channelDone && a.buf.empty() {
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()
	}
}

// PostStop ends the reader goroutine so it takes nothing more off the channel
// once the stream has terminated.
func (a *chanSourceActor[T]) PostStop(_ *actor.Context) error {
	a.stopOnce.Do(func() { close(a.stop) })
	return nil
}

// readLoop bridges the external channel into the mailbox of self. It runs on
// its own goroutine and reads one value per unit of readCredit. Each batch
// drains the values that are immediately available (up to chanBridgeBatchSize
// and the remaining credit) before sending a single chanBatch, which amortizes
// the per-element mailbox-enqueue cost on high-throughput channels while
// keeping latency low for slow channels (partial batches are flushed as soon
// as no element is immediately available). It returns when the channel is
// closed, after sending chanDone, or when the actor stops.
func (a *chanSourceActor[T]) readLoop(self *actor.PID) {
	for {
		if a.readCredit.Load() <= 0 {
			select {
			case <-a.wake:
				continue
			case <-a.stop:
				return
			}
		}

		var buf []any
		select {
		case v, ok := <-a.ch:
			if !ok {
				_ = actor.Tell(context.Background(), self, &chanDone{})
				return
			}

			buf = make([]any, 1, chanBridgeBatchSize)
			buf[0] = v
		case <-a.stop:
			return
		}

		credit := a.readCredit.Add(-1)

	drain:
		for len(buf) < chanBridgeBatchSize && credit > 0 {
			select {
			case v, ok := <-a.ch:
				if !ok {
					_ = actor.Tell(context.Background(), self, &chanBatch{values: buf})
					_ = actor.Tell(context.Background(), self, &chanDone{})
					return
				}

				buf = append(buf, v)
				credit = a.readCredit.Add(-1)
			default:
				break drain
			}
		}

		if err := actor.Tell(context.Background(), self, &chanBatch{values: buf}); err != nil {
			return
		}
	}
}

// actorSourceActor backs FromActor.
// It pulls elements from another GoAkt actor by sending PullRequest messages
// and expecting PullResponse[T] replies. Each pull is issued via rctx.PipeTo
// so the actor's receive loop is never blocked. The upstream actor is watched
// so unexpected termination is detected and propagated as a stream error.
type actorSourceActor[T any] struct {
	upstream      *actor.PID
	downstream    *actor.PID
	subID         string
	seqNo         uint64
	fetching      bool
	pendingDemand int64
	config        StageConfig
	metrics       *stageMetrics
}

// newActorSourceActor creates an actorSourceActor that pulls from pid.
func newActorSourceActor[T any](pid *actor.PID, config StageConfig) *actorSourceActor[T] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &actorSourceActor[T]{upstream: pid, metrics: m, config: config}
}

func (a *actorSourceActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, fetchResult, fetchErr, actor.Terminated,
// and streamCancel.
func (a *actorSourceActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		// Watch the upstream actor so we detect unexpected termination.
		rctx.Watch(a.upstream)

	case *streamRequest:
		a.pendingDemand += msg.n
		if !a.fetching {
			a.startFetch(rctx)
		}

	case *fetchResult:
		a.fetching = false
		if msg.done {
			rctx.UnWatch(a.upstream)
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
			rctx.Shutdown()
			return
		}

		for _, v := range msg.values {
			a.seqNo++
			a.metrics.elementsIn.Add(1)
			rctx.Tell(a.downstream, &streamElement{
				subID: a.subID,
				value: v,
				seqNo: a.seqNo,
			})
		}

		// Restore unfulfilled demand so we refetch to find end-of-stream.
		a.pendingDemand += msg.requested - int64(len(msg.values))
		if a.pendingDemand > 0 {
			a.startFetch(rctx)
		}

	case *fetchErr:
		a.fetching = false
		a.metrics.errors.Add(1)
		rctx.UnWatch(a.upstream)
		rctx.Tell(a.downstream, &streamError{subID: a.subID, err: msg.err})
		rctx.Shutdown()

	case *actor.Terminated:
		// The upstream actor stopped before we received end-of-stream.
		rctx.Tell(a.downstream, &streamError{
			subID: a.subID,
			err:   fmt.Errorf("stream: actor source %s terminated unexpectedly", msg.ActorPath().String()),
		})
		rctx.Shutdown()

	case *streamCancel:
		rctx.UnWatch(a.upstream)
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// startFetch issues an async pull via rctx.PipeTo so the receive loop stays unblocked.
// Errors are wrapped as fetchErr values (not returned) because PipeTo routes task
// errors to the dead-letter queue, not back to the actor's mailbox.
func (a *actorSourceActor[T]) startFetch(rctx *actor.ReceiveContext) {
	n := a.pendingDemand
	a.pendingDemand = 0
	a.fetching = true
	upPID := a.upstream
	timeout := a.config.PullTimeout
	rctx.PipeTo(rctx.Self(), func() (any, error) {
		resp, err := actor.Ask(context.Background(), upPID, &PullRequest{N: n}, timeout)
		if err != nil {
			return &fetchErr{err: err}, nil
		}
		pr, ok := resp.(*PullResponse[T])
		if !ok {
			return &fetchErr{err: fmt.Errorf("stream: unexpected pull response type %T", resp)}, nil
		}
		vals := make([]any, len(pr.Elements))
		for i, e := range pr.Elements {
			vals[i] = e
		}
		return &fetchResult{
			values:    vals,
			done:      len(pr.Elements) == 0,
			requested: n,
		}, nil
	})
}

func (a *actorSourceActor[T]) PostStop(_ *actor.Context) error { return nil }

// tickSourceActor backs Tick.
// It uses the GoAkt actor-system scheduler to deliver a tickTick message on
// every interval, keeping the actor mailbox as the sole synchronization point.
// The current time is captured at message receipt rather than at scheduling time.
type tickSourceActor struct {
	downstream *actor.PID
	subID      string
	seqNo      uint64
	interval   time.Duration
	demand     int64
	schedRef   string
	config     StageConfig
}

// newTickSourceActor creates a tickSourceActor that fires every interval.
func newTickSourceActor(interval time.Duration, config StageConfig) *tickSourceActor {
	return &tickSourceActor{interval: interval, config: config}
}

// PreStart captures the actor name as the schedule reference so it is safe
// to read from PostStop without a data race.
func (a *tickSourceActor) PreStart(ctx *actor.Context) error {
	a.schedRef = ctx.ActorName()
	return nil
}

// Receive handles stageWire, streamRequest, tickTick, and streamCancel.
func (a *tickSourceActor) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		_ = rctx.ActorSystem().Schedule(rctx.Context(), &tickTick{}, rctx.Self(), a.interval,
			actor.WithReference(a.schedRef))

	case *streamRequest:
		a.demand += msg.n

	case *tickTick:
		if a.demand > 0 {
			a.seqNo++
			rctx.Tell(a.downstream, &streamElement{
				subID: a.subID,
				value: time.Now().UTC(),
				seqNo: a.seqNo,
			})
			a.demand--
		}
		// When demand is zero, the tick is silently dropped (natural backpressure).

	case *streamCancel:
		_ = rctx.ActorSystem().CancelSchedule(a.schedRef)
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// PostStop cancels the recurring schedule if the actor is stopped externally.
func (a *tickSourceActor) PostStop(_ *actor.Context) error {
	if a.schedRef != "" {
		_ = a.config.System.CancelSchedule(a.schedRef)
	}
	return nil
}

// makeMergeSinkDesc returns a stageDesc for the internal sink of one input
// pipeline of a fan-in source: a mergeSinkActor that forwards elements and the
// terminal signal of the input to self.
func makeMergeSinkDesc(self *actor.PID, slot int) *stage {
	config := defaultStageConfig()
	return &stage{
		id:   newStageID(),
		kind: sinkKind,
		actorFn: func(config StageConfig) actor.Actor {
			return &mergeSinkActor{target: self, slot: slot, config: config}
		},
		config: config,
	}
}

// PreStart does nothing: the sink starts on its stageWire.
func (a *mergeSinkActor) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamElement, mergeSubAck, streamComplete, and
// streamError.
func (a *mergeSinkActor) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.subID = msg.subID
		rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: a.config.InitialDemand})

	case *streamElement:
		rctx.Tell(a.target, &mergeSubValue{slot: a.slot, value: msg.value, sink: rctx.Self()})

	case *mergeSubAck:
		rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: msg.n})

	case *streamComplete:
		rctx.Tell(a.target, &mergeSubDone{slot: a.slot})
		rctx.Shutdown()

	case *streamError:
		rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
		rctx.Tell(a.target, &mergeSubErr{slot: a.slot, err: msg.err})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// PostStop does nothing: the sink holds no resource of its own.
func (a *mergeSinkActor) PostStop(_ *actor.Context) error { return nil }

// spawn materializes stages as a sub-pipeline of the owning stage and records
// its handle. It returns the materialization error, if any.
func (x *inputPipelines) spawn(ctx context.Context, system actor.ActorSystem, stages []*stage) error {
	_, err := x.spawnWithHead(ctx, system, stages)
	return err
}

// spawnWithHead behaves like spawn and also returns the PID of the first
// stage of the sub-pipeline.
//
// The owning stage can be stopped while it is still spawning: PostStop, and
// with it abort, may run on another goroutine before or during the turn that
// calls spawnWithHead. A sub-pipeline is therefore never started once abort
// has run, and one that abort overtook while it was being materialized is
// aborted here. Both cases return ErrStreamCanceled.
func (x *inputPipelines) spawnWithHead(ctx context.Context, system actor.ActorSystem, stages []*stage) (*actor.PID, error) {
	x.mu.Lock()
	aborted := x.aborted
	x.mu.Unlock()

	if aborted {
		return nil, ErrStreamCanceled
	}

	handle, head, err := materializeWithHead(ctx, system, stages)
	if err != nil {
		return nil, err
	}

	x.mu.Lock()
	if x.aborted {
		x.mu.Unlock()
		handle.Abort()
		return nil, ErrStreamCanceled
	}

	// Forget the sub-pipelines that have ended, so a stage that spawns many
	// over its life (Concat, FlatMapConcat, a splitter) does not keep a handle
	// for each. The scan runs when the list has doubled since the last one.
	if len(x.handles) >= x.pruneAt {
		x.handles = slices.DeleteFunc(x.handles, func(h StreamHandle) bool {
			select {
			case <-h.Done():
				return true
			default:
				return false
			}
		})
		x.pruneAt = max(2*len(x.handles), inputPrunesFrom)
	}

	x.handles = append(x.handles, handle)
	x.mu.Unlock()
	return head, nil
}

// arrived records the sink that forwarded msg as the sink of its input slot.
// Fan-in actors call it for every mergeSubValue, before they buffer the value.
// A slot can be reused by a later pipeline (FlatMap does); when a new sink
// takes the slot, acknowledgements owed to the previous one are dropped.
func (x *inputPipelines) arrived(msg *mergeSubValue) {
	for len(x.sinks) <= msg.slot {
		x.sinks = append(x.sinks, nil)
		x.unacked = append(x.unacked, 0)
	}

	if x.sinks[msg.slot] != msg.sink {
		x.sinks[msg.slot] = msg.sink
		x.unacked[msg.slot] = 0
	}
}

// release tells the sink of the input at slot that n of its elements have
// left the fan-in buffer, so the sink may request as many again. Releases are
// accumulated and sent in batches of inputAckBatch. Fan-in actors call it
// when they emit, combine or otherwise consume a buffered element.
func (x *inputPipelines) release(rctx *actor.ReceiveContext, slot int, n int64) {
	if slot >= len(x.sinks) || x.sinks[slot] == nil {
		return
	}

	x.unacked[slot] += n
	if x.unacked[slot] < inputAckBatch {
		return
	}

	rctx.Tell(x.sinks[slot], &mergeSubAck{n: x.unacked[slot]})
	x.unacked[slot] = 0
}

// releaseValue releases the buffered element msg, like release, provided the
// sink that sent it still owns its slot. An element of a pipeline whose slot
// has since been reused is not acknowledged: its sink has already stopped.
func (x *inputPipelines) releaseValue(rctx *actor.ReceiveContext, msg *mergeSubValue) {
	if msg.slot < len(x.sinks) && x.sinks[msg.slot] == msg.sink {
		x.release(rctx, msg.slot, 1)
	}
}

// abort stops every sub-pipeline spawned so far and refuses any later spawn.
// The owning stage calls it from PostStop, so its sub-pipelines are released
// however the stage terminated: completion, failure, cancellation or an abort
// of the enclosing stream.
func (x *inputPipelines) abort() {
	x.mu.Lock()
	x.aborted = true
	handles := x.handles
	x.handles = nil
	x.mu.Unlock()

	for _, handle := range handles {
		handle.Abort()
	}
}

// mergeSourceActor backs Merge. It fans N sub-source pipelines into a single
// downstream, buffering elements that arrive before demand is available and
// completing only once all sub-sources have sent mergeSubDone.
type mergeSourceActor[T any] struct {
	subStages  [][]*stage
	system     actor.ActorSystem
	inputs     inputPipelines // materialized input pipelines; aborted in PostStop
	downstream *actor.PID
	subID      string
	seqNo      uint64
	buf        queue
	demand     int64
	doneCount  int
	metrics    *stageMetrics
	config     StageConfig
}

// newMergeSourceActor creates a mergeSourceActor for the given sub-source stage lists.
func newMergeSourceActor[T any](subStages [][]*stage, config StageConfig) *mergeSourceActor[T] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &mergeSourceActor[T]{subStages: subStages, system: config.System, metrics: m, config: config}
}

func (a *mergeSourceActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, mergeSubValue, mergeSubDone, mergeSubErr, and streamCancel.
func (a *mergeSourceActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		if len(a.subStages) == 0 {
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
			rctx.Shutdown()
			return
		}

		self := rctx.Self()
		ctx := rctx.Context()
		for i, sub := range a.subStages {
			sink := makeMergeSinkDesc(self, i)
			all := make([]*stage, len(sub)+1)
			copy(all, sub)
			all[len(sub)] = sink
			if err := a.inputs.spawn(ctx, a.system, all); err != nil {
				rctx.Tell(a.downstream, &streamError{subID: a.subID, err: err})
				rctx.Shutdown()
				return
			}
		}

	case *streamRequest:
		a.demand += msg.n
		a.tryFlush(rctx)

	case *mergeSubValue:
		a.metrics.elementsIn.Add(1)
		a.inputs.arrived(msg)
		a.buf.push(msg)
		a.tryFlush(rctx)

	case *mergeSubDone:
		a.doneCount++
		a.tryFlush(rctx)

	case *mergeSubErr:
		rctx.Tell(a.downstream, &streamError{subID: a.subID, err: msg.err})
		rctx.Shutdown()

	case *streamCancel:
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// tryFlush forwards buffered elements downstream and completes when all
// sub-sources are done and the buffer is empty.
func (a *mergeSourceActor[T]) tryFlush(rctx *actor.ReceiveContext) {
	for a.demand > 0 && !a.buf.empty() {
		elem := a.buf.pop().(*mergeSubValue)
		a.seqNo++
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: elem.value,
			seqNo: a.seqNo,
		})
		a.demand--
		a.inputs.release(rctx, elem.slot, 1)
	}

	if a.doneCount >= len(a.subStages) && a.buf.empty() {
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()
	}
}

// PostStop aborts the input pipelines so they do not outlive this stage.
func (a *mergeSourceActor[T]) PostStop(_ *actor.Context) error {
	a.inputs.abort()
	return nil
}

// combineSourceActor backs Combine. It zips elements from two sub-sources using
// a combine function, emitting one output element per input pair (zip semantics).
// It completes when either source is exhausted and all matched pairs are emitted.
type combineSourceActor[T, U, V any] struct {
	leftStages  []*stage
	rightStages []*stage
	combineFn   func(T, U) V
	system      actor.ActorSystem
	inputs      inputPipelines // materialized input pipelines; aborted in PostStop
	downstream  *actor.PID
	subID       string
	seqNo       uint64
	leftBuf     queue
	rightBuf    queue
	leftDone    bool
	rightDone   bool
	demand      int64
	metrics     *stageMetrics
	config      StageConfig
}

// newCombineSourceActor creates a combineSourceActor that zips left and right sources.
func newCombineSourceActor[T, U, V any](
	left []*stage, right []*stage,
	fn func(T, U) V, config StageConfig,
) *combineSourceActor[T, U, V] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}

	return &combineSourceActor[T, U, V]{
		leftStages:  left,
		rightStages: right,
		combineFn:   fn,
		system:      config.System,
		metrics:     m,
		config:      config,
	}
}

func (a *combineSourceActor[T, U, V]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, mergeSubValue, mergeSubDone, mergeSubErr, and streamCancel.
func (a *combineSourceActor[T, U, V]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		self := rctx.Self()
		ctx := rctx.Context()

		leftSink := makeMergeSinkDesc(self, 0)
		rightSink := makeMergeSinkDesc(self, 1)
		leftAll := make([]*stage, len(a.leftStages)+1)
		copy(leftAll, a.leftStages)
		leftAll[len(a.leftStages)] = leftSink
		rightAll := make([]*stage, len(a.rightStages)+1)
		copy(rightAll, a.rightStages)
		rightAll[len(a.rightStages)] = rightSink

		if err := a.inputs.spawn(ctx, a.system, leftAll); err != nil {
			rctx.Tell(a.downstream, &streamError{subID: a.subID, err: err})
			rctx.Shutdown()
			return
		}

		if err := a.inputs.spawn(ctx, a.system, rightAll); err != nil {
			rctx.Tell(a.downstream, &streamError{subID: a.subID, err: err})
			rctx.Shutdown()
			return
		}

	case *streamRequest:
		a.demand += msg.n
		a.tryEmit(rctx)

	case *mergeSubValue:
		a.metrics.elementsIn.Add(1)
		a.inputs.arrived(msg)
		if msg.slot == 0 {
			a.leftBuf.push(msg.value)
		} else {
			a.rightBuf.push(msg.value)
		}
		a.tryEmit(rctx)

	case *mergeSubDone:
		if msg.slot == 0 {
			a.leftDone = true
		} else {
			a.rightDone = true
		}
		a.tryEmit(rctx)

	case *mergeSubErr:
		rctx.Tell(a.downstream, &streamError{subID: a.subID, err: msg.err})
		rctx.Shutdown()

	case *streamCancel:
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// tryEmit pairs left and right buffered elements via combineFn and forwards
// each result downstream. Completes when either side is done and no more
// pairs can be formed.
func (a *combineSourceActor[T, U, V]) tryEmit(rctx *actor.ReceiveContext) {
	for a.demand > 0 && !a.leftBuf.empty() && !a.rightBuf.empty() {
		l, ok1 := a.leftBuf.peek().(T)
		r, ok2 := a.rightBuf.peek().(U)
		if !ok1 || !ok2 {
			rctx.Tell(a.downstream, &streamError{
				subID: a.subID,
				err:   fmt.Errorf("stream: Combine type mismatch l=%T r=%T", a.leftBuf.peek(), a.rightBuf.peek()),
			})
			rctx.Shutdown()
			return
		}
		a.leftBuf.pop()
		a.rightBuf.pop()
		a.inputs.release(rctx, 0, 1)
		a.inputs.release(rctx, 1, 1)
		a.seqNo++
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: a.combineFn(l, r),
			seqNo: a.seqNo,
		})
		a.demand--
		a.metrics.elementsOut.Add(1)
	}
	// Complete when either side is done and its buffer is empty — no more
	// pairs can be formed because no further elements will arrive from
	// that side.
	if (a.leftDone && a.leftBuf.empty()) || (a.rightDone && a.rightBuf.empty()) {
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()
	}
}

// PostStop aborts the input pipelines so they do not outlive this stage.
func (a *combineSourceActor[T, U, V]) PostStop(_ *actor.Context) error {
	a.inputs.abort()
	return nil
}

// connSourceActor backs FromConn. It reads from a net.Conn on demand,
// producing one []byte element per Read call. The source completes on
// io.EOF and errors on any other read failure.
//
// readPool pools the full-sized read buffers so the hot path avoids allocating
// bufSize bytes on every Read call. Each element sent downstream is a
// separately-allocated exact-sized slice so the downstream owns its data
// without sharing the pooled buffer.
type connSourceActor struct {
	conn       net.Conn
	bufSize    int
	downstream *actor.PID
	subID      string
	seqNo      uint64
	readPool   sync.Pool
	config     StageConfig
}

func newConnSourceActor(conn net.Conn, bufSize int, cfg StageConfig) *connSourceActor {
	a := &connSourceActor{conn: conn, bufSize: bufSize, config: cfg}
	a.readPool.New = func() any { return make([]byte, bufSize) }
	return a
}

func (a *connSourceActor) PreStart(_ *actor.Context) error { return nil }

func (a *connSourceActor) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID

	case *streamRequest:
		for i := int64(0); i < msg.n; i++ {
			readBuf := a.readPool.Get().([]byte)
			n, err := a.conn.Read(readBuf)
			if err != nil {
				a.readPool.Put(readBuf) //nolint:staticcheck
				if err == io.EOF {
					rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
				} else {
					rctx.Tell(a.downstream, &streamError{subID: a.subID, err: err})
				}
				rctx.Shutdown()
				return
			}
			// Copy to an exact-sized slice so the downstream owns its own data
			// and the pooled read buffer can be returned immediately.
			elem := make([]byte, n)
			copy(elem, readBuf[:n])
			a.readPool.Put(readBuf) //nolint:staticcheck
			a.seqNo++
			rctx.Tell(a.downstream, &streamElement{subID: a.subID, value: elem, seqNo: a.seqNo})
		}

	case *streamCancel:
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

func (a *connSourceActor) PostStop(_ *actor.Context) error { return nil }
