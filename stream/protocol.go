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
)

// stageWire is the first message sent to a stage actor.
// It wires the actor to its upstream and downstream neighbors.
type stageWire struct {
	subID      string
	upstream   *actor.PID // nil for source stages
	downstream *actor.PID // nil for sink stages
}

// streamRequest is sent downstream-to-upstream to signal demand for n elements.
type streamRequest struct {
	subID string
	n     int64
}

// streamElement carries a single pipeline element from upstream to downstream.
type streamElement struct {
	subID string
	value any
	seqNo uint64
}

// streamComplete signals normal upstream completion.
type streamComplete struct {
	subID string
}

// streamError signals upstream failure.
type streamError struct {
	subID string
	err   error
}

// streamCancel is sent downstream-to-upstream to cancel the subscription.
type streamCancel struct {
	subID string
}

// chanBatch is sent by the channel-reader goroutine to the channel source actor,
// carrying a batch of values drained from the external channel in one shot.
// Batching amortises the per-element actor.Tell overhead on high-throughput channels.
type chanBatch struct {
	values []any
}

// chanDone is sent by the channel-reader goroutine when the channel is closed.
type chanDone struct{}

// fetchResult is sent by the actor-pull goroutine to the actor source actor.
type fetchResult struct {
	values    []any
	done      bool
	requested int64 // how many were requested; used to restore unfulfilled demand
}

// fetchErr is sent by the actor-pull goroutine when the pull fails.
type fetchErr struct {
	err error
}

// tickTick is sent by the GoAkt scheduler to the tick source actor on each interval.
// The actor captures the current time when it processes the message so that the
// emitted timestamp reflects the actual delivery moment rather than when the
// tick was scheduled.
type tickTick struct{}

// batchFlush is sent to the batch actor by its internal timer.
type batchFlush struct{}

// throttleTick is sent by the throttle actor's ticker goroutine on each interval.
type throttleTick struct{}

// mergeSubDone is sent to a mergeSourceActor or combineSourceActor
// when one of its sub-source pipelines completes.
// The slot field identifies which sub-source finished (used by combineSourceActor).
type mergeSubDone struct {
	slot int
}

// mergeSubValue carries an element from a sub-source pipeline to the merge/combine actor.
type mergeSubValue struct {
	slot  int // 0=left, 1=right — used by combineSourceActor; ignored by mergeSourceActor
	value any
}

// slotDemand is sent by a broadcastSlotActor to the broadcastHubActor when its
// downstream requests more elements. The hub uses these counts to determine how
// many elements it may safely pull from upstream.
type slotDemand struct {
	slot int
	n    int64
}

// hubReady is sent by the broadcastHubActor to each broadcastSlotActor once the
// hub has been wired to its upstream. Slots that buffered demand before this
// message arrive flush it immediately on receipt.
type hubReady struct {
	hubPID *actor.PID
}

// slotCancel is sent by a broadcastSlotActor to the broadcastHubActor when its
// downstream cancels the subscription. When all slots have cancelled, the hub
// propagates cancellation to its upstream.
type slotCancel struct {
	slot int
}
