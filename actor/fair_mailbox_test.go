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
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v3/address"
)

func TestFairMailbox_Basic(t *testing.T) {
	ports := dynaport.Get(1)
	host := "127.0.0.1"
	addr1 := address.New("s1", "sys", host, ports[0])
	addr2 := address.New("s2", "sys", host, ports[0])

	mb := NewFairMailbox()

	s1 := &PID{fieldsLocker: new(sync.RWMutex), address: addr1}
	s2 := &PID{fieldsLocker: new(sync.RWMutex), address: addr2}

	m1 := &ReceiveContext{sender: s1}
	m2 := &ReceiveContext{sender: s2}
	m3 := &ReceiveContext{sender: s1}

	require.NoError(t, mb.Enqueue(m1))
	require.NoError(t, mb.Enqueue(m2))
	require.NoError(t, mb.Enqueue(m3))

	// Expect fair order: s1, s2, s1
	out1 := mb.Dequeue()
	require.NotNil(t, out1)
	assert.Equal(t, s1, out1.Sender())

	out2 := mb.Dequeue()
	require.NotNil(t, out2)
	assert.Equal(t, s2, out2.Sender())

	out3 := mb.Dequeue()
	require.NotNil(t, out3)
	assert.Equal(t, s1, out3.Sender())
}

func TestFairMailbox_NoStarvation(t *testing.T) {
	ports := dynaport.Get(1)
	host := "127.0.0.1"
	addr1 := address.New("s1", "sys", host, ports[0])
	addr2 := address.New("s2", "sys", host, ports[0])

	mb := NewFairMailbox()

	s1 := &PID{fieldsLocker: new(sync.RWMutex), address: addr1}
	s2 := &PID{fieldsLocker: new(sync.RWMutex), address: addr2}

	// hot sender s1
	for range 100 {
		require.NoError(t, mb.Enqueue(&ReceiveContext{sender: s1}))
	}
	// a single message from s2 should not starve
	require.NoError(t, mb.Enqueue(&ReceiveContext{sender: s2}))

	// After draining one from s1, we should see s2 next or very soon
	out1 := mb.Dequeue()
	require.NotNil(t, out1)
	// Expect s2 within the next dequeue
	out2 := mb.Dequeue()
	require.NotNil(t, out2)
	assert.Equal(t, s2, out2.Sender())
}

// mkPID is a small helper to build a PID suitable for tests/benchmarks without
// bootstrapping an ActorSystem. It creates a PID with a unique address.
func mkPID(name, system, host string, port int) *PID {
	return &PID{fieldsLocker: new(sync.RWMutex), address: address.New(name, system, host, port)}
}

func BenchmarkFairMailbox(b *testing.B) {
	ports := dynaport.Get(1)
	host := "127.0.0.1"
	sys := "sys"

	mb := NewFairMailbox()

	done := make(chan struct{})
	var processed int64

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

	var gid int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		// Each worker uses a distinct PID and reuses a single ReceiveContext
		id := atomic.AddInt64(&gid, 1)
		sender := mkPID(fmt.Sprintf("bench-%d", id), sys, host, ports[0])
		rc := &ReceiveContext{sender: sender}
		for pb.Next() {
			_ = mb.Enqueue(rc)
		}
	})
	<-done
	b.StopTimer()

	opsPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(opsPerSec, "ops/sec")
}

// BenchmarkFairMailbox_DistinctSendersPreallocated demonstrates a workload with
// many distinct senders (PIDs) pre-allocated and reused across goroutines. This
// avoids per-iteration PID creation overhead in the benchmark hot path.
func BenchmarkFairMailbox_DistinctSendersPreallocated(b *testing.B) {
	ports := dynaport.Get(1)
	host := "127.0.0.1"
	sys := "sys"

	mb := NewFairMailbox()

	done := make(chan struct{})
	var processed int64

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

	// Pre-allocate a set of distinct PIDs to emulate many senders
	senderCount := runtime.GOMAXPROCS(0) * 4
	senders := make([]*PID, senderCount)
	for i := range senderCount {
		senders[i] = mkPID(fmt.Sprintf("s%d", i+1), sys, host, ports[0])
	}

	var nextIdx int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Round-robin pick among the prebuilt PIDs to exercise fairness
			idx := int(atomic.AddInt64(&nextIdx, 1)-1) % senderCount
			_ = mb.Enqueue(&ReceiveContext{sender: senders[idx]})
		}
	})
	<-done
	b.StopTimer()

	opsPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(opsPerSec, "ops/sec")
}

// BenchmarkFairMailbox_OneHotManyCold models a hotspot: one hot sender enqueues
// aggressively while many cold senders enqueue at a lower rate. This highlights
// the fairness property—cold senders still make forward progress.
func BenchmarkFairMailbox_OneHotManyCold(b *testing.B) {
	ports := dynaport.Get(1)
	host := "127.0.0.1"
	sys := "sys"

	mb := NewFairMailbox()

	done := make(chan struct{})
	var processed int64

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

	hot := mkPID("hot", sys, host, ports[0])

	// cold senders pool
	coldCount := runtime.GOMAXPROCS(0)
	colds := make([]*PID, coldCount)
	for i := range coldCount {
		colds[i] = mkPID(fmt.Sprintf("cold-%d", i+1), sys, host, ports[0])
	}

	var coldIdx int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		// Each goroutine alternates: mostly hot, occasionally cold
		for pb.Next() {
			// 1 hot for every 7 total (approx 6:1 ratio hot:cold)
			for i := 0; i < 6 && pb.Next(); i++ {
				_ = mb.Enqueue(&ReceiveContext{sender: hot})
			}
			idx := int(atomic.AddInt64(&coldIdx, 1)-1) % coldCount
			_ = mb.Enqueue(&ReceiveContext{sender: colds[idx]})
		}
	})
	<-done
	b.StopTimer()

	opsPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(opsPerSec, "ops/sec")
}
