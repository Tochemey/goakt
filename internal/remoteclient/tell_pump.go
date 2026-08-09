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

package remoteclient

import (
	"sync"
	"sync/atomic"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
)

// queueCompactHead is the minimum abandoned prefix length before popAdmit
// compacts the underlying slice so steady drain/fill does not retain a
// permanently growing array offset.
const queueCompactHead = 32

// admittedTell is one tell owned by a lane pump, with the byte cost charged
// against the per-lane admission window at enqueue time.
type admittedTell struct {
	params tellParams
	cost   int64
}

// tellPump stages fire-and-forget tells admitted for one duplex lane and
// drains them in admission order. Admission never requires a live session:
// dialing, table-ref encoding, and the writer-queue submit all happen on the
// peer's pump goroutine (see peer.go). The queue is byte-capped to the
// client's initial credit window (same bound as the duplex writer queue) so
// per-peer memory stays predictable. Transport failures fan out through the
// shared tell-failure handler (dead letters) instead of surfacing to callers.
type tellPump struct {
	mu sync.Mutex
	// queue holds admitted-but-unwritten tells; live items are queue[head:].
	queue []admittedTell
	// head is the index of the next item to pop.
	head int
	// queuedBytes is the sum of admitCost across the live queue.
	queuedBytes int64
	// pending counts admitted tells not yet delivered (queued or in flight).
	// A nonzero count forces later same-lane tells through the queue so the
	// synchronous fast path can never overtake earlier admitted tells.
	pending atomic.Int64
	// notify wakes the pump when work is enqueued (capacity 1).
	notify chan types.Unit
	// space wakes admitters blocked on the byte cap (capacity 1).
	space chan types.Unit
}

// newTellPump constructs an idle lane pump with signaling channels.
func newTellPump() *tellPump {
	return &tellPump{
		notify: make(chan types.Unit, 1),
		space:  make(chan types.Unit, 1),
	}
}

// admitCost is the byte charge for one admitted tell (owned payload and
// metadata). Envelope framing is charged later on the writer queue.
func admitCost(params tellParams) int64 {
	return int64(len(params.payload) + len(params.metadata))
}

// liveLen returns the number of queued tells (excluding the abandoned prefix).
func (x *tellPump) liveLen() int {
	return len(x.queue) - x.head
}

// popAdmit removes the head tell and frees its byte charge.
func (x *tellPump) popAdmit() (admittedTell, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.head >= len(x.queue) {
		return admittedTell{}, false
	}

	item := x.queue[x.head]
	x.queue[x.head] = admittedTell{}
	x.head++
	x.queuedBytes -= item.cost
	if x.queuedBytes < 0 {
		x.queuedBytes = 0
	}

	if x.head >= queueCompactHead && x.head*2 >= len(x.queue) {
		x.queue = append(x.queue[:0], x.queue[x.head:]...)
		x.head = 0
	}

	select {
	case x.space <- types.Unit{}:
	default:
	}
	return item, true
}

// tellRemoteMessage converts tell params to the wire message shape shared by
// the coalescer submit path. The payload slice is shared with params.
func tellRemoteMessage(params tellParams) *internalpb.RemoteMessage {
	return &internalpb.RemoteMessage{
		Sender:   params.sender,
		Receiver: params.receiver,
		Message:  params.payload,
		Metadata: metadataMapFromBytes(params.metadata),
	}
}

// tellRemoteMessageOwned is like tellRemoteMessage but copies the payload so
// async failure handlers may retain the message after the caller recycles
// its buffers (payload pool on the synchronous tell path).
func tellRemoteMessageOwned(params tellParams) *internalpb.RemoteMessage {
	return &internalpb.RemoteMessage{
		Sender:   params.sender,
		Receiver: params.receiver,
		Message:  cloneBytes(params.payload),
		Metadata: metadataMapFromBytes(params.metadata),
	}
}

// cloneBytes returns a copy of b, or nil when b is empty, so admitted tells
// own their buffers independently of caller-pooled slices.
func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}

	out := make([]byte, len(b))
	copy(out, b)
	return out
}
