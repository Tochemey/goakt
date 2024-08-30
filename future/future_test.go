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

package future

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestWaitOnResult(t *testing.T) {
	executor := make(chan proto.Message)
	f := New(executor, time.Duration(30*time.Minute))
	var result *Result
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		result = f.Result()
		wg.Done()
	}()

	executor <- new(testpb.Ping)
	wg.Wait()

	assert.Nil(t, result.Failure())
	assert.True(t, proto.Equal(new(testpb.Ping), result.Success()))

	// ensure we don't get paused on the next iteration.
	result = f.Result()
	assert.True(t, proto.Equal(new(testpb.Ping), result.Success()))
	assert.Nil(t, result.Failure())
}

func TestHasResult(t *testing.T) {
	t.Run("With timeout set", func(t *testing.T) {
		executor := make(chan proto.Message)
		f := New(executor, time.Duration(30*time.Minute))

		assert.False(t, f.HasResult())

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			f.Result()
			wg.Done()
		}()

		executor <- new(testpb.Ping)
		wg.Wait()

		assert.True(t, f.HasResult())
	})
	t.Run("With context set", func(t *testing.T) {
		ctx := context.TODO()
		executor := make(chan proto.Message)
		f := NewWithContext(ctx, executor)

		assert.False(t, f.HasResult())

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			f.Result()
			wg.Done()
		}()

		executor <- new(testpb.Ping)
		wg.Wait()

		assert.True(t, f.HasResult())
	})
}

func TestTimeout(t *testing.T) {
	executor := make(chan proto.Message)
	f := New(executor, time.Duration(0))

	result := f.Result()

	require.NotNil(t, result)
	assert.Nil(t, result.Success())
	assert.NotNil(t, result.Failure())
}

func TestContextCancelation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	executor := make(chan proto.Message)
	f := NewWithContext(ctx, executor)
	cancel()

	result := f.Result()

	require.NotNil(t, result)
	assert.Nil(t, result.Success())
	assert.NotNil(t, result.Failure())
}

func BenchmarkFuture(b *testing.B) {
	executor := make(chan proto.Message)
	timeout := time.Duration(30 * time.Minute)
	var wg sync.WaitGroup

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		f := New(executor, timeout)
		go func() {
			f.Result()
			wg.Done()
		}()

		executor <- new(testpb.Ping)
		wg.Wait()
	}
}

func BenchmarkFutureWithContext(b *testing.B) {
	executor := make(chan proto.Message)
	ctx := context.TODO()
	var wg sync.WaitGroup

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		f := NewWithContext(ctx, executor)
		go func() {
			f.Result()
			wg.Done()
		}()

		executor <- new(testpb.Ping)
		wg.Wait()
	}
}
