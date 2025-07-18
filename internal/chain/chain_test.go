/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package chain

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChain(t *testing.T) {
	t.Run("With AddRunner FailFast", func(t *testing.T) {
		var (
			calledFn1 = false
			calledFn2 = false
			calledFn3 = false
		)

		fn1 := func() error { calledFn1 = true; return errors.New("err1") }
		fn2 := func() error { calledFn2 = true; return errors.New("err2") }
		fn3 := func() error { calledFn3 = true; return errors.New("err3") }

		chain := New(WithFailFast()).
			AddRunner(fn1).
			AddRunner(fn2).
			AddRunner(fn3)
		actual := chain.Run()

		require.EqualError(t, actual, "err1")
		require.True(t, calledFn1)
		require.False(t, calledFn2)
		require.False(t, calledFn3)
	})

	t.Run("With AddRunners FailFast", func(t *testing.T) {
		var (
			calledFn1 = false
			calledFn2 = false
			calledFn3 = false
		)

		fn1 := func() error { calledFn1 = true; return errors.New("err1") }
		fn2 := func() error { calledFn2 = true; return errors.New("err2") }
		fn3 := func() error { calledFn3 = true; return errors.New("err3") }

		chain := New(WithFailFast()).AddRunners(fn1, fn2, fn3)
		actual := chain.Run()

		require.EqualError(t, actual, "err1")
		require.True(t, calledFn1)
		require.False(t, calledFn2)
		require.False(t, calledFn3)
	})

	t.Run("With AddRunner ReturnAll", func(t *testing.T) {
		var (
			calledFn1 = false
			calledFn2 = false
			calledFn3 = false
		)

		fn1 := func() error { calledFn1 = true; return errors.New("err1") }
		fn2 := func() error { calledFn2 = true; return errors.New("err2") }
		fn3 := func() error { calledFn3 = true; return nil }

		chain := New(WithRunAll()).
			AddRunner(fn1).
			AddRunner(fn2).
			AddRunner(fn3)
		actual := chain.Run()

		require.EqualError(t, actual, "err1; err2")
		require.True(t, calledFn1)
		require.True(t, calledFn2)
		require.True(t, calledFn3)
	})
}

func TestAddContextRunnerIf(t *testing.T) {
	ctx := context.Background()

	t.Run("FailFast - condition true, error returned", func(t *testing.T) {
		called := false
		fn := func(_ context.Context) error {
			called = true
			return errors.New("err1")
		}
		chain := New(WithFailFast(), WithContext(ctx)).AddContextRunnerIf(true, fn)
		require.EqualError(t, chain.Run(), "err1")
		require.True(t, called)
	})

	t.Run("FailFast - condition false, fn not called", func(t *testing.T) {
		called := false
		fn := func(_ context.Context) error {
			called = true
			return errors.New("err1")
		}
		chain := New(WithFailFast()).AddContextRunnerIf(false, fn)
		require.NoError(t, chain.Run())
		require.False(t, called)
	})

	t.Run("ReturnAll - condition true, error returned", func(t *testing.T) {
		called := false
		fn := func(_ context.Context) error {
			called = true
			return errors.New("err2")
		}
		chain := New(WithRunAll()).AddContextRunnerIf(true, fn)
		require.EqualError(t, chain.Run(), "err2")
		require.True(t, called)
	})

	t.Run("ReturnAll - condition false, fn not called", func(t *testing.T) {
		called := false
		fn := func(_ context.Context) error {
			called = true
			return errors.New("err2")
		}
		chain := New(WithRunAll()).AddContextRunnerIf(false, fn)
		require.NoError(t, chain.Run())
		require.False(t, called)
	})

	t.Run("ReturnAll - condition true, fn returns nil", func(t *testing.T) {
		called := false
		fn := func(_ context.Context) error {
			called = true
			return nil
		}
		chain := New(WithRunAll()).AddContextRunnerIf(true, fn)
		require.NoError(t, chain.Run())
		require.True(t, called)
	})
}
