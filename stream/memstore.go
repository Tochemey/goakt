package stream

import (
	"context"
	"sync"
	"time"

	"github.com/pkg/errors"
)

type item struct {
	message    *Message
	lastAccess time.Time
}

// MemStore implements the storage with a TTL to each entry in the log
type MemStore struct {
	// topics holds the list of topics with their various messages
	topics    map[string][]*item
	connected bool
	stopChan  chan struct{}
	stopOnce  sync.Once
	expiry    time.Duration

	lock sync.RWMutex
}

var _ storage = &MemStore{}

// NewMemStore creates an instance of iLog
func NewMemStore(initialSize int, maxTTL time.Duration) *MemStore {
	// create an instance of the log
	memory := &MemStore{
		topics:   make(map[string][]*item, initialSize),
		stopChan: make(chan struct{}, 1),
		stopOnce: sync.Once{},
		expiry:   maxTTL,
		lock:     sync.RWMutex{},
	}
	return memory
}

// Connect handles connection. For MemStore there is nothing to connect
func (m *MemStore) Connect(context.Context) error {
	m.lock.Lock()
	m.connected = true
	m.lock.Unlock()
	go m.expireLoop()
	return nil
}

// Persist persists topics onto the log
func (m *MemStore) Persist(_ context.Context, topic *Topic) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	// always check whether the log is connected
	if !m.connected {
		return errors.New("the log is disconnected")
	}

	// iterate the messages list of messages
	items := make([]*item, len(topic.Messages))
	for index, message := range topic.Messages {
		// add a log entry
		items[index] = &item{
			message:    message,
			lastAccess: time.Now().UTC(),
		}
	}

	// append the list of topics the new entries
	m.topics[topic.Name] = append(m.topics[topic.Name], items...)
	return nil
}

// Disconnect disconnects the retention log
func (m *MemStore) Disconnect(ctx context.Context) error {
	m.lock.Lock()
	m.connected = false
	m.lock.Unlock()
	m.stopOnce.Do(func() { close(m.stopChan) })
	return nil
}

// GetMessages returns the list of messages in a topic
func (m *MemStore) GetMessages(_ context.Context, topic string) ([]*Message, error) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	// always check whether the log is connected
	if !m.connected {
		return nil, errors.New("the log is disconnected")
	}

	items, ok := m.topics[topic]
	if !ok {
		return nil, nil
	}

	messages := make([]*Message, 0, len(items))
	for _, entry := range items {
		if entry != nil {
			messages = append(messages, entry.message)
		}
	}
	return messages, nil
}

func (m *MemStore) expireLoop() {
	// create the ticker tha run every 10 ms to check whether an entry in the cache has expired or not
	ticker := time.NewTicker(10 * time.Millisecond)
	go func() {
		for {
			select {
			case <-ticker.C:
				// acquire lock to access the cache entries
				m.lock.Lock()
				for topic, entries := range m.topics {
					for i, entry := range entries {
						if entry != nil {
							// when an entry has expired remove it from the underlying map
							idleTime := time.Since(entry.lastAccess)
							// the entry has expired remove it
							if idleTime > m.expiry {
								// Remove the element at index i rom the list
								entries[i] = entries[len(entries)-1]
								entries[len(entries)-1] = nil
								entries = entries[:len(entries)-1]
							}
						}
					}
					// remove the topic from the topics map
					// when there are no entries
					if len(entries) == 0 {
						delete(m.topics, topic)
					}
				}
				// release the lock
				m.lock.Unlock()
			case <-m.stopChan:
				return
			}
		}
	}()
}
