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
	"github.com/tochemey/goakt/v3/internal/xsync"
)

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
	subscribers *xsync.Map[string, Subscriber]
	topics      *xsync.Map[string, *xsync.Map[string, Subscriber]]
}

// enforce a compilation error
var _ Stream = (*EventsStream)(nil)

// New creates an instance of EventsStream
func New() Stream {
	return &EventsStream{
		subscribers: xsync.NewMap[string, Subscriber](),
		topics:      xsync.NewMap[string, *xsync.Map[string, Subscriber]](),
	}
}

// AddSubscriber adds a subscriber
func (b *EventsStream) AddSubscriber() Subscriber {
	subscriber := newSubscriber()
	b.subscribers.Set(subscriber.ID(), subscriber)
	return subscriber
}

// RemoveSubscriber removes a subscriber
func (b *EventsStream) RemoveSubscriber(sub Subscriber) {
	// remove subscriber to the broker.
	//unsubscribe to all topics which s is subscribed to.
	for _, topic := range sub.Topics() {
		b.Unsubscribe(sub, topic)
	}
	b.subscribers.Delete(sub.ID())
	sub.Shutdown()
}

// Broadcast notifies all subscribers of a given topic of a new message
func (b *EventsStream) Broadcast(msg any, topics []string) {
	for _, topic := range topics {
		b.publishToTopic(topic, msg)
	}
}

// SubscribersCount returns the number of subscribers for a given topic
func (b *EventsStream) SubscribersCount(topic string) int {
	if subscribers, ok := b.topics.Get(topic); ok {
		return subscribers.Len()
	}
	return 0
}

// Subscribe subscribes a subscriber to a topic
func (b *EventsStream) Subscribe(subscriber Subscriber, topic string) {
	// subscribe to given topic
	// only subscribe active consumer
	if !subscriber.Active() {
		return
	}

	subscriber.subscribe(topic)
	if subscribers, ok := b.topics.Get(topic); ok && subscribers.Len() != 0 {
		subscribers.Set(subscriber.ID(), subscriber)
		return
	}

	// here the topic does not exist
	subscribers := xsync.NewMap[string, Subscriber]()
	subscribers.Set(subscriber.ID(), subscriber)
	b.topics.Set(topic, subscribers)
}

// Unsubscribe removes a subscriber from a topic
func (b *EventsStream) Unsubscribe(subscriber Subscriber, topic string) {
	subscriber.unsubscribe(topic)
	if subscribers, ok := b.topics.Get(topic); ok && subscribers.Len() != 0 {
		subscribers.Delete(subscriber.ID())
	}
}

// Publish publishes a message to a topic
func (b *EventsStream) Publish(topic string, msg any) {
	b.publishToTopic(topic, msg)
}

// Close closes the stream
func (b *EventsStream) Close() {
	for _, subscriber := range b.subscribers.Values() {
		if subscriber.Active() {
			subscriber.Shutdown()
		}
	}
	b.subscribers.Reset()
	b.topics.Reset()
}

// publishToTopic delivers the message to active subscribers of the given topic.
// It performs a single message allocation per topic publish and signals
// asynchronously to avoid blocking the caller.
func (b *EventsStream) publishToTopic(topic string, msg any) {
	subscribers, ok := b.topics.Get(topic)
	if !ok || subscribers.Len() == 0 {
		return
	}

	message := NewMessage(topic, msg)
	// fan-out concurrently so subscribers are signaled at roughly the same time.
	subscribers.Range(func(_ string, sub Subscriber) {
		if sub.Active() {
			go sub.signal(message)
		}
	})
}
