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
	"time"

	"github.com/hashicorp/go-sockaddr"
)

// GetHostPort returns the actual ip address and port from a given address
func GetHostPort(address string) (string, int, error) {
	// Get the address
	addr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return "", 0, err
	}

	return addr.IP.String(), addr.Port, nil
}

// GetBindIP returns the address to advertise to peers for the given "host:port"
// bind address. A concrete IP is returned unchanged. A wildcard (0.0.0.0 or ::)
// is replaced by the first private interface address, then by the first public
// one, and finally by the loopback address of the wildcard's family when the
// host has neither, so an offline machine still starts. Only the advertised
// address is resolved here; listeners keep binding the wildcard.
func GetBindIP(address string) (string, error) {
	bindIP, _, err := GetHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid address: %w", err)
	}

	ip := net.ParseIP(bindIP)
	if ip == nil || !ip.IsUnspecified() {
		return bindIP, nil
	}

	return wildcardAdvertisedIP(ip, sockaddr.GetPrivateIP, sockaddr.GetPublicIP)
}

// wildcardAdvertisedIP picks the address a wildcard bind address advertises to
// peers: the first private interface address, then the first public one, then
// the loopback address of the wildcard's family. privateIP and publicIP are the
// interface lookups; they return an empty string when they find nothing.
func wildcardAdvertisedIP(wildcard net.IP, privateIP, publicIP func() (string, error)) (string, error) {
	// if we're not bound to a specific IP, let's use a suitable private IP address.
	ipStr, err := privateIP()
	if err != nil {
		return "", fmt.Errorf("failed to get private interface addresses: %w", err)
	}

	// if we could not find a private address, we need to expand our search to a public
	// ip address
	if ipStr == "" {
		ipStr, err = publicIP()
		if err != nil {
			return "", fmt.Errorf("failed to get public interface addresses: %w", err)
		}
	}

	// a host with no routable address at all, such as an offline machine, can only
	// be reached through loopback
	if ipStr == "" {
		if wildcard.To4() == nil {
			return net.IPv6loopback.String(), nil
		}

		return "127.0.0.1", nil
	}

	parsed := net.ParseIP(ipStr)
	if parsed == nil {
		return "", fmt.Errorf("failed to parse private IP address: %q", ipStr)
	}

	return parsed.String(), nil
}

// KeepAliveListener sets TCP keep-alive timeouts on accepted
// connections. It's used by Serve so that dead TCP connections eventually
// go away.
type KeepAliveListener struct {
	*net.TCPListener
}

func (ln KeepAliveListener) Accept() (c net.Conn, err error) {
	if c, err = ln.AcceptTCP(); err != nil {
		return
	} else if err = c.(*net.TCPConn).SetKeepAlive(true); err != nil {
		return
	}
	// Ignore error from setting the KeepAlivePeriod as some systems, such as
	// OpenBSD, do not support setting TCP_USER_TIMEOUT on IPPROTO_TCP
	_ = c.(*net.TCPConn).SetKeepAlivePeriod(3 * time.Minute)
	return
}

// NewKeepAliveListener creates an instance of KeepAliveListener
func NewKeepAliveListener(address string) (*KeepAliveListener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	return &KeepAliveListener{listener.(*net.TCPListener)}, nil
}
