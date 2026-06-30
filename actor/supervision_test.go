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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var errSupervisionTest = errors.New("supervision test failure")

func TestSupervision(t *testing.T) {
	t.Run("start and stop are idempotent and the consumer exits", func(t *testing.T) {
		s := newSupervision()

		s.start()
		s.start() // second call must not spawn another goroutine

		s.stop()
		s.stop() // second call must not panic on a re-closed channel

		select {
		case <-s.done:
		case <-time.After(time.Second):
			t.Fatal("supervision consumer did not exit after stop")
		}
	})

	t.Run("Submit before start is dropped", func(t *testing.T) {
		s := newSupervision()

		// No consumer is running and started is false, so the signal must be
		// dropped rather than buffered.
		s.Submit(&PID{}, newSupervisionSignal(errSupervisionTest, nil))

		require.Empty(t, s.queue)
	})

	t.Run("Submit ignores a nil pid or signal", func(t *testing.T) {
		s := newSupervision()

		s.Submit(nil, newSupervisionSignal(errSupervisionTest, nil))
		s.Submit(&PID{}, nil)

		require.Empty(t, s.queue)
	})
}
