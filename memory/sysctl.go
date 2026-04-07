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

//go:build darwin || freebsd || openbsd || dragonfly || netbsd

package memory

import (
	"syscall"
	"unsafe"
)

// sysctl retrieves a numeric sysctl value as uint64. It uses syscall.Sysctl which
// returns the raw kernel value as a Go string.
//
// The unsafe.Pointer cast reinterprets the byte representation as a native-endian
// uint64 (equivalent to *(uint64*)buf in C).
//
// Padding: syscall.Sysctl converts the raw C buffer to a Go string, which strips
// the trailing null byte. For an 8-byte integer this leaves 7 bytes. We pad with
// zero bytes up to 8 to ensure the unsafe cast reads a full uint64.
// Reference: https://github.com/golang/go/issues/21614
func sysctl(name string) (uint64, error) {
	s, err := syscall.Sysctl(name)
	if err != nil {
		return 0, err
	}
	b := []byte(s)
	for len(b) < 8 {
		b = append(b, 0)
	}
	return *(*uint64)(unsafe.Pointer(&b[0])), nil
}
