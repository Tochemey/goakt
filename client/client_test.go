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
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	actors "github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/discovery/nats"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	mockremote "github.com/tochemey/goakt/v4/mocks/remoteclient"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestNewReturnsErrorWithNoNodes(t *testing.T) {
	ctx := context.Background()

	cl, err := New(ctx, nil)
	require.Error(t, err)
	require.Nil(t, cl)
	require.ErrorContains(t, err, "nodes are required")
}

// nolint:revive
func TestClientUpdateNodes(t *testing.T) {
	// Note: These tests now focus on Node weight management logic.
	// Full end-to-end metric fetching via proto TCP is tested in integration tests.

	t.Run("node weight can be set and retrieved", func(t *testing.T) {
		node := NewNode("127.0.0.1:9000")

		// Initial weight should be 0
		require.Equal(t, 0.0, node.getWeight())

		// Set weight to 42
		node.SetWeight(42.0)
		require.Equal(t, 42.0, node.getWeight())

		// Set weight to another value
		node.SetWeight(100.5)
		require.Equal(t, 100.5, node.getWeight())
	})

	t.Run("node weight setting is thread-safe", func(t *testing.T) {
		node := NewNode("127.0.0.1:9000")

		// Concurrent writes and reads
		done := make(chan bool)
		for i := 0; i < 10; i++ {
			go func(val float64) {
				node.SetWeight(val)
				_ = node.getWeight()
				done <- true
			}(float64(i))
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}

		// Should not panic and weight should be one of the set values
		weight := node.getWeight()
		require.GreaterOrEqual(t, weight, 0.0)
		require.LessOrEqual(t, weight, 9.0)
	})

	t.Run("node can be created with initial weight", func(t *testing.T) {
		node := NewNode("127.0.0.1:9000", WithWeight(75.5))
		require.Equal(t, 75.5, node.getWeight())
	})
}

func TestClient(t *testing.T) {
	t.Run("With defaults", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()
		compression := remote.NoCompression

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, compression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, compression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, compression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		// register grains kinds
		err := sys1.RegisterGrainKind(ctx, &MockGrain{})
		require.NoError(t, err)
		err = sys2.RegisterGrainKind(ctx, &MockGrain{})
		require.NoError(t, err)
		err = sys3.RegisterGrainKind(ctx, &MockGrain{})
		require.NoError(t, err)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(compression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actor.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)
		actorName := "actorName"
		request := &remote.SpawnRequest{
			Kind:        "client.testactor",
			Name:        actorName,
			Relocatable: true,
		}

		err = client.Spawn(ctx, request)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actorName, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}

		actualReply, ok := reply.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expectedReply, actualReply))

		pause.For(500 * time.Millisecond)

		grainRequest := &remote.GrainRequest{
			Name:              "grain",
			Kind:              types.Name(&MockGrain{}),
			ActivationTimeout: 0,
			ActivationRetries: 0,
		}

		reply, err = client.AskGrain(ctx, grainRequest, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		actualReply, ok = reply.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expectedReply, actualReply))

		pause.For(500 * time.Millisecond)

		err = client.Tell(ctx, actorName, new(testpb.TestSend))
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		err = client.TellGrain(ctx, grainRequest, new(testpb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		t.Cleanup(
			func() {
				client.Close()

				require.NoError(t, sys1.Stop(ctx))
				require.NoError(t, sys2.Stop(ctx))
				require.NoError(t, sys3.Stop(ctx))

				require.NoError(t, sd1.Close())
				require.NoError(t, sd2.Close())
				require.NoError(t, sd3.Close())

				srv.Shutdown()
				pause.For(time.Second)
			})
	})
	t.Run("With randomRouter strategy", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		compression := remote.GzipCompression
		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, compression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, compression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, compression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(compression))))
		}

		client, err := New(
			ctx,
			nodes,
			WithBalancerStrategy(RoundRobinStrategy),
		)

		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actor.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)

		actorName := "actorName"
		request := &remote.SpawnRequest{
			Kind:        "client.testactor",
			Name:        actorName,
			Relocatable: true,
		}

		err = client.Spawn(ctx, request)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actorName, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}

		actualReply, ok := reply.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expectedReply, actualReply))

		pause.For(time.Second)

		err = client.Tell(ctx, actorName, new(testpb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		t.Cleanup(
			func() {
				client.Close()

				require.NoError(t, sys1.Stop(ctx))
				require.NoError(t, sys2.Stop(ctx))
				require.NoError(t, sys3.Stop(ctx))

				require.NoError(t, sd1.Close())
				require.NoError(t, sd2.Close())
				require.NoError(t, sd3.Close())

				srv.Shutdown()

				pause.For(time.Second)
			},
		)
	})
	t.Run("With Least-Load strategy", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		compression := remote.ZstdCompression

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, compression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, compression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, compression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(compression))))
		}

		client, err := New(
			ctx,
			nodes,
			WithBalancerStrategy(LeastLoadStrategy),
		)

		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actor.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)
		actorName := "actorName"
		request := &remote.SpawnRequest{
			Kind:        "client.testactor",
			Name:        actorName,
			Relocatable: true,
		}

		err = client.Spawn(ctx, request)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actorName, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}

		actualReply, ok := reply.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expectedReply, actualReply))

		pause.For(time.Second)

		err = client.Tell(ctx, actorName, new(testpb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		t.Cleanup(
			func() {
				client.Close()

				require.NoError(t, sys1.Stop(ctx))
				require.NoError(t, sys2.Stop(ctx))
				require.NoError(t, sys3.Stop(ctx))

				require.NoError(t, sd1.Close())
				require.NoError(t, sd2.Close())
				require.NoError(t, sd3.Close())

				srv.Shutdown()
				pause.For(time.Second)
			},
		)
	})
	t.Run("With Refresh Interval", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()
		compression := remote.BrotliCompression

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, compression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, compression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, compression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(compression))))
		}

		client, err := New(ctx, nodes, WithRefresh(time.Minute))
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actor.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)

		actorName := "actorName"
		request := &remote.SpawnRequest{
			Kind:        "client.testactor",
			Name:        actorName,
			Relocatable: true,
		}
		err = client.Spawn(ctx, request)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actorName, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		actualReply, ok := reply.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expectedReply, actualReply))

		pause.For(time.Second)

		err = client.Tell(ctx, actorName, new(testpb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		t.Cleanup(
			func() {
				client.Close()

				require.NoError(t, sys1.Stop(ctx))
				require.NoError(t, sys2.Stop(ctx))
				require.NoError(t, sys3.Stop(ctx))

				require.NoError(t, sd1.Close())
				require.NoError(t, sd2.Close())
				require.NoError(t, sd3.Close())

				srv.Shutdown()

				pause.For(time.Second)
			},
		)
	})
	t.Run("With SpawnBalanced", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()
		compression := remote.BrotliCompression

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, compression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, compression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, compression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(compression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actor.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)

		actorName := "actorName"
		request := &remote.SpawnRequest{
			Kind:        "client.testactor",
			Name:        actorName,
			Relocatable: true,
		}

		err = client.SpawnBalanced(ctx, request, RandomStrategy)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actorName, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		actualReply, ok := reply.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expectedReply, actualReply))

		pause.For(time.Second)

		err = client.Tell(ctx, actorName, new(testpb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		t.Cleanup(
			func() {
				client.Close()

				require.NoError(t, sys1.Stop(ctx))
				require.NoError(t, sys2.Stop(ctx))
				require.NoError(t, sys3.Stop(ctx))

				require.NoError(t, sd1.Close())
				require.NoError(t, sd2.Close())
				require.NoError(t, sd3.Close())

				srv.Shutdown()
				pause.For(time.Second)
			},
		)
	})
	t.Run("With ReSpawn", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()
		compression := remote.BrotliCompression

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, compression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, compression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, compression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(compression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actor.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)

		actorName := "actorName"
		request := &remote.SpawnRequest{
			Kind:        "client.testactor",
			Name:        actorName,
			Relocatable: true,
		}
		err = client.Spawn(ctx, request)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actorName, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		actualReply, ok := reply.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expectedReply, actualReply))

		pause.For(time.Second)

		err = client.Tell(ctx, actorName, new(testpb.TestSend))
		require.NoError(t, err)

		err = client.ReSpawn(ctx, actorName)
		require.NoError(t, err)

		pause.For(time.Second)

		reply, err = client.Ask(ctx, actorName, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply = &testpb.Reply{Content: "received message"}
		actualReply, ok = reply.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expectedReply, actualReply))

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		t.Cleanup(
			func() {
				client.Close()

				require.NoError(t, sys1.Stop(ctx))
				require.NoError(t, sys2.Stop(ctx))
				require.NoError(t, sys3.Stop(ctx))

				require.NoError(t, sd1.Close())
				require.NoError(t, sd2.Close())
				require.NoError(t, sd3.Close())

				srv.Shutdown()
				pause.For(time.Second)
			},
		)
	})
	t.Run("With ReSpawn after Stop", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, remote.NoCompression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, remote.NoCompression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, remote.NoCompression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(remote.NoCompression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actor.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)
		actorName := "actorName"
		request := &remote.SpawnRequest{
			Kind:        "client.testactor",
			Name:        actorName,
			Relocatable: true,
		}

		err = client.Spawn(ctx, request)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actorName, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		actualReply, ok := reply.(*testpb.Reply)
		require.True(t, ok)
		assert.True(t, proto.Equal(expectedReply, actualReply))

		pause.For(time.Second)

		err = client.Tell(ctx, actorName, new(testpb.TestSend))
		require.NoError(t, err)

		pause.For(time.Second)

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		err = client.ReSpawn(ctx, actorName)
		require.NoError(t, err)

		t.Cleanup(
			func() {
				client.Close()

				require.NoError(t, sys1.Stop(ctx))
				require.NoError(t, sys2.Stop(ctx))
				require.NoError(t, sys3.Stop(ctx))

				require.NoError(t, sd1.Close())
				require.NoError(t, sd2.Close())
				require.NoError(t, sd3.Close())

				srv.Shutdown()
				pause.For(time.Second)
			},
		)
	})

	t.Run("With ReSpawn returns no error when lookup returns no sender", func(t *testing.T) {
		ctx := context.TODO()
		actorName := "actorName"

		mockRemoting := mockremote.NewClient(t)
		node := &Node{
			remoting: mockRemoting,
			address:  "127.0.0.1:12345",
		}

		remoteHost, remotePort := node.hostAndPort()
		mockRemoting.EXPECT().NetClient(remoteHost, remotePort).Return(inet.NewClient(net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))))
		mockRemoting.EXPECT().RemoteLookup(ctx, remoteHost, remotePort, actorName).Return(address.NoSender(), nil)
		mockRemoting.EXPECT().Close()

		client, err := New(ctx, []*Node{node})
		require.NoError(t, err)
		require.NotNil(t, client)

		err = client.ReSpawn(ctx, actorName)
		require.NoError(t, err)
		mockRemoting.AssertNotCalled(t, "RemoteReSpawn", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

		client.Close()
	})

	t.Run("With ReSpawn returns error when lookup fails", func(t *testing.T) {
		ctx := context.TODO()
		actorName := "actorName"

		mockRemoting := mockremote.NewClient(t)
		node := &Node{
			remoting: mockRemoting,
			address:  "127.0.0.1:12345",
		}

		remoteHost, remotePort := node.hostAndPort()
		expectedErr := fmt.Errorf("remote lookup failed: %s", node.address)
		mockRemoting.EXPECT().NetClient(remoteHost, remotePort).Return(inet.NewClient(net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))))
		mockRemoting.EXPECT().RemoteLookup(ctx, remoteHost, remotePort, actorName).Return(nil, expectedErr)
		mockRemoting.EXPECT().Close()

		client, err := New(ctx, []*Node{node})
		require.NoError(t, err)
		require.NotNil(t, client)

		err = client.ReSpawn(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)
		mockRemoting.AssertNotCalled(t, "RemoteReSpawn", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

		client.Close()
	})

	t.Run("With ReSpawn succeeds when lookup returns valid address", func(t *testing.T) {
		ctx := context.TODO()
		actorName := "actorName"

		mockRemoting := mockremote.NewClient(t)
		node := &Node{
			remoting: mockRemoting,
			address:  "127.0.0.1:12345",
		}

		remoteHost, remotePort := node.hostAndPort()
		addr := address.New(actorName, "sys", remoteHost, remotePort)
		mockRemoting.EXPECT().NetClient(remoteHost, remotePort).Return(inet.NewClient(net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))))
		mockRemoting.EXPECT().RemoteLookup(ctx, remoteHost, remotePort, actorName).Return(addr, nil)
		mockRemoting.EXPECT().RemoteReSpawn(ctx, addr.Host(), addr.Port(), actorName).Return(nil, nil)
		mockRemoting.EXPECT().Close()

		client, err := New(ctx, []*Node{node})
		require.NoError(t, err)
		require.NotNil(t, client)

		err = client.ReSpawn(ctx, actorName)
		require.NoError(t, err)

		client.Close()
	})

	t.Run("With ReSpawn returns error when RemoteReSpawn fails", func(t *testing.T) {
		ctx := context.TODO()
		actorName := "actorName"

		mockRemoting := mockremote.NewClient(t)
		node := &Node{
			remoting: mockRemoting,
			address:  "127.0.0.1:12345",
		}

		remoteHost, remotePort := node.hostAndPort()
		addr := address.New(actorName, "sys", remoteHost, remotePort)
		expectedErr := fmt.Errorf("remote respawn failed")
		mockRemoting.EXPECT().NetClient(remoteHost, remotePort).Return(inet.NewClient(net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))))
		mockRemoting.EXPECT().RemoteLookup(ctx, remoteHost, remotePort, actorName).Return(addr, nil)
		mockRemoting.EXPECT().RemoteReSpawn(ctx, addr.Host(), addr.Port(), actorName).Return(nil, expectedErr)
		mockRemoting.EXPECT().Close()

		client, err := New(ctx, []*Node{node})
		require.NoError(t, err)
		require.NotNil(t, client)

		err = client.ReSpawn(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)

		client.Close()
	})

	t.Run("With Whereis", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, remote.NoCompression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, remote.NoCompression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, remote.NoCompression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(remote.NoCompression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actor.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)
		actorName := "actorName"
		request := &remote.SpawnRequest{
			Kind:        "client.testactor",
			Name:        actorName,
			Relocatable: true,
		}

		err = client.Spawn(ctx, request)
		require.NoError(t, err)

		pause.For(time.Second)

		exists, err := client.Exists(ctx, actorName)
		require.NoError(t, err)
		assert.True(t, exists, "actor should exist after spawn")

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		t.Cleanup(
			func() {
				client.Close()

				require.NoError(t, sys1.Stop(ctx))
				require.NoError(t, sys2.Stop(ctx))
				require.NoError(t, sys3.Stop(ctx))

				require.NoError(t, sd1.Close())
				require.NoError(t, sd2.Close())
				require.NoError(t, sd3.Close())

				srv.Shutdown()
				pause.For(time.Second)
			})
	})
	t.Run("With AskGrain with error", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, remote.NoCompression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, remote.NoCompression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, remote.NoCompression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(remote.NoCompression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		grainRequest := &remote.GrainRequest{
			Name:              "grain",
			Kind:              types.Name(&MockGrain{}),
			ActivationTimeout: 0,
			ActivationRetries: 0,
		}

		reply, err := client.AskGrain(ctx, grainRequest, new(testpb.TestReply), time.Minute)
		require.Error(t, err)
		assert.ErrorContains(t, err, gerrors.ErrGrainNotRegistered.Error())
		require.Nil(t, reply)

		client.Close()

		require.NoError(t, sys1.Stop(ctx))
		require.NoError(t, sys2.Stop(ctx))
		require.NoError(t, sys3.Stop(ctx))

		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())

		srv.Shutdown()
		pause.For(time.Second)
	})

	t.Run("With Ask when actor not found", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, remote.NoCompression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, remote.NoCompression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, remote.NoCompression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(remote.NoCompression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		actorName := "actorName"

		// send a message
		reply, err := client.Ask(ctx, actorName, new(testpb.TestReply), time.Minute)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
		require.Nil(t, reply)

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		client.Close()

		require.NoError(t, sys1.Stop(ctx))
		require.NoError(t, sys2.Stop(ctx))
		require.NoError(t, sys3.Stop(ctx))

		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())

		srv.Shutdown()
		pause.For(time.Second)
	})

	t.Run("When RemoteAsk fails", func(t *testing.T) {
		ctx := context.TODO()
		actorName := "actorName"

		mockRemoting := mockremote.NewClient(t)
		node := &Node{
			remoting: mockRemoting,
			address:  "127.0.1:12345",
		}

		remoteHost, remotePort := node.hostAndPort()
		addr := address.New(actorName, "system", remoteHost, remotePort)

		expectedErr := fmt.Errorf("remote ask failed: %s", node.address)
		mockRemoting.EXPECT().NetClient(remoteHost, remotePort).Return(inet.NewClient(net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))))
		mockRemoting.EXPECT().RemoteLookup(ctx, remoteHost, remotePort, actorName).Return(addr, nil)
		mockRemoting.EXPECT().RemoteAsk(ctx, address.NoSender(), addr, new(testpb.TestReply), time.Minute).Return(nil, expectedErr)

		client, err := New(ctx, []*Node{node})
		require.NoError(t, err)
		require.NotNil(t, client)

		_, err = client.Ask(ctx, actorName, new(testpb.TestReply), time.Minute)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)
	})
	t.Run("With Tell when actor not found", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()
		compression := remote.BrotliCompression

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, compression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, compression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, compression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(compression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		actorName := "actorName"

		// send a message
		err = client.Tell(ctx, actorName, new(testpb.TestReply))
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		client.Close()

		require.NoError(t, sys1.Stop(ctx))
		require.NoError(t, sys2.Stop(ctx))
		require.NoError(t, sys3.Stop(ctx))

		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())

		srv.Shutdown()
		pause.For(time.Second)
	})
	t.Run("With Stop when actor not found returns no error", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()
		compression := remote.GzipCompression

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, compression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, compression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, compression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(compression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		actorName := "actorName"

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		client.Close()

		require.NoError(t, sys1.Stop(ctx))
		require.NoError(t, sys2.Stop(ctx))
		require.NoError(t, sys3.Stop(ctx))

		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())

		srv.Shutdown()
		pause.For(time.Second)
	})
	t.Run("With Whereis when actor not found", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()
		compression := remote.ZstdCompression

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, compression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, compression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, compression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(compression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		actorName := "actorName"
		whereis, err := client.Exists(ctx, actorName)
		require.Error(t, err)
		require.Nil(t, whereis)

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		client.Close()

		require.NoError(t, sys1.Stop(ctx))
		require.NoError(t, sys2.Stop(ctx))
		require.NoError(t, sys3.Stop(ctx))

		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())

		srv.Shutdown()
		pause.For(time.Second)
	})
	t.Run("With Reinstate when actor not found returns no error", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, remote.NoCompression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, remote.NoCompression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, remote.NoCompression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(remote.NoCompression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		actorName := "actorName"
		err = client.Reinstate(ctx, actorName)
		require.NoError(t, err)

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		client.Close()

		require.NoError(t, sys1.Stop(ctx))
		require.NoError(t, sys2.Stop(ctx))
		require.NoError(t, sys3.Stop(ctx))

		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())

		srv.Shutdown()
		pause.For(time.Second)
	})
	t.Run("With Reinstate", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, remote.NoCompression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, remote.NoCompression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, remote.NoCompression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(remote.NoCompression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		actorName := "actorName"
		request := &remote.SpawnRequest{
			Kind:        "client.testactor",
			Name:        actorName,
			Relocatable: true,
		}

		err = client.Spawn(ctx, request)
		require.NoError(t, err)

		pause.For(time.Second)

		err = client.Reinstate(ctx, actorName)
		require.NoError(t, err)

		err = client.Stop(ctx, actorName)
		require.NoError(t, err)

		client.Close()

		require.NoError(t, sys1.Stop(ctx))
		require.NoError(t, sys2.Stop(ctx))
		require.NoError(t, sys3.Stop(ctx))

		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())

		srv.Shutdown()
		pause.For(time.Second)
	})
	t.Run("When RemoteLookup fails", func(t *testing.T) {
		ctx := context.TODO()
		actorName := "actorName"

		mockRemoting := mockremote.NewClient(t)
		node := &Node{
			remoting: mockRemoting,
			address:  "127.0.1:12345",
		}

		remoteHost, remotePort := node.hostAndPort()
		expectedErr := fmt.Errorf("remote lookup failed: %s", node.address)
		mockRemoting.EXPECT().NetClient(remoteHost, remotePort).Return(inet.NewClient(net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))))
		mockRemoting.EXPECT().RemoteLookup(ctx, remoteHost, remotePort, actorName).Return(nil, expectedErr)

		client, err := New(ctx, []*Node{node})
		require.NoError(t, err)
		require.NotNil(t, client)

		// send a message
		err = client.Tell(ctx, actorName, new(testpb.TestReply))
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)

		_, err = client.Ask(ctx, actorName, new(testpb.TestReply), time.Minute)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)

		err = client.Stop(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)

		_, err = client.Exists(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)

		err = client.Reinstate(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("With Kinds using gzip compression", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()
		compression := remote.GzipCompression

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, compression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, compression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, compression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(compression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actor.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)

		t.Cleanup(
			func() {
				client.Close()

				require.NoError(t, sys1.Stop(ctx))
				require.NoError(t, sys2.Stop(ctx))
				require.NoError(t, sys3.Stop(ctx))

				require.NoError(t, sd1.Close())
				require.NoError(t, sd2.Close())
				require.NoError(t, sd3.Close())

				srv.Shutdown()
				pause.For(time.Second)
			})
	})

	t.Run("With Kinds using Zstandard compression", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()
		compression := remote.ZstdCompression

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, compression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, compression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, compression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(compression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actor.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)

		t.Cleanup(
			func() {
				client.Close()

				require.NoError(t, sys1.Stop(ctx))
				require.NoError(t, sys2.Stop(ctx))
				require.NoError(t, sys3.Stop(ctx))

				require.NoError(t, sd1.Close())
				require.NoError(t, sd2.Close())
				require.NoError(t, sd3.Close())

				srv.Shutdown()
				pause.For(time.Second)
			})
	})

	t.Run("With Kinds using brotli compression", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()
		compression := remote.BrotliCompression

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr, compression)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr, compression)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr, compression)

		// wait for a proper and clean setup of the cluster
		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr, WithRemoteConfig(remote.NewConfig("127.0.0.1", 0, remote.WithCompression(compression))))
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actor.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)

		t.Cleanup(
			func() {
				client.Close()

				require.NoError(t, sys1.Stop(ctx))
				require.NoError(t, sys2.Stop(ctx))
				require.NoError(t, sys3.Stop(ctx))

				require.NoError(t, sd1.Close())
				require.NoError(t, sd2.Close())
				require.NoError(t, sd3.Close())

				srv.Shutdown()
				pause.For(time.Second)
			})
	})

	t.Run("With Kinds failure when cluster not enabled", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"
		actorSystemName := "testSystem"

		system, err := actors.NewActorSystem(
			actorSystemName,
			actors.WithLogger(log.DiscardLogger),
			actors.WithRemote(remote.NewConfig(host, remotingPort)),
		)

		require.NotNil(t, system)
		require.NoError(t, err)

		// start the node
		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", host, remotingPort),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr)
		}

		random := NewRandom()
		random.Set(nodes...)

		client := &Client{
			nodes:    nodes,
			strategy: RandomStrategy,
			balancer: random,
		}
		kinds, err := client.Kinds(ctx)
		require.Error(t, err)
		assert.ErrorContains(t, err, gerrors.ErrClusterDisabled.Error())
		assert.Nil(t, kinds)
		assert.Empty(t, kinds)

		pause.For(time.Second)

		client.Close()

		require.NoError(t, system.Stop(ctx))
	})
	t.Run("With GetNodeMetric failure when cluster not enabled", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"
		actorSystemName := "testSystem"

		system, err := actors.NewActorSystem(
			actorSystemName,
			actors.WithLogger(log.DiscardLogger),
			actors.WithRemote(remote.NewConfig(host, remotingPort)),
		)

		require.NotNil(t, system)
		require.NoError(t, err)

		// start the node
		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		node := NewNode(fmt.Sprintf("%s:%d", host, remotingPort))

		metric, ok, err := getNodeMetric(ctx, node)
		require.Error(t, err)
		assert.ErrorContains(t, err, gerrors.ErrClusterDisabled.Error())
		assert.False(t, ok)
		assert.Zero(t, metric)

		pause.For(time.Second)

		require.NoError(t, system.Stop(ctx))
	})
}

func TestRefreshNodesLoopPanics(t *testing.T) {
	client := &Client{
		refreshInterval: 0,
	}

	require.PanicsWithValue(t, "intervals must be greater than zero", func() {
		client.refreshNodesLoop()
	})
}

func startNatsServer(t *testing.T) *natsserver.Server {
	t.Helper()
	serv, err := natsserver.NewServer(
		&natsserver.Options{
			Host: "127.0.0.1",
			Port: -1,
		},
	)

	require.NoError(t, err)

	ready := make(chan bool)
	go func() {
		ready <- true
		serv.Start()
	}()
	<-ready

	if !serv.ReadyForConnections(2 * time.Second) {
		t.Fatalf("nats-io server failed to start")
	}

	return serv
}

func startNode(t *testing.T, logger log.Logger, nodeName, serverAddr string, compression remote.Compression) (system actors.ActorSystem, remotingHost string, remotingPort int, provider discovery.Provider) {
	ctx := context.TODO()

	// generate the ports for the single startNode
	nodePorts := dynaport.Get(3)
	discoveryPort := nodePorts[0]
	peersPort := nodePorts[1]
	remotePort := nodePorts[2]

	// create a Cluster startNode
	host := "127.0.0.1"
	// create the various config option
	applicationName := "accounts"
	actorSystemName := "testSystem"
	natsSubject := "some-subject"
	// create the config
	config := nats.Config{
		ApplicationName: applicationName,
		ActorSystemName: actorSystemName,
		NatsServer:      fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:     natsSubject,
		Host:            host,
		DiscoveryPort:   discoveryPort,
	}

	hostNode := discovery.Node{
		Name:          nodeName,
		Host:          host,
		DiscoveryPort: discoveryPort,
		PeersPort:     peersPort,
		RemotingPort:  remotePort,
	}

	// create the instance of provider
	natsProvider := nats.NewDiscovery(&config, nats.WithLogger(logger))

	clusterConfig := actors.
		NewClusterConfig().
		WithKinds(new(testActor)).
		WithDiscovery(natsProvider).
		WithPeersPort(peersPort).
		WithDiscoveryPort(discoveryPort).
		WithReplicaCount(1).
		WithMinimumPeersQuorum(1).
		WithPartitionCount(7)

	// create the actor system
	system, err := actors.NewActorSystem(
		actorSystemName,
		actors.WithLogger(logger),
		actors.WithRemote(remote.NewConfig(host, remotePort, remote.WithCompression(compression))),
		actors.WithCluster(clusterConfig),
	)

	require.NotNil(t, system)
	require.NoError(t, err)

	// start the node
	require.NoError(t, system.Start(ctx))

	pause.For(time.Second)

	logger.Infof("node information=%s", hostNode.String())

	// return the cluster startNode
	return system, host, remotePort, natsProvider
}

type testActor struct {
	logger log.Logger
}

// enforce compilation error
var _ actors.Actor = (*testActor)(nil)

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing public
func (x *testActor) PreStart(*actors.Context) error {
	x.logger = log.DiscardLogger
	x.logger.Info("pre start")
	return nil
}

// Shutdown gracefully shuts down the given actor
func (x *testActor) PostStop(*actors.Context) error {
	x.logger.Info("post stop")
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (x *testActor) Receive(ctx *actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *actors.PostStart:
		x.logger.Info("post start")
	case *testpb.TestSend:
	case *testpb.TestReply:
		x.logger.Info("received a test reply message...")
		ctx.Response(&testpb.Reply{Content: "received message"})
	default:
		ctx.Unhandled()
	}
}

type MockGrain struct {
	name string
}

var _ actors.Grain = (*MockGrain)(nil)

func NewMockGrain() *MockGrain {
	return &MockGrain{}
}

func (m *MockGrain) OnActivate(ctx context.Context, props *actors.GrainProps) error {
	m.name = props.Identity().Name()
	return nil
}

func (m *MockGrain) OnDeactivate(ctx context.Context, props *actors.GrainProps) error {
	return nil
}

func (m *MockGrain) OnReceive(ctx *actors.GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		ctx.NoErr()
	case *testpb.TestReply:
		ctx.Response(&testpb.Reply{Content: "received message"})
	default:
		ctx.Unhandled()
	}
}
