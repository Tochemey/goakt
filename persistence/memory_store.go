package persistence

import (
	"context"
	"sort"
	"sync"

	actorspb "github.com/tochemey/goakt/actorpb/actors/v1"
	"google.golang.org/protobuf/proto"
)

type item struct {
	seqNr uint64
	data  []byte
}

// MemoryStore keep in memory every journal
type MemoryStore struct {
	mu    sync.Mutex
	cache map[string][]*item
}

var _ JournalStore = &MemoryStore{}

// NewMemoryStore creates a new instance of MemoryStore
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		mu:    sync.Mutex{},
		cache: map[string][]*item{},
	}
}

// Connect connects to the journal store
func (s *MemoryStore) Connect(ctx context.Context) error {
	return nil
}

// Disconnect disconnect the journal store
func (s *MemoryStore) Disconnect(ctx context.Context) error {
	s.mu.Lock()
	s.cache = map[string][]*item{}
	s.mu.Unlock()
	return nil
}

// WriteJournals persist journals in batches for a given persistenceID
func (s *MemoryStore) WriteJournals(ctx context.Context, journals []*actorspb.Journal) error {
	s.mu.Lock()
	for _, journal := range journals {
		bytea, err := proto.Marshal(journal)
		if err != nil {
			s.mu.Unlock()
			return err
		}

		// grab the existing items
		items := s.cache[journal.GetPersistenceId()]
		// add the new entry to the existing items
		items = append(items, &item{
			seqNr: journal.GetSequenceNumber(),
			data:  bytea,
		})

		// order the items per sequence number
		sort.SliceStable(items, func(i, j int) bool {
			return items[i].seqNr < items[j].seqNr
		})

		s.cache[journal.GetPersistenceId()] = items
	}
	s.mu.Unlock()
	return nil
}

// DeleteJournals deletes journals from the store upt to a given sequence number (inclusive)
func (s *MemoryStore) DeleteJournals(ctx context.Context, persistenceID string, toSequenceNumber uint64) error {
	s.mu.Lock()
	items := s.cache[persistenceID]

	// short circuit when there are no items
	if len(items) == 0 {
		s.mu.Unlock()
		return nil
	}

	// order the items per sequence number
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].seqNr < items[j].seqNr
	})

	// iterate the items
	for _, item := range items {
		if item.seqNr <= toSequenceNumber {
			// Remove the element at index from the slice
			items[item.seqNr] = items[len(items)-1] // Copy last element to index.
			items[len(items)-1] = nil               // Erase last element (write zero value).
			items = items[:len(items)-1]            // Truncate slice.
		}
	}

	// set the remaining items after the removal
	s.cache[persistenceID] = items
	s.mu.Unlock()
	return nil
}

// ReplayJournals fetches journals for a given persistence ID from a given sequence number(inclusive) to a given sequence number(inclusive)
func (s *MemoryStore) ReplayJournals(ctx context.Context, persistenceID string, fromSequenceNumber, toSequenceNumber uint64) ([]*actorspb.Journal, error) {
	s.mu.Lock()
	items := s.cache[persistenceID]

	// short circuit when there are no items
	if len(items) == 0 {
		s.mu.Unlock()
		return nil, nil
	}

	// sort the items per sequence number
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].seqNr < items[j].seqNr
	})

	subset := make([]*actorspb.Journal, 0, (toSequenceNumber-fromSequenceNumber)+1)
	for _, item := range items {
		if item.seqNr >= fromSequenceNumber && item.seqNr <= toSequenceNumber {
			// unmarshal it
			journal := new(actorspb.Journal)
			// return the error during unmarshaling
			if err := proto.Unmarshal(item.data, journal); err != nil {
				s.mu.Unlock()
				return nil, err
			}

			// add the item to the subset
			subset = append(subset, journal)
		}
	}

	// sort the subset by sequence number
	sort.SliceStable(subset, func(i, j int) bool {
		return subset[i].GetSequenceNumber() < subset[j].GetSequenceNumber()
	})

	s.mu.Unlock()
	return subset, nil
}

// GetLatestJournal fetches the latest journal
func (s *MemoryStore) GetLatestJournal(ctx context.Context, persistenceID string) (*actorspb.Journal, error) {
	s.mu.Lock()
	items := s.cache[persistenceID]

	// short circuit when there are no items
	if len(items) == 0 {
		s.mu.Unlock()
		return nil, nil
	}

	// sort the items per sequence number
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].seqNr < items[j].seqNr
	})

	// pick the last item in the array
	item := items[len(items)-1]
	// unmarshal it
	journal := new(actorspb.Journal)
	// return the error during unmarshaling
	if err := proto.Unmarshal(item.data, journal); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	return journal, nil
}

// Len return the length of the cache
func (s *MemoryStore) Len() int {
	return len(s.cache)
}
