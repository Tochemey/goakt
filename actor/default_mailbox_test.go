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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultMailbox_Basic(t *testing.T) {
	mailbox := NewDefaultMailbox()

	in1 := &ReceiveContext{}
	in2 := &ReceiveContext{}

	err := mailbox.Enqueue(in1)
	require.NoError(t, err)
	err = mailbox.Enqueue(in2)
	require.NoError(t, err)

	out1 := mailbox.Dequeue()
	out2 := mailbox.Dequeue()

	assert.Equal(t, in1, out1)
	assert.Equal(t, in2, out2)
	assert.True(t, mailbox.IsEmpty())

	// dequeue on empty should return nil
	assert.Nil(t, mailbox.Dequeue())

	mailbox.Dispose()
}

func TestDefaultMailbox_OneProducer(t *testing.T) {
	t.Helper()
	expCount := 100
	var wg sync.WaitGroup
	wg.Add(1)
	mailbox := NewDefaultMailbox()

	go func() {
		defer wg.Done()
		i := 0
		for {
			r := mailbox.Dequeue()
			if r == nil {
				runtime.Gosched()
				continue
			}
			i++
			if i == expCount {
				return
			}
		}
	}()

	for range expCount {
		require.NoError(t, mailbox.Enqueue(new(ReceiveContext)))
	}

	wg.Wait()
	assert.True(t, mailbox.IsEmpty())
}

func TestDefaultMailbox_MultipleProducers(t *testing.T) {
	t.Helper()
	producers := 4
	perProducer := 100
	expCount := producers * perProducer

	mailbox := NewDefaultMailbox()

	var consumerWg sync.WaitGroup
	consumerWg.Add(1)
	go func() {
		defer consumerWg.Done()
		i := 0
		for i < expCount {
			r := mailbox.Dequeue()
			if r == nil {
				runtime.Gosched()
				continue
			}
			i++
		}
	}()

	var producersWg sync.WaitGroup
	producersWg.Add(producers)
	for range producers {
		go func() {
			defer producersWg.Done()
			for range perProducer {
				_ = mailbox.Enqueue(new(ReceiveContext))
			}
		}()
	}

	producersWg.Wait()
	consumerWg.Wait()
	assert.True(t, mailbox.IsEmpty())
}

func BenchmarkDefaultMailbox(b *testing.B) {
	mailbox := NewDefaultMailbox()

	done := make(chan struct{})

	// single consumer
	go func(target int) {
		processed := 0
		for processed < target {
			if mailbox.Dequeue() != nil {
				processed++
			}
		}
		close(done)
	}(b.N)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		// Reuse the same message per goroutine to reduce allocs in the hot path.
		msg := new(ReceiveContext)
		for pb.Next() {
			_ = mailbox.Enqueue(msg)
		}
	})
	<-done
	b.StopTimer()

	opsPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(opsPerSec, "ops/sec")
}
