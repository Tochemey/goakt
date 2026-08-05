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

	"github.com/tochemey/goakt/v4/extension"
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
// Load, Store, Accept, and Confirm must be safe for concurrent use and
// linearizable. A database implementation should perform each method in one
// transaction or use an equivalent compare-and-swap operation. The writer epoch
// must be stored and checked in the same transaction as queue state so a writer
// on a departed node cannot commit after another node has completed Load.
//
// MessageID has first-write ownership. Once Store commits a MessageID, later
// calls for that ID return the original sequence and payload even if the new
// request contains different bytes. Implementations may remove the MessageID
// index only after both Accept and Confirm cover the message.
//
// Store returning nil is the durable boundary after which a retry must recover
// the stored message. Accept returning nil means the source-acceptance marker
// survives restart. Confirm returning nil means the confirmation watermark
// survives restart. Returned states must not expose mutable backing-store
// collections. ReliablePayload values may share their encapsulated bytes
// because Bytes always returns a copy.
//
// Store, Accept, and Confirm return the public ErrQueueFenced sentinel when
// epoch is zero or no longer owns the queue. State ownership conflicts, such as
// an invalid proposed sequence, unknown accepted MessageID, or confirmation
// beyond the current sequence, return ErrQueueConflict. Cancellation and
// backend failures are returned unchanged so the controller can apply its retry
// policy. Structurally invalid method arguments return ErrInvalidMessage.
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
	// once and in ascending order.
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
	Store(ctx context.Context, epoch QueueEpoch, request StoreRequest) (StoreResult, error)

	// Accept records that the producer durably removed messageID from its
	// recoverable source.
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
