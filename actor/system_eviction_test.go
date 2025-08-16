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

package actor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/errors"
)

func TestEvictionPolicy(t *testing.T) {
	t.Run("With Valid policy", func(t *testing.T) {
		require.Equal(t, "LRU", LRU.String())
		require.Equal(t, "LFU", LFU.String())
		require.Equal(t, "MRU", MRU.String())
		require.Equal(t, "Unknown", EvictionPolicy(100).String())
	})
}

func TestEvictionStrategy(t *testing.T) {
	t.Run("With valid eviction strategy", func(t *testing.T) {
		strategy, err := NewEvictionStrategy(10, LRU, 1)
		require.NoError(t, err)
		require.NotNil(t, strategy)
		require.Equal(t, LRU, strategy.Policy())
		require.Equal(t, "EvictionStrategy(limit=10, policy=LRU)", strategy.String())
		require.EqualValues(t, 10, strategy.Limit())
	})
	t.Run("With invalid policy", func(t *testing.T) {
		strategy, err := NewEvictionStrategy(1, EvictionPolicy(100), 1)
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrInvalidEvictionPolicy)
		require.Nil(t, strategy)
	})
	t.Run("With zero limit", func(t *testing.T) {
		strategy, err := NewEvictionStrategy(0, LRU, 1)
		require.Error(t, err)
		require.Nil(t, strategy)
	})
}
