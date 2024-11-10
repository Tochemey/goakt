/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package eventstream

import (
	"sync"
)

// Subscribers defines the map of subscribers
type Subscribers map[string]Subscriber

// EventsStream defines the stream broker
type EventsStream struct {
	subs   Subscribers
	topics map[string]Subscribers
	mu     sync.Mutex
}

// New creates an instance of EventsStream
func New() *EventsStream {
	return &EventsStream{
		subs:   Subscribers{},
		topics: map[string]Subscribers{},
		mu:     sync.Mutex{},
	}
}

// AddSubscriber adds a subscriber
func (b *EventsStream) AddSubscriber() Subscriber {
	b.mu.Lock()
	c := newSubscriber()
	b.subs[c.ID()] = c
	b.mu.Unlock()
	return c
}

// RemoveSubscriber removes a subscriber
func (b *EventsStream) RemoveSubscriber(sub Subscriber) {
	// remove subscriber to the broker.
	//unsubscribe to all topics which s is subscribed to.
	for _, topic := range sub.Topics() {
		b.Unsubscribe(sub, topic)
	}
	b.mu.Lock()
	// remove subscriber from list of subscribers.
	delete(b.subs, sub.ID())
	b.mu.Unlock()
	sub.Shutdown()
}

// Broadcast notifies all subscribers of a given topic of a new message
func (b *EventsStream) Broadcast(msg any, topics []string) {
	// broadcast message to all topics.
	for _, topic := range topics {
		for _, consumer := range b.topics[topic] {
			m := NewMessage(topic, msg)
			if !consumer.Active() {
				return
			}
			go (func(s Subscriber) {
				s.signal(m)
			})(consumer)
		}
	}
}

// SubscribersCount returns the number of subscribers for a given topic
func (b *EventsStream) SubscribersCount(topic string) int {
	// get total subscribers subscribed to given topic.
	b.mu.Lock()
	count := len(b.topics[topic])
	b.mu.Unlock()
	return count
}

// Subscribe subscribes a subscriber to a topic
func (b *EventsStream) Subscribe(sub Subscriber, topic string) {
	// subscribe to given topic
	b.mu.Lock()
	defer b.mu.Unlock()

	// only subscribe active consumer
	if !sub.Active() {
		return
	}

	if b.topics[topic] == nil {
		b.topics[topic] = Subscribers{}
	}
	sub.subscribe(topic)
	b.topics[topic][sub.ID()] = sub
}

// Unsubscribe removes a subscriber from a topic
func (b *EventsStream) Unsubscribe(sub Subscriber, topic string) {
	// unsubscribe to given topic
	b.mu.Lock()
	defer b.mu.Unlock()

	// only unsubscribe active subscriber
	if !sub.Active() {
		return
	}

	delete(b.topics[topic], sub.ID())
	sub.unsubscribe(topic)
}

// Publish publishes a message to a topic
func (b *EventsStream) Publish(topic string, msg any) {
	// publish the message to given topic.
	b.mu.Lock()
	defer b.mu.Unlock()
	bTopics := b.topics[topic]
	for _, consumer := range bTopics {
		m := NewMessage(topic, msg)
		if !consumer.Active() {
			return
		}
		go (func(s Subscriber) {
			s.signal(m)
		})(consumer)
	}
}

// Shutdown shutdowns the broker
func (b *EventsStream) Shutdown() {
	// acquire the lock
	b.mu.Lock()
	// release the lock once done
	defer b.mu.Unlock()
	for _, sub := range b.subs {
		if sub.Active() {
			sub.Shutdown()
		}
	}
	b.subs = Subscribers{}
	b.topics = map[string]Subscribers{}
}

// Subscribers returns the list of subscribers
func (b *EventsStream) Subscribers() Subscribers {
	b.mu.Lock()
	subs := b.subs
	b.mu.Unlock()
	return subs
}
