package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBehaviorStack(t *testing.T) {
	stack := newBehaviorStack()
	assert.NotNil(t, stack)
	assert.True(t, stack.IsEmpty())

	// push a behavior onto the stack
	behavior := func(ctx ReceiveContext) {}
	stack.Push(behavior)
	stack.Push(behavior)

	assert.False(t, stack.IsEmpty())
	assert.EqualValues(t, 2, stack.Len())

	peek, ok := stack.Peek()
	assert.True(t, ok)
	assert.NotNil(t, peek)
	assert.EqualValues(t, 2, stack.Len())

	pop, ok := stack.Pop()
	assert.True(t, ok)
	assert.NotNil(t, pop)
	assert.EqualValues(t, 1, stack.Len())

	stack.Clear()
	assert.True(t, stack.IsEmpty())
}
