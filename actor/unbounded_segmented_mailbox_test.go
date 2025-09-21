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

func TestUnboundedSegmentedMailbox_Basic(t *testing.T) {
	mb := NewUnboundedSegmentedMailbox()

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

func TestUnboundedSegmentedMailbox_OneProducer(t *testing.T) {
	t.Helper()
	exp := 1000
	mb := NewUnboundedSegmentedMailbox()

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

	for range exp {
		require.NoError(t, mb.Enqueue(new(ReceiveContext)))
	}

	wg.Wait()
	assert.True(t, mb.IsEmpty())
}

func TestUnboundedSegmentedMailbox_MultipleProducers(t *testing.T) {
	t.Helper()
	producers := 4
	perProducer := 1000
	total := producers * perProducer

	mb := NewUnboundedSegmentedMailbox()

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
	for range producers {
		go func() {
			defer prodWg.Done()
			for range perProducer {
				_ = mb.Enqueue(new(ReceiveContext))
			}
		}()
	}

	prodWg.Wait()
	consumerWg.Wait()
	assert.True(t, mb.IsEmpty())
}

func TestUnboundedSegmentedMailbox_Dequeue_OrderAcrossSegments(t *testing.T) {
	mb := NewUnboundedSegmentedMailbox()

	total := segmentSize*3 + 10 // span 3 full segments plus a partial
	msgs := make([]*ReceiveContext, total)

	for i := range total {
		rc := &ReceiveContext{}
		msgs[i] = rc
		require.NoError(t, mb.Enqueue(rc))
	}

	require.EqualValues(t, total, mb.Len())

	for i := range total {
		got := mb.Dequeue()
		require.NotNil(t, got, "expected message at index %d", i)
		if got != msgs[i] {
			t.Fatalf("out of order: index=%d expected=%p got=%p", i, msgs[i], got)
		}
	}

	assert.True(t, mb.IsEmpty())
	assert.EqualValues(t, 0, mb.Len())
	assert.Nil(t, mb.Dequeue())
}

func TestUnboundedSegmentedMailbox_Dequeue_Interleaved(t *testing.T) {
	mb := NewUnboundedSegmentedMailbox()

	total := segmentSize + 10 // force at least one segment rollover
	msgs := make([]*ReceiveContext, 0, total)

	half1 := total / 2
	for range half1 {
		rc := &ReceiveContext{}
		msgs = append(msgs, rc)
		require.NoError(t, mb.Enqueue(rc))
	}

	quarter1 := half1 / 2
	for i := range quarter1 {
		got := mb.Dequeue()
		require.NotNil(t, got)
		require.Equal(t, msgs[i], got)
	}

	// Enqueue the rest
	for i := half1; i < total; i++ {
		rc := &ReceiveContext{}
		msgs = append(msgs, rc)
		require.NoError(t, mb.Enqueue(rc))
	}

	// Dequeue remaining and validate full global order
	for i := quarter1; i < total; i++ {
		got := mb.Dequeue()
		require.NotNil(t, got)
		require.Equal(t, msgs[i], got, "mismatch at global index %d", i)
	}

	assert.True(t, mb.IsEmpty())
	assert.EqualValues(t, 0, mb.Len())
	assert.Nil(t, mb.Dequeue())
}

func BenchmarkUnboundedSegmentedMailbox(b *testing.B) {
	mb := NewUnboundedSegmentedMailbox()

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
