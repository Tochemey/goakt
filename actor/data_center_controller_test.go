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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/log"
)

func TestDataCenterReady(t *testing.T) {
	t.Run("returns true when multi-DC is not enabled", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		err = actorSystem.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, actorSystem.Stop(ctx)) })

		// Multi-DC not configured, should return true (nothing to wait for)
		assert.True(t, actorSystem.DataCenterReady())
	})

	t.Run("returns true when actor system not started and multi-DC not enabled", func(t *testing.T) {
		logger := log.DiscardLogger

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		// Multi-DC not configured, should return true even before start
		assert.True(t, actorSystem.DataCenterReady())
	})
}

func TestDataCenterLastRefresh(t *testing.T) {
	t.Run("returns zero time when multi-DC is not enabled", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		err = actorSystem.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, actorSystem.Stop(ctx)) })

		// Multi-DC not configured, should return zero time
		assert.True(t, actorSystem.DataCenterLastRefresh().IsZero())
	})

	t.Run("returns zero time when actor system not started", func(t *testing.T) {
		logger := log.DiscardLogger

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		// Multi-DC not configured, should return zero time
		assert.True(t, actorSystem.DataCenterLastRefresh().IsZero())
	})
}
