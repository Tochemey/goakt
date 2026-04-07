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

//go:build linux

package memory

import "syscall"

// Size returns the total physical memory of the system in bytes.
//
// It calls the Linux sysinfo(2) syscall and returns Totalram * Unit.
// The Unit field is the memory unit size (typically 1 on 64-bit systems),
// used to scale the values when they would otherwise overflow a 32-bit field.
// Reference: https://man7.org/linux/man-pages/man2/sysinfo.2.html
func Size() (uint64, error) {
	in := &syscall.Sysinfo_t{}
	err := syscall.Sysinfo(in)
	if err != nil {
		return 0, err
	}
	return uint64(in.Totalram) * uint64(in.Unit), nil
}

// Free returns the free physical memory of the system in bytes.
//
// It calls the Linux sysinfo(2) syscall and returns Freeram * Unit.
// This reflects memory not currently used by the kernel or userspace processes,
// but does not include reclaimable buffers/cache (use /proc/meminfo for that).
// Reference: https://man7.org/linux/man-pages/man2/sysinfo.2.html
func Free() (uint64, error) {
	in := &syscall.Sysinfo_t{}
	err := syscall.Sysinfo(in)
	if err != nil {
		return 0, err
	}
	return uint64(in.Freeram) * uint64(in.Unit), nil
}
