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
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/tochemey/goakt/v4/internal/queue"
)

// Subscriber defines the subscriber interface.
//
// Note: the unexported methods intentionally prevent external implementations.
// Subscribers are created by a Stream via AddSubscriber().
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

// subscriber defines the subscriber.
type subscriber struct {
	id string

	topicsMu sync.Mutex
	topics   map[string]bool

	messages *queue.Queue

	active atomic.Bool
}

var _ Subscriber = (*subscriber)(nil)

func newSubscriber() *subscriber {
	s := &subscriber{
		id:       uuid.NewString(),
		topics:   make(map[string]bool),
		messages: queue.NewQueue(),
	}
	s.active.Store(true)
	return s
}

func (s *subscriber) ID() string {
	return s.id
}

func (s *subscriber) Active() bool {
	return s.active.Load()
}

func (s *subscriber) Topics() []string {
	s.topicsMu.Lock()
	defer s.topicsMu.Unlock()

	topics := make([]string, 0, len(s.topics))
	for topic := range s.topics {
		topics = append(topics, topic)
	}
	return topics
}

func (s *subscriber) Shutdown() {
	s.active.Store(false)
}

// Iterator drains the messages that are buffered at the time of invocation and
// returns them through a closed channel.
//
// Messages enqueued concurrently with (or after) the call are not guaranteed to
// be included in this iterator.
func (s *subscriber) Iterator() chan *Message {
	n := int(s.messages.Length())
	out := make(chan *Message, n)
	for range n {
		v := s.messages.Dequeue()
		if v == nil {
			break
		}
		out <- v.(*Message)
	}
	close(out)
	return out
}

func (s *subscriber) signal(message *Message) {
	// only receive message when active
	if s.active.Load() {
		s.messages.Enqueue(message)
	}
}

func (s *subscriber) subscribe(topic string) {
	s.topicsMu.Lock()
	s.topics[topic] = true
	s.topicsMu.Unlock()
}

func (s *subscriber) unsubscribe(topic string) {
	s.topicsMu.Lock()
	delete(s.topics, topic)
	s.topicsMu.Unlock()
}
