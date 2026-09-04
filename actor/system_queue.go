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
	"sync/atomic"
	"unsafe"
)

// systemQueue is the actor's queue of system messages: PostStart, PoisonPill,
// Terminated, the supervision signals and the other control messages that
// runTurn drains ahead of the user mailbox. Producers push with one
// compare-and-swap; the single consumer, the dispatcher worker that owns the
// actor's turn, takes everything pushed so far in one swap and hands it out in
// send order. It needs no sentinel node, so an idle actor holds nothing: the
// two words below are the queue's entire footprint.
//
// Send order is preserved twice over. A swapped batch comes out most recent
// first and is reversed once, and the consumer finishes its private batch
// before swapping again, so everything in that batch was pushed before
// everything in the next one.
type systemQueue struct {
	// head is the top of the push list, most recent first. Producers only
	// ever compare-and-swap it and the consumer only ever swaps it to nil, so
	// a message is either in this list or in the consumer's private batch,
	// never in both.
	head atomic.Pointer[ReceiveContext]

	// last is the message most recently handed to the consumer. Its next
	// link carries the rest of the drained batch, oldest first. It is
	// released on the following pop, exactly as the user mailbox releases the
	// previous message on the next dequeue, and it is nil while the consumer
	// holds nothing. Only the consumer writes it, but it is atomic because
	// isEmpty may read it from a worker that has just given up the turn while
	// the next owner is already popping; see isEmpty.
	last atomic.Pointer[ReceiveContext]
}

// push appends ctx to the queue. Safe to call from any goroutine. The context
// must not be linked into any mailbox; its next link is overwritten.
func (x *systemQueue) push(ctx *ReceiveContext) {
	for {
		top := x.head.Load()
		atomic.StorePointer(&ctx.next, unsafe.Pointer(top))

		if x.head.CompareAndSwap(top, ctx) {
			return
		}
	}
}

// pop returns the next message in send order, or nil when there is none. It
// must be called from the single consumer. It releases the previously popped
// message, so the caller may use a returned context only until the next pop;
// a pop that returns nil releases it too, which is what keeps an idle actor
// from pinning a context.
//
// runTurn calls pop before every user message, so the idle path, nothing
// held and nothing pushed, is the hot one: it is two loads and no write,
// because a write here would evict the line that producers read on every
// message (the mailbox and actor system pointers share it).
func (x *systemQueue) pop() *ReceiveContext {
	last := x.last.Load()
	next := nextInBatch(last)
	if next == nil && x.head.Load() != nil {
		next = inSendOrder(x.head.Swap(nil))
	}

	if last == nil && next == nil {
		return nil
	}

	if last != nil {
		recycleContext(last)
	}

	x.last.Store(next)
	return next
}

// isEmpty reports whether no message is waiting. It serves the end-of-turn
// check in finishOrReclaim, which releases ownership of the actor before it
// asks, so it may run on a worker that no longer owns the turn while the next
// owner is already popping. Every read here is therefore atomic, and the
// answer is a snapshot: a push or a pop may land right after it, which the
// caller resolves through the scheduling state, exactly as it does for the
// user mailbox.
func (x *systemQueue) isEmpty() bool {
	return nextInBatch(x.last.Load()) == nil && x.head.Load() == nil
}

// nextInBatch returns the message after last in the drained batch, or nil when
// last is nil or the batch is exhausted.
func nextInBatch(last *ReceiveContext) *ReceiveContext {
	if last == nil {
		return nil
	}

	return (*ReceiveContext)(atomic.LoadPointer(&last.next))
}

// inSendOrder turns a push list, most recent first, into send order and
// returns its first message. It runs on the consumer's private copy of the
// list, which no producer can reach any more.
func inSendOrder(top *ReceiveContext) *ReceiveContext {
	var ordered *ReceiveContext

	for top != nil {
		next := (*ReceiveContext)(atomic.LoadPointer(&top.next))
		atomic.StorePointer(&top.next, unsafe.Pointer(ordered))
		ordered = top
		top = next
	}

	return ordered
}
