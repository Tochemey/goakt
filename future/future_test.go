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

package future

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestFuture(t *testing.T) {
	t.Run("With timeout", func(t *testing.T) {
		future := New(func() (proto.Message, error) {
			// simulate a long-running task
			pause.For(100 * time.Millisecond)
			return nil, nil
		})

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		result, err := future.Await(ctx)
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
	t.Run("With success", func(t *testing.T) {
		expected := new(testpb.TestPing)
		future := New(func() (proto.Message, error) {
			// simulate a long-running task
			pause.For(10 * time.Millisecond)
			return expected, nil
		})

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		result, err := future.Await(ctx)
		require.NoError(t, err)
		require.True(t, proto.Equal(expected, result))
	})
	t.Run("With failure", func(t *testing.T) {
		future := New(func() (proto.Message, error) {
			// simulate a long-running task
			pause.For(10 * time.Millisecond)
			return nil, assert.AnError
		})

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		result, err := future.Await(ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, assert.AnError)
		require.Nil(t, result)
	})
}
