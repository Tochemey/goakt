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

package errorschain

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrorsChain(t *testing.T) {
	t.Run("With ReturnFirst", func(t *testing.T) {
		e1 := errors.New("err1")
		e2 := errors.New("err2")
		e3 := errors.New("err3")

		chain := New(ReturnFirst()).AddError(e1).AddError(e2).AddError(e3)
		actual := chain.Error()
		require.True(t, errors.Is(actual, e1))
	})

	t.Run("With Error", func(t *testing.T) {
		chain := New(ReturnFirst()).AddError(nil)
		actual := chain.Error()
		require.NoError(t, actual)
	})

	t.Run("With AddErrorFn ReturnFirst", func(t *testing.T) {
		var (
			calledFn1 = false
			calledFn2 = false
			calledFn3 = false
		)

		fn1 := func() error { calledFn1 = true; return errors.New("err1") }
		fn2 := func() error { calledFn2 = true; return errors.New("err2") }
		fn3 := func() error { calledFn3 = true; return errors.New("err3") }

		chain := New(ReturnFirst()).
			AddErrorFn(fn1).
			AddErrorFn(fn2).
			AddErrorFn(fn3)
		actual := chain.Error()

		require.EqualError(t, actual, "err1")
		require.True(t, calledFn1)
		require.False(t, calledFn2)
		require.False(t, calledFn3)
	})

	t.Run("With AddErrorFns ReturnFirst", func(t *testing.T) {
		var (
			calledFn1 = false
			calledFn2 = false
			calledFn3 = false
		)

		fn1 := func() error { calledFn1 = true; return errors.New("err1") }
		fn2 := func() error { calledFn2 = true; return errors.New("err2") }
		fn3 := func() error { calledFn3 = true; return errors.New("err3") }

		chain := New(ReturnFirst()).AddErrorFns(fn1, fn2, fn3)
		actual := chain.Error()

		require.EqualError(t, actual, "err1")
		require.True(t, calledFn1)
		require.False(t, calledFn2)
		require.False(t, calledFn3)
	})

	t.Run("With AddErrorFn ReturnAll", func(t *testing.T) {
		var (
			calledFn1 = false
			calledFn2 = false
			calledFn3 = false
		)

		fn1 := func() error { calledFn1 = true; return errors.New("err1") }
		fn2 := func() error { calledFn2 = true; return errors.New("err2") }
		fn3 := func() error { calledFn3 = true; return nil }

		chain := New(ReturnAll()).
			AddErrorFn(fn1).
			AddErrorFn(fn2).
			AddErrorFn(fn3)
		actual := chain.Error()

		require.EqualError(t, actual, "err1; err2")
		require.True(t, calledFn1)
		require.True(t, calledFn2)
		require.True(t, calledFn3)
	})

	t.Run("With ReturnAll", func(t *testing.T) {
		e1 := errors.New("err1")
		e2 := errors.New("err2")
		e3 := errors.New("err3")

		chain := New(ReturnAll()).
			AddError(e1).
			AddError(e2).
			AddError(e3).
			AddError(nil)
		actual := chain.Error()
		require.EqualError(t, actual, "err1; err2; err3")
	})
}

func TestAddErrorFnIf(t *testing.T) {
	ctx := context.Background()

	t.Run("ReturnFirst - condition true, error returned", func(t *testing.T) {
		called := false
		fn := func(_ context.Context) error {
			called = true
			return errors.New("err1")
		}
		chain := New(ReturnFirst()).AddErrorFnIf(ctx, true, fn)
		require.EqualError(t, chain.Error(), "err1")
		require.True(t, called)
	})

	t.Run("ReturnFirst - condition false, fn not called", func(t *testing.T) {
		called := false
		fn := func(_ context.Context) error {
			called = true
			return errors.New("err1")
		}
		chain := New(ReturnFirst()).AddErrorFnIf(ctx, false, fn)
		require.NoError(t, chain.Error())
		require.False(t, called)
	})

	t.Run("ReturnAll - condition true, error returned", func(t *testing.T) {
		called := false
		fn := func(_ context.Context) error {
			called = true
			return errors.New("err2")
		}
		chain := New(ReturnAll()).AddErrorFnIf(ctx, true, fn)
		require.EqualError(t, chain.Error(), "err2")
		require.True(t, called)
	})

	t.Run("ReturnAll - condition false, fn not called", func(t *testing.T) {
		called := false
		fn := func(_ context.Context) error {
			called = true
			return errors.New("err2")
		}
		chain := New(ReturnAll()).AddErrorFnIf(ctx, false, fn)
		require.NoError(t, chain.Error())
		require.False(t, called)
	})

	t.Run("ReturnAll - condition true, fn returns nil", func(t *testing.T) {
		called := false
		fn := func(_ context.Context) error {
			called = true
			return nil
		}
		chain := New(ReturnAll()).AddErrorFnIf(ctx, true, fn)
		require.NoError(t, chain.Error())
		require.True(t, called)
	})
}
