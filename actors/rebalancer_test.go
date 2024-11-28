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

package actors

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestRebalancing(t *testing.T) {
	t.Run("With successful actors redeployment", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := startClusterSystem(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := startClusterSystem(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := startClusterSystem(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		// let us create 4 actors on each node
		for j := 1; j <= 4; j++ {
			actorName := fmt.Sprintf("Node1-Actor-%d", j)
			pid, err := node1.Spawn(ctx, actorName, newActor())
			require.NoError(t, err)
			require.NotNil(t, pid)
		}

		lib.Pause(time.Second)

		for j := 1; j <= 4; j++ {
			actorName := fmt.Sprintf("Node2-Actor-%d", j)
			pid, err := node2.Spawn(ctx, actorName, newActor())
			require.NoError(t, err)
			require.NotNil(t, pid)
		}

		lib.Pause(time.Second)

		for j := 1; j <= 4; j++ {
			actorName := fmt.Sprintf("Node3-Actor-%d", j)
			pid, err := node3.Spawn(ctx, actorName, newActor())
			require.NoError(t, err)
			require.NotNil(t, pid)
		}

		lib.Pause(time.Second)

		// take down node2
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, sd2.Close())

		// Wait for cluster rebalancing
		lib.Pause(time.Minute)

		// let us access some of the node2 actors from node 1 and  node 3
		actorName := "Node2-Actor-1"

		sender, err := node1.LocalActor("Node1-Actor-1")
		require.NoError(t, err)
		require.NotNil(t, sender)

		err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
		require.NoError(t, err)

		t.Cleanup(func() {
			assert.NoError(t, node1.Stop(ctx))
			assert.NoError(t, node3.Stop(ctx))
			assert.NoError(t, sd1.Close())
			assert.NoError(t, sd3.Close())
			srv.Shutdown()
		})
	})
}
