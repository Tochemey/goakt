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

package selfmanaged

import (
	"context"
	"net"
	"syscall"

	"golang.org/x/net/ipv4"
)

// listenUDPMulticastLoopback creates a UDP listener that joins the multicast
// group 224.0.0.1 on the loopback interface. Used for loopback discovery on
// macOS where UDP broadcast to 127.255.255.255 does not reach multiple receivers.
func listenUDPMulticastLoopback(port int) (*net.UDPConn, error) {
	ifi := loopbackInterface()
	if ifi == nil {
		return nil, net.UnknownNetworkError("no loopback interface")
	}
	addr := &net.UDPAddr{IP: multicastGroup, Port: port}
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var opErr error
			err := c.Control(func(fd uintptr) {
				opErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
				if opErr != nil {
					return
				}
				opErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
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
	conn := pc.(*net.UDPConn)
	pconn := ipv4.NewPacketConn(conn)
	if err := pconn.JoinGroup(ifi, &net.UDPAddr{IP: multicastGroup}); err != nil {
		conn.Close()
		return nil, err
	}
	if err := pconn.SetMulticastLoopback(true); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func loopbackInterface() *net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for i := range ifaces {
		if (ifaces[i].Flags & net.FlagLoopback) != 0 {
			return &ifaces[i]
		}
	}
	return nil
}
