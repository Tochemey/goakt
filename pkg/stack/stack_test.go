package stack

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStack(t *testing.T) {
	// create an instance of stack
	stack := New[int]()
	assert.NotNil(t, stack)
	assert.True(t, stack.IsEmpty())

	// assert push
	stack.Push(1)
	stack.Push(2)
	stack.Push(3)

	assert.False(t, stack.IsEmpty())
	assert.EqualValues(t, 3, stack.Len())

	// assert peek
	peek, ok := stack.Peek()
	assert.True(t, ok)
	assert.NotNil(t, peek)
	assert.EqualValues(t, 3, peek)
	assert.EqualValues(t, 3, stack.Len())

	// assert pop
	pop, ok := stack.Pop()
	assert.True(t, ok)
	assert.NotNil(t, pop)
	assert.EqualValues(t, 2, stack.Len())
	assert.EqualValues(t, 3, pop)
	peek, ok = stack.Peek()
	assert.True(t, ok)
	assert.NotNil(t, peek)
	assert.EqualValues(t, 2, peek)

	// assert clear
	stack.Clear()
	assert.True(t, stack.IsEmpty())
}
