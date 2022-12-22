package persistence

import (
	"context"

	actorspb "github.com/tochemey/goakt/actorpb/actors/v1"
)

// JournalStore represents the persistence store.
// This helps implement any persistence storage whether it is an RDBMS or No-SQL database
type JournalStore interface {
	// Connect connects to the journal store
	Connect(ctx context.Context) error
	// Disconnect disconnect the journal store
	Disconnect(ctx context.Context) error
	// WriteJournals persist journals in batches for a given persistenceID
	WriteJournals(ctx context.Context, journals []*actorspb.Journal) error
	// DeleteJournals deletes journals from the store upt to a given sequence number (inclusive)
	DeleteJournals(ctx context.Context, persistenceID string, toSequenceNumber uint64) error
	// ReplayJournals for a given persistence ID from a given sequence number(inclusive) to a given sequence number(inclusive) with a maximum of journals to be replayed.
	ReplayJournals(ctx context.Context, persistenceID string, fromSequenceNumber, toSequenceNumber uint64, max uint64) ([]*actorspb.Journal, error)
	// GetLatestJournal fetches the latest journal
	GetLatestJournal(ctx context.Context, persistenceID string) (*actorspb.Journal, error)
}
