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

package actor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

// nolint
func TestReleaseGrainContextResetsFields(t *testing.T) {
	ctx := getGrainContext()

	identity := &GrainIdentity{kind: "TestKind", name: "id"}
	ctx.self = identity
	ctx.message = &testpb.TestMessage{}
	ctx.err = make(chan error, 1)
	ctx.response = make(chan proto.Message, 1)
	ctx.pid = &grainPID{}

	releaseGrainContext(ctx)

	require.Nil(t, ctx.self)
	require.Nil(t, ctx.message)
	require.Nil(t, ctx.err)
	require.Nil(t, ctx.response)
	require.Nil(t, ctx.pid)

	// Return the context to the pool to keep the pool populated for other tests.
	// releaseGrainContext already returned it, but we re-acquire and release to ensure clean state.
	acquired := getGrainContext()
	releaseGrainContext(acquired)
}

func TestGrainContext(t *testing.T) {
	t.Run("With Grain to Actor messaging", func(t *testing.T) {
		ctx := t.Context()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start a system cluster
		node1, sd1 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start a system cluster
		node2, sd2 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create and start a system cluster
		node3, sd3 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		grain := NewMockGrain()
		identity, err := node1.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		pause.For(time.Second)

		// check if the grain is activated
		gp, ok := node1.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		// send a message to the grain
		message := new(testpb.TestReply)
		response, err := node1.AskGrain(ctx, identity, message, time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		// create an actor
		pid, err := node2.Spawn(ctx, "Actor20", NewMockGrainActor())
		require.NoError(t, err)
		require.NotNil(t, pid)

		pause.For(time.Second)

		// this simulates a message sent from a Grain to an actor
		response, err = node1.AskGrain(ctx, identity, new(testpb.TestPing), time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.TestPong{}, response)

		err = node2.TellGrain(ctx, identity, new(testpb.TestBye))
		require.NoError(t, err)

		pause.For(time.Second)

		exist, err := node3.ActorExists(ctx, "Actor20")
		require.NoError(t, err)
		require.False(t, exist)

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With Grain to Grain messaging", func(t *testing.T) {
		ctx := t.Context()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start a system cluster
		node1, sd1 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start a system cluster
		node2, sd2 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create and start a system cluster
		node3, sd3 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		identity, err := node1.GrainIdentity(ctx, "Grain1", func(_ context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// check if the grain is activated
		gp, ok := node1.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		// wait for cluster synchronization
		pause.For(time.Second)

		// send a message to the grain
		message := new(testpb.TestMessage)
		response, err := node2.AskGrain(ctx, identity, message, time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		pause.For(600 * time.Millisecond)

		// send a message to the grain
		err = node3.TellGrain(ctx, identity, new(testpb.TestReady))
		require.NoError(t, err)

		pause.For(600 * time.Millisecond)

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
}
