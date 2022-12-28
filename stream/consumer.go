package stream

import (
	"context"
	"sync"
	"time"

	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

type consumer struct {
	// id represents the consumer ID
	id uint32
	// stopped when true means that the consumer is stopped
	stopped  *atomic.Bool
	stopChan chan struct{}

	// helps check whether we are not sending to a closed channel.
	processingLock  sync.Mutex
	messagesChannel chan *Message

	logger log.Logger

	consumerCtx context.Context
}

// Stop stops the consumer
func (c *consumer) Stop() {
	// check whether the consumer is already stopped
	if !c.isActive() {
		return
	}

	// close the stop channel
	close(c.stopChan)
	c.processingLock.Lock()
	defer c.processingLock.Unlock()

	c.stopped.Store(true)
	close(c.messagesChannel)
}

// isActive helps check whether the consumer is active or not
// it returns TRUE when the consumer is active or not
func (c *consumer) isActive() bool {
	return c.stopped.Load()
}

// handleMessage handles the message given to a consumer
func (c *consumer) handleMessage(message *Message) {
	// acquire the processing lock
	c.processingLock.Lock()
	// free the lock once done
	defer c.processingLock.Unlock()

	ctx, cancel := context.WithCancel(c.consumerCtx)
	defer cancel()

loop:
	for {
		// since the same message is sent to all consumers we need to act on an immutable message
		msg := message.Clone()
		msg.SetContext(ctx)

		// only handleMessage message when the consumer is not closed
		if c.stopped.Load() {
			c.logger.Warning("message discarded because consumer is closed")
			return
		}

		// send the message to consumer
		select {
		case c.messagesChannel <- msg:
		case <-c.stopChan:
			c.logger.Info("Stopping. Message discarded...")
		}

		// patiently waiting for ack
		select {
		case <-msg.Accepted():
			return
		case <-msg.Rejected():
			continue loop
		case <-c.stopChan:
			c.logger.Info("Stopping. Message discarded...")
			return
		case <-time.After(time.Second): // TODO make it configurable
			return
		}
	}
}
