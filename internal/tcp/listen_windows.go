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
// +build windows

package tcp

import (
	"fmt"
	"syscall"
)

const (
	// tcpFastOpen is the Windows socket option TCP_FASTOPEN.
	// Enables TCP Fast Open for reduced connection latency.
	// Requires Windows 10, version 1607 or later.
	tcpFastOpen = 0x17
	// defaultFastOpenQueueLen is the default queue length for TCP Fast Open.
	defaultFastOpenQueueLen = 256
)

// controlFunc is a socket control function that configures a raw connection
// before it is bound. Used with [net.ListenConfig.Control].
type controlFunc func(network, address string, c syscall.RawConn) error

// applyListenSocketOptions returns a [controlFunc] that applies the socket
// options specified in [ListenConfig] to a listening TCP socket.
//
// Supported options (Windows-specific):
//   - TCP_FASTOPEN: enables TCP Fast Open with configurable queue length
//     (requires Windows 10, version 1607+)
//
// Note: SO_REUSEPORT and TCP_DEFER_ACCEPT are not supported on Windows
// and are silently ignored if set in the config.
//
// If the socket option fails to apply, an error is returned.
func applyListenSocketOptions(config *ListenConfig) controlFunc {
	return func(network, address string, conn syscall.RawConn) error {
		var optErr error
		ctrlErr := conn.Control(func(fd uintptr) {
			// Apply TCP_FASTOPEN if enabled.
			if config.SocketFastOpen {
				qlen := config.SocketFastOpenQueueLen
				if qlen <= 0 {
					qlen = defaultFastOpenQueueLen
				}
				if err := setSockOpt(syscall.Handle(fd), syscall.IPPROTO_TCP, tcpFastOpen, qlen, "TCP_FASTOPEN"); err != nil {
					optErr = err
				}
			}
		})

		// Control() failure takes precedence.
		if ctrlErr != nil {
			return ctrlErr
		}
		return optErr
	}
}

// setSockOpt sets a socket option and wraps any error with context.
func setSockOpt(handle syscall.Handle, level, opt, value int, name string) error {
	if err := syscall.SetsockoptInt(handle, level, opt, value); err != nil {
		return fmt.Errorf("failed to set %s: %w", name, err)
	}
	return nil
}
