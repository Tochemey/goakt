package eventstream

import (
	"sync"

	"github.com/google/uuid"
	"github.com/tochemey/goakt/pkg/queue"
)

// Subscriber defines the Subscriber Interface
type Subscriber interface {
	Topics() []string
	Iterator() chan *Message
	Shutdown()
	signal(message *Message)
	subscribe(topic string)
	unsubscribe(topic string)
	Active() bool
	ID() string
}

// subscriber defines the subscriber
type subscriber struct {
	// id defines the subscriber id
	id string
	// sem represents a lock
	sem sync.Mutex
	// messages of the subscriber
	messages *queue.Unbounded[*Message]
	// topics define the topic the subscriber subscribed to
	topics map[string]bool
	// states whether the given subscriber is active or not
	active bool
}

var _ Subscriber = &subscriber{}

// newSubscriber creates an instance of a stream consumer
// and returns the consumer id and its reference.
// The type of messages the consumer will consume is past as type
// parameter
func newSubscriber() *subscriber {
	// create the consumer id
	id := uuid.NewString()
	return &subscriber{
		id:       id,
		sem:      sync.Mutex{},
		messages: queue.NewUnbounded[*Message](),
		topics:   make(map[string]bool),
		active:   true,
	}
}

// ID return consumer id
func (x *subscriber) ID() string {
	// acquire the lock
	x.sem.Lock()
	// release the lock once done
	defer x.sem.Unlock()
	return x.id
}

// Active checks whether the consumer is active
func (x *subscriber) Active() bool {
	// acquire the lock
	x.sem.Lock()
	// release the lock once done
	defer x.sem.Unlock()
	return x.active
}

// Topics returns the list of topics the consumer has subscribed to
func (x *subscriber) Topics() []string {
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
func (x *subscriber) Shutdown() {
	// acquire the lock
	x.sem.Lock()
	// release the lock once done
	defer x.sem.Unlock()
	x.active = false
	x.messages.Close()
}

func (x *subscriber) Iterator() chan *Message {
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

// signal is used to push a message to the subscriber
func (x *subscriber) signal(message *Message) {
	// acquire the lock
	x.sem.Lock()
	// release the lock once done
	defer x.sem.Unlock()
	// only receive message when active
	if x.active {
		x.messages.Push(message)
	}
}

// subscribe subscribes the subscriber to a given topic
func (x *subscriber) subscribe(topic string) {
	// acquire the lock
	x.sem.Lock()
	// set the topic
	x.topics[topic] = true
	// release the lock
	x.sem.Unlock()
}

// unsubscribe unsubscribes the subscriber from the give topic
func (x *subscriber) unsubscribe(topic string) {
	// acquire the lock
	x.sem.Lock()
	// remove the topic from the consumer topics
	delete(x.topics, topic)
	// release the lock
	x.sem.Unlock()
}
