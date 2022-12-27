package stream

import (
	"context"
	"sync"
	"time"
)

type logEntry struct {
	message    *Message
	lastAccess time.Time
}

// MemoryLog implements the RetentionLog with a TTL to each entry in the log
type MemoryLog struct {
	// topics holds the list of topics with their various messages
	topics    map[string][]*logEntry
	connected bool
	stop      chan struct{}
	stopOnce  sync.Once
	expiry    time.Duration

	lock sync.RWMutex
}

var _ RetentionLog = &MemoryLog{}

// NewMemoryLog creates an instance of RetentionLog
func NewMemoryLog(initialSize int, maxTTL time.Duration) *MemoryLog {
	// create an instance of the log
	memory := &MemoryLog{
		topics:   make(map[string][]*logEntry, initialSize),
		stop:     make(chan struct{}, 1),
		stopOnce: sync.Once{},
		expiry:   maxTTL,
		lock:     sync.RWMutex{},
	}

	go memory.monitorExpiration()
	return memory
}

// Connect handles connection. For MemoryLog there is nothing to connect
func (m *MemoryLog) Connect(context.Context) error {
	m.lock.Lock()
	m.connected = true
	m.lock.Unlock()
	return nil
}

// Persist persists topics onto the log
func (m *MemoryLog) Persist(_ context.Context, topic *Topic) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	// get the list of entries
	entries := m.topics[topic.Name]
	// iterate the messages list of messages
	for _, message := range topic.Messages {
		var le *logEntry
		for _, entry := range entries {
			if entry.message.id == message.id {
				le = entry
				break
			}
		}

		// there is no entry for the given message
		if le == nil {
			// create a new entry with the message and append it to the existing
			// entry list
			le = &logEntry{message: message}
			entries = append(entries, le)
		}
		// set the lastAccess time
		le.lastAccess = time.Now().UTC()
	}

	return nil
}

// Disconnect disconnects the retention log
func (m *MemoryLog) Disconnect(ctx context.Context) error {
	m.lock.Lock()
	m.connected = false
	m.lock.Unlock()
	m.stopOnce.Do(func() { close(m.stop) })
	return nil
}

// GetMessages returns the list of messages in a topic
func (m *MemoryLog) GetMessages(_ context.Context, topic string) ([]*Message, error) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	// always check whether the log is connected
	if !m.connected {

	}

	entries, ok := m.topics[topic]
	if !ok {
		return nil, nil
	}

	messages := make([]*Message, len(entries))
	for i, entry := range entries {
		messages[i] = entry.message
	}
	return messages, nil
}

func (m *MemoryLog) monitorExpiration() {
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
						// when an entry has expired remove it from the underlying map
						idleTime := time.Since(entry.lastAccess)
						if idleTime > m.expiry {
							// remove the entry from the list
							entries = append(entries[:i], entries[i+1:]...)
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
			case <-m.stop:
				return
			}
		}
	}()
}
