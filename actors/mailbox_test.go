package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMailbox(t *testing.T) {
	t.Run("With happy path Push", func(t *testing.T) {
		mailbox := newDefaultMailbox(10)
		assert.True(t, mailbox.IsEmpty())
		err := mailbox.Push(new(receiveContext))
		assert.NoError(t, err)
		assert.EqualValues(t, 1, mailbox.Size())
	})
	t.Run("With Push when buffer is full", func(t *testing.T) {
		mailbox := newDefaultMailbox(10)
		assert.True(t, mailbox.IsEmpty())
		for i := 0; i < 10; i++ {
			err := mailbox.Push(new(receiveContext))
			assert.NoError(t, err)
		}
		assert.EqualValues(t, 10, mailbox.Size())
		// push another message to the buffer
		err := mailbox.Push(new(receiveContext))
		assert.Error(t, err)
		assert.EqualError(t, err, ErrFullMailbox.Error())
		assert.False(t, mailbox.IsEmpty())
		assert.True(t, mailbox.IsFull())
	})
	t.Run("With happy path Pop", func(t *testing.T) {
		mailbox := newDefaultMailbox(10)
		assert.True(t, mailbox.IsEmpty())
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))

		popped, err := mailbox.Pop()
		assert.NoError(t, err)
		assert.NotNil(t, popped)
		assert.EqualValues(t, 3, mailbox.Size())
	})

	t.Run("With Clone", func(t *testing.T) {
		mailbox := newDefaultMailbox(10)
		assert.True(t, mailbox.IsEmpty())
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))

		popped, err := mailbox.Pop()
		assert.NoError(t, err)
		assert.NotNil(t, popped)
		assert.EqualValues(t, 3, mailbox.Size())

		cloned := mailbox.Clone()
		assert.True(t, cloned.IsEmpty())
		assert.False(t, cloned.IsFull())
	})
	t.Run("With Pop when buffer is empty", func(t *testing.T) {
		mailbox := newDefaultMailbox(10)
		assert.True(t, mailbox.IsEmpty())

		popped, err := mailbox.Pop()
		assert.Error(t, err)
		assert.EqualError(t, err, ErrEmptyMailbox.Error())
		assert.Nil(t, popped)
	})
	t.Run("With Reset", func(t *testing.T) {
		mailbox := newDefaultMailbox(10)
		assert.True(t, mailbox.IsEmpty())
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))

		popped, err := mailbox.Pop()
		assert.NoError(t, err)
		assert.NotNil(t, popped)
		assert.EqualValues(t, 3, mailbox.Size())
		assert.False(t, mailbox.IsEmpty())

		mailbox.Reset()
		assert.True(t, mailbox.IsEmpty())
		assert.False(t, mailbox.IsFull())
	})
}
