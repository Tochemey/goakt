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
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAskDeadline(t *testing.T) {
	t.Run("is the ask timeout from now", func(t *testing.T) {
		remaining := untilAskDeadline(askDeadline(context.Background(), time.Minute))
		require.Greater(t, remaining, 59*time.Second)
		require.LessOrEqual(t, remaining, time.Minute)
	})

	t.Run("is the sender's deadline when it comes first", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		t.Cleanup(cancel)

		require.LessOrEqual(t, untilAskDeadline(askDeadline(ctx, time.Minute)), time.Second)
	})

	t.Run("is the ask timeout when the sender's deadline comes later", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		t.Cleanup(cancel)

		require.LessOrEqual(t, untilAskDeadline(askDeadline(ctx, time.Minute)), time.Minute)
	})

	t.Run("is never the zero that stands for no deadline", func(t *testing.T) {
		require.NotZero(t, askDeadline(context.Background(), -24*time.Hour))
	})
}

func TestAskDeadlineDoesNotWrapAround(t *testing.T) {
	ctx := context.Background()
	deadline := askDeadline(ctx, time.Duration(math.MaxInt64))
	require.Positive(t, deadline)
	require.False(t, askExpired(deadline))

	// the context derived for such an ask is still alive
	derived, cancel := askContext(ctx, deadline)
	require.NotNil(t, cancel)
	t.Cleanup(cancel)
	require.NoError(t, derived.Err())
}

func TestAskExpired(t *testing.T) {
	ctx := context.Background()
	require.False(t, askExpired(0))
	require.False(t, askExpired(askDeadline(ctx, time.Minute)))
	require.True(t, askExpired(askDeadline(ctx, -time.Minute)))
}

func TestAskContext(t *testing.T) {
	t.Run("ends when the sender stops waiting", func(t *testing.T) {
		ctx, cancel := askContext(context.Background(), askDeadline(context.Background(), time.Minute))
		require.NotNil(t, cancel)
		t.Cleanup(cancel)

		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.WithinDuration(t, time.Now().Add(time.Minute), deadline, time.Second)
	})

	t.Run("returns a sender context that ends earlier as is", func(t *testing.T) {
		sender, cancelSender := context.WithTimeout(context.Background(), time.Second)
		t.Cleanup(cancelSender)

		ctx, cancel := askContext(sender, askClock()+int64(time.Minute))
		require.Nil(t, cancel)
		require.Equal(t, sender, ctx)
	})
}
