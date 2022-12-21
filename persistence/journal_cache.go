package persistence

import (
	"sync"

	actorspb "github.com/tochemey/goakt/actorpb/actors/v1"
	"google.golang.org/protobuf/proto"
)

// journalsCache keep in memory every journal and
// after some threshold of records persists them to the JournalStore
// this help reduce I/O calls
type journalsCache struct {
	mu    sync.Mutex
	cache map[uint64][]byte
}

func newJournalsCache(initialSize int) *journalsCache {
	return &journalsCache{
		mu:    sync.Mutex{},
		cache: make(map[uint64][]byte, initialSize),
	}
}

// Put add a journal entry to the cache
func (c *journalsCache) Put(journal *actorspb.Journal) error {
	c.mu.Lock()
	bytea, err := proto.Marshal(journal)
	if err != nil {
		c.mu.Unlock()
		return err
	}

	c.cache[journal.GetSequenceNumber()] = bytea
	c.mu.Unlock()
	return nil
}

// Flush will return all the entries in the cache and clear it.
func (c *journalsCache) Flush() ([]*actorspb.Journal, error) {
	c.mu.Lock()
	output := make([]*actorspb.Journal, c.Len())
	for sedNr, entry := range c.cache {
		journal := new(actorspb.Journal)
		if err := proto.Unmarshal(entry, journal); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		// add to the output
		output = append(output, journal)
		// remove the entry from the cache
		delete(c.cache, sedNr)
	}
	c.mu.Unlock()
	return output, nil
}

// Len return the length of the cache
func (c *journalsCache) Len() int {
	return len(c.cache)
}
