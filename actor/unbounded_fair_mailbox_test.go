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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/address"
)

func makeReceiveContext(sender string) *ReceiveContext {
	return makeReceiveContextWithPayload(sender, "")
}

func makeReceiveContextWithPayload(sender, payload string) *ReceiveContext {
	addr := address.New(sender, "test-system", "localhost", 0)
	return makeReceiveContextFromAddress(addr, payload)
}

func makeReceiveContextFromAddress(addr *address.Address, payload string) *ReceiveContext {
	ctx := &ReceiveContext{remoteSender: addr}
	if payload != "" {
		ctx.message = &anypb.Any{TypeUrl: "test/payload", Value: []byte(payload)}
	}
	return ctx
}

func TestUnboundedFairMailboxNoStarvation(t *testing.T) {
	mailbox := NewUnboundedFairMailbox()
	verbose := address.New("verbose", "test-system", "localhost", 0)
	quiet := address.New("quiet", "test-system", "localhost", 0)

	for i := range 50 {
		require.NoError(t, mailbox.Enqueue(makeReceiveContextFromAddress(verbose, fmt.Sprintf("v-%d", i))))
	}

	require.NoError(t, mailbox.Enqueue(makeReceiveContextFromAddress(quiet, "q-0")))

	seenQuiet := false
	for range 51 {
		msg := mailbox.Dequeue()
		require.NotNil(t, msg)
		sender := msg.SenderAddress()
		if sender.Equals(quiet) {
			seenQuiet = true
			break
		}
	}

	require.True(t, seenQuiet, "quiet sender must be serviced despite noisy peers")
}

func TestUnboundedFairMailboxEnqueueActivatesSender(t *testing.T) {
	mailbox := NewUnboundedFairMailbox()
	msg := makeReceiveContext("sender-1")

	require.NoError(t, mailbox.Enqueue(msg))
	require.EqualValues(t, 1, mailbox.Len())

	key := deriveSenderKey(msg)
	value, ok := mailbox.senders.Load(key)
	require.True(t, ok)
	sq := value.(*senderBox)

	require.True(t, sq.active.Load())
	require.EqualValues(t, 1, atomic.LoadInt64(&sq.pending))

	received := mailbox.Dequeue()
	require.Equal(t, msg, received)
	require.Zero(t, atomic.LoadInt64(&sq.pending))
	require.False(t, sq.active.Load())
	require.Nil(t, mailbox.Dequeue())
}

func TestUnboundedFairMailboxFinalizeSenderRequeuesWithPending(t *testing.T) {
	mailbox := NewUnboundedFairMailbox()
	sq := &senderBox{mailbox: NewUnboundedMailbox()}
	sq.active.Store(true)
	atomic.StoreInt64(&sq.pending, 2)

	remaining := atomic.AddInt64(&sq.pending, -1)
	require.EqualValues(t, 1, remaining)

	mailbox.finalizeSender(sq, remaining)
	require.True(t, sq.active.Load())
	require.EqualValues(t, 1, atomic.LoadInt64(&sq.pending))
	require.Equal(t, sq, mailbox.active.dequeue())
	require.Nil(t, mailbox.active.dequeue())
}

func TestUnboundedFairMailboxFinalizeSenderRescuesLateArrival(t *testing.T) {
	mailbox := NewUnboundedFairMailbox()
	sq := &senderBox{mailbox: NewUnboundedMailbox()}
	sq.active.Store(true)
	atomic.StoreInt64(&sq.pending, 1)

	remaining := atomic.AddInt64(&sq.pending, -1)
	require.EqualValues(t, 0, remaining)

	late := makeReceiveContext("sender-late")
	require.NoError(t, sq.mailbox.Enqueue(late))
	atomic.AddInt64(&sq.pending, 1)

	mailbox.finalizeSender(sq, remaining)
	require.True(t, sq.active.Load())
	require.EqualValues(t, 1, atomic.LoadInt64(&sq.pending))
	require.Equal(t, sq, mailbox.active.dequeue())
	require.Equal(t, late, sq.mailbox.Dequeue())
	require.EqualValues(t, 0, atomic.AddInt64(&sq.pending, -1))
}

func TestUnboundedFairMailboxProcessesMultipleSenders(t *testing.T) {
	mailbox := NewUnboundedFairMailbox()
	alpha := address.New("alpha", "test-system", "localhost", 0)
	bravo := address.New("bravo", "test-system", "localhost", 0)
	charlie := address.New("charlie", "test-system", "localhost", 0)

	enqueueSeq := []*ReceiveContext{
		makeReceiveContextFromAddress(alpha, "alpha-1"),
		makeReceiveContextFromAddress(alpha, "alpha-2"),
		makeReceiveContextFromAddress(bravo, "bravo-1"),
		makeReceiveContextFromAddress(charlie, "charlie-1"),
		makeReceiveContextFromAddress(bravo, "bravo-2"),
		makeReceiveContextFromAddress(charlie, "charlie-2"),
	}

	for _, ctx := range enqueueSeq {
		require.NoError(t, mailbox.Enqueue(ctx))
	}

	assert.False(t, mailbox.IsEmpty())

	var received []string
	for range enqueueSeq {
		msg := mailbox.Dequeue()
		require.NotNil(t, msg)
		payload := string(msg.Message().(*anypb.Any).Value)
		received = append(received, payload)
	}

	require.Equal(t, []string{
		"alpha-1",
		"bravo-1",
		"charlie-1",
		"alpha-2",
		"bravo-2",
		"charlie-2",
	}, received)

	mailbox.Dispose()
}

func TestUnboundedFairMailboxPreservesPerSenderOrdering(t *testing.T) {
	mailbox := NewUnboundedFairMailbox()
	senderOne := address.New("sender-1", "test-system", "localhost", 0)
	senderTwo := address.New("sender-2", "test-system", "localhost", 0)

	enqueueSeq := []*ReceiveContext{
		makeReceiveContextFromAddress(senderOne, "s1-1"),
		makeReceiveContextFromAddress(senderOne, "s1-2"),
		makeReceiveContextFromAddress(senderOne, "s1-3"),
		makeReceiveContextFromAddress(senderTwo, "s2-1"),
		makeReceiveContextFromAddress(senderTwo, "s2-2"),
	}

	for _, ctx := range enqueueSeq {
		require.NoError(t, mailbox.Enqueue(ctx))
	}

	got := map[string][]string{}
	for range enqueueSeq {
		msg := mailbox.Dequeue()
		require.NotNil(t, msg)
		sender := msg.SenderAddress().String()
		payload := string(msg.Message().(*anypb.Any).Value)
		got[sender] = append(got[sender], payload)
	}

	require.Equal(t, []string{"s1-1", "s1-2", "s1-3"}, got[senderOne.String()])
	require.Equal(t, []string{"s2-1", "s2-2"}, got[senderTwo.String()])
}

func BenchmarkUnbundedFairMailbox(b *testing.B) {
	mb := NewUnboundedFairMailbox()

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
		rc := makeReceiveContext(fmt.Sprintf("bench-%d", id))
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
func BenchmarkUnboundedFairMailbox_DistinctSendersPreallocated(b *testing.B) {
	mb := NewUnboundedFairMailbox()

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
	messages := make([]*ReceiveContext, senderCount)
	for i := range senderCount {
		messages[i] = makeReceiveContext(fmt.Sprintf("sender-%d", i))
	}

	var nextIdx int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Round-robin pick among the prebuilt PIDs to exercise fairness
			idx := int(atomic.AddInt64(&nextIdx, 1)-1) % senderCount
			_ = mb.Enqueue(messages[idx])
		}
	})
	<-done
	b.StopTimer()

	opsPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(opsPerSec, "ops/sec")
}
