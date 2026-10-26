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

// TestEmbeddedMailboxFIFOOrder verifies that messages come out in the order they
// were enqueued, across more than one segment, and that each dequeue returns
// the very context that was put in.
func TestEmbeddedMailboxFIFOOrder(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	require.True(t, mailbox.IsEmpty())

	const count = 2*mailboxSegmentSlots + 5
	in := make([]*ReceiveContext, count)
	for i := range count {
		in[i] = embeddedMailboxMessage(i)
		require.NoError(t, mailbox.Enqueue(in[i]))
	}

	require.False(t, mailbox.IsEmpty())
	require.Equal(t, int64(count), mailbox.Len())

	for i := range count {
		out := mailbox.Dequeue()
		require.Same(t, in[i], out)
		require.Equal(t, i, out.Message())
	}

	require.Nil(t, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty())
	require.Zero(t, mailbox.Len())
	mailbox.Dispose()
}

// TestEmbeddedMailboxValuesAndContextsKeepOrder verifies that messages stored as
// values and messages enqueued in a context share one queue: they come out in
// the order they went in, a value message in the actor's turn context and an
// enqueued one in its own context.
func TestEmbeddedMailboxValuesAndContextsKeepOrder(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	self := (*PID)(mailbox)
	sender := &PID{}
	ctx := context.WithValue(context.Background(), mailboxTestCtxKey{}, "v")

	enqueued := embeddedMailboxMessage("enqueued")
	mailbox.put(ctx, sender, "first")
	require.NoError(t, mailbox.Enqueue(enqueued))
	mailbox.put(context.Background(), nil, "third")

	first := mailbox.Dequeue()
	require.Same(t, &mailbox.embedded.context, first)
	require.Equal(t, "first", first.Message())
	require.Same(t, sender, first.Sender())
	require.Same(t, self, first.Self())
	require.Equal(t, "v", first.Context().Value(mailboxTestCtxKey{}))

	require.Same(t, enqueued, mailbox.Dequeue())

	third := mailbox.Dequeue()
	require.Same(t, &mailbox.embedded.context, third)
	require.Equal(t, "third", third.Message())
	require.Nil(t, third.sender)
	require.Same(t, self, third.Self())

	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedMailboxRecyclesHeldContext verifies the release protocol of an
// enqueued context: the mailbox holds it after returning it and recycles it on
// the next dequeue, an empty one included, while a value message leaves no
// context behind.
func TestEmbeddedMailboxRecyclesHeldContext(t *testing.T) {
	mailbox := newEmbeddedMailbox()

	held := embeddedMailboxMessage("held")
	require.NoError(t, mailbox.Enqueue(held))
	require.Same(t, held, mailbox.Dequeue())
	require.Same(t, held, mailbox.embedded.held)

	require.Nil(t, mailbox.Dequeue())
	require.Nil(t, mailbox.embedded.held)
	require.Nil(t, held.Message(), "the held context must be reset when it is recycled")

	mailbox.put(context.Background(), nil, "value")
	require.NotNil(t, mailbox.Dequeue())
	require.Nil(t, mailbox.embedded.held, "a value message must leave no context to recycle")
}

// TestEmbeddedMailboxTakeClearsTheSlot verifies that a dequeued slot keeps no
// reference to its message, its sender, its context or its ReceiveContext, so
// a segment kept for reuse does not keep old messages alive.
func TestEmbeddedMailboxTakeClearsTheSlot(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	mailbox.put(context.WithValue(context.Background(), mailboxTestCtxKey{}, "v"), &PID{}, "value")
	require.NoError(t, mailbox.Enqueue(embeddedMailboxMessage("enqueued")))
	segment := mailbox.mailboxHead.Load()

	require.Equal(t, "value", mailbox.Dequeue().Message())
	require.Equal(t, "enqueued", mailbox.Dequeue().Message())

	for i := range 2 {
		slot := &segment.slots[i]
		require.Equal(t, uint32(slotEmpty), slot.state.Load(), "slot %d", i)
		require.Nil(t, slot.ctx, "slot %d", i)
		require.Nil(t, slot.message, "slot %d", i)
		require.Nil(t, slot.sender, "slot %d", i)
		require.Nil(t, slot.received, "slot %d", i)
	}
}

// TestEmbeddedMailboxAllocatesFirstSegmentOnFirstMessage verifies that a new
// mailbox holds no segment until a message arrives, and that it reports the
// message before its consumer has dequeued anything.
func TestEmbeddedMailboxAllocatesFirstSegmentOnFirstMessage(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	require.Nil(t, mailbox.mailboxHead.Load())
	require.Nil(t, mailbox.mailboxTail.Load())
	require.True(t, mailbox.IsEmpty())
	require.Zero(t, mailbox.Len())
	require.Nil(t, mailbox.Dequeue())

	mailbox.put(context.Background(), nil, "first")
	require.NotNil(t, mailbox.mailboxTail.Load())
	require.Same(t, mailbox.mailboxTail.Load(), mailbox.mailboxHead.Load(), "the first segment is both head and tail")
	require.False(t, mailbox.IsEmpty())
	require.Equal(t, int64(1), mailbox.Len())
	require.Equal(t, "first", mailbox.Dequeue().Message())
}

// TestEmbeddedMailboxBacklogBeforeFirstTurn verifies that messages that fill
// several segments before the consumer's first dequeue all come out, from the
// first segment on.
func TestEmbeddedMailboxBacklogBeforeFirstTurn(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	const count = 3*mailboxSegmentSlots + 1
	for i := range count {
		mailbox.put(context.Background(), nil, i)
	}

	require.NotSame(t, mailbox.mailboxHead.Load(), mailbox.mailboxTail.Load())
	require.Equal(t, int64(count), mailbox.Len())
	for i := range count {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedMailboxFirstSegmentRaceKeepsWinner verifies that a producer that
// loses the race to install the first segment waits for the winner to publish
// the tail and uses the winner's segment.
func TestEmbeddedMailboxFirstSegmentRaceKeepsWinner(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	winner := new(mailboxSegment)
	mailbox.mailboxHead.Store(winner)

	got := make(chan *mailboxSegment, 1)
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

// TestEmbeddedMailboxRewindsDrainedSegment verifies that a segment the consumer
// has drained is reused from its first slot under a new generation instead of
// being followed by a new one, also when it was drained exactly at its end.
func TestEmbeddedMailboxRewindsDrainedSegment(t *testing.T) {
	for _, count := range []int{1, mailboxSegmentSlots} {
		mailbox := newEmbeddedMailbox()
		for i := range count {
			mailbox.put(context.Background(), nil, i)
		}

		segment := mailbox.mailboxTail.Load()
		generation := embeddedMailboxGeneration(segment)
		for i := range count {
			require.Equal(t, i, mailbox.Dequeue().Message())
		}

		require.Nil(t, mailbox.Dequeue())
		require.Equal(t, generation+1, embeddedMailboxGeneration(segment), "count=%d", count)
		require.Zero(t, mailbox.embedded.read.Load())

		mailbox.put(context.Background(), nil, "again")
		require.Same(t, segment, mailbox.mailboxTail.Load(), "count=%d: the drained segment must be reused", count)
		require.Nil(t, segment.next.Load())
		require.Equal(t, "again", mailbox.Dequeue().Message())
	}
}

// TestEmbeddedMailboxDoesNotRewindPastAClaim verifies that the consumer leaves
// a segment alone while a producer has claimed a slot it has not published, and
// returns the message once it is published.
func TestEmbeddedMailboxDoesNotRewindPastAClaim(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	mailbox.put(context.Background(), nil, "first")
	require.Equal(t, "first", mailbox.Dequeue().Message())
	segment := mailbox.mailboxTail.Load()
	generation := embeddedMailboxGeneration(segment)

	mailbox.put(context.Background(), nil, "second")
	require.Equal(t, "second", mailbox.Dequeue().Message())

	slot := mailbox.claim()
	require.Nil(t, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty(), "a claimed slot that is not published holds no message yet")
	require.Equal(t, generation, embeddedMailboxGeneration(segment), "a claimed slot must stop the rewind")

	slot.message = "late"
	slot.state.Store(uint32(slotReady))
	require.False(t, mailbox.IsEmpty())
	require.Equal(t, "late", mailbox.Dequeue().Message())
}

// TestEmbeddedMailboxReusesSpareSegment verifies that a backlog longer than one
// segment links further segments, that each segment the consumer finishes is
// kept as the spare, and that the next link takes the spare instead of
// allocating.
func TestEmbeddedMailboxReusesSpareSegment(t *testing.T) {
	mailbox := newEmbeddedMailbox()
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
	require.Same(t, first, mailbox.embedded.spare.Load(), "the finished segment must become the spare")
	require.Nil(t, first.next.Load())

	// one message is in the second segment's first slot: fill it and overflow
	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	require.Same(t, first, second.next.Load(), "the link must take the spare")
	require.Same(t, first, mailbox.mailboxTail.Load())
	require.Nil(t, mailbox.embedded.spare.Load())

	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedMailboxRecycleSkipsSegmentStillTail verifies that the consumer
// does not reuse a segment while it is still the tail, the window in which the
// producer linking its successor has not published the new tail.
func TestEmbeddedMailboxRecycleSkipsSegmentStillTail(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	full := new(mailboxSegment)
	full.cursor.Store(mailboxSegmentSlots + 1)
	full.next.Store(new(mailboxSegment))
	mailbox.mailboxTail.Store(full)

	mailbox.recycle(full)
	require.Nil(t, mailbox.embedded.spare.Load())
	require.Zero(t, embeddedMailboxGeneration(full))
	require.NotNil(t, full.next.Load())

	mailbox.mailboxTail.Store(full.next.Load())
	mailbox.recycle(full)
	require.Same(t, full, mailbox.embedded.spare.Load())
	require.Equal(t, uint64(1), embeddedMailboxGeneration(full))
	require.Zero(t, cursorClaims(full.cursor.Load()))
	require.Nil(t, full.next.Load())
}

// TestEmbeddedMailboxIsEmptyAfterReadingAFullSegment verifies that a mailbox
// whose consumer has read every slot of its only segment reports empty before
// the next Dequeue rewinds that segment, and not empty once a message links
// the next segment.
func TestEmbeddedMailboxIsEmptyAfterReadingAFullSegment(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.EqualValues(t, mailboxSegmentSlots, mailbox.embedded.read.Load())
	require.True(t, mailbox.IsEmpty(), "a fully read segment holds no message")

	mailbox.put(context.Background(), nil, "next")
	require.NotNil(t, mailbox.mailboxHead.Load().next.Load(), "the message must link the next segment")
	require.False(t, mailbox.IsEmpty())
	require.Equal(t, "next", mailbox.Dequeue().Message())
}

// TestEmbeddedMailboxStaleClaimIsAbandoned verifies the generation check: a
// producer whose claim lands after the segment was reused marks the slot it
// claimed abandoned and retries, and the consumer skips that slot.
func TestEmbeddedMailboxStaleClaimIsAbandoned(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	mailbox.put(context.Background(), nil, "first")
	segment := mailbox.mailboxTail.Load()
	stale := embeddedMailboxGeneration(segment)

	require.Equal(t, "first", mailbox.Dequeue().Message())
	require.Nil(t, mailbox.Dequeue())
	require.Equal(t, stale+1, embeddedMailboxGeneration(segment))

	require.Nil(t, mailbox.claimIn(segment, stale), "a claim under a stale generation must be retried")
	require.Equal(t, uint32(slotAbandoned), segment.slots[0].state.Load())
	require.True(t, mailbox.IsEmpty(), "an abandoned slot holds no message")

	mailbox.put(context.Background(), nil, "after")
	require.False(t, mailbox.IsEmpty())
	require.Equal(t, "after", mailbox.Dequeue().Message())
	require.Equal(t, uint32(slotEmpty), segment.slots[0].state.Load(), "the consumer must clear the abandoned slot")
	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedMailboxClaimRefusesSegmentNoLongerTail verifies that a producer
// that loaded a segment as the tail, stalled while the consumer finished it and
// kept it as the spare, and then read the spare's new generation, claims
// nothing in it: the spare is not linked into the queue, so a message written
// there would be delivered late or not at all.
func TestEmbeddedMailboxClaimRefusesSegmentNoLongerTail(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	for i := range mailboxSegmentSlots + 1 {
		mailbox.put(context.Background(), nil, i)
	}

	// the stalled producer loaded the first segment while it was the tail
	stale := mailbox.mailboxHead.Load()
	for i := range mailboxSegmentSlots + 1 {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Same(t, stale, mailbox.embedded.spare.Load(), "the finished segment must be the spare")
	cursor := stale.cursor.Load()

	require.Nil(t, mailbox.claimIn(stale, embeddedMailboxGeneration(stale)), "a claim in a segment that is no longer the tail must be retried")
	require.Equal(t, cursor, stale.cursor.Load(), "the spare must not be claimed in")
	for i := range stale.slots {
		require.Equal(t, uint32(slotEmpty), stale.slots[i].state.Load())
	}

	mailbox.put(context.Background(), nil, "next")
	require.Equal(t, "next", mailbox.Dequeue().Message())
}

// TestEmbeddedMailboxStaleClaimPastTheEnd verifies that a stale claim past the
// end of a reused segment marks nothing, since the slot does not exist, and
// links nothing, since the segment it read is gone.
func TestEmbeddedMailboxStaleClaimPastTheEnd(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	segment := new(mailboxSegment)
	segment.cursor.Store(1<<mailboxGenerationShift | mailboxSegmentSlots)
	mailbox.mailboxTail.Store(segment)

	require.Nil(t, mailbox.claimIn(segment, 0))
	for i := range segment.slots {
		require.Equal(t, uint32(slotEmpty), segment.slots[i].state.Load())
	}

	require.Nil(t, segment.next.Load(), "a stale claim must not link")
	require.Same(t, segment, mailbox.mailboxTail.Load())
}

// TestEmbeddedMailboxStaleClaimOnTheLinkingIndex verifies the schedule that
// wedged the mailbox: a producer reads the generation, stalls while the
// segment is drained, rewound and filled again, and then claims the first
// index past the end under the new generation. Its claim is stale, so it only
// retries; the next producer must link the segment instead of waiting for
// that claim to do it.
func TestEmbeddedMailboxStaleClaimOnTheLinkingIndex(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	mailbox.put(context.Background(), nil, "first")

	segment := mailbox.mailboxTail.Load()
	stale := embeddedMailboxGeneration(segment)

	require.Equal(t, "first", mailbox.Dequeue().Message())
	require.Nil(t, mailbox.Dequeue())
	require.Equal(t, stale+1, embeddedMailboxGeneration(segment), "the drained segment must be rewound")

	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	require.Nil(t, mailbox.claimIn(segment, stale))
	require.EqualValues(t, mailboxSegmentSlots+1, cursorClaims(segment.cursor.Load()), "the stale claim must take the first index past the end")
	require.Nil(t, segment.next.Load(), "a stale claim must not link")

	requirePutReturns(t, mailbox, "after")
	require.NotSame(t, segment, mailbox.mailboxTail.Load(), "the next producer must link the segment")

	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Equal(t, "after", mailbox.Dequeue().Message())
	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedMailboxLinksPastADelayedLinker verifies that the first producer
// to claim past the end does not have to be the one that links: a producer
// that claimed the first index past the end and has not linked yet does not
// hold up the next one, which links, and the delayed producer then only
// retries.
func TestEmbeddedMailboxLinksPastADelayedLinker(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	segment := mailbox.mailboxTail.Load()
	generation := embeddedMailboxGeneration(segment)

	// the delayed producer has claimed the first index past the end
	segment.cursor.Add(1)

	requirePutReturns(t, mailbox, "after")
	require.False(t, mailbox.claimLink(segment, generation), "the delayed producer must not link a second time")

	for i := range mailboxSegmentSlots {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Equal(t, "after", mailbox.Dequeue().Message())
}

// TestEmbeddedMailboxLinksPastASpareFullOfStaleClaims verifies that a spare
// segment linked in with every slot already claimed by stale producers, which
// stalled across its recycling and abandoned their slots, is linked past by
// the first producer that claims in it, and that the consumer skips it.
func TestEmbeddedMailboxLinksPastASpareFullOfStaleClaims(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	for i := range mailboxSegmentSlots + 1 {
		mailbox.put(context.Background(), nil, i)
	}

	for i := range mailboxSegmentSlots + 1 {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	spare := mailbox.embedded.spare.Load()
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

	requirePutReturns(t, mailbox, "after")
	require.Same(t, spare, mailbox.mailboxHead.Load().next.Load(), "the spare must be linked first")
	require.NotSame(t, spare, mailbox.mailboxTail.Load(), "the spare must be linked past")

	for i := range mailboxSegmentSlots - 1 {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Equal(t, "after", mailbox.Dequeue().Message(), "the consumer must skip the abandoned spare")
	require.Nil(t, mailbox.Dequeue())
}

// TestEmbeddedMailboxClaimLink verifies that one producer per generation takes
// on linking, that a producer of an older generation, whose segment was linked
// and reused since, never does, and that the generation after the last one,
// which wraps to zero, links again.
func TestEmbeddedMailboxClaimLink(t *testing.T) {
	const lastGeneration = 1<<mailboxGenerationShift - 1

	mailbox := newEmbeddedMailbox()
	segment := new(mailboxSegment)

	require.True(t, mailbox.claimLink(segment, 0))
	require.False(t, mailbox.claimLink(segment, 0), "a second producer of the generation must wait")

	require.True(t, mailbox.claimLink(segment, 3), "a later generation links again")
	require.False(t, mailbox.claimLink(segment, 1), "an older generation must not link")
	require.False(t, mailbox.claimLink(segment, 3))

	wrapping := new(mailboxSegment)
	require.True(t, mailbox.claimLink(wrapping, lastGeneration), "the last generation links")
	require.True(t, mailbox.claimLink(wrapping, 0), "the generation after the last one, which wraps to zero, links")
	require.False(t, mailbox.claimLink(wrapping, lastGeneration), "the last generation is older than the wrapped one")
	require.False(t, mailbox.claimLink(wrapping, 0))
}

// TestEmbeddedMailboxLinkedInGeneration verifies the modular comparison of the
// link marker with a generation, at the wrap in particular.
func TestEmbeddedMailboxLinkedInGeneration(t *testing.T) {
	const lastGeneration = 1<<mailboxGenerationShift - 1

	require.False(t, linkedInGeneration(0, 0), "a segment never linked has no link in any generation")
	require.False(t, linkedInGeneration(0, lastGeneration))
	require.True(t, linkedInGeneration(1, 0), "a link in the generation itself")
	require.True(t, linkedInGeneration(6, 2), "a link in a later generation")
	require.False(t, linkedInGeneration(6, 7), "no link in a later generation yet")
	require.False(t, linkedInGeneration(lastGeneration+1, 0), "the wrapped generation is newer than the last one")
	require.True(t, linkedInGeneration(1, lastGeneration), "the last generation is older than the wrapped one")
	require.False(t, linkedInGeneration(1, 1<<31), "up to half the range ahead is newer")
	require.True(t, linkedInGeneration(1, 1<<31+1), "further ahead reads as just under half the range behind, which no producer is")
}

// TestEmbeddedMailboxLinksAfterGenerationWrap verifies that a segment whose
// generation wraps from the last one to zero is still linked when it fills:
// the link marker of the last generation must not read as a link in every
// generation after the wrap.
func TestEmbeddedMailboxLinksAfterGenerationWrap(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	mailbox.put(context.Background(), nil, 0)
	segment := mailbox.mailboxTail.Load()
	require.Equal(t, 0, mailbox.Dequeue().Message())
	require.Nil(t, mailbox.Dequeue())

	// the segment has been reused into the last generation
	segment.cursor.Store((1<<mailboxGenerationShift - 1) << mailboxGenerationShift)
	for i := range mailboxSegmentSlots + 1 {
		mailbox.put(context.Background(), nil, i)
	}

	require.NotSame(t, segment, mailbox.mailboxTail.Load(), "the segment must be linked in the last generation")
	for i := range mailboxSegmentSlots + 1 {
		require.Equal(t, i, mailbox.Dequeue().Message())
	}

	require.Same(t, segment, mailbox.embedded.spare.Load(), "the finished segment must be the spare")
	require.Zero(t, embeddedMailboxGeneration(segment), "recycling must wrap the generation to zero")

	// fill the tail, so the spare is linked back in, then fill the spare
	for i := range mailboxSegmentSlots - 1 {
		mailbox.put(context.Background(), nil, i)
	}

	for i := range mailboxSegmentSlots {
		mailbox.put(context.Background(), nil, i)
	}

	require.Same(t, segment, mailbox.mailboxTail.Load())
	requirePutReturns(t, mailbox, "after the wrap")
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

// TestEmbeddedMailboxSegmentFitsTheSizeClass verifies that a segment still fits
// the 2 KiB allocation size class mailboxSegmentSlots is chosen for.
func TestEmbeddedMailboxSegmentFitsTheSizeClass(t *testing.T) {
	var segment mailboxSegment
	require.LessOrEqual(t, unsafe.Sizeof(segment), uintptr(2048))
}

// TestEmbeddedMailboxAwaitLinkReturns verifies that a producer waiting for the
// next segment of a full one stops waiting when the tail moves on, and also
// when the segment is reused under a new generation, which would otherwise
// keep it waiting on a segment that became the tail again.
func TestEmbeddedMailboxAwaitLinkReturns(t *testing.T) {
	t.Run("when the tail moves on", func(t *testing.T) {
		mailbox := newEmbeddedMailbox()
		full := new(mailboxSegment)
		mailbox.mailboxTail.Store(full)

		done := make(chan struct{})
		go func() {
			mailbox.awaitLink(full, 0)
			close(done)
		}()

		pause.For(10 * time.Millisecond)
		mailbox.mailboxTail.Store(new(mailboxSegment))
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
		mailbox := newEmbeddedMailbox()
		full := new(mailboxSegment)
		full.cursor.Store(1 << mailboxGenerationShift)
		mailbox.mailboxTail.Store(full)

		mailbox.awaitLink(full, 0)
		require.Same(t, full, mailbox.mailboxTail.Load())
	})
}

// TestEmbeddedMailboxConcurrentProducers drains the mailbox from a single
// consumer while many producers enqueue into it, half as values and half in
// contexts, and checks that every message arrives exactly once and that each
// producer's messages keep their order. Producers yield now and then, so they
// stall between loading the tail and claiming a slot, and the consumer pauses
// now and then, so the backlog spans segments and segments are rewound,
// recycled and taken as the spare while producers run. Run under the race
// detector.
func TestEmbeddedMailboxConcurrentProducers(t *testing.T) {
	const producers, perProducer = 16, 5000

	mailbox := newEmbeddedMailbox()
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

				require.NoError(t, mailbox.Enqueue(embeddedMailboxMessage(message)))
			}
		}()
	}

	next := make([]int, producers)
	received := 0

	for received < producers*perProducer {
		if received%97 == 0 {
			runtime.Gosched()
		}

		ctx := mailbox.Dequeue()
		if ctx == nil {
			runtime.Gosched()
			continue
		}

		id := ctx.Message().([2]int)
		require.Equal(t, next[id[0]], id[1], "producer %d delivered out of order", id[0])
		next[id[0]]++
		received++
	}

	wg.Wait()
	require.Nil(t, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty())
	require.Zero(t, mailbox.Len())
}

// TestEmbeddedMailboxDefaultSpawnUsesIt verifies that a default-spawned actor
// runs on the embedded mailbox, its Mailbox pointing straight into the PID and
// its first segment appearing with its first message, while an actor given a
// custom mailbox has no default mailbox state and dispatches through the custom
// box instead.
func TestEmbeddedMailboxDefaultSpawnUsesIt(t *testing.T) {
	ctx := context.TODO()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))

	pause.For(time.Second)

	t.Cleanup(func() {
		require.NoError(t, sys.Stop(ctx))
	})

	defaultPID, err := sys.Spawn(ctx, "Default", NewMockActor())
	require.NoError(t, err)
	require.NotNil(t, defaultPID.mailbox)
	require.NotNil(t, defaultPID.embedded)
	require.Nil(t, defaultPID.mailboxTail.Load(), "no segment before the first message")

	box, ok := defaultPID.mailbox.(*embeddedMailbox)
	require.True(t, ok, "default spawn must use the embedded mailbox")
	require.Same(t, (*embeddedMailbox)(defaultPID), box)

	require.NoError(t, Tell(ctx, defaultPID, new(testpb.TestSend)))
	require.Eventually(t, func() bool {
		return defaultPID.mailboxTail.Load() != nil && defaultPID.ProcessedCount() > 0
	}, time.Second, time.Millisecond)

	custom := NewUnboundedMailbox()
	customPID, err := sys.Spawn(ctx, "Custom", NewMockActor(), WithMailbox(custom))
	require.NoError(t, err)
	require.Same(t, custom, customPID.mailbox)
	require.Nil(t, customPID.embedded)
	require.Nil(t, customPID.mailboxHead.Load())
	require.Nil(t, customPID.mailboxTail.Load())
}
