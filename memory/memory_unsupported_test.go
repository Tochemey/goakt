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

//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package memory

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSizeUnsupported(t *testing.T) {
	total, err := Size()
	require.ErrorIs(t, err, errors.ErrUnsupported)
	require.Zero(t, total)
}

func TestFreeUnsupported(t *testing.T) {
	free, err := Free()
	require.ErrorIs(t, err, errors.ErrUnsupported)
	require.Zero(t, free)
}

// TestUsedOnUnsupportedPlatform asserts that heap accounting keeps working even
// where the host memory syscalls are unavailable, since Used reads the Go
// runtime rather than the operating system.
func TestUsedOnUnsupportedPlatform(t *testing.T) {
	require.NotZero(t, Used())
}
