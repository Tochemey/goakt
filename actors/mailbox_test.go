package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMailbox(t *testing.T) {
	t.Run("With happy path Push", func(t *testing.T) {
		buffer := newMailbox(10)
		assert.True(t, buffer.IsEmpty())
		err := buffer.Push(new(receiveContext))
		assert.NoError(t, err)
		assert.EqualValues(t, 1, buffer.Size())
	})
	t.Run("With Push when buffer is full", func(t *testing.T) {
		buffer := newMailbox(10)
		assert.True(t, buffer.IsEmpty())
		for i := 0; i < 10; i++ {
			err := buffer.Push(new(receiveContext))
			assert.NoError(t, err)
		}
		assert.EqualValues(t, 10, buffer.Size())
		// push another message to the buffer
		err := buffer.Push(new(receiveContext))
		assert.Error(t, err)
		assert.EqualError(t, err, errFullMailbox.Error())
		assert.False(t, buffer.IsEmpty())
	})
	t.Run("With happy path Pop", func(t *testing.T) {
		buffer := newMailbox(10)
		assert.True(t, buffer.IsEmpty())
		assert.NoError(t, buffer.Push(new(receiveContext)))
		assert.NoError(t, buffer.Push(new(receiveContext)))
		assert.NoError(t, buffer.Push(new(receiveContext)))
		assert.NoError(t, buffer.Push(new(receiveContext)))

		popped, err := buffer.Pop()
		assert.NoError(t, err)
		assert.NotNil(t, popped)
		assert.EqualValues(t, 3, buffer.Size())
	})
	t.Run("With Pop when buffer is empty", func(t *testing.T) {
		buffer := newMailbox(10)
		assert.True(t, buffer.IsEmpty())

		popped, err := buffer.Pop()
		assert.Error(t, err)
		assert.EqualError(t, err, errEmptyMailbox.Error())
		assert.Nil(t, popped)
	})
}
