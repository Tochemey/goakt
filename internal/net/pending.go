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

package net

import (
	"github.com/tochemey/goakt/v4/internal/xsync"
)

// pendingWaitersCap is the bounded pool size for correlation wait channels.
const pendingWaitersCap = 1024

// pendingWaiterCh is a channel-based pool of buffered (cap 1) frame channels
// used by [pendingTable] waiters.
var pendingWaiterCh = make(chan chan Frame, pendingWaitersCap)

// pendingTable parks callers waiting for REPLY or ERROR frames keyed by
// correlation ID. Completing an unknown ID is a silent no-op so a late reply
// after a local timeout is dropped without error.
type pendingTable struct {
	// slots maps correlation ID to a pooled, buffered (cap 1) waiter channel.
	slots *xsync.Map[uint64, chan Frame]
}

// newPendingTable returns an empty correlation waiter table.
func newPendingTable() *pendingTable {
	return &pendingTable{slots: xsync.NewMap[uint64, chan Frame]()}
}

// register reserves a waiter for correlation and returns the channel the
// caller waits on.
//
// Ownership rule: whoever removes the slot from the table owns the channel.
// A caller that receives a delivered frame owns it and must return it via
// [putPendingWaiter]. A caller whose [pendingTable.abandon] returns true has
// nothing left to do (abandon pooled it). When abandon returns false, a
// concurrent [pendingTable.complete] won the slot and its send may still be
// in flight, so the channel must NOT be pooled; it is left to the garbage
// collector instead.
func (x *pendingTable) register(correlation uint64) chan Frame {
	ch := getPendingWaiter()
	x.slots.Set(correlation, ch)
	return ch
}

// complete delivers frame to the waiter for its correlation ID, if any.
// It returns true when a waiter was present. The waiter (or [failAll]) is
// responsible for returning the channel to the pool after receipt.
func (x *pendingTable) complete(correlation uint64, frame Frame) bool {
	ch, ok := x.slots.LoadAndDelete(correlation)
	if !ok {
		return false
	}

	// Cannot block: the slot is buffered and LoadAndDelete made this the
	// sole owner. A waiter that already timed out abandoned the slot first.
	ch <- frame
	return true
}

// abandon removes the waiter for correlation without delivering a frame and
// reports whether this call won the slot. On true the channel has been
// returned to the pool. On false a concurrent [pendingTable.complete] (or an
// earlier abandon) owns the channel and its send may still be in flight, so
// the caller must not pool or reuse it.
func (x *pendingTable) abandon(correlation uint64) bool {
	ch, ok := x.slots.LoadAndDelete(correlation)
	if !ok {
		return false
	}

	putPendingWaiter(ch)
	return true
}

// failAll completes every pending waiter with frame (typically signaling
// transport loss) and clears the table. Waiters return their channels to
// the pool after receiving the frame.
func (x *pendingTable) failAll(frame Frame) {
	for _, correlation := range x.slots.Keys() {
		_ = x.complete(correlation, frame)
	}
}

// len returns the number of outstanding waiters.
func (x *pendingTable) len() int {
	return x.slots.Len()
}

// getPendingWaiter returns a pooled buffered frame channel.
func getPendingWaiter() chan Frame {
	select {
	case ch := <-pendingWaiterCh:
		return ch
	default:
		return make(chan Frame, 1)
	}
}

// putPendingWaiter drains ch and returns it to the pool when there is room.
func putPendingWaiter(ch chan Frame) {
	for {
		select {
		case <-ch:
		default:
			select {
			case pendingWaiterCh <- ch:
			default:
			}
			return
		}
	}
}
