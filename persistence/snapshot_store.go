package persistence

import (
	"context"

	actorspb "github.com/tochemey/goakt/actorpb/actors/v1"
)

// SnapshotStore represents the persistence snapshot store
type SnapshotStore interface {
	// Connect connects to the journal store
	Connect(ctx context.Context) error
	// Disconnect disconnect the journal store
	Disconnect(ctx context.Context) error
	// LoadSnapshots loads snapshots from the store for a given persistenceID
	LoadSnapshots(ctx context.Context, persistenceID string, toSequenceNumber uint64, criteria *actorspb.SnapshotCriteria) ([]*actorspb.Snapshot, error)
	// SaveSnapshot saves a snapshot into the store
	SaveSnapshot(ctx context.Context, snapshot *actorspb.Snapshot) error
	// DeleteSnapshot deletes a snapshot
	DeleteSnapshot(ctx context.Context, snapshot *actorspb.Snapshot) error
	// DeleteSnapshots removes all snapshots that match `criteria`.
	DeleteSnapshots(ctx context.Context, persistenceID string, criteria *actorspb.SnapshotCriteria) error
}
