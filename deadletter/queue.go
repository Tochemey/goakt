package deadletter

import (
	"sync"

	deadletterpb "github.com/tochemey/goakt/pb/deadletter/v1"
	"github.com/tochemey/goakt/pkg/pubsub"
)

const (
	topic = "deadletters"
)

// queue defines the deadletter queue
// There should be a single deadletter queue per actor system
type queue struct {
	engine *pubsub.PubSub[*deadletterpb.Deadletter]
	sem    sync.Mutex
}

// newQueue creates an instance of queue
func newQueue(capacity int) *queue {
	// create an instance of engine
	eng := pubsub.New[*deadletterpb.Deadletter](capacity)
	return &queue{engine: eng}
}

// Publish publishes a deadletter
func (x *queue) Publish(deadletter *deadletterpb.Deadletter) {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	x.engine.TryPub(deadletter, topic)
}

// Subscribe to the deadletters queue
func (x *queue) Subscribe() chan *deadletterpb.Deadletter {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	// subscribe to the `deadletters` topic
	return x.engine.Sub(topic)
}

// Unsubscribe the given subscriber
func (x *queue) Unsubscribe(ch chan *deadletterpb.Deadletter) {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	// unsubscribe the given channel
	x.engine.Unsub(ch, topic)
}

// Shutdown shuts down the queue
func (x *queue) Shutdown() {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	// shutdown the engine
	x.engine.Shutdown()
}
