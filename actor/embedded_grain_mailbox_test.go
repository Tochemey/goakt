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
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// TestEmbeddedGrainMailboxFIFOOrder verifies that messages come out in the
// order they were enqueued, across more than one segment, and that each dequeue
// returns the very context that was put in.
func TestEmbeddedGrainMailboxFIFOOrder(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	require.True(t, mailbox.IsEmpty())
	require.Zero(t, mailbox.Len())

	const count = 2*mailboxSegmentSlots + 5
	in := make([]*GrainContext, count)

	for i := range count {
		in[i] = embeddedGrainMailboxMessage(i)
		mailbox.Enqueue(in[i])
	}

	require.False(t, mailbox.IsEmpty())
	require.EqualValues(t, count, mailbox.Len())

	for i := range count {
		out := mailbox.Dequeue()
		require.Same(t, in[i], out)
		require.Equal(t, i, out.Message())
		require.EqualValues(t, count-i-1, mailbox.Len())
	}

	require.Nil(t, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty())
}

// TestEmbeddedGrainMailboxValuesAndContextsKeepOrder verifies that messages
// stored as values and messages enqueued in a context share one queue: they
// come out in the order they went in, a value message in the grain's turn
// context, seeded as a one-way message to the grain, and an enqueued one in its
// own context.
func TestEmbeddedGrainMailboxValuesAndContextsKeepOrder(t *testing.T) {
	system := &actorSystem{logger: log.DiscardLogger}
	mailbox := newEmbeddedGrainMailbox()
	mailbox.actorSystem = system
	self := &GrainIdentity{kind: "kind", name: "name"}
	ctx := context.WithValue(context.Background(), mailboxTestCtxKey{}, "v")

	enqueued := embeddedGrainMailboxMessage("enqueued")
	mailbox.put(ctx, self, "first")
	mailbox.Enqueue(enqueued)
	mailbox.put(context.Background(), nil, "third")

	first := mailbox.Dequeue()
	state := mailbox.embedded.Load()
	require.NotNil(t, state, "the first dequeue of a message allocates the state")
	require.Same(t, &state.context, first)
	require.Equal(t, "first", first.Message())
	require.Same(t, self, first.Self())
	require.Equal(t, "v", first.Context().Value(mailboxTestCtxKey{}))
	require.True(t, first.oneWay)
	require.Same(t, (*grainPID)(mailbox), first.pid)
	require.Same(t, system, first.actorSystem)

	require.Same(t, enqueued, mailbox.Dequeue())

	third := mailbox.Dequeue()
	require.Same(t, &state.context, third)
	require.Equal(t, "third", third.Message())
	require.Nil(t, third.self)

	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedGrainMailboxRecyclesHeldContext verifies the release protocol of
// an enqueued context: the mailbox holds it after returning it and recycles it
// on the next dequeue, an empty one included, while a value message leaves no
// context behind.
func TestEmbeddedGrainMailboxRecyclesHeldContext(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()

	held := embeddedGrainMailboxMessage("held")
	mailbox.Enqueue(held)
	require.Same(t, held, mailbox.Dequeue())
	require.Same(t, held, mailbox.embedded.Load().held)

	require.Nil(t, mailbox.Dequeue())
	require.Nil(t, mailbox.embedded.Load().held)
	require.Nil(t, held.Message(), "the held context must be reset when it is recycled")

	mailbox.put(context.Background(), nil, "value")
	require.NotNil(t, mailbox.Dequeue())
	require.Nil(t, mailbox.embedded.Load().held, "a value message must leave no context to recycle")
}

// TestEmbeddedGrainMailboxTakeClearsTheSlot verifies that a dequeued slot keeps
// no reference to its message, its identity, its context or its GrainContext,
// so a segment kept for reuse does not keep old messages alive.
func TestEmbeddedGrainMailboxTakeClearsTheSlot(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	mailbox.put(context.WithValue(context.Background(), mailboxTestCtxKey{}, "v"), &GrainIdentity{}, "value")
	mailbox.Enqueue(embeddedGrainMailboxMessage("enqueued"))
	segment := mailbox.mailboxHead.Load()

	require.Equal(t, "value", mailbox.Dequeue().Message())
	require.Equal(t, "enqueued", mailbox.Dequeue().Message())

	for i := range 2 {
		slot := &segment.slots[i]
		require.Equal(t, uint32(slotEmpty), slot.state.Load(), "slot %d", i)
		require.Nil(t, slot.ctx, "slot %d", i)
		require.Nil(t, slot.message, "slot %d", i)
		require.Nil(t, slot.self, "slot %d", i)
		require.Nil(t, slot.received, "slot %d", i)
	}
}

// TestEmbeddedGrainMailboxAllocatesOnFirstMessage verifies that a new mailbox
// holds no segment and no state until a message arrives, that it reports the
// message before its consumer has dequeued anything, and that the state
// appears with the first dequeue of a message.
func TestEmbeddedGrainMailboxAllocatesOnFirstMessage(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	require.Nil(t, mailbox.mailboxHead.Load())
	require.Nil(t, mailbox.mailboxTail.Load())
	require.Nil(t, mailbox.embedded.Load())
	require.True(t, mailbox.IsEmpty())
	require.Zero(t, mailbox.Len())
	require.Nil(t, mailbox.Dequeue())
	require.Nil(t, mailbox.embedded.Load(), "an empty dequeue allocates nothing")

	mailbox.put(context.Background(), nil, "first")
	require.NotNil(t, mailbox.mailboxTail.Load())
	require.Same(t, mailbox.mailboxTail.Load(), mailbox.mailboxHead.Load(), "the first segment is both head and tail")
	require.Nil(t, mailbox.embedded.Load(), "producers never allocate the state")
	require.False(t, mailbox.IsEmpty())
	require.EqualValues(t, 1, mailbox.Len())
	require.Equal(t, "first", mailbox.Dequeue().Message())
	require.NotNil(t, mailbox.embedded.Load())
}

// TestEmbeddedGrainMailboxBacklogBeforeFirstTurn verifies that messages that
// fill several segments before the consumer's first dequeue all come out, from
// the first segment on.
func TestEmbeddedGrainMailboxBacklogBeforeFirstTurn(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	const count = 3*mailboxSegmentSlots + 1
	for i := range count {
		mailbox.put(context.Background(), nil, i)
	}

	require.NotSame(t, mailbox.mailboxHead.Load(), mailbox.mailboxTail.Load())
	require.EqualValues(t, count, mailbox.Len())
	for i := range count {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedGrainMailboxFirstSegmentRaceKeepsWinner verifies that a producer
// that loses the race to install the first segment waits for the winner to
// publish the tail and uses the winner's segment.
func TestEmbeddedGrainMailboxFirstSegmentRaceKeepsWinner(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	winner := new(grainMailboxSegment)
	mailbox.mailboxHead.Store(winner)

	got := make(chan *grainMailboxSegment, 1)
	go func() { got <- mailbox.firstSegment() }()

	pause.For(10 * time.Millisecond)
	require.Empty(t, got, "the loser must wait for the winner's tail")
	mailbox.mailboxTail.Store(winner)

	select {
	case segment := <-got:
		require.Same(t, winner, segment)
	case <-time.After(time.Second):
		t.Fatal("the loser never saw the published tail")
	}

	require.Same(t, winner, mailbox.mailboxHead.Load())
}

// TestEmbeddedGrainMailboxRewindsSegmentReadToItsEnd verifies that a segment
// is reused from its first slot under a new generation once the consumer has
// read every slot in it, instead of being followed by a new one, and that a
// segment drained before its end is read on, not rewound: the consumer never
// writes the cursor while a grain answers one message at a time.
func TestEmbeddedGrainMailboxRewindsSegmentReadToItsEnd(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	mailbox.put(context.Background(), nil, "first")
	segment := mailbox.mailboxTail.Load()
	generation := grainMailboxGeneration(segment)

	require.Equal(t, "first", mailbox.Dequeue().Message())
	require.Nil(t, mailbox.Dequeue())
	require.Equal(t, generation, grainMailboxGeneration(segment), "a segment drained before its end is not rewound")
	require.EqualValues(t, 1, mailbox.embedded.Load().read.Load(), "the consumer reads on")

	for i := 1; i < mailboxSegmentSlots; i++ {
		mailbox.put(context.Background(), nil, i)
		require.Equal(t, i, mailbox.Dequeue().Message())
		require.Same(t, segment, mailbox.mailboxTail.Load(), "i=%d: the segment is still in use", i)
	}

	require.Nil(t, mailbox.Dequeue())
	require.Equal(t, generation+1, grainMailboxGeneration(segment), "a segment read to its end is rewound")
	require.Zero(t, mailbox.embedded.Load().read.Load())
	require.Nil(t, segment.next.Load())

	mailbox.put(context.Background(), nil, "again")
	require.Same(t, segment, mailbox.mailboxTail.Load(), "the rewound segment must be reused")
	require.Equal(t, uint32(slotReady), segment.slots[0].state.Load(), "from its first slot")
	require.Equal(t, "again", mailbox.Dequeue().Message())
}

// TestEmbeddedGrainMailboxDoesNotRewindPastAClaim verifies that the consumer
// leaves a segment it has read to its end alone while a producer has claimed
// past that end and not linked yet, and finds the message once the link is
// made.
func TestEmbeddedGrainMailboxDoesNotRewindPastAClaim(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	segment := mailbox.mailboxTail.Load()
	generation := grainMailboxGeneration(segment)
	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	// a producer has claimed past the end and is about to link
	segment.cursor.Add(1)
	require.Nil(t, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty(), "a claim past the end holds no message yet")
	require.Equal(t, generation, grainMailboxGeneration(segment), "a claim past the end must stop the rewind")
	require.EqualValues(t, mailboxSegmentSlots, mailbox.embedded.Load().read.Load())

	mailbox.link(segment)
	mailbox.put(context.Background(), nil, "late")
	require.False(t, mailbox.IsEmpty())
	require.Equal(t, "late", mailbox.Dequeue().Message())
	require.Same(t, segment, firstGrainSpare(mailbox), "the finished segment must become the spare")
}

// TestEmbeddedGrainMailboxReusesSpareSegment verifies that a backlog longer
// than one segment links further segments, that each segment the consumer
// finishes is kept as the spare, and that the next link takes the spare instead
// of allocating.
func TestEmbeddedGrainMailboxReusesSpareSegment(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	for i := range mailboxSegmentSlots + 1 {
		mailbox.put(context.Background(), nil, i)
	}

	first := mailbox.mailboxHead.Load()
	second := mailbox.mailboxTail.Load()
	require.NotSame(t, first, second)
	require.Same(t, second, first.next.Load(), "the last message must have linked a second segment")

	for i := 0; i <= mailboxSegmentSlots; i++ {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Same(t, second, mailbox.mailboxHead.Load())
	require.Same(t, first, firstGrainSpare(mailbox), "the finished segment must become the spare")
	require.Nil(t, first.next.Load())

	// one message is in the second segment's first slot: fill it and overflow
	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	require.Same(t, first, second.next.Load(), "the link must take the spare")
	require.Same(t, first, mailbox.mailboxTail.Load())
	require.Nil(t, firstGrainSpare(mailbox))

	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedGrainMailboxRecycleSkipsSegmentStillTail verifies that the
// consumer does not reuse a segment while it is still the tail, the window in
// which the producer linking its successor has not published the new tail.
func TestEmbeddedGrainMailboxRecycleSkipsSegmentStillTail(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	state := mailbox.newState()
	full := new(grainMailboxSegment)
	full.cursor.Store(mailboxSegmentSlots + 1)
	full.next.Store(new(grainMailboxSegment))
	mailbox.mailboxTail.Store(full)

	mailbox.recycle(state, full)
	require.Nil(t, firstGrainSpare(mailbox))
	require.Zero(t, grainMailboxGeneration(full))
	require.NotNil(t, full.next.Load())

	mailbox.mailboxTail.Store(full.next.Load())
	mailbox.recycle(state, full)
	require.Same(t, full, firstGrainSpare(mailbox))
	require.Equal(t, uint64(1), grainMailboxGeneration(full))
	require.Zero(t, cursorClaims(full.cursor.Load()))
	require.Nil(t, full.next.Load())
}

// TestEmbeddedGrainMailboxIsEmptyAfterReadingAFullSegment verifies that a
// mailbox whose consumer has read every slot of its only segment reports empty
// before the next Dequeue rewinds that segment, and not empty once a message
// links the next segment.
func TestEmbeddedGrainMailboxIsEmptyAfterReadingAFullSegment(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.EqualValues(t, mailboxSegmentSlots, mailbox.embedded.Load().read.Load())
	require.True(t, mailbox.IsEmpty(), "a fully read segment holds no message")
	require.Zero(t, mailbox.Len())

	mailbox.put(context.Background(), nil, "next")
	require.NotNil(t, mailbox.mailboxHead.Load().next.Load(), "the message must link the next segment")
	require.False(t, mailbox.IsEmpty())
	require.EqualValues(t, 1, mailbox.Len())
	require.Equal(t, "next", mailbox.Dequeue().Message())
}

// TestEmbeddedGrainMailboxStaleClaimIsAbandoned verifies the generation check:
// a producer whose claim lands after the segment was reused marks the slot it
// claimed abandoned and retries, and the consumer skips that slot.
func TestEmbeddedGrainMailboxStaleClaimIsAbandoned(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	segment := mailbox.mailboxTail.Load()
	stale := grainMailboxGeneration(segment)

	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Nil(t, mailbox.Dequeue())
	require.Equal(t, stale+1, grainMailboxGeneration(segment))

	require.Nil(t, mailbox.claimIn(segment, stale), "a claim under a stale generation must be retried")
	require.Equal(t, uint32(slotAbandoned), segment.slots[0].state.Load())
	require.True(t, mailbox.IsEmpty(), "an abandoned slot holds no message")

	mailbox.put(context.Background(), nil, "after")
	require.False(t, mailbox.IsEmpty())
	require.Equal(t, "after", mailbox.Dequeue().Message())
	require.Equal(t, uint32(slotEmpty), segment.slots[0].state.Load(), "the consumer must clear the abandoned slot")
	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedGrainMailboxClaimRefusesSegmentNoLongerTail verifies that a
// producer that loaded a segment as the tail, stalled while the consumer
// finished it and kept it as the spare, and then read the spare's new
// generation, claims nothing in it: the spare is not linked into the queue.
func TestEmbeddedGrainMailboxClaimRefusesSegmentNoLongerTail(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	for i := range mailboxSegmentSlots + 1 {
		mailbox.put(context.Background(), nil, i)
	}

	// the stalled producer loaded the first segment while it was the tail
	stale := mailbox.mailboxHead.Load()
	for i := range mailboxSegmentSlots + 1 {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Same(t, stale, firstGrainSpare(mailbox), "the finished segment must be the spare")
	cursor := stale.cursor.Load()

	require.Nil(t, mailbox.claimIn(stale, grainMailboxGeneration(stale)), "a claim in a segment that is no longer the tail must be retried")
	require.Equal(t, cursor, stale.cursor.Load(), "the spare must not be claimed in")
	for i := range stale.slots {
		require.Equal(t, uint32(slotEmpty), stale.slots[i].state.Load())
	}

	mailbox.put(context.Background(), nil, "next")
	require.Equal(t, "next", mailbox.Dequeue().Message())
}

// TestEmbeddedGrainMailboxStaleClaimPastTheEnd verifies that a stale claim past
// the end of a reused segment marks nothing, since the slot does not exist, and
// links nothing, since the segment it read is gone.
func TestEmbeddedGrainMailboxStaleClaimPastTheEnd(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	segment := new(grainMailboxSegment)
	segment.cursor.Store(1<<mailboxGenerationShift | mailboxSegmentSlots)
	mailbox.mailboxTail.Store(segment)

	require.Nil(t, mailbox.claimIn(segment, 0))
	for i := range segment.slots {
		require.Equal(t, uint32(slotEmpty), segment.slots[i].state.Load())
	}

	require.Nil(t, segment.next.Load(), "a stale claim must not link")
	require.Same(t, segment, mailbox.mailboxTail.Load())
}

// TestEmbeddedGrainMailboxStaleClaimOnTheLinkingIndex verifies that a stale
// claim that takes the first index past the end under the new generation only
// retries, and that the next producer links the segment instead of waiting for
// that claim to do it.
func TestEmbeddedGrainMailboxStaleClaimOnTheLinkingIndex(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	segment := mailbox.mailboxTail.Load()
	stale := grainMailboxGeneration(segment)

	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Nil(t, mailbox.Dequeue())
	require.Equal(t, stale+1, grainMailboxGeneration(segment), "the segment read to its end must be rewound")

	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	require.Nil(t, mailbox.claimIn(segment, stale))
	require.EqualValues(t, mailboxSegmentSlots+1, cursorClaims(segment.cursor.Load()), "the stale claim must take the first index past the end")
	require.Nil(t, segment.next.Load(), "a stale claim must not link")

	requireGrainPutReturns(t, mailbox, "after")
	require.NotSame(t, segment, mailbox.mailboxTail.Load(), "the next producer must link the segment")

	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Equal(t, "after", mailbox.Dequeue().Message())
	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedGrainMailboxLinksPastADelayedLinker verifies that a producer
// that claimed the first index past the end and has not linked yet does not
// hold up the next one, which links, and that the delayed producer then only
// retries.
func TestEmbeddedGrainMailboxLinksPastADelayedLinker(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	segment := mailbox.mailboxTail.Load()
	generation := grainMailboxGeneration(segment)

	// the delayed producer has claimed the first index past the end
	segment.cursor.Add(1)

	requireGrainPutReturns(t, mailbox, "after")
	require.False(t, mailbox.claimLink(segment, generation), "the delayed producer must not link a second time")

	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Equal(t, "after", mailbox.Dequeue().Message())
}

// TestEmbeddedGrainMailboxLinksPastASpareFullOfStaleClaims verifies that a
// spare segment linked in with every slot already abandoned by stale producers
// is linked past by the first producer that claims in it, and that the consumer
// skips it.
func TestEmbeddedGrainMailboxLinksPastASpareFullOfStaleClaims(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	for i := range mailboxSegmentSlots + 1 {
		mailbox.put(context.Background(), nil, i)
	}

	for i := range mailboxSegmentSlots + 1 {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	spare := firstGrainSpare(mailbox)
	require.NotNil(t, spare, "the finished segment must be the spare")

	// stale producers claimed every slot of the spare and one past its end
	spare.cursor.Add(mailboxSegmentSlots + 1)
	for i := range spare.slots {
		spare.slots[i].state.Store(uint32(slotAbandoned))
	}

	// fill the tail, so the next message links the spare and then claims in it
	for i := range mailboxSegmentSlots - 1 {
		mailbox.put(context.Background(), nil, i)
	}

	requireGrainPutReturns(t, mailbox, "after")
	require.Same(t, spare, mailbox.mailboxHead.Load().next.Load(), "the spare must be linked first")
	require.NotSame(t, spare, mailbox.mailboxTail.Load(), "the spare must be linked past")

	for i := range mailboxSegmentSlots - 1 {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Equal(t, "after", mailbox.Dequeue().Message(), "the consumer must skip the abandoned spare")
	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedGrainMailboxClaimLink verifies that one producer per generation
// takes on linking, that a producer of an older generation never does, and that
// the generation after the last one, which wraps to zero, links again.
func TestEmbeddedGrainMailboxClaimLink(t *testing.T) {
	const lastGeneration = 1<<mailboxGenerationShift - 1

	mailbox := newEmbeddedGrainMailbox()
	segment := new(grainMailboxSegment)

	require.True(t, mailbox.claimLink(segment, 0))
	require.False(t, mailbox.claimLink(segment, 0), "a second producer of the generation must wait")

	require.True(t, mailbox.claimLink(segment, 3), "a later generation links again")
	require.False(t, mailbox.claimLink(segment, 1), "an older generation must not link")
	require.False(t, mailbox.claimLink(segment, 3))

	wrapping := new(grainMailboxSegment)
	require.True(t, mailbox.claimLink(wrapping, lastGeneration), "the last generation links")
	require.True(t, mailbox.claimLink(wrapping, 0), "the generation after the last one, which wraps to zero, links")
	require.False(t, mailbox.claimLink(wrapping, lastGeneration), "the last generation is older than the wrapped one")
	require.False(t, mailbox.claimLink(wrapping, 0))
}

// TestEmbeddedGrainMailboxLinksAfterGenerationWrap verifies that a segment
// whose generation wraps from the last one to zero is still linked when it
// fills.
func TestEmbeddedGrainMailboxLinksAfterGenerationWrap(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	segment := mailbox.mailboxTail.Load()
	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Nil(t, mailbox.Dequeue())
	require.Zero(t, mailbox.embedded.Load().read.Load(), "the segment read to its end must be rewound")

	// the segment has been reused into the last generation
	segment.cursor.Store((1<<mailboxGenerationShift - 1) << mailboxGenerationShift)
	for i := range mailboxSegmentSlots + 1 {
		mailbox.put(context.Background(), nil, i)
	}

	require.NotSame(t, segment, mailbox.mailboxTail.Load(), "the segment must be linked in the last generation")
	for i := range mailboxSegmentSlots + 1 {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Same(t, segment, firstGrainSpare(mailbox), "the finished segment must be the spare")
	require.Zero(t, grainMailboxGeneration(segment), "recycling must wrap the generation to zero")

	// fill the tail, so the spare is linked back in, then fill the spare
	for i := range mailboxSegmentSlots - 1 {
		mailbox.put(context.Background(), nil, i)
	}

	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	require.Same(t, segment, mailbox.mailboxTail.Load())
	requireGrainPutReturns(t, mailbox, "after the wrap")
	require.NotSame(t, segment, mailbox.mailboxTail.Load(), "the segment must be linked in the wrapped generation")

	for i := range mailboxSegmentSlots - 1 {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Equal(t, "after the wrap", mailbox.Dequeue().Message())
	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedGrainMailboxSegmentFitsTheSizeClass verifies that a grain segment
// fits the 2 KiB allocation size class mailboxSegmentSlots is chosen for.
func TestEmbeddedGrainMailboxSegmentFitsTheSizeClass(t *testing.T) {
	var segment grainMailboxSegment
	require.LessOrEqual(t, unsafe.Sizeof(segment), uintptr(2048))
}

// TestEmbeddedGrainMailboxAwaitLinkReturns verifies that a producer waiting for
// the next segment of a full one stops waiting when the tail moves on, and also
// when the segment is reused under a new generation.
func TestEmbeddedGrainMailboxAwaitLinkReturns(t *testing.T) {
	t.Run("when the tail moves on", func(t *testing.T) {
		mailbox := newEmbeddedGrainMailbox()
		full := new(grainMailboxSegment)
		mailbox.mailboxTail.Store(full)

		done := make(chan struct{})
		go func() {
			mailbox.awaitLink(full, 0)
			close(done)
		}()

		pause.For(10 * time.Millisecond)
		mailbox.mailboxTail.Store(new(grainMailboxSegment))
		require.Eventually(t, func() bool {
			select {
			case <-done:
				return true
			default:
				return false
			}
		}, time.Second, time.Millisecond)
	})

	t.Run("when the segment is reused", func(t *testing.T) {
		mailbox := newEmbeddedGrainMailbox()
		full := new(grainMailboxSegment)
		full.cursor.Store(1 << mailboxGenerationShift)
		mailbox.mailboxTail.Store(full)

		mailbox.awaitLink(full, 0)
		require.Same(t, full, mailbox.mailboxTail.Load())
	})
}

// TestEmbeddedGrainMailboxConcurrentProducers drains the mailbox from a single
// consumer while many producers enqueue into it, half as values and half in
// contexts, and checks that every message arrives exactly once and that each
// producer's messages keep their order. Producers yield now and then, so they
// stall between loading the tail and claiming a slot, and the consumer pauses
// now and then, so the backlog spans segments and segments are rewound,
// recycled and taken as the spare while producers run. Run under the race
// detector.
func TestEmbeddedGrainMailboxConcurrentProducers(t *testing.T) {
	const producers, perProducer = 16, 5000

	mailbox := newEmbeddedGrainMailbox()
	var wg sync.WaitGroup
	wg.Add(producers)

	for p := range producers {
		go func() {
			defer wg.Done()

			for i := range perProducer {
				if i%7 == 0 {
					runtime.Gosched()
				}

				message := [2]int{p, i}
				if p%2 == 0 {
					mailbox.put(context.Background(), nil, message)
					continue
				}

				mailbox.Enqueue(embeddedGrainMailboxMessage(message))
			}
		}()
	}

	next := make([]int, producers)
	received := 0

	for received < producers*perProducer {
		if received%97 == 0 {
			runtime.Gosched()
		}

		gctx := mailbox.Dequeue()
		if gctx == nil {
			runtime.Gosched()
			continue
		}

		id := gctx.Message().([2]int)
		require.Equal(t, next[id[0]], id[1], "producer %d delivered out of order", id[0])
		next[id[0]]++
		received++
	}

	wg.Wait()
	require.Nil(t, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty())
	require.Zero(t, mailbox.Len())
}

// TestEmbeddedGrainMailboxKeepsSeveralSpares verifies that every segment a
// backlog deeper than two segments drains is kept, in link order, and that the
// links that follow reuse them oldest first instead of allocating.
func TestEmbeddedGrainMailboxKeepsSeveralSpares(t *testing.T) {
	const segments = 4
	mailbox := newEmbeddedGrainMailbox()
	for i := range segments * mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	var linked []*grainMailboxSegment
	for segment := mailbox.mailboxHead.Load(); segment != nil; segment = segment.next.Load() {
		linked = append(linked, segment)
	}
	require.Len(t, linked, segments)

	for i := range segments * mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	last := linked[segments-1]
	require.Same(t, last, mailbox.mailboxHead.Load(), "the last segment stays, rewound")
	require.Same(t, last, mailbox.mailboxTail.Load())
	require.Equal(t, linked[:segments-1], grainSpareSegments(mailbox), "every drained segment must be kept")

	for i := range segments * mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	require.Same(t, linked[0], last.next.Load(), "the first link must take the oldest spare")
	require.Same(t, linked[1], linked[0].next.Load())
	require.Same(t, linked[2], linked[1].next.Load())
	require.Empty(t, grainSpareSegments(mailbox))

	for i := range segments * mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedGrainMailboxDropsSparesBeyondTheCap verifies that the spares
// keep at most mailboxSpareSegments drained segments and leave the rest to the
// garbage collector.
func TestEmbeddedGrainMailboxDropsSparesBeyondTheCap(t *testing.T) {
	const segments = mailboxSpareSegments + 3
	mailbox := newEmbeddedGrainMailbox()
	for i := range segments * mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	var linked []*grainMailboxSegment
	for segment := mailbox.mailboxHead.Load(); segment != nil; segment = segment.next.Load() {
		linked = append(linked, segment)
	}
	require.Len(t, linked, segments)

	for i := range segments * mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	// the last segment stays as the tail; of the others, the first
	// mailboxSpareSegments are kept and the rest dropped
	require.Equal(t, linked[:mailboxSpareSegments], grainSpareSegments(mailbox))
	state := mailbox.embedded.Load()
	require.False(t, state.offerSpare(new(grainMailboxSegment)), "a full ring must refuse")

	require.NotNil(t, state.takeSpare())
	require.True(t, state.offerSpare(linked[0]), "a pop must make room")
}

// TestEmbeddedGrainMailboxSteadyBacklogAllocatesNothing verifies that a grain
// whose backlog repeatedly spans several segments allocates no segment and no
// context once the spares hold the segments, which is what keeps a grain under
// a one-way stream off the garbage collector.
func TestEmbeddedGrainMailboxSteadyBacklogAllocatesNothing(t *testing.T) {
	const backlog = 100
	mailbox := newEmbeddedGrainMailbox()
	cycle := func() {
		for i := range backlog {
			mailbox.put(context.Background(), nil, i)
		}

		for range backlog {
			mailbox.Dequeue()
		}
	}

	cycle() // links the segments the backlog needs
	require.Zero(t, testing.AllocsPerRun(20, cycle))
}

// TestEmbeddedGrainMailboxDefaultGrainUsesIt verifies that a grain activated
// without a capacity runs on the embedded mailbox, its first segment and state
// appearing with its first message, while a grain given a capacity runs on a
// standalone mailbox and never touches the embedded words.
func TestEmbeddedGrainMailboxDefaultGrainUsesIt(t *testing.T) {
	ctx := context.TODO()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))

	t.Cleanup(func() {
		require.NoError(t, sys.Stop(ctx))
	})

	identity, err := GrainOf[*MockGrain](ctx, sys, "default")
	require.NoError(t, err)

	pid, ok := sys.(*actorSystem).grains.Get(identity.String())
	require.True(t, ok)
	require.Nil(t, pid.boundedMailbox)
	require.Nil(t, pid.mailboxTail.Load(), "no segment before the first message")
	require.Nil(t, pid.embedded.Load(), "no state before the first message")

	require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay()))
	require.Eventually(t, func() bool {
		return pid.processedCount.Load() > 0
	}, time.Second, time.Millisecond)

	require.NotNil(t, pid.mailboxTail.Load())
	require.NotNil(t, pid.embedded.Load())
	require.True(t, pid.mailboxEmpty())

	bounded, err := GrainOf[*MockGrain](ctx, sys, "bounded", WithGrainMailboxCapacity(4))
	require.NoError(t, err)

	boundedPID, ok := sys.(*actorSystem).grains.Get(bounded.String())
	require.True(t, ok)
	require.NotNil(t, boundedPID.boundedMailbox)

	require.NoError(t, sys.TellGrain(ctx, bounded, new(testpb.TestSend), WithOneWay()))
	require.Eventually(t, func() bool {
		return boundedPID.processedCount.Load() > 0
	}, time.Second, time.Millisecond)

	require.Nil(t, boundedPID.mailboxHead.Load())
	require.Nil(t, boundedPID.mailboxTail.Load())
	require.Nil(t, boundedPID.embedded.Load())
}
