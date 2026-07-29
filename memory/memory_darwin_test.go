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

//go:build darwin

package memory

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestPageSizeMatchesRuntime guards the page size used by Free. Apple Silicon
// uses 16 KiB pages while Intel Macs use 4 KiB, so any hardcoded constant
// misreports free memory by a factor of four on one of the two.
func TestPageSizeMatchesRuntime(t *testing.T) {
	pageSize, err := unix.SysctlUint32("hw.pagesize")
	require.NoError(t, err)
	require.Equal(t, uint64(os.Getpagesize()), uint64(pageSize))
}

// TestFreeIsWholePages asserts Free returns a whole number of pages, which only
// holds when the page size it multiplies by is the one the kernel reports.
func TestFreeIsWholePages(t *testing.T) {
	free, err := Free()
	require.NoError(t, err)
	require.Zero(t, free%uint64(os.Getpagesize()), "free memory is not a whole number of pages")
}

// TestSysctlWidths pins the integer width of each sysctl Size and Free depend
// on. vm.page_free_count is a 32-bit node and fails with EIO on an 8-byte read,
// whereas hw.memsize is 64-bit and overflows a 4-byte read.
func TestSysctlWidths(t *testing.T) {
	_, err := unix.SysctlUint64("hw.memsize")
	require.NoError(t, err, "hw.memsize must be readable as a 64-bit value")

	_, err = unix.SysctlUint32("vm.page_free_count")
	require.NoError(t, err, "vm.page_free_count must be readable as a 32-bit value")
}
