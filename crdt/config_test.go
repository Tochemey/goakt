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

package crdt

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		c := NewConfig()
		require.NotNil(t, c)
		assert.Equal(t, 30*time.Second, c.AntiEntropyInterval())
		assert.Equal(t, 64*1024, c.MaxDeltaSize())
		assert.Equal(t, 5*time.Minute, c.PruneInterval())
		assert.Equal(t, 24*time.Hour, c.TombstoneTTL())
		assert.Empty(t, c.Role())
		assert.Equal(t, 5*time.Second, c.CoordinationTimeout())
		assert.Equal(t, time.Duration(0), c.SnapshotInterval())
		assert.Empty(t, c.SnapshotDir())
	})

	t.Run("with anti-entropy interval", func(t *testing.T) {
		c := NewConfig(WithAntiEntropyInterval(10 * time.Second))
		assert.Equal(t, 10*time.Second, c.AntiEntropyInterval())
	})

	t.Run("with max delta size", func(t *testing.T) {
		c := NewConfig(WithMaxDeltaSize(128 * 1024))
		assert.Equal(t, 128*1024, c.MaxDeltaSize())
	})

	t.Run("with prune interval", func(t *testing.T) {
		c := NewConfig(WithPruneInterval(10 * time.Minute))
		assert.Equal(t, 10*time.Minute, c.PruneInterval())
	})

	t.Run("with tombstone TTL", func(t *testing.T) {
		c := NewConfig(WithTombstoneTTL(48 * time.Hour))
		assert.Equal(t, 48*time.Hour, c.TombstoneTTL())
	})

	t.Run("with role", func(t *testing.T) {
		c := NewConfig(WithRole("cache-nodes"))
		assert.Equal(t, "cache-nodes", c.Role())
	})

	t.Run("with coordination timeout", func(t *testing.T) {
		c := NewConfig(WithCoordinationTimeout(10 * time.Second))
		assert.Equal(t, 10*time.Second, c.CoordinationTimeout())
	})

	t.Run("with snapshot interval", func(t *testing.T) {
		c := NewConfig(WithSnapshotInterval(1 * time.Minute))
		assert.Equal(t, 1*time.Minute, c.SnapshotInterval())
	})

	t.Run("with snapshot dir", func(t *testing.T) {
		c := NewConfig(WithSnapshotDir("/tmp/crdt-snapshots"))
		assert.Equal(t, "/tmp/crdt-snapshots", c.SnapshotDir())
	})

	t.Run("multiple options", func(t *testing.T) {
		c := NewConfig(
			WithAntiEntropyInterval(15*time.Second),
			WithMaxDeltaSize(32*1024),
			WithRole("stateful"),
			WithCoordinationTimeout(3*time.Second),
			WithSnapshotInterval(2*time.Minute),
			WithSnapshotDir("/data/snapshots"),
		)
		assert.Equal(t, 15*time.Second, c.AntiEntropyInterval())
		assert.Equal(t, 32*1024, c.MaxDeltaSize())
		assert.Equal(t, "stateful", c.Role())
		assert.Equal(t, defaultPruneInterval, c.PruneInterval())
		assert.Equal(t, defaultTombstoneTTL, c.TombstoneTTL())
		assert.Equal(t, 3*time.Second, c.CoordinationTimeout())
		assert.Equal(t, 2*time.Minute, c.SnapshotInterval())
		assert.Equal(t, "/data/snapshots", c.SnapshotDir())
	})
}
