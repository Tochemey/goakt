package stream

import (
	"fmt"
	"testing"
	"time"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestScanner(t *testing.T) {
	t.Run("testcase 1", func(t *testing.T) {
		messagesCount := 50

		var messages []*Message
		messagesCh := make(chan *Message, messagesCount)

		// push messages to the channel
		for i := 0; i < messagesCount; i++ {
			msg := NewMessage(uuid.NewString(), []byte(fmt.Sprintf("message=%d", i)))
			messages = append(messages, msg)
			messagesCh <- msg
		}

		// create an new instance of Sink
		scanner := NewScanner(messagesCh, messagesCount, time.Second)

		// read the messages
		consumed, ok := scanner.Scan()
		assert.True(t, ok)
		assert.Equal(t, len(messages), len(consumed))

		// let us sort both produced and consumed ids
		sentIDs := goset.NewSet[string]()
		for _, sent := range messages {
			sentIDs.Add(sent.ID())
		}

		consumedIDs := goset.NewSet[string]()
		for _, msg := range consumed {
			consumedIDs.Add(msg.ID())
		}

		assert.Equal(t, len(sentIDs.ToSlice()), len(consumedIDs.ToSlice()))
	})
	t.Run("testcase 2", func(t *testing.T) {
		messagesCount := 50

		var messages []*Message
		messagesCh := make(chan *Message, messagesCount)

		// push messages to the channel
		for i := 0; i < messagesCount; i++ {
			msg := NewMessage(uuid.NewString(), []byte(fmt.Sprintf("message=%d", i)))
			messages = append(messages, msg)
			messagesCh <- msg
		}

		scanner := NewScanner(messagesCh, messagesCount, time.Second, WithDedupe())
		// read the messages
		consumed, ok := scanner.Scan()
		assert.True(t, ok)
		assert.Equal(t, len(messages), len(consumed))

		// let us sort both produced and consumed ids
		sentIDs := goset.NewSet[string]()
		for _, sent := range messages {
			sentIDs.Add(sent.ID())
		}

		consumedIDs := goset.NewSet[string]()
		for _, msg := range consumed {
			consumedIDs.Add(msg.ID())
		}

		assert.Equal(t, len(sentIDs.ToSlice()), len(consumedIDs.ToSlice()))
	})
	t.Run("with timeout testcase 1", func(t *testing.T) {
		messagesCount := 100
		sendLimit := 90

		var messages []*Message
		messagesCh := make(chan *Message, messagesCount)

		// push messages to the channel
		for i := 0; i < messagesCount; i++ {
			msg := NewMessage(uuid.NewString(), []byte(fmt.Sprintf("message=%d", i)))
			messages = append(messages, msg)
			if i < sendLimit {
				messagesCh <- msg
			}
		}

		scanner := NewScanner(messagesCh, messagesCount, time.Millisecond)
		// read the messages
		start := time.Now()
		consumed, ok := scanner.Scan()
		assert.WithinDuration(t, start, time.Now(), time.Millisecond*100)
		assert.False(t, ok)
		assert.Equal(t, sendLimit, len(consumed))
	})
	t.Run("with timeout testcase 2", func(t *testing.T) {
		messagesCount := 100
		sendLimit := 90

		var messages []*Message
		messagesCh := make(chan *Message, messagesCount)

		// push messages to the channel
		for i := 0; i < messagesCount; i++ {
			msg := NewMessage(uuid.NewString(), []byte(fmt.Sprintf("message=%d", i)))
			messages = append(messages, msg)
			if i < sendLimit {
				messagesCh <- msg
			}
		}

		scanner := NewScanner(messagesCh, messagesCount, time.Millisecond, WithDedupe())
		// read the messages
		start := time.Now()
		consumed, ok := scanner.Scan()
		assert.WithinDuration(t, start, time.Now(), time.Millisecond*100)
		assert.False(t, ok)
		assert.Equal(t, sendLimit, len(consumed))
	})
	t.Run("within limit testcase 1", func(t *testing.T) {
		messagesCount := 50
		limit := 40

		var messages []*Message
		messagesCh := make(chan *Message, messagesCount)

		// push messages to the channel
		for i := 0; i < messagesCount; i++ {
			msg := NewMessage(uuid.NewString(), []byte(fmt.Sprintf("message=%d", i)))
			messages = append(messages, msg)
			messagesCh <- msg
		}

		scanner := NewScanner(messagesCh, limit, time.Second)
		// read the messages
		consumed, ok := scanner.Scan()
		assert.True(t, ok)
		assert.Equal(t, limit, len(consumed))
	})
	t.Run("within limit testcase 2", func(t *testing.T) {
		messagesCount := 50
		limit := 40

		var messages []*Message
		messagesCh := make(chan *Message, messagesCount)

		// push messages to the channel
		for i := 0; i < messagesCount; i++ {
			msg := NewMessage(uuid.NewString(), []byte(fmt.Sprintf("message=%d", i)))
			messages = append(messages, msg)
			messagesCh <- msg
		}

		scanner := NewScanner(messagesCh, limit, time.Second, WithDedupe())
		// read the messages
		consumed, ok := scanner.Scan()
		assert.True(t, ok)
		assert.Equal(t, limit, len(consumed))
	})
	t.Run("when channel is closed testcase 1", func(t *testing.T) {
		messagesCount := 50
		limit := 30
		var messages []*Message
		messagesCh := make(chan *Message, messagesCount)
		channelClosed := false

		// push messages to the channel
		for i := 0; i < messagesCount; i++ {
			msg := NewMessage(uuid.NewString(), []byte(fmt.Sprintf("message=%d", i)))
			messages = append(messages, msg)
			if i < limit {
				messagesCh <- msg
			} else if !channelClosed {
				close(messagesCh)
				channelClosed = true
			}
		}

		scanner := NewScanner(messagesCh, messagesCount, time.Second)
		// read the messages
		startTime := time.Now()
		_, ok := scanner.Scan()
		assert.WithinDuration(t, startTime, time.Now(), time.Millisecond*100)
		assert.False(t, ok)
	})
	t.Run("when channel is closed testcase 2", func(t *testing.T) {
		messagesCount := 50
		limit := 30
		var messages []*Message
		messagesCh := make(chan *Message, messagesCount)
		channelClosed := false

		// push messages to the channel
		for i := 0; i < messagesCount; i++ {
			msg := NewMessage(uuid.NewString(), []byte(fmt.Sprintf("message=%d", i)))
			messages = append(messages, msg)
			if i < limit {
				messagesCh <- msg
			} else if !channelClosed {
				close(messagesCh)
				channelClosed = true
			}
		}

		scanner := NewScanner(messagesCh, messagesCount, time.Second, WithDedupe())
		// read the messages
		startTime := time.Now()
		_, ok := scanner.Scan()
		assert.WithinDuration(t, startTime, time.Now(), time.Millisecond*100)
		assert.False(t, ok)
	})
	t.Run("with dedupe", func(t *testing.T) {
		messagesCh := make(chan *Message, 3)
		id1 := uuid.NewString()
		id2 := uuid.NewString()
		msg1 := NewMessage(id1, nil)
		msg2 := NewMessage(id2, nil)
		messagesCh <- msg1
		messagesCh <- msg2
		messagesCh <- msg1
		scanner := NewScanner(messagesCh, 2, time.Second)
		consumed, ok := scanner.Scan()
		assert.True(t, ok)
		assert.Equal(t, 2, len(consumed))
		assert.Equal(t, id1, consumed[0].ID())
		assert.Equal(t, id2, consumed[1].ID())
	})
}
