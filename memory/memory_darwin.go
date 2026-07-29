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

import "golang.org/x/sys/unix"

// Size returns the total physical memory of the system in bytes.
//
// It reads the "hw.memsize" sysctl, which reports total RAM on macOS as a
// 64-bit value.
// Reference: https://developer.apple.com/documentation/kernel/1387446-sysctlbyname
func Size() (uint64, error) {
	return unix.SysctlUint64("hw.memsize")
}

// Free returns the free physical memory of the system in bytes.
//
// It multiplies the "vm.page_free_count" sysctl by the "hw.pagesize" sysctl.
// The kernel types both nodes as 32-bit integers, so they are read with
// SysctlUint32; requesting an 8-byte read of vm.page_free_count fails with EIO.
//
// The result counts only pages on the free list. macOS keeps a large
// reclaimable pool of inactive, purgeable and speculative pages that this
// figure excludes, so it understates the memory an allocation could actually
// obtain.
// Reference: https://developer.apple.com/documentation/kernel/1387446-sysctlbyname
func Free() (uint64, error) {
	freePages, err := unix.SysctlUint32("vm.page_free_count")
	if err != nil {
		return 0, err
	}

	pageSize, err := unix.SysctlUint32("hw.pagesize")
	if err != nil {
		return 0, err
	}

	return uint64(freePages) * uint64(pageSize), nil
}
