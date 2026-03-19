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
	"sync"
	"time"

	"github.com/tochemey/goakt/v4/actor"
)

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
// A goroutine bridges the external channel into the actor mailbox via chanValue
// and chanDone messages so the receive loop is never blocked on a channel read.
// Received values are queued internally and forwarded downstream on demand.
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
}

// newChanSourceActor creates a chanSourceActor that reads from ch.
func newChanSourceActor[T any](ch <-chan T, config StageConfig) *chanSourceActor[T] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &chanSourceActor[T]{ch: ch, metrics: m, config: config}
}

func (a *chanSourceActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, chanValue, chanDone, and streamCancel.
func (a *chanSourceActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		// Bridge the external channel into the actor mailbox using a batching
		// goroutine. Each batch drains all immediately-available elements (up to
		// chanBridgeBatchSize) before sending a single actor.Tell, which
		// amortizes the per-element mailbox-enqueue cost on high-throughput channels
		// while keeping latency low for slow channels (partial batches are flushed
		// as soon as no element is immediately available).
		self := rctx.Self()
		ch := a.ch
		go func() {
			const batchSize = 64
			for {
				v, ok := <-ch
				if !ok {
					_ = actor.Tell(context.Background(), self, &chanDone{})
					return
				}
				buf := make([]any, 1, batchSize)
				buf[0] = v
			drain:
				for len(buf) < batchSize {
					select {
					case v, ok = <-ch:
						if !ok {
							_ = actor.Tell(context.Background(), self, &chanBatch{values: buf})
							_ = actor.Tell(context.Background(), self, &chanDone{})
							return
						}
						buf = append(buf, v)
					default:
						break drain
					}
				}
				if err := actor.Tell(context.Background(), self, &chanBatch{values: buf}); err != nil {
					return
				}
			}
		}()

	case *streamRequest:
		a.demand += msg.n
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

func (a *chanSourceActor[T]) PostStop(_ *actor.Context) error { return nil }

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

// makeMergeSinkDesc returns a stageDesc for an internal sink that forwards
// received elements and its completion signal to self.
func makeMergeSinkDesc(self *actor.PID, slot int) *stageDesc {
	config := defaultStageConfig()
	return &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(config StageConfig) actor.Actor {
			return newSinkActor(func(v any) error {
				return actor.Tell(context.Background(), self, &mergeSubValue{slot: slot, value: v})
			}, func() {
				_ = actor.Tell(context.Background(), self, &mergeSubDone{slot: slot})
			}, config)
		},
		config: config,
	}
}

// spawnSubPipeline materializes stages (appended with an internal sink) into system.
func spawnSubPipeline(ctx context.Context, system actor.ActorSystem, stages []*stageDesc) {
	_, _ = materialize(ctx, system, stages)
}

// mergeSourceActor backs Merge. It fans N sub-source pipelines into a single
// downstream, buffering elements that arrive before demand is available and
// completing only once all sub-sources have sent mergeSubDone.
type mergeSourceActor[T any] struct {
	subStages  [][]*stageDesc
	system     actor.ActorSystem
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
func newMergeSourceActor[T any](subStages [][]*stageDesc, config StageConfig) *mergeSourceActor[T] {
	m := config.Metrics
	if m == nil {
		m = &stageMetrics{}
	}
	return &mergeSourceActor[T]{subStages: subStages, system: config.System, metrics: m, config: config}
}

func (a *mergeSourceActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, mergeSubValue, mergeSubDone, and streamCancel.
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
			all := make([]*stageDesc, len(sub)+1)
			copy(all, sub)
			all[len(sub)] = sink
			spawnSubPipeline(ctx, a.system, all)
		}

	case *streamRequest:
		a.demand += msg.n
		a.tryFlush(rctx)

	case *mergeSubValue:
		a.metrics.elementsIn.Add(1)
		a.buf.push(msg.value)
		a.tryFlush(rctx)

	case *mergeSubDone:
		a.doneCount++
		a.tryFlush(rctx)

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
		a.seqNo++
		rctx.Tell(a.downstream, &streamElement{
			subID: a.subID,
			value: a.buf.pop(),
			seqNo: a.seqNo,
		})
		a.demand--
	}

	if a.doneCount >= len(a.subStages) && a.buf.empty() {
		rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		rctx.Shutdown()
	}
}

func (a *mergeSourceActor[T]) PostStop(_ *actor.Context) error { return nil }

// combineSourceActor backs Combine. It zips elements from two sub-sources using
// a combine function, emitting one output element per input pair (zip semantics).
// It completes when either source is exhausted and all matched pairs are emitted.
type combineSourceActor[T, U, V any] struct {
	leftStages  []*stageDesc
	rightStages []*stageDesc
	combineFn   func(T, U) V
	system      actor.ActorSystem
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
	left []*stageDesc, right []*stageDesc,
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

// Receive handles stageWire, streamRequest, mergeSubValue, mergeSubDone, and streamCancel.
func (a *combineSourceActor[T, U, V]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		self := rctx.Self()
		ctx := rctx.Context()

		leftSink := makeMergeSinkDesc(self, 0)
		rightSink := makeMergeSinkDesc(self, 1)
		leftAll := make([]*stageDesc, len(a.leftStages)+1)
		copy(leftAll, a.leftStages)
		leftAll[len(a.leftStages)] = leftSink
		rightAll := make([]*stageDesc, len(a.rightStages)+1)
		copy(rightAll, a.rightStages)
		rightAll[len(a.rightStages)] = rightSink

		spawnSubPipeline(ctx, a.system, leftAll)
		spawnSubPipeline(ctx, a.system, rightAll)

	case *streamRequest:
		a.demand += msg.n
		a.tryEmit(rctx)

	case *mergeSubValue:
		a.metrics.elementsIn.Add(1)
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

func (a *combineSourceActor[T, U, V]) PostStop(_ *actor.Context) error { return nil }

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
