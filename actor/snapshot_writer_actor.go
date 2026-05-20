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
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/persistence"
)

// snapshotWriterChildName is the child name used when an event-sourced actor
// spawns its [snapshotWriterActor] helper.
const snapshotWriterChildName = "snapshot-writer"

// writeSnapshotCmd is the message sent to [snapshotWriterActor].
type writeSnapshotCmd struct {
	snapshot *persistence.PersistedSnapshot
}

// snapshotWriterActor persists snapshots asynchronously for its parent
// event-sourced actor and applies the configured retention policy after a
// successful write. Failures are logged and published as
// [SnapshotWriteFailed], [SnapshotDeleteFailed], or [EventsDeleteFailed]
// events; they do not propagate to the parent.
type snapshotWriterActor struct {
	snapshotStore persistence.SnapshotStore
	eventsStore   persistence.EventsStore
	criteria      *persistence.SnapshotCriteria
	eventsStream  eventstream.Stream
	logger        log.Logger
}

var _ Actor = (*snapshotWriterActor)(nil)

func (x *snapshotWriterActor) PreStart(ctx *Context) error {
	x.logger = ctx.Logger()
	return nil
}

func (x *snapshotWriterActor) Receive(ctx *ReceiveContext) {
	m, ok := ctx.Message().(writeSnapshotCmd)
	if !ok {
		return
	}

	if err := x.snapshotStore.WriteSnapshot(ctx.Context(), m.snapshot); err != nil {
		x.logger.Warnf("snapshot write failed: persistenceID=%s seq=%d: %v",
			m.snapshot.PersistenceID, m.snapshot.SequenceNumber, err)
		publishFailure(x.eventsStream, NewSnapshotWriteFailed(m.snapshot.PersistenceID, m.snapshot.SequenceNumber, err))
		return
	}

	applyRetentionPolicy(ctx.Context(), x.logger, x.eventsStream, x.eventsStore, x.snapshotStore, x.criteria, m.snapshot)
}

func (x *snapshotWriterActor) PostStop(_ *Context) error {
	return nil
}
