package stream

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
)

func TestEventsStream(t *testing.T) {
	t.Run("with Produce/Scan with no retention store", func(t *testing.T) {
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
	})
}
