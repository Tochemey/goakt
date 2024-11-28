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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testkit "github.com/tochemey/goakt/v2/mocks/discovery"
)

func TestClusterConfig(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		disco := new(testkit.Provider)
		exchanger := new(exchanger)
		tester := new(actorQA)
		kinds := []Actor{tester, exchanger}
		config := NewClusterConfig().
			WithKinds(kinds...).
			WithDiscoveryPort(3220).
			WithPeersPort(3222).
			WithMinimumPeersQuorum(1).
			WithReplicaCount(1).
			WithPartitionCount(3).
			WithDiscovery(disco)

		require.NoError(t, config.Validate())
		assert.EqualValues(t, 3220, config.DiscoveryPort())
		assert.EqualValues(t, 3222, config.PeersPort())
		assert.EqualValues(t, 1, config.MinimumPeersQuorum())
		assert.EqualValues(t, 1, config.ReplicaCount())
		assert.EqualValues(t, 3, config.PartitionCount())
		assert.True(t, disco == config.Discovery())
		assert.Len(t, config.Kinds(), 3)
	})

	t.Run("With invalid config setting", func(t *testing.T) {
		config := NewClusterConfig().
			WithKinds(new(exchanger), new(actorQA)).
			WithDiscoveryPort(3220).
			WithPeersPort(3222).
			WithMinimumPeersQuorum(1).
			WithReplicaCount(1).
			WithPartitionCount(0). // invalid partition count
			WithDiscovery(new(testkit.Provider))

		assert.Error(t, config.Validate())
	})
}
