package stream

import (
	"sync"

	"github.com/google/uuid"
)

// Consumer defines the consumer
type Consumer struct {
	// id defines the consumer id
	id string
	// sem represents a lock
	sem sync.Mutex
	// messages of the consumer
	messages *unboundedQueue[*Message]
	// topics define the topic the consumer subscribed to
	topics map[string]bool
	// states whether the given consumer is active or not
	active bool
}

// NewConsumer creates an instance of a stream consumer
// and returns the consumer id and its reference.
// The type of messages the consumer will consume is past as type
// parameter
func NewConsumer() (string, *Consumer) {
	// create the consumer id
	id := uuid.NewString()
	return id, &Consumer{
		id:       id,
		sem:      sync.Mutex{},
		messages: newUnboundedQueue[*Message](10),
		topics:   make(map[string]bool),
		active:   true,
	}
}

// Topics returns the list of topics the consumer has subscribed to
func (x *Consumer) Topics() []string {
	// acquire the lock
	x.sem.Lock()
	// release the lock once done
	defer x.sem.Unlock()
	var topics []string
	for topic := range x.topics {
		topics = append(topics, topic)
	}
	return topics
}

// Shutdown shutdowns the consumer
func (x *Consumer) Shutdown() {
	// acquire the lock
	x.sem.Lock()
	// release the lock once done
	defer x.sem.Unlock()
	x.active = false
	x.messages.Close()
}

func (x *Consumer) Iterator() chan *Message {
	out := make(chan *Message, x.messages.Len())
	defer close(out)
	for {
		msg, ok := x.messages.Pop()
		if !ok {
			break
		}
		out <- msg
	}
	return out
}

// signal is used to push a message to the consumer
func (x *Consumer) signal(message *Message) {
	// acquire the lock
	x.sem.Lock()
	// release the lock once done
	defer x.sem.Unlock()
	// only receive message when active
	if x.active {
		x.messages.Push(message)
	}
}

// subscribe subscribes the consumer to a given topic
func (x *Consumer) subscribe(topic string) *Consumer {
	// acquire the lock
	x.sem.Lock()
	// set the topic
	x.topics[topic] = true
	// release the lock
	x.sem.Unlock()
	// return the instance
	return x
}

// unsubscribe unsubscribes the consumer from the give topic
func (x *Consumer) unsubscribe(topic string) *Consumer {
	// acquire the lock
	x.sem.Lock()
	// remove the topic from the consumer topics
	delete(x.topics, topic)
	// release the lock
	x.sem.Unlock()
	// return the instance
	return x
}
