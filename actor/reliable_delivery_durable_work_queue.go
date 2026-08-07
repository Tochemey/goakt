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

// DurableWorkQueue stores reliable-delivery state for one work-pulling
// producer stream.
//
// Store and Accept keep the point-to-point semantics (append at CurrentSeq+1,
// MessageID first-write-wins, epoch fencing). Confirmation is per message
// because workers complete out of order and a cumulative watermark cannot
// express holes. Load returns every stored, accepted, not-yet-confirmed
// message in store order; gaps in the returned set are normal.
//
// Load, Store, Accept, and ConfirmMessage must be safe for concurrent use and
// linearizable. Fencing and conflict errors reuse ErrQueueFenced and
// ErrQueueConflict. DurableWorkQueue is also an extension.Dependency with the
// same reconstruction rules as DurableProducerQueue: MarshalBinary serializes
// only reconnect configuration, never queued messages.
type DurableWorkQueue interface {
	extension.Dependency

	// Load atomically restores queue state and acquires exclusive writership.
	//
	// The returned state must satisfy NewWorkQueueState invariants: CurrentSeq
	// is non-negative, and Unconfirmed holds accepted, not-yet-confirmed
	// messages in ascending store-sequence order with unique MessageIDs.
	// Holes relative to CurrentSeq are allowed.
	//
	// A successful call returns a positive epoch newer than every epoch
	// returned previously for this queue. A failed call must not change writer
	// ownership. Malformed persisted state is an ErrQueueConflict.
	Load(ctx context.Context) (WorkQueueState, QueueEpoch, error)

	// Store durably records request under epoch.
	//
	// For a new MessageID, ProposedSeq must equal CurrentSeq+1. Store appends
	// the message and returns AlreadyStored false. A different proposed
	// sequence is an ErrQueueConflict.
	//
	// For an existing MessageID, Store ignores the proposed sequence and
	// payload and returns the authoritative first-write sequence and payload
	// with AlreadyStored true.
	Store(ctx context.Context, epoch QueueEpoch, request StoreRequest) (StoreResult, error)

	// Accept records that the producer durably removed messageID from its
	// recoverable source. Repeating Accept for a retained, already accepted
	// MessageID is a no-op. An unknown MessageID is an ErrQueueConflict.
	Accept(ctx context.Context, epoch QueueEpoch, messageID string) error

	// ConfirmMessage marks one message complete by MessageID. Repeating
	// ConfirmMessage for an already confirmed MessageID is a no-op. An
	// unknown MessageID returns ErrQueueConflict. After confirmation the
	// implementation may compact the message when it was also accepted.
	ConfirmMessage(ctx context.Context, epoch QueueEpoch, messageID string) error
}
