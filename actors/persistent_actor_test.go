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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestPersistentActor(t *testing.T) {
	t.Run("With PersistResponse", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemoting("127.0.0.1", int32(ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		lib.Pause(time.Second)

		durableStore := newMockStateStore()
		mockPersistentActor := newMockPersistentActor()
		actorName := "mockPersistentActor"
		pid, err := actorSystem.SpawnPersistentActor(ctx, actorName, mockPersistentActor, durableStore)
		require.NoError(t, err)
		require.NotNil(t, pid)
		lib.Pause(time.Second)

		actor := &mockForwardActor{}
		to, err := actorSystem.Spawn(ctx, "forwarder", actor)
		require.NoError(t, err)
		require.NotNil(t, to)
		lib.Pause(time.Second)

		// test state command response
		actorState, err := durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)

		// let us persist some state
		require.NoError(t, to.Tell(ctx, pid, &testpb.TestStateCommandResponse{}))
		lib.Pause(time.Second)

		actorState, err = durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.EqualValues(t, 1, actorState.Version)
		require.Equal(t, pid.ID(), actorState.ActorID)

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With NoOpResponse", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemoting("127.0.0.1", int32(ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		lib.Pause(time.Second)

		durableStore := newMockStateStore()
		mockPersistentActor := newMockPersistentActor()
		actorName := "mockPersistentActor"
		pid, err := actorSystem.SpawnPersistentActor(ctx, actorName, mockPersistentActor, durableStore)
		require.NoError(t, err)
		require.NotNil(t, pid)
		lib.Pause(time.Second)

		actor := &mockForwardActor{}
		to, err := actorSystem.Spawn(ctx, "forwarder", actor)
		require.NoError(t, err)
		require.NotNil(t, to)
		lib.Pause(time.Second)

		// test state command response
		actorState, err := durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)

		// let us persist some state
		require.NoError(t, to.Tell(ctx, pid, &testpb.TestNoStateCommandResponse{}))
		lib.Pause(time.Second)

		actorState, err = durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)
		require.Equal(t, pid.ID(), actorState.ActorID)

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With PersistAndForwardResponse", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemoting("127.0.0.1", int32(ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		lib.Pause(time.Second)

		durableStore := newMockStateStore()
		mockPersistentActor := newMockPersistentActor()
		actorName := "mockPersistentActor"
		pid, err := actorSystem.SpawnPersistentActor(ctx, actorName, mockPersistentActor, durableStore)
		require.NoError(t, err)
		require.NotNil(t, pid)
		lib.Pause(time.Second)

		actor := &mockForwardActor{}
		to, err := actorSystem.Spawn(ctx, "forwarder", actor)
		require.NoError(t, err)
		require.NotNil(t, to)
		lib.Pause(time.Second)

		// test state command response
		actorState, err := durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)

		// let us persist some state
		require.NoError(t, to.Tell(ctx, pid, &testpb.TestStateCommandResponseWithForward{
			Recipient: "forwarder",
		}))
		lib.Pause(time.Second)

		actorState, err = durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.EqualValues(t, 1, actorState.Version)
		require.Equal(t, pid.ID(), actorState.ActorID)

		require.EqualValues(t, 1, to.ProcessedCount()-1)

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With PersistAndForwardResponse error suspends the actor", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemoting("127.0.0.1", int32(ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		lib.Pause(time.Second)

		durableStore := newMockStateStore()
		mockPersistentActor := newMockPersistentActor()
		actorName := "mockPersistentActor"
		pid, err := actorSystem.SpawnPersistentActor(ctx, actorName, mockPersistentActor, durableStore)
		require.NoError(t, err)
		require.NotNil(t, pid)
		lib.Pause(time.Second)

		actor := &mockForwardActor{}
		to, err := actorSystem.Spawn(ctx, "forwarder", actor)
		require.NoError(t, err)
		require.NotNil(t, to)
		lib.Pause(time.Second)

		// test state command response
		actorState, err := durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)

		// let us persist some state
		require.NoError(t, to.Tell(ctx, pid, &testpb.TestStateCommandResponseWithForward{
			Recipient: "notfound",
		}))
		lib.Pause(time.Second)

		actorState, err = durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.EqualValues(t, 1, actorState.Version)
		require.Equal(t, pid.ID(), actorState.ActorID)

		require.True(t, pid.IsSuspended())

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With PersistResponse wrong versioning requirement suspends the actor", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemoting("127.0.0.1", int32(ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		lib.Pause(time.Second)

		durableStore := newMockStateStore()
		mockPersistentActor := newMockPersistentActor()
		actorName := "mockPersistentActor"
		pid, err := actorSystem.SpawnPersistentActor(ctx, actorName, mockPersistentActor, durableStore)
		require.NoError(t, err)
		require.NotNil(t, pid)
		lib.Pause(time.Second)

		actor := &mockForwardActor{}
		to, err := actorSystem.Spawn(ctx, "forwarder", actor)
		require.NoError(t, err)
		require.NotNil(t, to)
		lib.Pause(time.Second)

		// test state command response
		actorState, err := durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)

		// let us persist some state
		require.NoError(t, to.Tell(ctx, pid, &testpb.TestStateCommandResponseWithInvalidVersion{}))
		lib.Pause(time.Second)

		actorState, err = durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)
		require.Equal(t, pid.ID(), actorState.ActorID)

		require.True(t, pid.IsSuspended())

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With ForwardResponse", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemoting("127.0.0.1", int32(ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		lib.Pause(time.Second)

		durableStore := newMockStateStore()
		mockPersistentActor := newMockPersistentActor()
		actorName := "mockPersistentActor"
		pid, err := actorSystem.SpawnPersistentActor(ctx, actorName, mockPersistentActor, durableStore)
		require.NoError(t, err)
		require.NotNil(t, pid)
		lib.Pause(time.Second)

		actor := &mockForwardActor{}
		to, err := actorSystem.Spawn(ctx, "forwarder", actor)
		require.NoError(t, err)
		require.NotNil(t, to)
		lib.Pause(time.Second)

		// test state command response
		actorState, err := durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)

		// let us persist some state
		require.NoError(t, to.Tell(ctx, pid, &testpb.TestForwardCommandResponse{
			Recipient: "forwarder",
		}))
		lib.Pause(time.Second)

		actorState, err = durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)
		require.Equal(t, pid.ID(), actorState.ActorID)

		require.EqualValues(t, 1, to.ProcessedCount()-1)

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With ErrorResponse suspend the actor", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemoting("127.0.0.1", int32(ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		lib.Pause(time.Second)

		durableStore := newMockStateStore()
		mockPersistentActor := newMockPersistentActor()
		actorName := "mockPersistentActor"
		pid, err := actorSystem.SpawnPersistentActor(ctx, actorName, mockPersistentActor, durableStore)
		require.NoError(t, err)
		require.NotNil(t, pid)
		lib.Pause(time.Second)

		actor := &mockForwardActor{}
		to, err := actorSystem.Spawn(ctx, "forwarder", actor)
		require.NoError(t, err)
		require.NotNil(t, to)
		lib.Pause(time.Second)

		// test state command response
		actorState, err := durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)

		// let us persist some state
		require.NoError(t, to.Tell(ctx, pid, &testpb.TestErrorCommandResponse{
			ErrorMessage: "error",
		}))
		lib.Pause(time.Second)

		actorState, err = durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)
		require.Equal(t, pid.ID(), actorState.ActorID)

		require.True(t, pid.IsSuspended())

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With ForwardResponse failure suspends the actor", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)
		actorSystem, err := NewActorSystem("sys",
			WithRemoting("127.0.0.1", int32(ports[0])),
			WithLogger(log.DiscardLogger))

		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		lib.Pause(time.Second)

		durableStore := newMockStateStore()
		mockPersistentActor := newMockPersistentActor()
		actorName := "mockPersistentActor"
		pid, err := actorSystem.SpawnPersistentActor(ctx, actorName, mockPersistentActor, durableStore)
		require.NoError(t, err)
		require.NotNil(t, pid)
		lib.Pause(time.Second)

		actor := &mockForwardActor{}
		to, err := actorSystem.Spawn(ctx, "forwarder", actor)
		require.NoError(t, err)
		require.NotNil(t, to)
		lib.Pause(time.Second)

		// test state command response
		actorState, err := durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)

		// let us persist some state
		require.NoError(t, to.Tell(ctx, pid, &testpb.TestForwardCommandResponse{
			Recipient: "notfound",
		}))
		lib.Pause(time.Second)

		actorState, err = durableStore.GetState(ctx, pid.ID())
		require.NoError(t, err)
		require.NotNil(t, actorState)
		require.Zero(t, actorState.Version)
		require.Equal(t, pid.ID(), actorState.ActorID)

		require.True(t, pid.IsSuspended())

		assert.NoError(t, actorSystem.Stop(ctx))
	})
}
