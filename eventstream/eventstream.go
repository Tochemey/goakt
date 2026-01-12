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

import "sync"

// Stream defines the event stream broker.
type Stream interface {
	// AddSubscriber adds a subscriber.
	AddSubscriber() Subscriber
	// RemoveSubscriber removes a subscriber.
	RemoveSubscriber(sub Subscriber)
	// SubscribersCount returns the number of subscribers for a given topic.
	SubscribersCount(topic string) int
	// Subscribe subscribes a subscriber to a topic.
	Subscribe(sub Subscriber, topic string)
	// Unsubscribe removes a subscriber from a topic.
	Unsubscribe(sub Subscriber, topic string)
	// Publish publishes a message to a topic.
	Publish(topic string, msg any)
	// Broadcast notifies all subscribers of the given topics of a new message.
	Broadcast(msg any, topics []string)
	// Close closes the stream.
	Close()
}

// EventsStream is the default Stream implementation.
type EventsStream struct {
	subsMu      sync.RWMutex
	subscribers map[string]Subscriber

	topicsMu sync.RWMutex
	topics   map[string]map[string]Subscriber
}

var _ Stream = (*EventsStream)(nil)

// New creates an instance of EventsStream.
func New() Stream {
	return &EventsStream{
		subscribers: make(map[string]Subscriber),
		topics:      make(map[string]map[string]Subscriber),
	}
}

func (b *EventsStream) AddSubscriber() Subscriber {
	sub := newSubscriber()
	b.subsMu.Lock()
	b.subscribers[sub.ID()] = sub
	b.subsMu.Unlock()
	return sub
}

func (b *EventsStream) RemoveSubscriber(sub Subscriber) {
	for _, topic := range sub.Topics() {
		b.Unsubscribe(sub, topic)
	}

	b.subsMu.Lock()
	delete(b.subscribers, sub.ID())
	b.subsMu.Unlock()

	sub.Shutdown()
}

func (b *EventsStream) SubscribersCount(topic string) int {
	b.topicsMu.RLock()
	subs := b.topics[topic]
	b.topicsMu.RUnlock()
	return len(subs)
}

func (b *EventsStream) Subscribe(sub Subscriber, topic string) {
	if !sub.Active() {
		return
	}

	sub.subscribe(topic)

	b.topicsMu.Lock()
	subs, ok := b.topics[topic]
	if !ok {
		subs = make(map[string]Subscriber)
		b.topics[topic] = subs
	}
	subs[sub.ID()] = sub
	b.topicsMu.Unlock()
}

func (b *EventsStream) Unsubscribe(sub Subscriber, topic string) {
	sub.unsubscribe(topic)

	b.topicsMu.Lock()
	subs, ok := b.topics[topic]
	if ok {
		delete(subs, sub.ID())
		if len(subs) == 0 {
			delete(b.topics, topic)
		}
	}
	b.topicsMu.Unlock()
}

func (b *EventsStream) Publish(topic string, msg any) {
	b.publishToTopic(topic, msg)
}

func (b *EventsStream) Broadcast(msg any, topics []string) {
	for _, topic := range topics {
		b.publishToTopic(topic, msg)
	}
}

func (b *EventsStream) Close() {
	b.subsMu.Lock()
	for _, sub := range b.subscribers {
		if sub.Active() {
			sub.Shutdown()
		}
	}
	b.subscribers = make(map[string]Subscriber)
	b.subsMu.Unlock()

	b.topicsMu.Lock()
	b.topics = make(map[string]map[string]Subscriber)
	b.topicsMu.Unlock()
}

func (b *EventsStream) publishToTopic(topic string, msg any) {
	b.topicsMu.RLock()
	subs := b.topics[topic]
	if len(subs) == 0 {
		b.topicsMu.RUnlock()
		return
	}
	snapshot := make([]Subscriber, 0, len(subs))
	for _, sub := range subs {
		snapshot = append(snapshot, sub)
	}
	b.topicsMu.RUnlock()

	message := NewMessage(topic, msg)
	for _, sub := range snapshot {
		if sub.Active() {
			sub.signal(message)
		}
	}
}
