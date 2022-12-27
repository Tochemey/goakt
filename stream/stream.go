package stream

import (
	"context"
	"fmt"
	"sync"

	cmp "github.com/orcaman/concurrent-map/v2"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

// EventsStream is a Producer/Consumer implementation.
//
// We only need one instance of EventsStream in the whole actor system.
// When EventsStream is persistent, messages order is not guaranteed.
type EventsStream struct {
	// holds the list of consumers
	consumers cmp.ConcurrentMap[string, []*consumer]
	// holds the lock to the consumers
	consumerLock sync.RWMutex
	// helps lock each topic when a message is sent to that topic
	topicLocks sync.Map
	// helps wait for all the consumers to complete the consumption
	consumersWg sync.WaitGroup
	// states that the events stream  is stopped
	stopped *atomic.Bool

	stopLock sync.Mutex
	stopChan chan struct{}

	// retention log is for persistence
	retentionLog  RetentionLog
	retentionLock sync.Mutex

	logger log.Logger
}

// NewEventsStream creates an instance of EventsStream
func NewEventsStream(logger log.Logger, persistenceStorage RetentionLog) *EventsStream {
	return &EventsStream{
		consumers:     cmp.New[[]*consumer](),
		consumerLock:  sync.RWMutex{},
		topicLocks:    sync.Map{},
		stopped:       atomic.NewBool(false),
		stopLock:      sync.Mutex{},
		stopChan:      make(chan struct{}, 1),
		retentionLog:  persistenceStorage,
		retentionLock: sync.Mutex{},
		logger:        logger,
	}
}

// Produce produces messages to given topic.
func (s *EventsStream) Produce(ctx context.Context, topic string, messages ...*Message) error {
	// only produce message when the events stream is not closed
	if s.stopped.Load() {
		s.logger.Warning("events stream is already closed")
		return errors.New("events stream is already closed")
	}

	// acquire a lock on the consumers and release them once done
	s.consumerLock.RLock()
	defer s.consumerLock.RUnlock()

	// acquire a lock on the topic and release them once done
	topicLock, _ := s.topicLocks.LoadOrStore(topic, &sync.Mutex{})
	topicLock.(*sync.Mutex).Lock()
	defer topicLock.(*sync.Mutex).Unlock()

	// persist the message when persistence storage is set
	if s.retentionLog != nil {
		s.retentionLock.Lock()
		if err := s.retentionLog.Persist(ctx, &Topic{
			Name:     topic,
			Messages: messages,
		}); err != nil {
			s.retentionLock.Unlock()
			return errors.Wrapf(err, "failed to persist events to topic=%s", topic)
		}
		s.retentionLock.Unlock()
	}

	// send messages to the various subscribers of the topic
	for _, message := range messages {
		// send the message to topic's consumers to process
		accepted, err := s.sendToConsumers(topic, message)
		// handle the processing error
		if err != nil {
			return err
		}
		select {
		case <-accepted:
			s.logger.Info("Message accepted by consumers")
		case <-s.stopChan:
			s.logger.Warning("EventsStream stopping before receipts from consumers")
		}
	}

	return nil
}

// Consume returns channel to which all produces messages are sent.
// Messages are not persisted. If there are no subscribers and message is produced it will be gone.
//
// There are no consumer groups support etc. Every consumer will receive every produced message.
func (s *EventsStream) Consume(ctx context.Context, topic string) (<-chan *Message, error) {
	// only consume when the event stream is not stopped
	s.stopLock.Lock()
	if s.stopped.Load() {
		s.stopLock.Unlock()
		return nil, errors.New("events stream is already closed")
	}

	s.stopLock.Unlock()
	s.consumersWg.Add(1)
	s.consumerLock.Lock()

	// acquire a lock on the topic and release them once done
	topicLock, _ := s.topicLocks.LoadOrStore(topic, &sync.Mutex{})
	topicLock.(*sync.Mutex).Lock()

	// create a consumer
	c := &consumer{
		id:              0,
		stopped:         atomic.NewBool(false),
		stopChan:        make(chan struct{}),
		processingLock:  sync.Mutex{},
		messagesChannel: make(chan *Message, 10_000), // TODO
		logger:          s.logger,
		consumerCtx:     ctx,
	}

	// listen to the context cancelled while the consumer is processing message
	go func(es *EventsStream, c *consumer) {
		select {
		case <-ctx.Done():
			// unblock
		case <-es.stopChan:
			// unblock
		}

		// stop the consumer
		c.Stop()

		es.consumerLock.Lock()
		defer es.consumerLock.Unlock()

		// acquire a lock on the topic and release them once done
		topicLock, _ := es.topicLocks.LoadOrStore(topic, &sync.Mutex{})
		topicLock.(*sync.Mutex).Lock()
		defer topicLock.(*sync.Mutex).Unlock()

		// let us remove the consumer from the events stream consumer list
		removed := false
		if consumers, ok := es.consumers.Get(topic); ok {
			for i, cons := range consumers {
				if cons.id == c.id {
					consumers = append(consumers[:i], consumers[i+1:]...)
					es.consumers.Set(topic, consumers)
					removed = true
					break
				}
			}
			// panic when we cannot remove the subscriber
			if !removed {
				panic(fmt.Sprintf("failed to remove subscriber=%d", c.id))
			}
		}

		es.consumersWg.Done()
	}(s, c)

	// no persistence log is set
	if s.retentionLog == nil {
		defer s.consumerLock.Unlock()
		defer topicLock.(*sync.Mutex).Unlock()
		// add the consumer to the consumers list for the given topic
		if consumers, ok := s.consumers.Get(topic); ok {
			consumers = append(consumers, c)
			s.consumers.Set(topic, consumers)
		} else {
			consumers := []*consumer{c}
			s.consumers.Set(topic, consumers)
		}

		return c.messagesChannel, nil
	}

	// replay messages to the consumers
	go func(c *consumer) {
		defer s.consumerLock.Unlock()
		defer topicLock.(*sync.Mutex).Unlock()

		// acquire the persistence lock
		s.retentionLock.Lock()
		messages, err := s.retentionLog.GetMessages(ctx, topic)
		// handle the error while fetching messages from the persistence store
		if err != nil {
			s.retentionLock.Unlock()
			wrapped := errors.Wrapf(err, "failed to replay")
			panic(wrapped)
		}
		s.retentionLock.Unlock()

		// send the messages to the consumer to process
		for _, message := range messages {
			go c.process(message)
		}
	}(c)
	return c.messagesChannel, nil
}

// Stop the events stream
func (s *EventsStream) Stop(ctx context.Context) error {
	s.stopLock.Lock()
	defer s.stopLock.Unlock()
	if s.stopped.Load() {
		return nil
	}

	s.stopped.Store(true)
	close(s.stopChan)

	s.consumersWg.Wait()

	// disconnect the retention log
	if s.retentionLog != nil {
		return s.retentionLog.Disconnect(ctx)
	}

	return nil
}

// sendToConsumers send the message to the topic's consumers to process
func (s *EventsStream) sendToConsumers(topic string, message *Message) (<-chan struct{}, error) {
	// create an accepted receipt channel
	acceptedChan := make(chan struct{}, 1)
	// get the list of consumers for the given topic
	consumers, ok := s.consumers.Get(topic)
	if !ok {
		close(acceptedChan)
		s.logger.Infof("Topic=%s does not have any consumers", topic)
		return acceptedChan, nil
	}

	go func(consumers []*consumer) {
		wg := &sync.WaitGroup{}

		for _, consumer := range consumers {
			consumer := consumer
			wg.Add(1)
			go func() {
				consumer.process(message)
				wg.Done()
			}()
		}

		wg.Wait()
		close(acceptedChan)
	}(consumers)

	return acceptedChan, nil
}
