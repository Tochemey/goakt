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

package stream_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tochemey/goakt/v4/stream"
)

func TestErrorSentinels(t *testing.T) {
	assert.EqualError(t, stream.ErrStreamCanceled, "stream: canceled")
	assert.EqualError(t, stream.ErrPullTimeout, "stream: pull from actor source timed out")
	assert.EqualError(t, stream.ErrInvalidGraph, "stream: graph must have at least a source and a sink")
}

func TestErrorStrategy_Constants(t *testing.T) {
	// Ensure the constants are distinct.
	assert.NotEqual(t, stream.FailFast, stream.Resume)
	assert.NotEqual(t, stream.Resume, stream.Retry)
	assert.NotEqual(t, stream.FailFast, stream.Retry)
}
