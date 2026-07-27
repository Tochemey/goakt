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

// Package pendingasks tracks callers that are blocked waiting for an
// asynchronous reply, keyed by the correlation ID carried on the request.
package pendingasks

import (
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/xsync"
)

// Table holds the callers blocked inside an ask whose reply arrives out of band.
//
// A reentrant grain does not necessarily answer from the turn that received the
// request: it may issue its own requests first and reply from a later turn, so
// the reply cannot travel back through a channel owned by the message being
// handled. The caller therefore registers a slot here before its request is
// enqueued and waits on it, and the reply reaches that slot no matter which
// turn, or which node, produces it.
//
// The reply and the caller's own timeout race by nature. Both resolve the race
// through a single LoadAndDelete, so exactly one of them takes the slot: either
// the reply arrives in time and the caller reads it, or the caller gives up
// first and a late reply finds nothing to write to. That removes the
// alternative of leaving orphaned channels behind for a late writer to find.
//
// A Table is safe for concurrent use.
type Table struct {
	slots *xsync.Map[string, chan *commands.AsyncResponse]
}

// New creates an empty Table.
func New() *Table {
	return &Table{
		slots: xsync.NewMap[string, chan *commands.AsyncResponse](),
	}
}

// Register reserves a slot for the given correlation ID and returns the channel
// the caller waits on.
//
// The channel is buffered so that Complete never blocks, even when the caller
// has already stopped waiting.
func (t *Table) Register(correlationID string) <-chan *commands.AsyncResponse {
	slot := make(chan *commands.AsyncResponse, 1)
	t.slots.Set(correlationID, slot)
	return slot
}

// Complete hands a response to the caller waiting on its correlation ID and
// reports whether a waiting caller was found.
//
// A false return means the caller already abandoned the ask, which is a normal
// outcome of a reply racing a timeout rather than an error.
func (t *Table) Complete(response *commands.AsyncResponse) bool {
	if response == nil || response.CorrelationID == "" {
		return false
	}

	slot, ok := t.slots.LoadAndDelete(response.CorrelationID)
	if !ok {
		return false
	}

	// Sending cannot block: LoadAndDelete made this goroutine the only owner of
	// the slot, and the slot is buffered.
	slot <- response
	return true
}

// Abandon releases the slot without delivering a response. The waiting caller
// calls it when its timeout or context expires, so that a reply arriving
// afterwards is discarded instead of being written to a slot nobody reads.
func (t *Table) Abandon(correlationID string) {
	t.slots.Delete(correlationID)
}

// Len reports how many callers are currently waiting.
func (t *Table) Len() int {
	return t.slots.Len()
}
