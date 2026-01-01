// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package eventstream

import (
	"sync"

	"github.com/google/uuid"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/internal/queue"
)

// Subscriber defines the Subscriber Interface
type Subscriber interface {
	ID() string
	Active() bool
	Topics() []string
	Iterator() chan *Message
	Shutdown()
	signal(message *Message)
	subscribe(topic string)
	unsubscribe(topic string)
}

// subscriber defines the subscriber
type subscriber struct {
	// id defines the subscriber id
	id string
	// sem represents a lock
	sem sync.Mutex
	// messages of the subscriber
	messages *queue.Queue
	// topics define the topic the subscriber subscribed to
	topics map[string]bool
	// states whether the given subscriber is active or not
	active *atomic.Bool
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
		messages: queue.NewQueue(),
		topics:   make(map[string]bool),
		active:   atomic.NewBool(true),
	}
}

// ID return consumer id
func (x *subscriber) ID() string {
	return x.id
}

// Active checks whether the consumer is active
func (x *subscriber) Active() bool {
	return x.active.Load()
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
	x.active.Store(false)
}

func (x *subscriber) Iterator() chan *Message {
	// buffer to the current queue length to avoid blocking the caller
	out := make(chan *Message, x.messages.Length())
	for x.active.Load() && x.messages.Length() > 0 {
		msg := x.messages.Dequeue()
		if msg == nil {
			break
		}
		out <- msg.(*Message)
	}
	close(out)
	return out
}

// signal is used to push a message to the subscriber
func (x *subscriber) signal(message *Message) {
	// only receive message when active
	if x.active.Load() {
		x.messages.Enqueue(message)
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
