package stream

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
)

func TestEventsStream(t *testing.T) {
	t.Run("with Produce/Consume with no retention store", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		es := NewEventsStream(logger, nil)
		topic := "test-topic"

		// start consuming messages
		msgs, err := es.Consume(ctx, topic)
		require.NoError(t, err, "cannot consume messages")

		// create the message ID
		msgID := uuid.NewString()

		producedChan := make(chan struct{})
		go func() {
			// build messages to produce
			err = es.Produce(ctx, topic, NewMessage(msgID, nil))
			require.NoError(t, err, "cannot produce messages")
			close(producedChan)
		}()

		msg1 := <-msgs
		select {
		case <-producedChan:
			t.Fatal("production should be blocked until accept")
		default:
			// ok
		}

		msg1.Reject()
		select {
		case <-producedChan:
			t.Fatal("production should be blocked after reject")
		default:
			// ok
		}

		msg2 := <-msgs
		msg2.Accept()

		select {
		case <-producedChan:
			// ok
		case <-time.After(time.Second):
			t.Fatal("produce should be not blocked after accept")
		}

		// stop the events stream
		assert.NoError(t, es.Stop(ctx))
	})
	t.Run("With Produce/Consume with others subscribers not blocked", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		es := NewEventsStream(logger, nil)
		topic := "test-topic"

		// start consuming messages
		msgsChan1, err := es.Consume(ctx, topic)
		require.NoError(t, err, "cannot consume messages")

		_, err = es.Consume(ctx, topic)
		require.NoError(t, err)

		msgsChan3, err := es.Consume(ctx, topic)
		require.NoError(t, err)

		err = es.Produce(ctx, topic, NewMessage(uuid.NewString(), nil))
		require.NoError(t, err)

		consumedChan := make(chan struct{})
		go func() {
			msg := <-msgsChan1
			msg.Accept()

			msg = <-msgsChan3
			msg.Accept()

			close(consumedChan)
		}()

		select {
		case <-consumedChan:
		// ok
		case <-time.After(5 * time.Second):
			t.Fatal("consumer which didn't accept a message blocked other subscribers from receiving it")
		}
	})
	t.Run("fail to produce/consume when stopped", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		es := NewEventsStream(logger, nil)
		topic := "test-topic"

		// stop the events stream
		assert.NoError(t, es.Stop(ctx))

		_, err := es.Consume(ctx, topic)
		require.Error(t, err)
		assert.EqualError(t, err, "events stream is already closed")

		err = es.Produce(ctx, topic, NewMessage(uuid.NewString(), nil))
		require.Error(t, err)
		assert.EqualError(t, err, "events stream is already closed")
	})
}
