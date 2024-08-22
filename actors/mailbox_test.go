/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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
)

func TestMailbox_PushPop(t *testing.T) {
	q := newMailbox()

	in1 := &ReceiveContext{}
	in2 := &ReceiveContext{}

	q.Push(in1)
	q.Push(in2)

	out1 := q.Pop()
	out2 := q.Pop()

	assert.Equal(t, in1, out1)
	assert.Equal(t, in2, out2)
	assert.True(t, q.IsEmpty())
}

func TestMailbox_IsEmpty(t *testing.T) {
	q := newMailbox()
	assert.True(t, q.IsEmpty())
	q.Push(new(ReceiveContext))
	assert.False(t, q.IsEmpty())
}

func TestMailbox_PushPopOneProducer(t *testing.T) {
	t.Helper()
	expCount := 100
	var wg sync.WaitGroup
	wg.Add(1)
	q := newMailbox()
	go func() {
		i := 0
		for {
			r := q.Pop()
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
		q.Push(new(ReceiveContext))
	}

	wg.Wait()
}
