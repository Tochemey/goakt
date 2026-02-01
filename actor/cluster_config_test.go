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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/datacenter"
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
			WithGrainActivationBarrier(5*time.Second).
			WithClusterBalancerInterval(5*time.Second).
			WithDiscovery(provider).
			WithDataCenter(func() *datacenter.Config {
				dc := datacenter.NewConfig()
				dc.ControlPlane = &MockControlPlane{}
				dc.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
				dc.Endpoints = []string{"127.0.0.1:8080"}
				return dc
			}()).
			WithRoles("role1", "role2", "role1") // role1 is duplicated to test deduplication

		require.NoError(t, config.Validate())
		assert.EqualValues(t, 3220, config.discoveryPort)
		assert.EqualValues(t, 3222, config.peersPort)
		assert.EqualValues(t, 1, config.minimumPeersQuorum)
		assert.EqualValues(t, 1, config.replicaCount)
		assert.EqualValues(t, 1, config.readQuorum)
		assert.EqualValues(t, 1, config.writeQuorum)
		assert.EqualValues(t, 3, config.partitionCount)
		assert.Equal(t, 10*time.Second, config.writeTimeout)
		assert.Equal(t, 10*time.Second, config.readTimeout)
		assert.Equal(t, 10*time.Second, config.shutdownTimeout)
		assert.Equal(t, 10*time.Second, config.bootstrapTimeout)
		assert.Equal(t, 10*time.Second, config.clusterStateSyncInterval)
		assert.True(t, config.grainActivationBarrierEnabled())
		assert.Equal(t, 5*time.Second, config.grainActivationBarrierTimeout())
		assert.ElementsMatch(t, []string{"role1", "role2"}, config.getRoles())
		assert.NotNil(t, config.dataCenterConfig)

		assert.Exactly(t, uint64(10*size.MB), config.tableSize)
		assert.True(t, provider == config.discovery)
		assert.Len(t, config.kinds.Values(), 3)
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
		assert.EqualValues(t, 3220, config.discoveryPort)
		assert.EqualValues(t, 3222, config.peersPort)
		assert.EqualValues(t, 1, config.minimumPeersQuorum)
		assert.EqualValues(t, 1, config.replicaCount)
		assert.EqualValues(t, 1, config.readQuorum)
		assert.EqualValues(t, 1, config.writeQuorum)
		assert.EqualValues(t, 3, config.partitionCount)
		assert.Equal(t, 10*time.Second, config.writeTimeout)
		assert.Equal(t, 10*time.Second, config.readTimeout)
		assert.Equal(t, 10*time.Second, config.shutdownTimeout)
		assert.Equal(t, 10*time.Second, config.bootstrapTimeout)
		assert.Equal(t, 10*time.Second, config.clusterStateSyncInterval)

		assert.Exactly(t, uint64(10*size.MB), config.tableSize)
		assert.True(t, provider == config.discovery)
		assert.Len(t, config.grains.Values(), 1)
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

	t.Run("With grain activation barrier invalid timeout", func(t *testing.T) {
		config := NewClusterConfig().
			WithKinds(new(exchanger), new(MockActor)).
			WithDiscoveryPort(3220).
			WithPeersPort(3222).
			WithMinimumPeersQuorum(1).
			WithReplicaCount(1).
			WithPartitionCount(3).
			WithGrainActivationBarrier(-1 * time.Second).
			WithDiscovery(new(testkit.Provider))

		assert.Error(t, config.Validate())
	})

	t.Run("With grain activation barrier unset returns zero timeout", func(t *testing.T) {
		config := NewClusterConfig()

		assert.False(t, config.grainActivationBarrierEnabled())
		assert.Zero(t, config.grainActivationBarrierTimeout())
	})
}
