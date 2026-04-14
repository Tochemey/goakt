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

package net

import (
	"fmt"
	"net"
	"strconv"
	"sync"
)

const portAllocAttempts = 32

var (
	allocatedMu    sync.Mutex
	allocatedPorts = make(map[int]struct{})
)

// Get returns n ports that are free on both TCP and UDP, panicking on failure.
func Get(n int) []int {
	ports, err := GetWithErr(n)
	if err != nil {
		panic(err)
	}
	return ports
}

// GetS returns n ports as strings, panicking on failure.
func GetS(n int) []string {
	ports, err := GetSWithErr(n)
	if err != nil {
		panic(err)
	}
	return ports
}

// GetWithErr returns n ports that are free on both TCP and UDP.
func GetWithErr(n int) ([]int, error) {
	if n <= 0 {
		return nil, nil
	}

	// Hold all sockets until we have collected n ports. This prevents the
	// kernel from returning the same port twice within a single call when
	// n > 1, which would otherwise be possible between our close and the
	// next bind.
	tcpHeld := make([]*net.TCPListener, 0, n)
	udpHeld := make([]*net.UDPConn, 0, n)

	defer func() {
		for _, ln := range tcpHeld {
			_ = ln.Close()
		}
		for _, pc := range udpHeld {
			_ = pc.Close()
		}
	}()

	ports := make([]int, 0, n)
	for len(ports) < n {
		port, tcp, udp, err := reserveUnique()
		if err != nil {
			return nil, err
		}
		tcpHeld = append(tcpHeld, tcp)
		udpHeld = append(udpHeld, udp)
		ports = append(ports, port)
	}
	return ports, nil
}

// reserveUnique reserves a port that has not been previously handed out by
// this process. Because sockets are closed once a Get call returns, the OS
// can reassign the same port to a concurrent or subsequent Get call; the
// process-wide allocatedPorts set prevents that from producing duplicates.
func reserveUnique() (int, *net.TCPListener, *net.UDPConn, error) {
	var (
		rejectedTCP []*net.TCPListener
		rejectedUDP []*net.UDPConn
		lastErr     error
	)
	defer func() {
		for _, ln := range rejectedTCP {
			_ = ln.Close()
		}
		for _, pc := range rejectedUDP {
			_ = pc.Close()
		}
	}()

	for range portAllocAttempts {
		port, tcp, udp, err := reserveBoth()
		if err != nil {
			lastErr = err
			continue
		}

		allocatedMu.Lock()
		if _, dup := allocatedPorts[port]; dup {
			allocatedMu.Unlock()
			rejectedTCP = append(rejectedTCP, tcp)
			rejectedUDP = append(rejectedUDP, udp)
			continue
		}
		allocatedPorts[port] = struct{}{}
		allocatedMu.Unlock()
		return port, tcp, udp, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all candidate ports already allocated")
	}
	return 0, nil, nil, fmt.Errorf("dynaport: could not reserve a unique TCP+UDP port after %d attempts: %w", portAllocAttempts, lastErr)
}

// GetSWithErr returns n ports as strings that are free on both TCP and UDP.
func GetSWithErr(n int) ([]string, error) {
	ports, err := GetWithErr(n)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ports))
	for i, p := range ports {
		out[i] = strconv.Itoa(p)
	}
	return out, nil
}

// reserveBoth binds TCP on 127.0.0.1:0, reads back the assigned port, then
// binds UDP on the same port. The two sockets are returned to the caller,
// which is expected to close them before handing the port number off.
//
// The rare case where the OS-assigned TCP port is already bound on UDP by
// another process is handled by retrying up to portAllocAttempts times.
func reserveBoth() (int, *net.TCPListener, *net.UDPConn, error) {
	var lastErr error
	for range portAllocAttempts {
		tcp, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		if err != nil {
			lastErr = err
			continue
		}
		port := tcp.Addr().(*net.TCPAddr).Port

		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
		if err != nil {
			_ = tcp.Close()
			lastErr = err
			continue
		}
		return port, tcp, udp, nil
	}
	return 0, nil, nil, fmt.Errorf("dynaport: could not reserve a free TCP+UDP port after %d attempts: %w", portAllocAttempts, lastErr)
}
