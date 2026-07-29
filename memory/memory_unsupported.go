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

import "errors"

// Size reports that the total physical memory of the system cannot be
// determined on this platform.
//
// Size is implemented for Linux, macOS, Windows and the BSDs. On any other
// GOOS, such as wasip1, js, solaris, aix or plan9, it returns
// errors.ErrUnsupported. Keeping this stub means importing the package stays a
// compile-time success everywhere and the limitation surfaces as a runtime
// error instead of a build failure in a consumer's toolchain.
func Size() (uint64, error) {
	return 0, errors.ErrUnsupported
}

// Free reports that the free physical memory of the system cannot be determined
// on this platform. See Size for the list of supported platforms.
func Free() (uint64, error) {
	return 0, errors.ErrUnsupported
}
