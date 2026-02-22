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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/log"
)

func TestContext(t *testing.T) {
	ctx := context.Background()

	actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	c := newContext(ctx, "name", actorSystem)
	require.NotNil(t, c.ActorSystem())
	require.Equal(t, "testSys", c.ActorSystem().Name())
	pids, err := c.ActorSystem().Actors(ctx, time.Second)
	require.NoError(t, err)
	require.Empty(t, pids)
	require.NotNil(t, c.ActorSystem().Logger())
	require.Empty(t, c.Extensions())
	require.Nil(t, c.Extension("extensionID"))
	require.Empty(t, c.Dependencies())
	require.Nil(t, c.Dependency("extensionID"))
	require.NotNil(t, c.Logger())

	dl, ok := c.Context().Deadline()
	require.False(t, ok)
	require.Zero(t, dl)
	require.Equal(t, "name", c.ActorName())

	done := c.Context().Done()
	require.Nil(t, done)
	require.NoError(t, c.Context().Err())
}

func TestContextWithDependencies(t *testing.T) {
	ctx := context.Background()

	actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	dep1 := NewMockDependency("dep-1", "alice", "alice@example.com")
	dep2 := NewMockDependency("dep-2", "bob", "bob@example.com")

	c := newContext(ctx, "myActor", actorSystem, dep1, dep2)

	t.Run("Dependencies returns all injected dependencies", func(t *testing.T) {
		deps := c.Dependencies()
		require.Len(t, deps, 2)
	})

	t.Run("Dependency returns the matching dependency by ID", func(t *testing.T) {
		got := c.Dependency("dep-1")
		require.NotNil(t, got)
		assert.Equal(t, "dep-1", got.ID())

		got2 := c.Dependency("dep-2")
		require.NotNil(t, got2)
		assert.Equal(t, "dep-2", got2.ID())
	})

	t.Run("Dependency returns nil for an unknown ID", func(t *testing.T) {
		assert.Nil(t, c.Dependency("unknown"))
	})
}

func TestContextWithExtension(t *testing.T) {
	ctx := context.Background()

	ext := NewMockExtension()
	actorSystem, err := NewActorSystem("testSys",
		WithLogger(log.DiscardLogger),
		WithExtensions(ext),
	)
	require.NoError(t, err)

	c := newContext(ctx, "myActor", actorSystem)

	t.Run("Extensions returns all registered extensions", func(t *testing.T) {
		exts := c.Extensions()
		require.Len(t, exts, 1)
	})

	t.Run("Extension returns the matching extension by ID", func(t *testing.T) {
		got := c.Extension("MockStateStore")
		require.NotNil(t, got)
		assert.Equal(t, "MockStateStore", got.ID())
	})

	t.Run("Extension returns nil for an unknown ID", func(t *testing.T) {
		assert.Nil(t, c.Extension("unknown-ext"))
	})
}
