/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actor

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSegmentedRingBufferMailbox_Basic(t *testing.T) {
	mb := NewSegmentedRingBufferMailbox()

	in1 := &ReceiveContext{}
	in2 := &ReceiveContext{}

	require.NoError(t, mb.Enqueue(in1))
	require.NoError(t, mb.Enqueue(in2))

	out1 := mb.Dequeue()
	out2 := mb.Dequeue()

	assert.Equal(t, in1, out1)
	assert.Equal(t, in2, out2)
	assert.True(t, mb.IsEmpty())
	assert.Nil(t, mb.Dequeue())
}

func TestSegmentedRingBufferMailbox_OneProducer(t *testing.T) {
	t.Helper()
	exp := 1000
	mb := NewSegmentedRingBufferMailbox()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		cnt := 0
		for cnt < exp {
			if mb.Dequeue() != nil {
				cnt++
			} else {
				runtime.Gosched()
			}
		}
	}()

	for i := 0; i < exp; i++ {
		require.NoError(t, mb.Enqueue(new(ReceiveContext)))
	}

	wg.Wait()
	assert.True(t, mb.IsEmpty())
}

func TestSegmentedRingBufferMailbox_MultipleProducers(t *testing.T) {
	t.Helper()
	producers := 4
	perProducer := 1000
	total := producers * perProducer

	mb := NewSegmentedRingBufferMailbox()

	var consumerWg sync.WaitGroup
	consumerWg.Add(1)
	go func() {
		defer consumerWg.Done()
		cnt := 0
		for cnt < total {
			if mb.Dequeue() != nil {
				cnt++
			} else {
				runtime.Gosched()
			}
		}
	}()

	var prodWg sync.WaitGroup
	prodWg.Add(producers)
	for p := 0; p < producers; p++ {
		go func() {
			defer prodWg.Done()
			for i := 0; i < perProducer; i++ {
				_ = mb.Enqueue(new(ReceiveContext))
			}
		}()
	}

	prodWg.Wait()
	consumerWg.Wait()
	assert.True(t, mb.IsEmpty())
}

func BenchmarkSegmentedRingBufferMailbox(b *testing.B) {
	mb := NewSegmentedRingBufferMailbox()

	var processed int64
	done := make(chan struct{})

	// single consumer
	go func(target int64) {
		for atomic.LoadInt64(&processed) < target {
			if r := mb.Dequeue(); r != nil {
				atomic.AddInt64(&processed, 1)
			} else {
				runtime.Gosched()
			}
		}
		close(done)
	}(int64(b.N))

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		msg := new(ReceiveContext)
		for pb.Next() {
			_ = mb.Enqueue(msg)
		}
	})
	<-done
	b.StopTimer()

	opsPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(opsPerSec, "ops/sec")
}
