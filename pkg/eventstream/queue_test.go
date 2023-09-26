package eventstream

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnboundedQueue(t *testing.T) {
	t.Run("With Push/Pop", func(t *testing.T) {
		q := newUnboundedQueue[int](2)
		for j := 0; j < 100; j++ {
			if q.Len() != 0 {
				t.Fatal("expected no elements")
			} else if _, ok := q.Pop(); ok {
				t.Fatal("expected no elements")
			}

			for i := 0; i < j; i++ {
				q.Push(i)
			}

			for i := 0; i < j; i++ {
				if x, ok := q.Pop(); !ok {
					t.Fatal("expected an element")
				} else if x != i {
					t.Fatalf("expected %d got %d", i, x)
				}
			}
		}

		a := 0
		r := 0
		for j := 0; j < 100; j++ {
			for i := 0; i < 4; i++ {
				q.Push(a)
				a++
			}

			for i := 0; i < 2; i++ {
				if x, ok := q.Pop(); !ok {
					t.Fatal("expected an element")
				} else if x != r {
					t.Fatalf("expected %d got %d", r, x)
				}
				r++
			}
		}

		if q.Len() != 200 {
			t.Fatalf("expected 200 elements have %d", q.Len())
		}

		assert.False(t, q.IsClosed())
		assert.True(t, q.Cap() > 0)

		// close the queue
		q.Close()
		assert.True(t, q.IsClosed())
		assert.Zero(t, q.Len())
		assert.Zero(t, q.Cap())
	})
	t.Run("With Wait", func(t *testing.T) {
		q := newUnboundedQueue[int](2)
		assert.True(t, q.Push(1))
		assert.True(t, q.Push(2))
		assert.True(t, q.Push(3))
		assert.True(t, q.Push(4))
		assert.EqualValues(t, 4, q.Len())

		wait, ok := q.Wait()
		assert.True(t, ok)
		assert.EqualValues(t, 1, wait)
		// close the queue
		q.Close()
		assert.True(t, q.IsClosed())
		assert.Zero(t, q.Len())
		assert.Zero(t, q.Cap())
	})
}
