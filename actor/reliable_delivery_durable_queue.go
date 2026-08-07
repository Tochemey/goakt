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
	"strconv"
	"strings"

	"github.com/tochemey/goakt/v4/extension"
)

const (
	// durableChunkIDPrefix marks StoreChunked identities so they cannot be
	// mistaken for an application MessageID. Format:
	// GoAktChunk:<index>/<count>:<businessMessageID>
	durableChunkIDPrefix = "GoAktChunk:"
)

// QueueEpoch identifies the producer-controller incarnation that owns a queue.
// Zero is not a valid writer epoch. Every successful Load returns a newer value
// and fences every value returned by an earlier Load.
type QueueEpoch int64

// DurableProducerQueue stores reliable-delivery state for one producer stream.
//
// An implementation persists the current and confirmed sequence watermarks and
// every unconfirmed message. Each stored message contains its MessageID,
// sequence, immutable ReliablePayload, and whether the producer has accepted
// it. Implementations normally maintain indexes by both MessageID and sequence.
//
// Load, Store, StoreChunked, Accept, and Confirm must be safe for concurrent use
// and linearizable. A database implementation should perform each method in one
// transaction or use an equivalent compare-and-swap operation. The writer epoch
// must be stored and checked in the same transaction as queue state so a writer
// on a departed node cannot commit after another node has completed Load.
//
// MessageID has first-write ownership. Once Store commits a MessageID, later
// calls for that ID return the original sequence and payload even if the new
// request contains different bytes. StoreChunked applies the same ownership to
// a whole chunked batch keyed by the shared business MessageID: a retry returns
// every original chunk and ignores the proposed bytes. Chunk entries use
// derived MessageIDs (`GoAktChunk:<index>/<count>:<businessID>`, 1-based
// index) so the gap-free unique-ID Load invariant stays honest while Delivery
// and Confirmed still carry the business MessageID. The reserved prefix keeps
// those identities distinct from application MessageIDs: NewProduced rejects
// an application MessageID that starts with it. Implementations may remove
// the MessageID index only after both Accept and Confirm cover the message.
//
// Store or StoreChunked returning nil is the durable boundary after which a
// retry must recover the stored message. Accept returning nil means the
// source-acceptance marker survives restart. Confirm returning nil means the
// confirmation watermark survives restart. Returned states must not expose
// mutable backing-store collections. ReliablePayload values may share their
// encapsulated bytes because Bytes always returns a copy.
//
// Store, StoreChunked, Accept, and Confirm return the public ErrQueueFenced
// sentinel when epoch is zero or no longer owns the queue. State ownership
// conflicts, such as an invalid proposed sequence, unknown accepted MessageID,
// or confirmation beyond the current sequence, return ErrQueueConflict.
// Cancellation and backend failures are returned unchanged so the controller
// can apply its retry policy. Structurally invalid method arguments return
// ErrInvalidMessage.
//
// DurableProducerQueue is also an extension.Dependency. ID must be a stable
// reconstruction key. MarshalBinary serializes only the configuration required
// to reconnect to the backing store; it must never serialize queued messages.
// UnmarshalBinary restores that configuration, and the reconstructed instance
// must observe the same durable state from every eligible actor-system node.
type DurableProducerQueue interface {
	extension.Dependency

	// Load atomically restores queue state and acquires exclusive writership.
	//
	// The returned state must satisfy NewDurableQueueState invariants: sequence
	// watermarks are non-negative, ConfirmedSeq does not exceed CurrentSeq, and
	// Unconfirmed contains each sequence in (ConfirmedSeq, CurrentSeq] exactly
	// once and in ascending order. Chunk entries are ordinary gap-free
	// unconfirmed messages under their derived MessageIDs; a reloaded
	// incarnation resends them as stored and never re-chunks.
	//
	// A successful call returns a positive epoch newer than every epoch returned
	// previously for this queue. A failed call must not change writer ownership.
	// Malformed persisted state is an ErrQueueConflict.
	Load(ctx context.Context) (DurableQueueState, QueueEpoch, error)

	// Store durably records request under epoch.
	//
	// For a new MessageID, ProposedSeq must equal CurrentSeq+1. Store appends the
	// message and returns AlreadyStored false. A different proposed sequence is
	// an ErrQueueConflict.
	//
	// For an existing MessageID, Store ignores the proposed sequence and payload
	// and returns the authoritative first-write sequence and payload with
	// AlreadyStored true. This retry path must not append another message.
	//
	// A business MessageID whose retained first write is a chunked batch is
	// owned by that batch, not free for a whole-message append: Store must
	// return ErrQueueChunkedBatch without appending, because a StoreResult
	// cannot carry the batch. The controller recovers the original chunks
	// through a StoreChunked retry, which keeps the first write authoritative
	// when a resubmitted payload re-encodes below the chunk threshold.
	Store(ctx context.Context, epoch QueueEpoch, request StoreRequest) (StoreResult, error)

	// StoreChunked durably records every chunk of one business message under
	// epoch in a single atomic unit: either every request is indexed or none
	// is. Each request carries a derived chunk MessageID and a contiguous
	// ProposedSeq starting at CurrentSeq+1 for a new batch.
	//
	// First-write-wins applies to the whole batch, keyed by the shared business
	// MessageID: a retry returns the original batch's sequences and payloads
	// with AlreadyStored true on every result and ignores the proposed bytes.
	// A queue that cannot store the batch atomically cannot support chunking.
	StoreChunked(ctx context.Context, epoch QueueEpoch, requests []StoreRequest) ([]StoreResult, error)

	// Accept records that the producer durably removed messageID from its
	// recoverable source. messageID is the producer-facing business MessageID
	// from Produced: for a chunked batch it covers every derived chunk identity
	// of that message.
	//
	// Repeating Accept for a retained, already accepted MessageID is a no-op.
	// An unknown MessageID is an ErrQueueConflict. The implementation may delete
	// the MessageID index and message only when its sequence is also confirmed.
	Accept(ctx context.Context, epoch QueueEpoch, messageID string) error

	// Confirm durably advances the highest contiguous consumer-confirmed
	// sequence.
	//
	// Values at or below ConfirmedSeq are idempotent no-ops. A value above
	// CurrentSeq is an ErrQueueConflict. After advancing the watermark, the
	// implementation may compact messages that were also accepted.
	Confirm(ctx context.Context, epoch QueueEpoch, upToSeq int64) error
}

// durableChunkMessageID builds the durable identity of one chunk of a business
// message. Index is 1-based. The reserved prefix keeps the identity distinct
// from any application MessageID; the business ID is a suffix so it may contain
// ':' or '#' without ambiguity.
func durableChunkMessageID(businessID string, index, count int) string {
	return durableChunkIDPrefix + strconv.Itoa(index) + "/" + strconv.Itoa(count) + ":" + businessID
}

// parseDurableChunkMessageID reports whether messageID is a derived chunk
// identity and, when it is, returns the business MessageID and 1-based
// position. It allocates nothing: deliveryID runs it on every emission,
// resend, and confirmation of a durable chunk entry, and the returned
// business ID shares the input's backing bytes.
func parseDurableChunkMessageID(messageID string) (businessID string, index, count int, ok bool) {
	if !strings.HasPrefix(messageID, durableChunkIDPrefix) {
		return "", 0, 0, false
	}

	rest := messageID[len(durableChunkIDPrefix):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return "", 0, 0, false
	}

	colon := strings.IndexByte(rest[slash+1:], ':')
	if colon < 0 {
		return "", 0, 0, false
	}

	colon += slash + 1
	if colon == len(rest)-1 {
		return "", 0, 0, false
	}

	parsedIndex, okIndex := parseChunkPosition(rest[:slash])
	parsedCount, okCount := parseChunkPosition(rest[slash+1 : colon])

	if !okIndex || !okCount || parsedIndex > parsedCount {
		return "", 0, 0, false
	}

	return rest[colon+1:], parsedIndex, parsedCount, true
}

// parseChunkPosition parses a canonical positive decimal: digits only, no
// sign, no leading zero, so every derived identity has exactly one spelling.
// The length cap bounds the value far above any admissible chunk count while
// keeping the accumulation overflow-free.
func parseChunkPosition(input string) (int, bool) {
	if len(input) == 0 || len(input) > 9 || input[0] == '0' {
		return 0, false
	}

	value := 0

	for i := 0; i < len(input); i++ {
		digit := input[i]
		if digit < '0' || digit > '9' {
			return 0, false
		}

		value = value*10 + int(digit-'0')
	}

	return value, true
}

// idFrom returns the producer-facing MessageID of a stored identity:
// the business ID when messageID is a derived chunk identity, otherwise the
// MessageID itself.
func idFrom(messageID string) string {
	if business, _, _, ok := parseDurableChunkMessageID(messageID); ok {
		return business
	}

	return messageID
}

// hydrateUnconfirmedChunk restores chunk position marks from a derived durable
// chunk MessageID so a reloaded entry emits with the same flags the producer
// stored. Only the reserved GoAktChunk prefix is recognized, so ordinary
// application MessageIDs are never reinterpreted as chunks.
func hydrateUnconfirmedChunk(message UnconfirmedMessage) UnconfirmedMessage {
	_, index, count, ok := parseDurableChunkMessageID(message.messageID)
	if !ok {
		return message
	}

	message.chunk = chunkMark{chunked: true, first: index == 1, last: index == count}
	return message
}
