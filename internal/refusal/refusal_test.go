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

package refusal

import (
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMark covers marking an error as a node refusal: the marked error reads
// and matches like the original, and the mark is found anywhere in a chain.
func TestMark(t *testing.T) {
	sentinel := errors.New("shutting down")

	marked := Mark(sentinel)
	require.EqualError(t, marked, sentinel.Error())
	require.ErrorIs(t, marked, sentinel)
	require.True(t, Marked(marked))
	require.True(t, Marked(errors.Join(errors.New("context"), marked)))

	require.False(t, Marked(sentinel))
	require.False(t, Marked(nil))
}

// TestUnmark covers removing the mark: an unmarked error is returned as is,
// and a marked one, alone or inside a chain, loses the mark but keeps
// matching everything else.
func TestUnmark(t *testing.T) {
	sentinel := errors.New("shutting down")

	t.Run("an unmarked error is returned as is", func(t *testing.T) {
		require.Same(t, sentinel, Unmark(sentinel))
		require.NoError(t, Unmark(nil))
	})

	t.Run("a marked error loses the mark", func(t *testing.T) {
		require.Same(t, sentinel, Unmark(Mark(sentinel)))
		require.Same(t, sentinel, Unmark(Mark(Mark(sentinel))))
	})

	t.Run("a mark inside a chain is hidden and the chain still matches", func(t *testing.T) {
		opErr := &net.OpError{Op: "dial", Err: errors.New("refused")}
		chain := errors.Join(opErr, Mark(sentinel))

		unmarked := Unmark(chain)
		require.False(t, Marked(unmarked))
		require.False(t, Marked(errors.Join(errors.New("context"), unmarked)))
		require.EqualError(t, unmarked, chain.Error())
		require.ErrorIs(t, unmarked, sentinel)

		got, ok := errors.AsType[*net.OpError](unmarked)
		require.True(t, ok)
		require.Same(t, opErr, got)
	})
}
