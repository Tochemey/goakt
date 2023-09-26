package stream

import (
	"sync"
)

// Consumers defines the map of consumers
type Consumers map[string]*Consumer

// Broker defines the stream broker
type Broker struct {
	consumers Consumers
	topics    map[string]Consumers
	mu        sync.Mutex
}

// NewBroker creates an instance of Broker
func NewBroker() *Broker {
	return &Broker{
		consumers: Consumers{},
		topics:    map[string]Consumers{},
		mu:        sync.Mutex{},
	}
}

// AddConsumer adds a consumer to the broker
func (b *Broker) AddConsumer() *Consumer {
	b.mu.Lock()
	defer b.mu.Unlock()
	id, c := NewConsumer()
	b.consumers[id] = c
	return c
}

// RemoveConsumer removes a consumer
func (b *Broker) RemoveConsumer(consumer *Consumer) {
	// remove subscriber to the broker.
	//unsubscribe to all topics which s is subscribed to.
	for topic := range consumer.topics {
		b.Unsubscribe(consumer, topic)
	}
	b.mu.Lock()
	// remove subscriber from list of subscribers.
	delete(b.consumers, consumer.id)
	b.mu.Unlock()
	consumer.Shutdown()
}

// Broadcast notifies all consumers of a given topic of a new message
func (b *Broker) Broadcast(msg any, topics []string) {
	// broadcast message to all topics.
	for _, topic := range topics {
		for _, consumer := range b.topics[topic] {
			m := NewMessage(topic, msg)
			go (func(s *Consumer) {
				s.signal(m)
			})(consumer)
		}
	}
}

// ConsumersCount returns the number of consumers for a given topic
func (b *Broker) ConsumersCount(topic string) int {
	// get total subscribers subscribed to given topic.
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.topics[topic])
}

// Subscribe subscribes a consumer to a topic
func (b *Broker) Subscribe(consumer *Consumer, topic string) {
	// subscribe to given topic
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.topics[topic] == nil {
		b.topics[topic] = Consumers{}
	}
	consumer.subscribe(topic)
	b.topics[topic][consumer.id] = consumer
}

// Unsubscribe removes a consumer from a topic
func (b *Broker) Unsubscribe(consumer *Consumer, topic string) {
	// unsubscribe to given topic
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.topics[topic], consumer.id)
	consumer.unsubscribe(topic)
}

// Publish publishes a message to a topic
func (b *Broker) Publish(topic string, msg any) {
	// publish the message to given topic.
	b.mu.Lock()
	bTopics := b.topics[topic]
	b.mu.Unlock()
	for _, consumer := range bTopics {
		m := NewMessage(topic, msg)
		if !consumer.active {
			return
		}
		go (func(s *Consumer) {
			s.signal(m)
		})(consumer)
	}
}

// Shutdown shutdowns the broker
func (b *Broker) Shutdown() {
	// acquire the lock
	b.mu.Lock()
	// release the lock once done
	defer b.mu.Unlock()
	for _, cons := range b.consumers {
		if cons.active {
			cons.Shutdown()
		}
	}
	b.consumers = Consumers{}
	b.topics = map[string]Consumers{}
}
