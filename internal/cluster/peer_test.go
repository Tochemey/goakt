/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package cluster

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/remote"
)

func TestPeer(t *testing.T) {
	peer := Peer{
		Host:          "127.0.0.1",
		PeersPort:     9000,
		RemotingPort:  9100,
		Roles:         []string{"role1", "role2"},
		DiscoveryPort: 9200,
	}
	require.True(t, peer.HasRole("role1"))
	expectedPeerAddress := "127.0.0.1:9000"
	require.Equal(t, expectedPeerAddress, peer.PeerAddress())
}

func TestToRemotePeers(t *testing.T) {
	peers := []*Peer{
		{
			Host:          "127.0.0.1",
			PeersPort:     9000,
			RemotingPort:  9100,
			Roles:         []string{"role1", "role2"},
			DiscoveryPort: 9200,
		},
	}
	remotePeers := ToRemotePeers(peers)
	require.NotEmpty(t, remotePeers)
	require.Equal(t, 1, len(remotePeers))
	expected := &remote.Peer{
		Host:          "127.0.0.1",
		PeersPort:     9000,
		RemotingPort:  9100,
		Roles:         []string{"role1", "role2"},
		DiscoveryPort: 9200,
	}
	require.True(t, reflect.DeepEqual(expected, remotePeers[0]))
}
