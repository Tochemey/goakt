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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLateGrainMessagesQueue(t *testing.T) {
	queues := &lateGrainMessages{}
	pid := &grainPID{}
	other := &grainPID{}

	// the first push creates the queue and asks the caller to start forwarding
	require.True(t, queues.push(pid, &GrainContext{message: "first"}))
	require.False(t, queues.push(pid, &GrainContext{message: "second"}))
	require.True(t, queues.push(other, &GrainContext{message: "other"}))

	late := queues.pop(pid)
	require.NotNil(t, late)
	require.Equal(t, "first", late.message)

	late = queues.pop(pid)
	require.NotNil(t, late)
	require.Equal(t, "second", late.message)

	// an empty queue is deleted, so the next push starts forwarding again
	require.Nil(t, queues.pop(pid))
	_, exists := queues.queues[pid]
	require.False(t, exists)
	require.True(t, queues.push(pid, &GrainContext{message: "third"}))

	// queues of different instances are independent
	late = queues.pop(other)
	require.NotNil(t, late)
	require.Equal(t, "other", late.message)
}

func TestLateGrainMessagesPopWithoutQueue(t *testing.T) {
	queues := &lateGrainMessages{}
	require.Nil(t, queues.pop(&grainPID{}))
}
