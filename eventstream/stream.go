package eventstream

import (
	"sync"

	"github.com/tochemey/goakt/pkg/pubsub"
	"google.golang.org/protobuf/proto"
)

// EventsStream defines the deadletter EventsStream
// There should be a single deadletter EventsStream per actor system
type EventsStream[T proto.Message] struct {
	engine   *pubsub.PubSub[T]
	sem      sync.Mutex
	capacity int
}

// New creates an instance of EventsStream
func New[T proto.Message](capacity int) *EventsStream[T] {
	// create an instance of engine
	s := &EventsStream[T]{
		sem:      sync.Mutex{},
		capacity: capacity,
	}
	// set the engine
	s.engine = pubsub.New[T](capacity)
	return s
}

// Publish publishes a deadletter
func (x *EventsStream[T]) Publish(topic string, event T) {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	x.engine.TryPub(event, topic)
}

// Subscribe to the deadletters queue
func (x *EventsStream[T]) Subscribe(topic string) chan T {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	// subscribe to the `deadletters` topic
	return x.engine.Sub(topic)
}

// Unsubscribe the given subscriber
func (x *EventsStream[T]) Unsubscribe(ch chan T, topic string) {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	// unsubscribe the given channel
	x.engine.Unsub(ch, topic)
}

// Close closes the stream
func (x *EventsStream[T]) Close() {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	// shutdown the engine
	x.engine.Shutdown()
}
