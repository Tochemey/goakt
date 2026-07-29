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

//go:build freebsd || openbsd || dragonfly || netbsd

package memory

import "golang.org/x/sys/unix"

// Size returns the total physical memory of the system in bytes.
//
// It reads the "hw.physmem" sysctl, which reports the amount of physical memory
// available to the kernel (total RAM minus firmware reservations).
// Reference: https://man.freebsd.org/cgi/man.cgi?sysctl(3)
func Size() (uint64, error) {
	return sysctlMemSize("hw.physmem")
}

// Free returns the user-accessible memory of the system in bytes.
//
// It reads the "hw.usermem" sysctl, which reports physical memory available to
// userspace (total minus kernel-wired pages). Note: this is not the same as
// currently unused memory, it includes memory actively used by applications.
// Reference: https://man.freebsd.org/cgi/man.cgi?sysctl(3)
func Free() (uint64, error) {
	return sysctlMemSize("hw.usermem")
}

// sysctlMemSize reads a memory-size sysctl, preferring the explicit 64-bit
// variant when the platform publishes one.
//
// NetBSD and OpenBSD type the classic hw.physmem and hw.usermem nodes as 32-bit
// integers, which truncate on hosts with more than 4 GiB of RAM, and publish
// hw.physmem64 and hw.usermem64 alongside them. FreeBSD and DragonFly type the
// classic nodes as long and publish no 64-bit variant. Probing for the "64"
// suffix first keeps every BSD correct without per-platform build tags.
//
// When neither node satisfies a 64-bit read the underlying error is returned
// rather than a truncated value, so a caller never observes a silently wrong
// size.
func sysctlMemSize(name string) (uint64, error) {
	if value, err := unix.SysctlUint64(name + "64"); err == nil {
		return value, nil
	}

	return unix.SysctlUint64(name)
}
