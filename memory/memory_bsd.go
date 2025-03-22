//go:build freebsd || openbsd || dragonfly || netbsd
// +build freebsd openbsd dragonfly netbsd

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

package memory

// Size returns the total memory of the system in bytes
// It uses the sysctl command to get the memory size
// and returns the value as an uint64.
func Size() (uint64, error) {
	s, err := sysctl("hw.physmem")
	if err != nil {
		return 0, err
	}
	return s, nil
}

// Free returns the free memory of the system in bytes
// It uses the sysctl command to get the memory size
// and returns the value as an uint64.
func Free() (uint64, error) {
	s, err := sysctlUint64("hw.usermem")
	if err != nil {
		return 0, err
	}
	return s, nil
}
