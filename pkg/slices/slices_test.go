package slices

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConcurrentSlice(t *testing.T) {
	// create a concurrent slice of integer
	sl := NewConcurrentSlice[int]()

	// add some items
	sl.Append(2)
	sl.Append(4)
	sl.Append(5)

	// assert the length
	assert.EqualValues(t, 3, sl.Len())
	// get the element at index 2
	assert.EqualValues(t, 5, sl.Get(2))
	// remove the element at index 1
	sl.Delete(1)
	// assert the length
	assert.EqualValues(t, 2, sl.Len())
	assert.Nil(t, sl.Get(4))
}
