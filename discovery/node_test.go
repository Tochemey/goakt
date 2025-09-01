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

package discovery

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNode_PeersAddress(t *testing.T) {
	t.Run("IPv4 host", func(t *testing.T) {
		n := &Node{Host: "127.0.0.1", PeersPort: 7000}
		require.Equal(t, "127.0.0.1:7000", n.PeersAddress())
	})

	t.Run("IPv6 host", func(t *testing.T) {
		n := &Node{Host: "::1", PeersPort: 7001}
		require.Equal(t, "[::1]:7001", n.PeersAddress())
	})
}

func TestNode_DiscoveryAddress(t *testing.T) {
	t.Run("IPv4 host", func(t *testing.T) {
		n := &Node{Host: "localhost", DiscoveryPort: 9000}
		require.Equal(t, "localhost:9000", n.DiscoveryAddress())
	})

	t.Run("IPv6 host", func(t *testing.T) {
		n := &Node{Host: "fe80::1", DiscoveryPort: 9100}
		require.Equal(t, "[fe80::1]:9100", n.DiscoveryAddress())
	})
}

func TestNode_String(t *testing.T) {
	n := &Node{
		Name:          "node-a",
		Host:          "10.0.0.1",
		DiscoveryPort: 7946,
		PeersPort:     8500,
		RemotingPort:  8080,
	}
	// keep exact spacing as in fmt string (double space before peers)
	expected := "[name=node-a host=10.0.0.1 gossip=7946  peers=8500 remoting=8080]"
	require.Equal(t, expected, n.String())
}
