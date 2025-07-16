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

package remote

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/size"
)

func TestConfig(t *testing.T) {
	t.Run("With default config", func(t *testing.T) {
		config := DefaultConfig()
		require.NoError(t, config.Validate())
		require.NoError(t, config.Sanitize())
		assert.EqualValues(t, 16*size.MB, config.MaxFrameSize())
		assert.Exactly(t, 10*time.Second, config.WriteTimeout())
		assert.Exactly(t, 10*time.Second, config.ReadIdleTimeout())
		assert.Exactly(t, 1200*time.Second, config.IdleTimeout())
		assert.Exactly(t, "127.0.0.1", config.BindAddr())
		assert.Exactly(t, 0, config.BindPort())
	})
	t.Run("With config", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 8080, WithReadIdleTimeout(10*time.Second), WithWriteTimeout(10*time.Second))
		require.NoError(t, config.Validate())
		require.NoError(t, config.Sanitize())
		assert.EqualValues(t, 16*size.MB, config.MaxFrameSize())
		assert.Exactly(t, 10*time.Second, config.WriteTimeout())
		assert.Exactly(t, 10*time.Second, config.ReadIdleTimeout())
		assert.Exactly(t, 1200*time.Second, config.IdleTimeout())
		assert.Exactly(t, "127.0.0.1", config.BindAddr())
		assert.Exactly(t, 8080, config.BindPort())
	})
	t.Run("With invalid framesize", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 8080, WithMaxFrameSize(20*size.MB))
		err := config.Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "maxFrameSize must be between 16KB and 16MB")
	})
	t.Run("With invalid bindAddr", func(t *testing.T) {
		config := NewConfig("256.256.256.256", 8080, WithMaxFrameSize(20*size.MB))
		err := config.Sanitize()
		require.Error(t, err)
	})
}
