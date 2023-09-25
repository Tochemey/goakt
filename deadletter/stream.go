package deadletter

import (
	"sync"

	"github.com/tochemey/goakt/log"
	deadletterpb "github.com/tochemey/goakt/pb/deadletter/v1"
	"github.com/tochemey/goakt/pkg/pubsub"
	"github.com/tochemey/goakt/telemetry"
)

const (
	topic = "deadletters"
	// BufferCapacity defines the default deadletter buffer capacity
	BufferCapacity = 20
)

// Stream defines the deadletter Stream
// There should be a single deadletter Stream per actor system
type Stream struct {
	engine    *pubsub.PubSub[*deadletterpb.Deadletter]
	sem       sync.Mutex
	logger    log.Logger
	telemetry *telemetry.Telemetry
	capacity  int
}

// NewStream creates an instance of Stream
func NewStream(opts ...Option) *Stream {
	// create an instance of engine
	s := &Stream{
		sem:       sync.Mutex{},
		logger:    log.DefaultLogger,
		telemetry: telemetry.New(),
		capacity:  BufferCapacity,
	}
	// apply the options
	for _, opt := range opts {
		opt.Apply(s)
	}
	// set the engine
	s.engine = pubsub.New[*deadletterpb.Deadletter](BufferCapacity)
	return s
}

// Publish publishes a deadletter
func (x *Stream) Publish(deadletter *deadletterpb.Deadletter) {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	x.engine.TryPub(deadletter, topic)
}

// Subscribe to the deadletters queue
func (x *Stream) Subscribe() chan *deadletterpb.Deadletter {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	// subscribe to the `deadletters` topic
	return x.engine.Sub(topic)
}

// Unsubscribe the given subscriber
func (x *Stream) Unsubscribe(ch chan *deadletterpb.Deadletter) {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	// unsubscribe the given channel
	x.engine.Unsub(ch, topic)
}

// Close closes the stream
func (x *Stream) Close() {
	// acquire the lock
	x.sem.Lock()
	// release the lock
	defer x.sem.Unlock()
	// shutdown the engine
	x.engine.Shutdown()
}
