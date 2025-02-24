/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

type Stream interface {
	// AddSubscriber adds a subscriber
	AddSubscriber() Subscriber
	// RemoveSubscriber removes a subscriber
	RemoveSubscriber(sub Subscriber)
	// SubscribersCount returns the number of subscribers for a given topic
	SubscribersCount(topic string) int
	// Subscribe subscribes a subscriber to a topic
	Subscribe(sub Subscriber, topic string)
	// Unsubscribe removes a subscriber from a topic
	Unsubscribe(sub Subscriber, topic string)
	// Publish publishes a message to a topic
	Publish(topic string, msg any)
	// Broadcast notifies all subscribers of a given topic of a new message
	Broadcast(msg any, topics []string)
	// Close closes the stream
	Close()
}

// EventsStream defines the stream broker
type EventsStream struct {
	subs   Subscribers
	topics map[string]Subscribers
	mu     sync.Mutex
}

// enforce a compilation error
var _ Stream = (*EventsStream)(nil)

// New creates an instance of EventsStream
func New() Stream {
	return &EventsStream{
		subs:   Subscribers{},
		topics: map[string]Subscribers{},
		mu:     sync.Mutex{},
	}
}

// AddSubscriber adds a subscriber
func (b *EventsStream) AddSubscriber() Subscriber {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := newSubscriber()
	b.subs[c.ID()] = c
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
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, topic := range topics {
		for _, consumer := range b.topics[topic] {
			if !consumer.Active() {
				continue
			}
			go consumer.signal(NewMessage(topic, msg))
		}
	}
}

// SubscribersCount returns the number of subscribers for a given topic
func (b *EventsStream) SubscribersCount(topic string) int {
	// get total subscribers subscribed to given topic.
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.topics[topic])
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
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.topics[topic], sub.ID())
	sub.unsubscribe(topic)
}

// Publish publishes a message to a topic
func (b *EventsStream) Publish(topic string, msg any) {
	b.mu.Lock()
	subscribers := b.topics[topic]
	b.mu.Unlock()

	for _, consumer := range subscribers {
		if !consumer.Active() {
			continue
		}
		go consumer.signal(NewMessage(topic, msg))
	}
}

// Close closes the stream
func (b *EventsStream) Close() {
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
