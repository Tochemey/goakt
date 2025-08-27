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

package grpcc

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNewErrorWithUnderlying(t *testing.T) {
	underlying := fmt.Errorf("something went wrong")
	err := NewError(codes.InvalidArgument, underlying)

	require.Equal(t, underlying.Error(), err.Error())
	require.ErrorIs(t, err, underlying)

	var target *Error
	require.ErrorAs(t, err, &target)
	require.Equal(t, codes.InvalidArgument, err.Code())
}

func TestNewErrorWithoutUnderlying(t *testing.T) {
	err := NewError(codes.NotFound, nil)

	assert.Equal(t, "unknown grpc error", err.Error())
	require.Nil(t, err.Unwrap())
	require.Equal(t, codes.NotFound, err.Code())
}

func TestGRPCStatus(t *testing.T) {
	underlying := fmt.Errorf("user not found")
	err := NewError(codes.NotFound, underlying)

	st := err.GRPCStatus()
	assert.Equal(t, codes.NotFound, st.Code(), "GRPCStatus() should return correct gRPC code")
	assert.Equal(t, underlying.Error(), st.Message(), "GRPCStatus() should include underlying error message")

	// Round-trip through gRPC's status.FromError
	s, ok := status.FromError(err)
	require.True(t, ok, "status.FromError() should recognize *Error")

	assert.Equal(t, codes.NotFound, s.Code(), "status.FromError() should preserve code")
	assert.Equal(t, underlying.Error(), s.Message(), "status.FromError() should preserve message")
}
