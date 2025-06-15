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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/log"
	clustermock "github.com/tochemey/goakt/v3/mocks/cluster"
)

func TestDeathWatch(t *testing.T) {
	t.Run("With unhandled message", func(t *testing.T) {
		ctx := context.Background()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)

		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the system to start properly
		util.Pause(500 * time.Millisecond)

		// create a deadletter subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		pid := actorSystem.getDeathWatch()
		// send an unhandled message to the system guardian
		err = Tell(ctx, pid, new(anypb.Any))
		require.NoError(t, err)

		util.Pause(time.Second)

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*goaktpb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 1)
		consumer.Shutdown()
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("Shutdown system when RemoveActor call failed in cluster mode", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		actorID := "testID"

		// mock the cluster interface
		clmock := new(clustermock.Interface)
		clmock.EXPECT().RemoveActor(mock.Anything, mock.Anything).Return(assert.AnError)
		clmock.EXPECT().GetActor(mock.Anything, actorID).Return(nil, nil)

		err = sys.Start(ctx)
		require.NoError(t, err)

		// wait for the system to start properly
		util.Pause(500 * time.Millisecond)
		sys.(*actorSystem).cluster = clmock
		sys.(*actorSystem).clusterEnabled.Store(true)

		cid, err := sys.Spawn(ctx, actorID, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, cid)

		util.Pause(500 * time.Millisecond)

		// reset the actor system
		sys.(*actorSystem).cluster = nil
		sys.(*actorSystem).clusterEnabled.Store(false)
		pid := sys.getDeathWatch()

		// this is to simulate the fact that the actor is in a cluster
		pid.Actor().(*deathWatch).clusterEnabled = true
		pid.Actor().(*deathWatch).cluster = clmock

		require.NoError(t, cid.Shutdown(ctx))

		util.Pause(time.Second)

		require.False(t, pid.IsRunning())
		require.False(t, sys.Running())
		clmock.AssertExpectations(t)
	})
	t.Run("With Terminated when PID not found return no error", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		actorID := "testID"

		err = sys.Start(ctx)
		require.NoError(t, err)

		// wait for the system to start properly
		util.Pause(500 * time.Millisecond)
		pid := sys.getDeathWatch()

		err = Tell(ctx, pid, &goaktpb.Terminated{ActorId: actorID})
		require.NoError(t, err)

		util.Pause(time.Second)
		require.NoError(t, pid.Shutdown(ctx))
	})
}
