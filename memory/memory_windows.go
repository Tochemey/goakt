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

//go:build windows

package memory

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

// memStatusEx mirrors the Windows MEMORYSTATUSEX structure.
// Reference: https://learn.microsoft.com/en-us/windows/win32/api/sysinfoapi/ns-sysinfoapi-memorystatusex
type memStatusEx struct {
	dwLength     uint32
	dwMemoryLoad uint32
	ullTotalPhys uint64
	ullAvailPhys uint64
	unused       [5]uint64
}

// globalMemoryStatusExProc holds the resolved GlobalMemoryStatusEx procedure.
// Resolved once via sync.Once to avoid repeated LoadDLL + FindProc calls.
//
// Reference: https://learn.microsoft.com/en-us/windows/win32/api/sysinfoapi/nf-sysinfoapi-globalmemorystatusex
var (
	globalMemoryStatusExProc *syscall.Proc
	globalMemoryStatusExErr  error
	globalMemoryStatusExOnce sync.Once
)

// loadGlobalMemoryStatusEx resolves the GlobalMemoryStatusEx procedure from
// kernel32.dll exactly once. Subsequent calls return the cached result.
func loadGlobalMemoryStatusEx() (*syscall.Proc, error) {
	globalMemoryStatusExOnce.Do(func() {
		kernel32, err := syscall.LoadDLL("kernel32.dll")
		if err != nil {
			globalMemoryStatusExErr = err
			return
		}
		globalMemoryStatusExProc, globalMemoryStatusExErr = kernel32.FindProc("GlobalMemoryStatusEx")
	})
	return globalMemoryStatusExProc, globalMemoryStatusExErr
}

// callGlobalMemoryStatusEx invokes the Windows GlobalMemoryStatusEx API and
// populates a memStatusEx struct.
//
// On failure (return value 0), the error from GetLastError is propagated.
// Reference: https://learn.microsoft.com/en-us/windows/win32/api/sysinfoapi/nf-sysinfoapi-globalmemorystatusex
func callGlobalMemoryStatusEx() (*memStatusEx, error) {
	proc, err := loadGlobalMemoryStatusEx()
	if err != nil {
		return nil, err
	}
	msx := &memStatusEx{
		dwLength: 64,
	}
	// Proc.Call always returns a non-nil error (from GetLastError).
	// Only inspect it when r == 0 (API failure).
	// Reference: https://pkg.go.dev/syscall#Proc.Call
	r, _, callErr := proc.Call(uintptr(unsafe.Pointer(msx)))
	if r == 0 {
		return nil, fmt.Errorf("GlobalMemoryStatusEx failed: %w", callErr)
	}
	return msx, nil
}

// Size returns the total physical memory of the system in bytes.
//
// It calls the Windows GlobalMemoryStatusEx API and returns the ullTotalPhys
// field from the MEMORYSTATUSEX structure.
// Reference: https://learn.microsoft.com/en-us/windows/win32/api/sysinfoapi/nf-sysinfoapi-globalmemorystatusex
func Size() (uint64, error) {
	msx, err := callGlobalMemoryStatusEx()
	if err != nil {
		return 0, err
	}
	return msx.ullTotalPhys, nil
}

// Free returns the available physical memory of the system in bytes.
//
// It calls the Windows GlobalMemoryStatusEx API and returns the ullAvailPhys
// field from the MEMORYSTATUSEX structure.
// Reference: https://learn.microsoft.com/en-us/windows/win32/api/sysinfoapi/nf-sysinfoapi-globalmemorystatusex
func Free() (uint64, error) {
	msx, err := callGlobalMemoryStatusEx()
	if err != nil {
		return 0, err
	}
	return msx.ullAvailPhys, nil
}
