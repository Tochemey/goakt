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

package selfmanaged

import (
	"context"
	"net"
	"syscall"
)

// listenUDPReusePort creates a UDP listener with SO_REUSEADDR so multiple nodes
// on the same host can bind to the same broadcast port (e.g. 127.0.0.1 for loopback).
// Windows does not support SO_REUSEPORT; SO_REUSEADDR allows multiple bindings.
func listenUDPReusePort(port int) (*net.UDPConn, error) {
	return listenUDPReusePortToAddr(&net.UDPAddr{IP: net.IPv4zero, Port: port})
}

// listenUDPReusePortToAddr creates a UDP listener bound to addr with SO_REUSEADDR.
func listenUDPReusePortToAddr(addr *net.UDPAddr) (*net.UDPConn, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var opErr error
			err := c.Control(func(fd uintptr) {
				opErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			})
			if err != nil {
				return err
			}
			return opErr
		},
	}
	pc, err := lc.ListenPacket(context.Background(), "udp4", addr.String())
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}
