/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package actors

import (
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnboundedMailbox(t *testing.T) {
	mailbox := NewUnboundedMailbox()

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
}

func TestUnboundedMailboxOneProducer(t *testing.T) {
	t.Helper()
	expCount := 100
	var wg sync.WaitGroup
	wg.Add(1)
	mailbox := NewUnboundedMailbox()
	go func() {
		i := 0
		for {
			r := mailbox.Dequeue()
			if r == nil {
				runtime.Gosched()
				continue
			}
			i++
			if i == expCount {
				wg.Done()
				return
			}
		}
	}()

	for i := 0; i < expCount; i++ {
		err := mailbox.Enqueue(new(ReceiveContext))
		require.NoError(t, err)
	}

	wg.Wait()
}
