package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/pkg/errors"

	"github.com/hashicorp/go-memdb"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/persistence"
	"github.com/tochemey/goakt/telemetry"
	"google.golang.org/protobuf/proto"
)

type item struct {
	seqNr uint64
	data  []byte
}

// JournalStore keep in memory every journal
// NOTE: NOT RECOMMENDED FOR PRODUCTION CODE because all records are in memory and does not provide durability.
type JournalStore struct {
	// specifies the semaphore to ensure synchronized operations
	mu sync.Mutex
	// specifies the underlying database
	db *memdb.MemDB
	// this is only useful for tests
	keepRecordsAfterDisconnect bool
}

var _ persistence.JournalStore = &JournalStore{}

// NewJournalStore creates a new instance of MemoryEventStore
func NewJournalStore() *JournalStore {
	return &JournalStore{
		mu:                         sync.Mutex{},
		keepRecordsAfterDisconnect: false,
	}
}

// Connect connects to the journal store
func (s *JournalStore) Connect(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Journal.Connect")
	defer span.End()

	// create an instance of the database
	db, err := memdb.NewMemDB(journalSchema)
	// handle the eventual error
	if err != nil {
		return err
	}
	// set the journal store underlying database
	s.db = db

	return nil
}

// Disconnect disconnect the journal store
func (s *JournalStore) Disconnect(ctx context.Context) error {
	return nil
}

// PersistenceIDs returns the distinct list of all the persistence ids in the journal store
func (s *JournalStore) PersistenceIDs(ctx context.Context) (persistenceIDs []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	persistenceIDs = make([]string, 0, s.Len())
	for k := range s.cache {
		persistenceIDs = append(persistenceIDs, k)
	}
	return
}

// WriteEvents persist events in batches for a given persistenceID
func (s *JournalStore) WriteEvents(ctx context.Context, events []*pb.Event) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Journal.WriteEvents")
	defer span.End()
	s.mu.Lock()

	// spawn a db transaction
	txn := s.db.Txn(true)
	// iterate the event and persist the record
	for _, event := range events {
		// serialize the event and resulting state
		eventBytes, _ := proto.Marshal(event.GetEvent())
		stateBytes, _ := proto.Marshal(event.GetResultingState())

		// grab the manifest
		eventManifest := string(event.GetEvent().ProtoReflect().Descriptor().FullName())
		stateManifest := string(event.GetResultingState().ProtoReflect().Descriptor().FullName())

		// create an instance of Journal
		journal := &Journal{
			PersistenceID:  event.GetPersistenceId(),
			SequenceNumber: event.GetSequenceNumber(),
			IsDeleted:      event.GetIsDeleted(),
			EventPayload:   eventBytes,
			EventManifest:  eventManifest,
			StatePayload:   stateBytes,
			StateManifest:  stateManifest,
			Timestamp:      event.GetTimestamp(),
		}

		// persist the record
		if err := txn.Insert(tableName, journal); err != nil {
			// abort the transaction
			txn.Abort()
			// return the error
			return errors.Wrap(err, "failed to persist event on to the journal store")
		}
	}
	// commit the transaction
	txn.Commit()

	// release the lock
	s.mu.Unlock()
	return nil
}

// DeleteEvents deletes events from the store upt to a given sequence number (inclusive)
func (s *JournalStore) DeleteEvents(ctx context.Context, persistenceID string, toSequenceNumber uint64) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Journal.DeleteEvents")
	defer span.End()

	s.mu.Lock()
	// spawn a db transaction for read-only
	txn := s.db.Txn(false)
	// fetch all the records that are not deleted and filter them out
	it, err := txn.Get(tableName, persistenceIdIndexName, persistenceID)
	// handle the error
	if err != nil {
		// abort the transaction
		txn.Abort()
		return errors.Wrapf(err, "failed to delete %d persistenceId=%s events", toSequenceNumber, persistenceID)
	}

	// loop over

	// short circuit when there are no items
	if len(items) == 0 {
		s.mu.Unlock()
		return nil
	}

	// iterate the items
	for i, item := range items {
		if item.seqNr <= toSequenceNumber {
			// Remove the element at index from the slice
			items[i] = items[len(items)-1] // Copy last element to index.
			items[len(items)-1] = nil      // Erase last element (write zero value).
			items = items[:len(items)-1]   // Truncate slice.
		}
	}

	// set the remaining items after the removal
	s.cache[persistenceID] = items
	s.mu.Unlock()
	return nil
}

// ReplayEvents fetches events for a given persistence ID from a given sequence number(inclusive) to a given sequence number(inclusive)
func (s *JournalStore) ReplayEvents(ctx context.Context, persistenceID string, fromSequenceNumber, toSequenceNumber uint64, max uint64) ([]*pb.Event, error) {
	s.mu.Lock()
	items := s.cache[persistenceID]

	// short circuit when there are no items
	if len(items) == 0 {
		s.mu.Unlock()
		return nil, nil
	}

	subset := make([]*pb.Event, 0, (toSequenceNumber-fromSequenceNumber)+1)
	for _, item := range items {
		if item.seqNr >= fromSequenceNumber && item.seqNr <= toSequenceNumber {
			// unmarshal it
			event := new(pb.Event)
			// return the error during unmarshalling
			if err := proto.Unmarshal(item.data, event); err != nil {
				s.mu.Unlock()
				return nil, err
			}

			// add the item to the subset
			if len(subset) <= int(max) {
				subset = append(subset, event)
			}
		}
	}

	// sort the subset by sequence number
	sort.SliceStable(subset, func(i, j int) bool {
		return subset[i].GetSequenceNumber() < subset[j].GetSequenceNumber()
	})

	s.mu.Unlock()
	return subset, nil
}

// GetLatestEvent fetches the latest event
func (s *JournalStore) GetLatestEvent(ctx context.Context, persistenceID string) (*pb.Event, error) {
	s.mu.Lock()
	items := s.cache[persistenceID]

	// short circuit when there are no items
	if len(items) == 0 {
		s.mu.Unlock()
		return nil, nil
	}

	// pick the last item in the array
	item := items[len(items)-1]
	// unmarshal it
	event := new(pb.Event)
	// return the error during unmarshalling
	if err := proto.Unmarshal(item.data, event); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	return event, nil
}

// Len return the length of the cache
func (s *JournalStore) Len() int {
	return len(s.cache)
}
