/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package tcp

import (
	"fmt"
	"net"

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

// GetBindIP tries to find an appropriate bindIP to bind and propagate.
func GetBindIP(address string) (string, error) {
	bindIP, _, err := GetHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid address: %w", err)
	}

	if bindIP == "0.0.0.0" {
		// if we're not bound to a specific IP, let's use a suitable private IP address.
		ipStr, err := sockaddr.GetPrivateIP()
		if err != nil {
			return "", fmt.Errorf("failed to get private interface addresses: %w", err)
		}

		// if we could not find a private address, we need to expand our search to a public
		// ip address
		if ipStr == "" {
			ipStr, err = sockaddr.GetPublicIP()
			if err != nil {
				return "", fmt.Errorf("failed to get public interface addresses: %w", err)
			}
		}

		if ipStr == "" {
			return "", fmt.Errorf("no private IP address found, and explicit IP not provided")
		}

		parsed := net.ParseIP(ipStr)
		if parsed == nil {
			return "", fmt.Errorf("failed to parse private IP address: %q", ipStr)
		}
		bindIP = parsed.String()
	}
	return bindIP, nil
}
