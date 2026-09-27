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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/log"
)

func TestQualifiedName(t *testing.T) {
	t.Run("single name is a top-level actor's qualified name", func(t *testing.T) {
		name, err := QualifiedName("orders")
		require.NoError(t, err)
		assert.Equal(t, "orders", name)
	})

	t.Run("names are joined from the root down", func(t *testing.T) {
		name, err := QualifiedName("orders", "cart", "item")
		require.NoError(t, err)
		assert.Equal(t, "orders/cart/item", name)
	})

	t.Run("no names is rejected", func(t *testing.T) {
		name, err := QualifiedName()
		require.ErrorIs(t, err, gerrors.ErrInvalidActorName)
		assert.Empty(t, name)
	})

	t.Run("an empty name is rejected", func(t *testing.T) {
		name, err := QualifiedName("orders", "")
		require.ErrorIs(t, err, gerrors.ErrInvalidActorName)
		assert.Empty(t, name)
	})

	t.Run("a name containing the separator is rejected", func(t *testing.T) {
		name, err := QualifiedName("orders/cart", "item")
		require.ErrorIs(t, err, gerrors.ErrInvalidActorName)
		assert.Empty(t, name)
	})

	t.Run("a name reserved for system actors is rejected", func(t *testing.T) {
		name, err := QualifiedName("orders", reservedNamesPrefix+"worker")
		require.ErrorIs(t, err, gerrors.ErrReservedName)
		assert.Empty(t, name)
	})

	t.Run("a name that is too long is rejected", func(t *testing.T) {
		name, err := QualifiedName(strings.Repeat("a", 256))
		require.ErrorIs(t, err, gerrors.ErrInvalidActorName)
		assert.Empty(t, name)
	})

	t.Run("matches the running actor's path and resolves it through ActorOf", func(t *testing.T) {
		ctx := context.TODO()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, actorSystem.Start(ctx))

		parent, err := actorSystem.Spawn(ctx, "orders", NewMockActor())
		require.NoError(t, err)
		child, err := parent.SpawnChild(ctx, "worker", NewMockActor())
		require.NoError(t, err)
		grandchild, err := child.SpawnChild(ctx, "retry", NewMockActor())
		require.NoError(t, err)

		assert.Equal(t, "orders", parent.Path().QualifiedName())
		assert.Equal(t, "orders/worker", child.Path().QualifiedName())
		assert.Equal(t, "orders/worker/retry", grandchild.Path().QualifiedName())

		name, err := QualifiedName("orders", "worker", "retry")
		require.NoError(t, err)
		assert.Equal(t, grandchild.Path().QualifiedName(), name)

		resolved, err := actorSystem.ActorOf(ctx, name)
		require.NoError(t, err)
		assert.Equal(t, grandchild.Path().String(), resolved.Path().String())

		require.NoError(t, actorSystem.Stop(ctx))
	})
}
