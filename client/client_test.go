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

package client

import (
	"context"
	"fmt"
	nethttp "net/http"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	actors "github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/discovery/nats"
	"github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	mocks "github.com/tochemey/goakt/v3/mocks/remote"
	"github.com/tochemey/goakt/v3/remote"
	testpb "github.com/tochemey/goakt/v3/test/data/testpb"
)

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

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		nodes := make([]*Node, len(addresses))
		for i, addr := range addresses {
			nodes[i] = NewNode(addr)
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
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor, false, true)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		pause.For(time.Second)

		err = client.Tell(ctx, actor, new(testpb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actor)
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
			nodes[i] = NewNode(addr)
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
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor, false, true)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		pause.For(time.Second)

		err = client.Tell(ctx, actor, new(testpb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actor)
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
			nodes[i] = NewNode(addr)
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
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor, false, true)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		pause.For(time.Second)

		err = client.Tell(ctx, actor, new(testpb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actor)
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
			nodes[i] = NewNode(addr)
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
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor, false, true)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		pause.For(time.Second)

		err = client.Tell(ctx, actor, new(testpb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actor)
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
			nodes[i] = NewNode(addr)
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
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.SpawnBalanced(ctx, actor, false, true, RandomStrategy)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		pause.For(time.Second)

		err = client.Tell(ctx, actor, new(testpb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actor)
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
			nodes[i] = NewNode(addr)
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
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor, false, true)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		pause.For(time.Second)

		err = client.Tell(ctx, actor, new(testpb.TestSend))
		require.NoError(t, err)

		err = client.ReSpawn(ctx, actor)
		require.NoError(t, err)

		pause.For(time.Second)

		reply, err = client.Ask(ctx, actor, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply = &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		err = client.Stop(ctx, actor)
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
			nodes[i] = NewNode(addr)
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
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor, false, true)
		require.NoError(t, err)

		pause.For(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testpb.TestReply), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		pause.For(time.Second)

		err = client.Tell(ctx, actor, new(testpb.TestSend))
		require.NoError(t, err)

		pause.For(time.Second)

		err = client.Stop(ctx, actor)
		require.NoError(t, err)

		err = client.ReSpawn(ctx, actor)
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
			nodes[i] = NewNode(addr)
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
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor, false, true)
		require.NoError(t, err)

		pause.For(time.Second)

		whereis, err := client.Whereis(ctx, actor)
		require.NoError(t, err)
		require.NotNil(t, whereis)
		assert.Equal(t, actor.Name(), whereis.Name())

		err = client.Stop(ctx, actor)
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
			nodes[i] = NewNode(addr)
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		actor := NewActor("client.testactor").WithName("actorName")

		// send a message
		reply, err := client.Ask(ctx, actor, new(testpb.TestReply), time.Minute)
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrActorNotFound)
		require.Nil(t, reply)

		err = client.Stop(ctx, actor)
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
		actor := NewActor("client.testactor").WithName("actorName")

		httpClient := nethttp.DefaultClient
		mockRemoting := mocks.NewRemoting(t)
		node := &Node{
			remoting: mockRemoting,
			address:  "127.0.1:12345",
			mutex:    &sync.RWMutex{},
			client:   httpClient,
		}

		remoteHost, remotePort := node.HostAndPort()
		addr := address.New(actor.Name(), "system", remoteHost, remotePort)

		expectedErr := fmt.Errorf("remote ask failed: %s", node.address)
		mockRemoting.EXPECT().RemoteLookup(ctx, remoteHost, remotePort, actor.Name()).Return(addr, nil)
		mockRemoting.EXPECT().MaxReadFrameSize().Return(1024 * 1024)
		mockRemoting.EXPECT().Compression().Return(remote.NoCompression)
		mockRemoting.EXPECT().RemoteAsk(ctx, address.NoSender(), addr, new(testpb.TestReply), time.Minute).Return(nil, expectedErr)

		client, err := New(ctx, []*Node{node})
		require.NoError(t, err)
		require.NotNil(t, client)

		_, err = client.Ask(ctx, actor, new(testpb.TestReply), time.Minute)
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
			nodes[i] = NewNode(addr)
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		actor := NewActor("client.testactor").WithName("actorName")

		// send a message
		err = client.Tell(ctx, actor, new(testpb.TestReply))
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrActorNotFound)

		err = client.Stop(ctx, actor)
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
			nodes[i] = NewNode(addr)
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Stop(ctx, actor)
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
			nodes[i] = NewNode(addr)
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		actor := NewActor("client.testactor").WithName("actorName")
		whereis, err := client.Whereis(ctx, actor)
		require.Error(t, err)
		require.Nil(t, whereis)

		err = client.Stop(ctx, actor)
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
			nodes[i] = NewNode(addr)
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		actor := NewActor("client.testactor").WithName("actorName")
		err = client.Reinstate(ctx, actor)
		require.NoError(t, err)

		err = client.Stop(ctx, actor)
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
			nodes[i] = NewNode(addr)
		}

		client, err := New(ctx, nodes)
		require.NoError(t, err)
		require.NotNil(t, client)

		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor, false, true)
		require.NoError(t, err)

		pause.For(time.Second)

		err = client.Reinstate(ctx, actor)
		require.NoError(t, err)

		err = client.Stop(ctx, actor)
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
		actor := NewActor("client.testactor").WithName("actorName")

		httpClient := nethttp.DefaultClient
		mockRemoting := mocks.NewRemoting(t)
		node := &Node{
			remoting: mockRemoting,
			address:  "127.0.1:12345",
			mutex:    &sync.RWMutex{},
			client:   httpClient,
		}

		remoteHost, remotePort := node.HostAndPort()
		expectedErr := fmt.Errorf("remote lookup failed: %s", node.address)
		mockRemoting.EXPECT().RemoteLookup(ctx, remoteHost, remotePort, actor.Name()).Return(nil, expectedErr)
		mockRemoting.EXPECT().MaxReadFrameSize().Return(1024 * 1024)
		mockRemoting.EXPECT().Compression().Return(remote.NoCompression)

		client, err := New(ctx, []*Node{node})
		require.NoError(t, err)
		require.NotNil(t, client)

		// send a message
		err = client.Tell(ctx, actor, new(testpb.TestReply))
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)

		_, err = client.Ask(ctx, actor, new(testpb.TestReply), time.Minute)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)

		err = client.Stop(ctx, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)

		_, err = client.Whereis(ctx, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)

		err = client.Reinstate(ctx, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, expectedErr)
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
func (p *testActor) PreStart(*actors.Context) error {
	p.logger = log.DiscardLogger
	p.logger.Info("pre start")
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *testActor) PostStop(*actors.Context) error {
	p.logger.Info("post stop")
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *testActor) Receive(ctx *actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		p.logger.Info("post start")
	case *testpb.TestSend:
	case *testpb.TestReply:
		p.logger.Info("received a test reply message...")
		ctx.Response(&testpb.Reply{Content: "received message"})
	default:
		ctx.Unhandled()
	}
}
