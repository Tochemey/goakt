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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/breaker"
	gerrors "github.com/tochemey/goakt/v3/errors"
)

func TestPipeToWithTimeout(t *testing.T) {
	timeout := 5 * time.Second
	pt, err := newPipeTo(WithPipeToTimeout(timeout))
	require.NoError(t, err)
	require.NotNil(t, pt.timeout)
	require.Equal(t, timeout, *pt.timeout)
	require.Nil(t, pt.circuitBreaker)
}

func TestPipeToWithCircuitBreaker(t *testing.T) {
	cb := breaker.NewCircuitBreaker()
	pt, err := newPipeTo(WithPipeToCircuitBreaker(cb))
	require.NoError(t, err)
	require.Equal(t, cb, pt.circuitBreaker)
	require.Nil(t, pt.timeout)
}

func TestPipeToMutualExclusivity(t *testing.T) {
	timeout := 5 * time.Second
	cb := breaker.NewCircuitBreaker()

	_, err := newPipeTo(
		WithPipeToTimeout(timeout),
		WithPipeToCircuitBreaker(cb),
	)
	assert.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrOnlyOneOptionAllowed)
}

func TestPipeToNoOptions(t *testing.T) {
	pt, err := newPipeTo()
	require.NoError(t, err)
	require.NotNil(t, pt)
	require.Nil(t, pt.timeout)
	require.Nil(t, pt.circuitBreaker)
}
