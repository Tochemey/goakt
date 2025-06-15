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

package actor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestGrain(t *testing.T) {
	t.Run("With without cluster mode", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		err = testSystem.RegisterGrains(grain)
		require.NoError(t, err)

		// get the grain identity
		identity := NewIdentity(grain, "testGrain")
		require.NotNil(t, identity)

		// prepare a message to send to the grain
		message := new(testpb.TestSend)
		response, err := testSystem.Send(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		response, err = testSystem.Send(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		// let us shutdown the grain by sending PoisonPill
		response, err = testSystem.Send(ctx, identity, new(goaktpb.PoisonPill))
		require.NoError(t, err)
		require.Nil(t, response)

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.False(t, gp.isRunning())

		// send a message to the grain to reactivate it
		response, err = testSystem.Send(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With cluster mode", func(t *testing.T) {
		ctx := t.Context()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start a system cluster
		node1, sd1 := testCluster(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start a system cluster
		node2, sd2 := testCluster(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create and start a system cluster
		node3, sd3 := testCluster(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		// create a grain instance
		grain := NewMockGrain()
		identity := NewIdentity(grain, "testGrain")
		require.NotNil(t, identity)

		// prepare a message to send to the grain
		message := new(testpb.TestSend)
		response, err := node1.Send(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		util.Pause(time.Second)

		// check if the grain is activated
		gp, ok := node1.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		response, err = node2.Send(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		util.Pause(time.Second)

		// let us shutdown the grain by sending PoisonPill
		response, err = node3.Send(ctx, identity, new(goaktpb.PoisonPill))
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &goaktpb.NoMessage{}, response)

		util.Pause(time.Second)

		// check if the grain is activated
		gp, ok = node1.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.False(t, gp.isRunning())

		// send a message to the grain to reactivate it
		response, err = node3.Send(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		util.Pause(time.Second)

		// check if the grain is activated
		gp, ok = node1.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		assert.NoError(t, node1.Stop(ctx))
		assert.NoError(t, node3.Stop(ctx))
		assert.NoError(t, sd1.Close())
		assert.NoError(t, sd3.Close())
		srv.Shutdown()
	})
}
