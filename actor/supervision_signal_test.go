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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/goaktpb"
)

type supervisionSignalTestError struct{}

func (supervisionSignalTestError) Error() string { return "supervision-signal" }

func TestSupervisionSignalAccessors(t *testing.T) {
	expectedErr := errors.New("boom")
	expectedMsg := new(goaktpb.PostStart)

	signal := newSupervisionSignal(expectedErr, expectedMsg)

	require.Equal(t, expectedErr, signal.Err())
	require.Equal(t, expectedMsg, signal.Msg())
	require.NotNil(t, signal.Timestamp())
	require.False(t, signal.Timestamp().AsTime().IsZero())
}

func TestErrorTypeNil(t *testing.T) {
	require.Equal(t, "nil", errorType(nil))
}

func TestErrorTypePointerAndValue(t *testing.T) {
	valueErr := supervisionSignalTestError{}
	require.Equal(t, errorType(valueErr), errorType(&valueErr))

	var nilPtr *supervisionSignalTestError
	require.Equal(t, errorType(valueErr), errorType(nilPtr))
}
