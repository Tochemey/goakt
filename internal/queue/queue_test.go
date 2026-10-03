// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package queue

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestQueueDequeueEmpty(t *testing.T) {
	q := NewQueue()
	if q.Dequeue() != nil {
		t.Fatalf("dequeue empty queue returns non-nil")
	}
}

func TestQueueLength(t *testing.T) {
	q := NewQueue()
	if q.Length() != 0 {
		t.Fatalf("empty queue has non-zero length")
	}

	q.Enqueue(1)
	if q.Length() != 1 {
		t.Fatalf("count of enqueue wrong, want %d, got %d.", 1, q.Length())
	}

	q.Dequeue()
	if q.Length() != 0 {
		t.Fatalf("count of dequeue wrong, want %d, got %d", 0, q.Length())
	}
}

// TestQueueConcurrentProducersDeliverEveryValueOnce runs many producers against
// one consumer that drains while they publish, and checks that every value is
// dequeued exactly once and that the round finishes. A node that is handed out
// again while a producer still holds it as the tail either loses the value
// linked onto it or makes the list cyclic, in which case Enqueue spins forever;
// the watchdog turns that spin into a failure instead of a hang.
func TestQueueConcurrentProducersDeliverEveryValueOnce(t *testing.T) {
	const (
		rounds      = 200
		producers   = 8
		perProducer = 2000
		total       = producers * perProducer
		watchdog    = 10 * time.Second
	)

	for round := range rounds {
		q := NewQueue()
		start := make(chan struct{})
		published := make(chan struct{})

		var wg sync.WaitGroup
		for p := range producers {
			wg.Go(func() {
				<-start

				for i := range perProducer {
					q.Enqueue(p*perProducer + i)
				}
			})
		}

		go func() {
			wg.Wait()
			close(published)
		}()

		seen := make([]bool, total)
		got := 0
		deadline := time.Now().Add(watchdog)
		close(start)

		for got < total {
			if time.Now().After(deadline) {
				t.Fatalf("round %d: only %d of %d values dequeued before the watchdog fired", round, got, total)
			}

			v := q.Dequeue()
			if v == nil {
				select {
				case <-published:
					// the producers are done and the queue reads empty: whatever
					// is missing was lost
					if q.Dequeue() == nil {
						t.Fatalf("round %d: %d of %d values were lost", round, total-got, total)
					}
				default:
					runtime.Gosched()
				}

				continue
			}

			i := v.(int)
			if seen[i] {
				t.Fatalf("round %d: value %d dequeued twice", round, i)
			}

			seen[i] = true
			got++
		}

		select {
		case <-published:
		case <-time.After(time.Until(deadline)):
			t.Fatalf("round %d: every value was dequeued but a producer never returned from Enqueue", round)
		}

		if q.Dequeue() != nil {
			t.Fatalf("round %d: the queue still holds a value after %d dequeues", round, total)
		}

		if q.Length() != 0 {
			t.Fatalf("round %d: length is %d after draining", round, q.Length())
		}
	}
}
