package persistence

import (
	"context"

	pb "github.com/tochemey/goakt/pb/goakt/v1"
)

// JournalStore represents the persistence store.
// This helps implement any persistence storage whether it is an RDBMS or No-SQL database
type JournalStore interface {
	// Connect connects to the journal store
	Connect(ctx context.Context) error
	// Disconnect disconnect the journal store
	Disconnect(ctx context.Context) error
	// WriteJournals persist journals in batches for a given persistenceID
	WriteJournals(ctx context.Context, journals []*pb.Journal) error
	// DeleteJournals deletes journals from the store upt to a given sequence number (inclusive)
	DeleteJournals(ctx context.Context, persistenceID string, toSequenceNumber uint64) error
	// ReplayJournals fetches journals for a given persistence ID from a given sequence number(inclusive) to a given sequence number(inclusive)
	ReplayJournals(ctx context.Context, persistenceID string, fromSequenceNumber, toSequenceNumber uint64) ([]*pb.Journal, error)
	// GetLatestJournal fetches the latest journal
	GetLatestJournal(ctx context.Context, persistenceID string) (*pb.Journal, error)
}
