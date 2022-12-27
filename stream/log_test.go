package stream

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryLog(t *testing.T) {
	t.Run("with new instance", func(t *testing.T) {
		TTL := time.Second
		c := NewMemoryLog(10, TTL)
		assert.NotNil(t, c)
		assert.IsType(t, &MemoryLog{}, c)
		var p interface{} = c
		_, ok := p.(RetentionLog)
		assert.True(t, ok)
	})
	t.Run("with Persist and Get", func(t *testing.T) {
		ctx := context.TODO()
		TTL := time.Minute
		memoryLog := NewMemoryLog(10, TTL)
		assert.NotNil(t, memoryLog)

		// connect the memory log
		require.NoError(t, memoryLog.Connect(ctx))

		// create the topic name
		topicName := "test"

		// create some messages that we can persist
		messages := make([]*Message, 5)
		for i := 0; i < 5; i++ {
			messages[i] = NewMessage(uuid.NewString(), nil)
		}

		// create the topic
		topic := &Topic{
			Name:     topicName,
			Messages: messages,
		}

		// persist the data
		err := memoryLog.Persist(ctx, topic)
		require.NoError(t, err)

		// fetch the messages
		actual, err := memoryLog.GetMessages(ctx, topicName)
		require.NoError(t, err)
		require.NotEmpty(t, actual)
		assert.Equal(t, len(messages), len(actual))

		// disconnect the memory log
		require.NoError(t, memoryLog.Disconnect(ctx))
	})
	t.Run("with expired entry", func(t *testing.T) {
		ctx := context.TODO()
		TTL := time.Second
		memoryLog := NewMemoryLog(10, TTL)
		assert.NotNil(t, memoryLog)

		// connect the memory log
		require.NoError(t, memoryLog.Connect(ctx))

		// create the topic name
		topicName := "test"

		// create some messages that we can persist
		messages := make([]*Message, 5)
		for i := 0; i < 5; i++ {
			messages[i] = NewMessage(uuid.NewString(), nil)
		}

		// create the topic
		topic := &Topic{
			Name:     topicName,
			Messages: messages,
		}

		// persist the data
		err := memoryLog.Persist(ctx, topic)
		require.NoError(t, err)
		// let us wait for more than a second and access the value
		time.Sleep(3 * time.Second)
		// fetch the messages
		actual, err := memoryLog.GetMessages(ctx, topicName)
		require.NoError(t, err)
		require.NotEmpty(t, actual)

		// disconnect the memory log
		require.NoError(t, memoryLog.Disconnect(ctx))
	})
}
