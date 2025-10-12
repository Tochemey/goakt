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

	"github.com/tochemey/goakt/v3/internal/size"
	testkit "github.com/tochemey/goakt/v3/mocks/discovery"
)

func TestClusterConfig(t *testing.T) {
	t.Run("With happy path with Kinds", func(t *testing.T) {
		provider := new(testkit.Provider)
		exchanger := new(exchanger)
		tester := new(MockActor)
		kinds := []Actor{tester, exchanger}

		config := NewClusterConfig().
			WithKinds(kinds...).
			WithDiscoveryPort(3220).
			WithPeersPort(3222).
			WithMinimumPeersQuorum(1).
			WithReplicaCount(1).
			WithWriteQuorum(1).
			WithReadQuorum(1).
			WithPartitionCount(3).
			WithTableSize(10*size.MB).
			WithWriteTimeout(10*time.Second).
			WithShutdownTimeout(10*time.Second).
			WithReadTimeout(10*time.Second).
			WithBootstrapTimeout(10*time.Second).
			WithClusterStateSyncInterval(10*time.Second).
			WithDiscovery(provider).
			WithRoles("role1", "role2", "role1") // role1 is duplicated to test deduplication

		require.NoError(t, config.Validate())
		assert.EqualValues(t, 3220, config.DiscoveryPort())
		assert.EqualValues(t, 3222, config.PeersPort())
		assert.EqualValues(t, 1, config.MinimumPeersQuorum())
		assert.EqualValues(t, 1, config.ReplicaCount())
		assert.EqualValues(t, 1, config.ReadQuorum())
		assert.EqualValues(t, 1, config.WriteQuorum())
		assert.EqualValues(t, 3, config.PartitionCount())
		assert.Equal(t, 10*time.Second, config.WriteTimeout())
		assert.Equal(t, 10*time.Second, config.ReadTimeout())
		assert.Equal(t, 10*time.Second, config.ShutdownTimeout())
		assert.Equal(t, 10*time.Second, config.BootstrapTimeout())
		assert.Equal(t, 10*time.Second, config.ClusterStateSyncInterval())
		assert.ElementsMatch(t, []string{"role1", "role2"}, config.Roles())

		assert.Exactly(t, uint64(10*size.MB), config.TableSize())
		assert.True(t, provider == config.Discovery())
		assert.Len(t, config.Kinds(), 3)
	})
	t.Run("With happy path with Grains", func(t *testing.T) {
		provider := new(testkit.Provider)

		config := NewClusterConfig().
			WithGrains(new(MockGrain)).
			WithDiscoveryPort(3220).
			WithPeersPort(3222).
			WithMinimumPeersQuorum(1).
			WithReplicaCount(1).
			WithWriteQuorum(1).
			WithReadQuorum(1).
			WithPartitionCount(3).
			WithTableSize(10 * size.MB).
			WithWriteTimeout(10 * time.Second).
			WithShutdownTimeout(10 * time.Second).
			WithReadTimeout(10 * time.Second).
			WithBootstrapTimeout(10 * time.Second).
			WithClusterStateSyncInterval(10 * time.Second).
			WithDiscovery(provider)

		require.NoError(t, config.Validate())
		assert.EqualValues(t, 3220, config.DiscoveryPort())
		assert.EqualValues(t, 3222, config.PeersPort())
		assert.EqualValues(t, 1, config.MinimumPeersQuorum())
		assert.EqualValues(t, 1, config.ReplicaCount())
		assert.EqualValues(t, 1, config.ReadQuorum())
		assert.EqualValues(t, 1, config.WriteQuorum())
		assert.EqualValues(t, 3, config.PartitionCount())
		assert.Equal(t, 10*time.Second, config.WriteTimeout())
		assert.Equal(t, 10*time.Second, config.ReadTimeout())
		assert.Equal(t, 10*time.Second, config.ShutdownTimeout())
		assert.Equal(t, 10*time.Second, config.BootstrapTimeout())
		assert.Equal(t, 10*time.Second, config.ClusterStateSyncInterval())

		assert.Exactly(t, uint64(10*size.MB), config.TableSize())
		assert.True(t, provider == config.Discovery())
		assert.Len(t, config.Grains(), 1)
	})
	t.Run("With invalid config setting", func(t *testing.T) {
		config := NewClusterConfig().
			WithKinds(new(exchanger), new(MockActor)).
			WithDiscoveryPort(3220).
			WithPeersPort(3222).
			WithMinimumPeersQuorum(1).
			WithReplicaCount(1).
			WithPartitionCount(0). // invalid partition count
			WithDiscovery(new(testkit.Provider))

		assert.Error(t, config.Validate())
	})
}
