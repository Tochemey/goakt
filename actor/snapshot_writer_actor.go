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
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/persistence"
)

// snapshotWriterChildName is the well-known child name under which an
// event-sourced actor spawns its [snapshotWriterActor] helper.
const snapshotWriterChildName = "snapshot-writer"

// writeSnapshotCmd is the fire-and-forget message sent to snapshotWriterActor.
type writeSnapshotCmd struct {
	snapshot *persistence.PersistedSnapshot
}

// snapshotWriterActor is an internal child actor that persists snapshots
// asynchronously so the parent EventSourcedActor is never blocked waiting for
// snapshot writes. Snapshot write failures are logged at warn level and do not
// propagate to the parent.
type snapshotWriterActor struct {
	store  persistence.SnapshotStore
	logger log.Logger
}

var _ Actor = (*snapshotWriterActor)(nil)

func (x *snapshotWriterActor) PreStart(ctx *Context) error {
	x.logger = ctx.Logger()
	return nil
}

func (x *snapshotWriterActor) Receive(ctx *ReceiveContext) {
	if m, ok := ctx.Message().(writeSnapshotCmd); ok {
		if err := x.store.WriteSnapshot(ctx.Context(), m.snapshot); err != nil {
			x.logger.Warnf("snapshot write failed: persistenceID=%s seq=%d: %v",
				m.snapshot.PersistenceID, m.snapshot.SequenceNumber, err)
		}
	}
}

func (x *snapshotWriterActor) PostStop(_ *Context) error {
	return nil
}
