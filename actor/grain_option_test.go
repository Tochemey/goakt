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

package actor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGrainOptions(t *testing.T) {
	t.Run("WithRequestTimeout", func(t *testing.T) {
		opt := WithRequestTimeout(10 * time.Second)
		config := newGrainOptConfig(opt)
		require.EqualValues(t, 10*time.Second, config.RequestTimeout())
	})
	t.Run("With default request timeout", func(t *testing.T) {
		opt := WithRequestTimeout(0)
		config := newGrainOptConfig(opt)
		require.EqualValues(t, 5*time.Minute, config.RequestTimeout())
	})
	t.Run("WithRequestSender", func(t *testing.T) {
		sender := &Identity{kind: "test", name: "sender"}
		opt := WithRequestSender(sender)
		config := &grainOptConfig{}
		opt.Apply(config)
		require.True(t, config.RequestSender().Equal(sender), "expected sender to be set correctly")
	})
}
