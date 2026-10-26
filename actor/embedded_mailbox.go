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
	"runtime"
	"sync/atomic"

	"golang.org/x/sys/cpu"
)

// mailboxSegmentSlots is the number of messages one segment of the default
// mailbox holds. A segment of 32 slots fits the 2 KiB allocation size class, so
// the runtime serves a new one from the per-P cache rather than from the heap
// lock that a larger segment would take on every allocation.
const mailboxSegmentSlots = 32

// mailboxGenerationShift splits a segment cursor: the bits above it hold the
// segment's reuse generation, the bits below it the slots claimed in that
// generation.
const mailboxGenerationShift = 32

// mailboxSpinLimit bounds how long a producer spins on loads while another
// producer links the next segment before it yields its processor once.
const mailboxSpinLimit = 1 << 12

// mailboxSlotState is the publication state of one mailbox slot.
type mailboxSlotState uint32

const (
	// slotEmpty marks a slot that holds no message: never written in the
	// current generation, or already read by the consumer.
	slotEmpty mailboxSlotState = iota
	// slotReady marks a slot whose message the producer has finished writing.
	slotReady
	// slotAbandoned marks a slot claimed by a producer that found the segment
	// reused under it. The producer retries elsewhere and the consumer skips
	// the slot.
	slotAbandoned
)

// mailboxSlot holds one message of the default mailbox. A Tell stores the
// message, its sender and its context as values, so a local send takes no
// pooled ReceiveContext. A message that needs a full ReceiveContext (an Ask, a
// remote message, a stash replay) travels in received instead, in the same
// queue, so the two kinds keep their relative order.
type mailboxSlot struct {
	// state publishes the slot. The producer stores slotReady after writing
	// the other fields, and the consumer reads them only after loading it.
	state atomic.Uint32
	// ctx is the send context of a value message.
	ctx context.Context
	// message is the payload of a value message.
	message any
	// sender is the sender of a value message.
	sender *PID
	// received is the context of a message enqueued through Enqueue, nil for
	// a value message.
	received *ReceiveContext
}

// mailboxSegment is one fixed-size block of the default mailbox. Producers
// claim slots with a fetch-and-add on cursor, which always succeeds, so many
// senders to one actor never retry each other's claims.
//
// A segment is reused rather than left to the garbage collector. Reuse bumps
// the generation in the high bits of cursor, so a producer that loaded the
// segment before it was reused sees a different generation on its claim and
// retries; it marks the slot it claimed in the new generation slotAbandoned,
// so the consumer does not wait for it.
type mailboxSegment struct {
	// cursor holds the generation in its high 32 bits and the number of slots
	// claimed in this generation in its low 32 bits.
	cursor atomic.Uint64
	// the pad keeps the producers' fetch-and-add off the line that holds next
	// and the first slots.
	_ cpu.CacheLinePad
	// next is the segment that follows this one once it is full. The first
	// producer whose claim past the end matches the generation links it.
	next atomic.Pointer[mailboxSegment]
	// linked is one more than the last generation in which a producer took on
	// linking next, zero before any. It makes that producer unique per
	// generation without tying the duty to one index, which a claim made
	// under an older generation may have consumed. Generations wrap, so it is
	// compared with linkedInGeneration, not with the ordering of its values.
	linked atomic.Uint64
	// slots holds the messages in claim order.
	slots [mailboxSegmentSlots]mailboxSlot
}

// embeddedMailboxState is the part of the default mailbox that does not fit in
// the PID's two segment pointers. newPID allocates it for every actor that uses
// the default mailbox; an actor with a custom mailbox has none.
type embeddedMailboxState struct {
	// context is handed to the behavior for every message a Tell stored as
	// values. Only the processing turn touches it, so one per actor is enough.
	context ReceiveContext
	// held is the pooled context the previous Dequeue returned. It is recycled
	// on the next Dequeue, as a sentinel is in UnboundedMailbox.
	held *ReceiveContext
	// spare is a drained segment kept for the next producer that needs one.
	// The consumer offers it, a producer takes it with a swap.
	spare atomic.Pointer[mailboxSegment]
	// read is the index of the next slot to read in the head segment. Only the
	// consumer writes it; it is atomic because IsEmpty may run on a worker that
	// has just given the actor up while the next owner reads.
	read atomic.Uint32
}

// embeddedMailbox is the default user mailbox, run on the segment pointers
// carried inside the PID (mailboxHead and mailboxTail) and on the PID's
// embeddedMailboxState. It is a lock-free multi-producer, single-consumer FIFO
// queue: many producers enqueue concurrently, and exactly one consumer, the
// processing turn, dequeues.
//
// The type is a defined type over PID so (*embeddedMailbox)(pid) is a
// zero-allocation pointer conversion and the Mailbox interface value the PID
// hands to runTurn points straight into the PID.
//
// Messages are stored as values in fixed-size segments, in the manner of the
// segmented array queues of JCTools (MpscUnboundedXaddArrayQueue) and .NET
// (ConcurrentQueue): a send writes into a slot instead of allocating a node.
// The first segment is allocated on the first message, so an actor that never
// receives one pays nothing for it. A segment the consumer has drained is
// rewound in place, so an actor whose backlog never exceeds one segment keeps
// reusing it. A longer backlog links further segments, and each one the
// consumer finishes becomes the spare that the next link reuses.
//
// A PID given a custom mailbox never becomes an embeddedMailbox and leaves the
// segment pointers nil.
type embeddedMailbox PID

// enforces compilation error
var _ Mailbox = (*embeddedMailbox)(nil)

// newEmbeddedMailboxState returns the default mailbox state of pid.
func newEmbeddedMailboxState(pid *PID) *embeddedMailboxState {
	state := new(embeddedMailboxState)
	state.context.self = pid
	return state
}

// Enqueue appends value to the tail of the mailbox. Safe to call
// concurrently from multiple producers. Always returns nil; the error
// is present to satisfy the Mailbox interface.
func (x *embeddedMailbox) Enqueue(value *ReceiveContext) error {
	slot := x.claim()
	slot.received = value
	slot.state.Store(uint32(slotReady))
	return nil
}

// Dequeue removes and returns the next message, or nil when empty.
// Must be called from a single consumer goroutine.
//
// A message enqueued with Enqueue comes back in the very context that was put
// in, which the next Dequeue recycles. A message stored by put comes back in
// the actor's own turn context, refilled for each such message. Either way the
// caller must not keep or release the returned context.
func (x *embeddedMailbox) Dequeue() *ReceiveContext {
	state := x.embedded
	if held := state.held; held != nil {
		state.held = nil
		recycleContext(held)
	}

	segment := x.mailboxHead.Load()
	if segment == nil {
		return nil
	}

	for {
		read := state.read.Load()
		if read < mailboxSegmentSlots {
			slot := &segment.slots[read]
			switch mailboxSlotState(slot.state.Load()) {
			case slotEmpty:
				x.rewind(segment, read)
				return nil
			case slotAbandoned:
				slot.state.Store(uint32(slotEmpty))
				state.read.Store(read + 1)
				continue
			}

			state.read.Store(read + 1)
			return x.take(slot)
		}

		next := segment.next.Load()
		if next == nil {
			x.rewind(segment, read)
			return nil
		}

		x.mailboxHead.Store(next)
		state.read.Store(0)
		x.recycle(segment)
		segment = next
	}
}

// Len returns an approximate number of messages currently in the mailbox.
// Racy with producers and the consumer; use only outside hot paths.
func (x *embeddedMailbox) Len() int64 {
	segment := x.mailboxHead.Load()
	if segment == nil {
		return 0
	}

	var count int64
	read := uint64(x.embedded.read.Load())
	for segment != nil {
		claimed := min(cursorClaims(segment.cursor.Load()), mailboxSegmentSlots)
		if claimed > read {
			count += int64(claimed - read)
		}

		segment, read = segment.next.Load(), 0
	}

	return count
}

// IsEmpty reports whether the mailbox currently holds no message ready to be
// read. The result is a racy snapshot. It only loads, so a worker that has just
// given the actor up may call it while the next owner dequeues.
func (x *embeddedMailbox) IsEmpty() bool {
	segment := x.mailboxHead.Load()
	if segment == nil {
		return true
	}

	read := x.embedded.read.Load()
	for segment != nil {
		for ; read < mailboxSegmentSlots; read++ {
			switch mailboxSlotState(segment.slots[read].state.Load()) {
			case slotEmpty:
				return true
			case slotReady:
				return false
			}
		}

		segment, read = segment.next.Load(), 0
	}

	return true
}

// Dispose is a no-op for embeddedMailbox; present for interface compliance.
// The segments live as long as the PID and are collected with it.
func (x *embeddedMailbox) Dispose() {}

// put appends a message stored as values: the send context, the sender and
// the payload. It is the path of a local Tell, which therefore takes no pooled
// ReceiveContext. Safe to call concurrently from multiple producers.
func (x *embeddedMailbox) put(ctx context.Context, sender *PID, message any) {
	slot := x.claim()
	slot.ctx = ctx
	slot.message = message
	slot.sender = sender
	slot.state.Store(uint32(slotReady))
}

// claim reserves the next slot for the calling producer, linking a new
// segment when the tail is full. The caller writes the slot and publishes it
// by storing slotReady.
func (x *embeddedMailbox) claim() *mailboxSlot {
	for {
		segment := x.mailboxTail.Load()
		if segment == nil {
			segment = x.firstSegment()
		}

		if slot := x.claimIn(segment, cursorGeneration(segment.cursor.Load())); slot != nil {
			return slot
		}
	}
}

// claimIn claims one slot of segment for a producer that loaded segment as the
// tail and then read generation from its cursor. It returns nil when the caller
// must load the tail and try again: the segment is no longer the tail, it was
// full and has been extended since, or it was reused between the producer's
// read of generation and its claim.
//
// The tail check comes after the read of generation. A producer may load the
// tail, stall while the consumer finishes the segment and keeps it as the
// spare, and only then read the spare's new generation: its claim would match
// and land in a segment that is not linked into the queue. A segment that is
// still the tail after the read is either the tail of the generation read, or
// was reused since, which the generation check below catches.
func (x *embeddedMailbox) claimIn(segment *mailboxSegment, generation uint64) *mailboxSlot {
	if x.mailboxTail.Load() != segment {
		return nil
	}

	claimed := segment.cursor.Add(1) - 1
	index := cursorClaims(claimed)

	// The segment was reused before the claim: the slot belongs to the new
	// generation, whose consumer must not wait for it.
	if cursorGeneration(claimed) != generation {
		if index < mailboxSegmentSlots {
			segment.slots[index].state.Store(uint32(slotAbandoned))
		}
		return nil
	}

	switch {
	case index < mailboxSegmentSlots:
		return &segment.slots[index]
	case x.claimLink(segment, generation):
		x.link(segment)
	default:
		x.awaitLink(segment, generation)
	}

	return nil
}

// firstSegment installs the mailbox's first segment on the first message, as
// both its head and its tail. The head is set first, so the consumer finds
// every segment from the first one on, however many producers have filled
// before its first turn. A producer that loses the race waits for the winner to
// publish the tail and uses it.
func (x *embeddedMailbox) firstSegment() *mailboxSegment {
	segment := new(mailboxSegment)
	if x.mailboxHead.CompareAndSwap(nil, segment) {
		x.mailboxTail.Store(segment)
		return segment
	}

	for spins := 0; ; spins++ {
		if tail := x.mailboxTail.Load(); tail != nil {
			return tail
		}

		if spins == mailboxSpinLimit {
			runtime.Gosched()
			spins = 0
		}
	}
}

// claimLink reports whether the caller links the segment after full: it must
// be the first producer of generation to claim past the end of full. The
// caller's claim matched generation, so full holds more claims than slots in
// it. A segment in that state can be neither rewound, which needs every claim
// read, nor recycled, which needs it linked, so the winner links the tail of
// generation.
//
// The duty is not tied to the first index past the end. A producer that read
// an older generation and claimed after full was rewound into a new one takes
// any index, that one included, and only retries; a segment taken as the spare
// may also come back with such claims past its end. Either way the producers
// whose claims match the generation would otherwise wait for a link nobody
// makes.
func (x *embeddedMailbox) claimLink(full *mailboxSegment, generation uint64) bool {
	for {
		linked := full.linked.Load()
		if linkedInGeneration(linked, generation) {
			return false
		}

		if full.linked.CompareAndSwap(linked, generation+1) {
			return true
		}
	}
}

// link appends a segment after full, the spare when there is one, and makes it
// the tail. Only the producer that won claimLink for the generation calls it,
// so no two producers link the same segment.
func (x *embeddedMailbox) link(full *mailboxSegment) {
	next := x.embedded.spare.Swap(nil)
	if next == nil {
		next = new(mailboxSegment)
	}

	full.next.Store(next)
	x.mailboxTail.Store(next)
}

// awaitLink waits while the producer that won claimLink links the segment
// after full. It
// returns once the tail has moved on, or once full has been reused under a new
// generation, which makes the caller's view of it stale.
func (x *embeddedMailbox) awaitLink(full *mailboxSegment, generation uint64) {
	for spins := 0; x.mailboxTail.Load() == full && cursorGeneration(full.cursor.Load()) == generation; spins++ {
		if spins == mailboxSpinLimit {
			runtime.Gosched()
			spins = 0
		}
	}
}

// take empties a ready slot and returns its message as the context the turn
// dispatches.
func (x *embeddedMailbox) take(slot *mailboxSlot) *ReceiveContext {
	if received := slot.received; received != nil {
		slot.received = nil
		slot.state.Store(uint32(slotEmpty))
		x.embedded.held = received
		return received
	}

	turnContext := &x.embedded.context
	turnContext.reuse(slot.ctx, slot.sender, (*PID)(x), slot.message)
	slot.ctx, slot.message, slot.sender = nil, nil, nil
	slot.state.Store(uint32(slotEmpty))
	return turnContext
}

// rewind restarts segment at its first slot once the consumer has read every
// slot claimed in it. The compare-and-swap succeeds only if no producer has
// claimed a slot since, so the segment is empty and producers continue in it
// under the next generation.
func (x *embeddedMailbox) rewind(segment *mailboxSegment, read uint32) {
	if read == 0 {
		return
	}

	cursor := segment.cursor.Load()
	if cursorClaims(cursor) != uint64(read) {
		return
	}

	if segment.cursor.CompareAndSwap(cursor, nextGenerationCursor(cursor)) {
		x.embedded.read.Store(0)
	}
}

// recycle offers a segment the consumer has left as the spare. A segment that
// is still the tail is left alone: the producer linking its successor has not
// published the new tail yet, and producers still claim slots past its end.
func (x *embeddedMailbox) recycle(segment *mailboxSegment) {
	if x.mailboxTail.Load() == segment {
		return
	}

	segment.next.Store(nil)
	segment.cursor.Store(nextGenerationCursor(segment.cursor.Load()))
	x.embedded.spare.CompareAndSwap(nil, segment)
}

// linkedInGeneration reports whether linked, a segment's link marker, records
// a link made in generation or in a later one, so that a producer of
// generation must not link. The marker is one more than the generation last
// linked and zero before any link.
//
// A generation is 32 bits and wraps, so the two are compared by their modular
// distance, as sequence numbers are: a generation less than 2^31 behind the
// marker's is older, one less than 2^31 ahead is newer. The producer's
// generation is at most a few behind the marker's, since a producer that
// stalled through 2^31 reuses of one segment is no more plausible than one
// that stalled through the 2^32 it takes to fool the generation check.
func linkedInGeneration(linked, generation uint64) bool {
	if linked == 0 {
		return false
	}

	return int32(uint32(linked-1)-uint32(generation)) >= 0
}

// cursorGeneration returns the reuse generation a segment cursor holds.
func cursorGeneration(cursor uint64) uint64 {
	return cursor >> mailboxGenerationShift
}

// cursorClaims returns the number of slots a segment cursor counts as claimed
// in its generation.
func cursorClaims(cursor uint64) uint64 {
	return cursor & (1<<mailboxGenerationShift - 1)
}

// nextGenerationCursor returns the cursor of a segment reused after cursor: the
// next generation, with no slot claimed.
func nextGenerationCursor(cursor uint64) uint64 {
	return (cursorGeneration(cursor) + 1) << mailboxGenerationShift
}
