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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFuture_AwaitUninterruptible(t *testing.T) {
	t.Run("With Success", func(t *testing.T) {
		ctx := context.TODO()
		f := New[string](ctx, func(_ context.Context) (string, error) {
			wg := sync.WaitGroup{}
			wg.Add(1)
			go func() {
				time.Sleep(time.Second)
				wg.Done()
			}()
			// block until timer is up
			wg.Wait()
			return "done", nil
		})

		expected := "done"
		result := f.AwaitUninterruptible()
		require.NotNil(t, result)
		require.NotEmpty(t, result.Success())
		require.NoError(t, result.Failure())
		assert.Equal(t, expected, result.Success())
	})
	t.Run("With Failure", func(t *testing.T) {
		ctx := context.TODO()
		f := New[string](ctx, func(_ context.Context) (string, error) {
			wg := sync.WaitGroup{}
			wg.Add(1)
			go func() {
				time.Sleep(time.Second)
				wg.Done()
			}()
			// block until timer is up
			wg.Wait()
			return "", fmt.Errorf("something went wrong")
		})

		expected := "something went wrong"
		result := f.AwaitUninterruptible()
		require.NotNil(t, result)
		require.Empty(t, result.Success())
		require.Error(t, result.Failure())
		assert.Equal(t, expected, result.Failure().Error())
	})
}

func TestFuture_Await(t *testing.T) {
	t.Run("With Success", func(t *testing.T) {
		ctx := context.TODO()
		f := New[string](ctx, func(_ context.Context) (string, error) {
			wg := sync.WaitGroup{}
			wg.Add(1)
			go func() {
				time.Sleep(time.Second)
				wg.Done()
			}()
			// block until timer is up
			wg.Wait()
			return "done", nil
		})

		expected := "done"
		result := f.Await(2 * time.Second)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Success())
		require.NoError(t, result.Failure())
		assert.Equal(t, expected, result.Success())
	})
	t.Run("With Failure", func(t *testing.T) {
		ctx := context.TODO()
		f := New[string](ctx, func(_ context.Context) (string, error) {
			wg := sync.WaitGroup{}
			wg.Add(1)
			go func() {
				time.Sleep(time.Second)
				wg.Done()
			}()
			// block until timer is up
			wg.Wait()
			return "", fmt.Errorf("something went wrong")
		})

		expected := "something went wrong"
		result := f.Await(2 * time.Second)
		require.NotNil(t, result)
		require.Empty(t, result.Success())
		require.Error(t, result.Failure())
		assert.Equal(t, expected, result.Failure().Error())
	})
	t.Run("With Timeout", func(t *testing.T) {
		ctx := context.TODO()
		f := New[string](ctx, func(_ context.Context) (string, error) {
			wg := sync.WaitGroup{}
			wg.Add(1)
			go func() {
				time.Sleep(2 * time.Second)
				wg.Done()
			}()
			// block until timer is up
			wg.Wait()
			return "", fmt.Errorf("something went wrong")
		})

		expected := ErrFutureTimeout.Error()
		result := f.Await(time.Second)
		require.NotNil(t, result)
		require.Empty(t, result.Success())
		require.Error(t, result.Failure())
		assert.EqualError(t, result.Failure(), expected)
	})
}

func TestFuture_Cancel(t *testing.T) {
	ctx := context.TODO()
	f := New[string](ctx, func(_ context.Context) (string, error) {
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			time.Sleep(time.Second)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
		return "done", nil
	})

	f.Cancel()
	expected := context.Canceled.Error()
	result := f.AwaitUninterruptible()
	require.NotNil(t, result)
	require.Empty(t, result.Success())
	require.Error(t, result.Failure())
	assert.Equal(t, expected, result.Failure().Error())
}
