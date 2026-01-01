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

package actor

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGrainMailboxEmpty(t *testing.T) {
	mailbox := newGrainMailbox()
	require.True(t, mailbox.IsEmpty())
	require.EqualValues(t, 0, mailbox.Len())
	require.Nil(t, mailbox.Dequeue())
}

func TestGrainMailboxFIFO(t *testing.T) {
	mailbox := newGrainMailbox()

	ctx1 := &GrainContext{}
	ctx2 := &GrainContext{}
	ctx3 := &GrainContext{}

	mailbox.Enqueue(ctx1)
	mailbox.Enqueue(ctx2)
	mailbox.Enqueue(ctx3)

	require.EqualValues(t, 3, mailbox.Len())
	require.False(t, mailbox.IsEmpty())

	require.Equal(t, ctx1, mailbox.Dequeue())
	require.Equal(t, ctx2, mailbox.Dequeue())
	require.Equal(t, ctx3, mailbox.Dequeue())

	require.Nil(t, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty())
	require.EqualValues(t, 0, mailbox.Len())
}

func TestGrainMailboxConcurrentEnqueue(t *testing.T) {
	const producers = 16
	const messagesPerProducer = 32
	total := producers * messagesPerProducer

	mailbox := newGrainMailbox()

	var wg sync.WaitGroup
	wg.Add(producers)

	for range producers {
		go func() {
			defer wg.Done()
			for range messagesPerProducer {
				mailbox.Enqueue(&GrainContext{})
			}
		}()
	}

	wg.Wait()

	require.Eventually(t, func() bool {
		return mailbox.Len() == int64(total)
	}, time.Second, time.Millisecond)

	count := 0
	for {
		ctx := mailbox.Dequeue()
		if ctx == nil {
			break
		}
		count++
	}

	require.Equal(t, total, count)
	require.True(t, mailbox.IsEmpty())
	require.EqualValues(t, 0, mailbox.Len())
}
