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

// grainMailboxSlot holds one message of the default grain mailbox. A one-way
// tell stores the message, the identity it was sent to and its context as
// values, so a local one-way send takes no pooled GrainContext. A message that
// needs a full GrainContext (an acknowledged tell, an ask, an envelope, a timer
// tick, a runtime pill) travels in received instead, in the same queue, so the
// two kinds keep their relative order.
type grainMailboxSlot struct {
	// state publishes the slot. The producer stores slotReady after writing
	// the other fields, and the consumer reads them only after loading it.
	state atomic.Uint32
	// ctx is the send context of a value message.
	ctx context.Context
	// message is the payload of a value message.
	message any
	// self is the identity a value message was sent to.
	self *GrainIdentity
	// received is the context of a message enqueued through Enqueue, nil for
	// a value message.
	received *GrainContext
}

// grainMailboxSegment is one fixed-size block of the default grain mailbox,
// the grain counterpart of mailboxSegment with the same cursor, generation and
// link protocol: producers claim slots with a fetch-and-add on cursor, a
// drained segment is reused under the next generation, and a producer whose
// claim lands after the reuse abandons its slot and retries.
type grainMailboxSegment struct {
	// cursor holds the generation in its high 32 bits and the number of slots
	// claimed in this generation in its low 32 bits.
	cursor atomic.Uint64
	// the pad keeps the producers' fetch-and-add off the line that holds next
	// and the first slots.
	_ cpu.CacheLinePad
	// next is the segment that follows this one once it is full. The first
	// producer whose claim past the end matches the generation links it.
	next atomic.Pointer[grainMailboxSegment]
	// linked is one more than the last generation in which a producer took on
	// linking next, zero before any; see mailboxSegment.linked.
	linked atomic.Uint64
	// slots holds the messages in claim order.
	slots [mailboxSegmentSlots]grainMailboxSlot
}

// grainMailboxSpareRing is a ring of drained segments, with one pusher, the
// consumer, and one popper, the producer linking a segment; see
// mailboxSpareRing.
type grainMailboxSpareRing struct {
	segments [mailboxSpareSegments]atomic.Pointer[grainMailboxSegment]
	push     atomic.Uint32
	pop      atomic.Uint32
}

// embeddedGrainMailboxState is the part of the default grain mailbox that does
// not fit in the process's two segment pointers. The consumer allocates it on
// its first dequeue of a message, so a grain that never receives one pays only
// the pointer; a grain with a bounded mailbox never has one.
type embeddedGrainMailboxState struct {
	// context is handed to the grain for every message a one-way tell stored
	// as values. Only the processing turn touches it, so one per grain is
	// enough. Its process, actor system and one-way flag are set once.
	context GrainContext
	// held is the pooled context the previous Dequeue returned. It is recycled
	// on the next Dequeue, as the previous sentinel was in the linked queue.
	held *GrainContext
	// spares holds the drained segments kept for the producers that link. The
	// consumer allocates it the first time it drains a segment it cannot
	// rewind.
	spares atomic.Pointer[grainMailboxSpareRing]
	// read is the index of the next slot to read in the head segment. Only the
	// consumer writes it; it is atomic because IsEmpty may run on a worker that
	// has just given the grain up while the next owner reads.
	read atomic.Uint32
}

// embeddedGrainMailbox is the default user mailbox of a grain, run on the
// segment pointers carried inside the process (mailboxHead and mailboxTail)
// and on the state behind embedded. It is the same lock-free multi-producer,
// single-consumer segmented queue as the actor's embeddedMailbox: messages are
// stored as values in fixed-size segments, the first segment is allocated on
// the first message, a segment read to its end is rewound in place (see
// rewind), and a longer backlog links further segments that the consumer keeps
// as spares once it has finished them. It is never bounded and keeps no length
// counter.
//
// The type is a defined type over grainPID so (*embeddedGrainMailbox)(pid) is a
// zero-allocation pointer reinterpretation. Placement of the two words belongs
// to the grainPID layout: the head sits on the turn-written line and the tail on
// the line producers read. A grain with a bounded mailbox never uses this type;
// its mailbox pointer names a standalone grainMailbox instead.
type embeddedGrainMailbox grainPID

// Enqueue appends value at the tail. Safe for concurrent producers; value must
// be a context no other mailbox holds.
func (x *embeddedGrainMailbox) Enqueue(value *GrainContext) {
	slot := x.claim()
	slot.received = value
	slot.state.Store(uint32(slotReady))
}

// Dequeue removes and returns the next GrainContext, or nil when empty. Single
// consumer only.
//
// A message enqueued with Enqueue comes back in the very context that was put
// in, which the next Dequeue recycles. A message stored by put comes back in
// the grain's own turn context, refilled for each such message. Either way the
// caller must not keep or release the returned context.
func (x *embeddedGrainMailbox) Dequeue() *GrainContext {
	state := x.embedded.Load()
	if state != nil {
		if held := state.held; held != nil {
			state.held = nil
			releaseGrainContext(held)
		}
	}

	segment := x.mailboxHead.Load()
	if segment == nil {
		return nil
	}

	if state == nil {
		state = x.newState()
	}

	for {
		read := state.read.Load()
		if read < mailboxSegmentSlots {
			slot := &segment.slots[read]
			switch mailboxSlotState(slot.state.Load()) {
			case slotEmpty:
				return nil
			case slotAbandoned:
				slot.state.Store(uint32(slotEmpty))
				state.read.Store(read + 1)
				continue
			}

			state.read.Store(read + 1)
			return x.take(state, slot)
		}

		next := segment.next.Load()
		if next == nil {
			x.rewind(state, segment)
			return nil
		}

		x.mailboxHead.Store(next)
		state.read.Store(0)
		x.recycle(state, segment)
		segment = next
	}
}

// IsEmpty reports whether the mailbox currently holds no message ready to be
// read. The result is a racy snapshot. It only loads, so a worker that has just
// given the grain up may call it while the next owner dequeues.
func (x *embeddedGrainMailbox) IsEmpty() bool {
	segment := x.mailboxHead.Load()
	if segment == nil {
		return true
	}

	var read uint32
	if state := x.embedded.Load(); state != nil {
		read = state.read.Load()
	}

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

// Len returns an approximate number of queued messages. Racy with producers
// and the consumer; for tests and diagnostics, never hot paths.
func (x *embeddedGrainMailbox) Len() int64 {
	segment := x.mailboxHead.Load()
	if segment == nil {
		return 0
	}

	var read uint64
	if state := x.embedded.Load(); state != nil {
		read = uint64(state.read.Load())
	}

	var count int64
	for segment != nil {
		claimed := min(cursorClaims(segment.cursor.Load()), mailboxSegmentSlots)
		if claimed > read {
			count += int64(claimed - read)
		}

		segment, read = segment.next.Load(), 0
	}

	return count
}

// put appends a message stored as values: the send context, the identity it
// was sent to and the payload. It is the path of a local one-way tell, which
// therefore takes no pooled GrainContext. Safe to call concurrently from
// multiple producers.
func (x *embeddedGrainMailbox) put(ctx context.Context, self *GrainIdentity, message any) {
	slot := x.claim()
	slot.ctx = ctx
	slot.message = message
	slot.self = self
	slot.state.Store(uint32(slotReady))
}

// newState allocates the consumer state on the first dequeue of a message and
// seeds the turn context with what every one-way message shares: the process,
// its actor system and the one-way flag. Only the consumer calls it.
func (x *embeddedGrainMailbox) newState() *embeddedGrainMailboxState {
	state := new(embeddedGrainMailboxState)
	state.context.pid = (*grainPID)(x)
	state.context.actorSystem = x.actorSystem
	state.context.oneWay = true
	x.embedded.Store(state)
	return state
}

// claim reserves the next slot for the calling producer, linking a new
// segment when the tail is full. The caller writes the slot and publishes it
// by storing slotReady.
func (x *embeddedGrainMailbox) claim() *grainMailboxSlot {
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
// read of generation and its claim. The tail check comes after the read of
// generation, for the reason given on embeddedMailbox.claimIn.
func (x *embeddedGrainMailbox) claimIn(segment *grainMailboxSegment, generation uint64) *grainMailboxSlot {
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
// every segment from the first one on. A producer that loses the race waits
// for the winner to publish the tail and uses it.
func (x *embeddedGrainMailbox) firstSegment() *grainMailboxSegment {
	segment := new(grainMailboxSegment)
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
// be the first producer of generation to claim past the end of full; see
// embeddedMailbox.claimLink.
func (x *embeddedGrainMailbox) claimLink(full *grainMailboxSegment, generation uint64) bool {
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

// link appends a segment after full, a spare when there is one, and makes it
// the tail. Only the producer that won claimLink for the generation calls it,
// so no two producers link the same segment.
func (x *embeddedGrainMailbox) link(full *grainMailboxSegment) {
	var next *grainMailboxSegment
	if state := x.embedded.Load(); state != nil {
		next = state.takeSpare()
	}

	if next == nil {
		next = new(grainMailboxSegment)
	}

	full.next.Store(next)
	x.mailboxTail.Store(next)
}

// awaitLink waits while the producer that won claimLink links the segment
// after full. It returns once the tail has moved on, or once full has been
// reused under a new generation, which makes the caller's view of it stale.
func (x *embeddedGrainMailbox) awaitLink(full *grainMailboxSegment, generation uint64) {
	for spins := 0; x.mailboxTail.Load() == full && cursorGeneration(full.cursor.Load()) == generation; spins++ {
		if spins == mailboxSpinLimit {
			runtime.Gosched()
			spins = 0
		}
	}
}

// take empties a ready slot and returns its message as the context the turn
// dispatches.
func (x *embeddedGrainMailbox) take(state *embeddedGrainMailboxState, slot *grainMailboxSlot) *GrainContext {
	if received := slot.received; received != nil {
		slot.received = nil
		slot.state.Store(uint32(slotEmpty))
		state.held = received
		return received
	}

	turnContext := &state.context
	turnContext.reuseOneWay(slot.ctx, slot.self, slot.message)
	slot.ctx, slot.message, slot.self = nil, nil, nil
	slot.state.Store(uint32(slotEmpty))
	return turnContext
}

// rewind restarts segment at its first slot once the consumer has read every
// slot in it and no producer has linked the next segment. The compare-and-swap
// succeeds only if no producer has claimed past the end since, so the segment
// is empty and producers continue in it under the next generation.
//
// Unlike the actor mailbox, which rewinds a segment whenever the consumer has
// read every slot claimed in it, the grain mailbox rewinds only at the end of
// a segment. A grain answering one message at a time would otherwise
// compare-and-swap the cursor on every empty dequeue, and the producer's next
// claim would then fetch that line back from the consumer's core: one cache
// line bouncing per message on the acknowledged tell and the ask. Reading on
// to the end costs nothing, the slots are there anyway, and the segment is
// still never left for a new one while nobody has filled it.
func (x *embeddedGrainMailbox) rewind(state *embeddedGrainMailboxState, segment *grainMailboxSegment) {
	cursor := segment.cursor.Load()
	if cursorClaims(cursor) != mailboxSegmentSlots {
		return
	}

	if segment.cursor.CompareAndSwap(cursor, nextGenerationCursor(cursor)) {
		state.read.Store(0)
	}
}

// recycle offers a segment the consumer has left as a spare. A segment that
// is still the tail is left alone: the producer linking its successor has not
// published the new tail yet, and producers still claim slots past its end.
// A segment the spares have no room for is left to the garbage collector.
func (x *embeddedGrainMailbox) recycle(state *embeddedGrainMailboxState, segment *grainMailboxSegment) {
	if x.mailboxTail.Load() == segment {
		return
	}

	segment.next.Store(nil)
	segment.cursor.Store(nextGenerationCursor(segment.cursor.Load()))
	state.offerSpare(segment)
}

// takeSpare pops the oldest spare segment, or returns nil when there is none.
// Only the producer that links calls it, one link at a time.
func (x *embeddedGrainMailboxState) takeSpare() *grainMailboxSegment {
	ring := x.spares.Load()
	if ring == nil {
		return nil
	}

	pop := ring.pop.Load()
	if pop == ring.push.Load() {
		return nil
	}

	segment := ring.segments[pop%mailboxSpareSegments].Swap(nil)
	ring.pop.Store(pop + 1)
	return segment
}

// offerSpare pushes a drained segment onto the spares, allocating the ring on
// the first push, and reports whether there was room for it. Only the consumer
// calls it.
func (x *embeddedGrainMailboxState) offerSpare(segment *grainMailboxSegment) bool {
	ring := x.spares.Load()
	if ring == nil {
		ring = new(grainMailboxSpareRing)
		x.spares.Store(ring)
	}

	push := ring.push.Load()
	if push-ring.pop.Load() == mailboxSpareSegments {
		return false
	}

	ring.segments[push%mailboxSpareSegments].Store(segment)
	ring.push.Store(push + 1)
	return true
}
