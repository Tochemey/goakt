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

package client

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v4/remote"
)

func TestNode(t *testing.T) {
	ports := dynaport.Get(1)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[0]))

	node := NewNode(address, WithWeight(10), WithRemoteConfig(remote.NewConfig("127.0.0.1", ports[0])))
	require.NotNil(t, node)
	require.Equal(t, address, node.getAddress())
	require.Exactly(t, float64(10), node.getWeight())
	require.NoError(t, node.Validate())
	require.NotNil(t, node.remoteClient())
	host, port := node.hostAndPort()
	require.Equal(t, "127.0.0.1", host)
	require.Equal(t, ports[0], port)
	node.close()
}

func TestWithRemoteConfigForwardsSerializers(t *testing.T) {
	t.Run("user-defined serializer is available on the node remoting client", func(t *testing.T) {
		custom := &nodeTestSerializer{}
		config := remote.NewConfig("127.0.0.1", 0,
			remote.WithSerializers(new(nodeTestMsg), custom),
		)

		ports := dynaport.Get(1)
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[0]))
		node := NewNode(address, WithRemoteConfig(config))

		s := node.remoteClient().Serializer(&nodeTestMsg{})
		require.NotNil(t, s, "expected user-registered serializer to be forwarded to the node client")
		require.Same(t, custom, s)
	})
	t.Run("no user serializers leaves client with proto default only", func(t *testing.T) {
		config := remote.NewConfig("127.0.0.1", 0)
		ports := dynaport.Get(1)
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[0]))
		node := NewNode(address, WithRemoteConfig(config))

		// Unknown type returns nil — only the proto.Message default is registered.
		s := node.remoteClient().Serializer(&nodeTestMsg{})
		require.Nil(t, s)
	})
}

// nodeTestMsg is a local concrete type used to verify serializer forwarding.
type nodeTestMsg struct{}

// nodeTestSerializer is a minimal no-op Serializer for test assertions.
type nodeTestSerializer struct{}

func (nodeTestSerializer) Serialize(any) ([]byte, error)   { return nil, nil }
func (nodeTestSerializer) Deserialize([]byte) (any, error) { return nil, nil }
