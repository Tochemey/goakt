package memory

import (
	"context"
	"sort"
	"sync"

	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/persistence"
	"google.golang.org/protobuf/proto"
)

type item struct {
	seqNr uint64
	data  []byte
}

// JournalStore keep in memory every journal
// NOTE: NOT RECOMMENDED FOR PRODUCTION CODE
type JournalStore struct {
	mu    sync.Mutex
	cache map[string][]*item

	// this is only useful for tests
	keepRecordsAfterDisconnect bool
}

var _ persistence.JournalStore = &JournalStore{}

// NewJournalStore creates a new instance of MemoryEventStore
func NewJournalStore() *JournalStore {
	return &JournalStore{
		mu:                         sync.Mutex{},
		cache:                      map[string][]*item{},
		keepRecordsAfterDisconnect: false,
	}
}

// Connect connects to the journal store
func (s *JournalStore) Connect(ctx context.Context) error {
	return nil
}

// Disconnect disconnect the journal store
func (s *JournalStore) Disconnect(ctx context.Context) error {
	s.mu.Lock()
	s.cache = map[string][]*item{}
	s.mu.Unlock()
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
	s.mu.Lock()
	for _, event := range events {
		bytea, err := proto.Marshal(event)
		if err != nil {
			s.mu.Unlock()
			return err
		}

		// grab the existing items
		items := s.cache[event.GetPersistenceId()]
		// add the new entry to the existing items
		items = append(items, &item{
			seqNr: event.GetSequenceNumber(),
			data:  bytea,
		})

		// order the items per sequence number
		sort.SliceStable(items, func(i, j int) bool {
			return items[i].seqNr < items[j].seqNr
		})

		s.cache[event.GetPersistenceId()] = items
	}
	s.mu.Unlock()
	return nil
}

// DeleteEvents deletes events from the store upt to a given sequence number (inclusive)
func (s *JournalStore) DeleteEvents(ctx context.Context, persistenceID string, toSequenceNumber uint64) error {
	s.mu.Lock()
	items := s.cache[persistenceID]

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
