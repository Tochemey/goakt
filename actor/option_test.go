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
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/hash"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
)

func TestOption(t *testing.T) {
	clusterConfig := NewClusterConfig()
	hasher := hash.DefaultHasher()
	// nolint
	tlsConfig := &tls.Config{}
	tlsInfo := &TLSInfo{
		ClientTLS: tlsConfig,
		ServerTLS: tlsConfig,
	}
	remoteConfig := remote.DefaultConfig()

	testCases := []struct {
		name   string
		option Option
		check  func(t *testing.T, sys *actorSystem)
	}{
		{
			name:   "WithActorInitMaxRetries",
			option: WithActorInitMaxRetries(2),
			check: func(t *testing.T, sys *actorSystem) {
				assert.Equal(t, 2, sys.actorInitMaxRetries)
			},
		},
		{
			name:   "WithLogger",
			option: WithLogger(log.DebugLogger),
			check: func(t *testing.T, sys *actorSystem) {
				assert.Equal(t, log.DebugLogger, sys.logger)
			},
		},
		{
			name:   "WithShutdownTimeout",
			option: WithShutdownTimeout(2 * time.Second),
			check: func(t *testing.T, sys *actorSystem) {
				assert.Equal(t, 2*time.Second, sys.shutdownTimeout)
			},
		},
		{
			name:   "WithPartitionHasher",
			option: WithPartitionHasher(hasher),
			check: func(t *testing.T, sys *actorSystem) {
				assert.Equal(t, hasher, sys.partitionHasher)
			},
		},
		{
			name:   "WithActorInitTimeout",
			option: WithActorInitTimeout(2 * time.Second),
			check: func(t *testing.T, sys *actorSystem) {
				assert.Equal(t, 2*time.Second, sys.actorInitTimeout)
			},
		},
		{
			name:   "WithCluster",
			option: WithCluster(clusterConfig),
			check: func(t *testing.T, sys *actorSystem) {
				assert.True(t, sys.clusterEnabled.Load())
				assert.Equal(t, clusterConfig, sys.clusterConfig)
			},
		},
		{
			name:   "WithTLS",
			option: WithTLS(tlsInfo),
			check: func(t *testing.T, sys *actorSystem) {
				assert.Equal(t, tlsConfig, sys.serverTLS)
				assert.Equal(t, tlsConfig, sys.clientTLS)
			},
		},
		{
			name:   "WithRemote",
			option: WithRemote(remoteConfig),
			check: func(t *testing.T, sys *actorSystem) {
				assert.True(t, sys.remotingEnabled.Load())
				assert.Equal(t, remoteConfig, sys.remoteConfig)
			},
		},
		{
			name:   "WithoutRelocation",
			option: WithoutRelocation(),
			check: func(t *testing.T, sys *actorSystem) {
				assert.False(t, sys.relocationEnabled.Load())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var system actorSystem
			tc.option.Apply(&system)
			tc.check(t, &system)
		})
	}
}

func TestWithPeerStateInterval(t *testing.T) {
	system := new(actorSystem)
	system.clusterConfig = NewClusterConfig()
	opt := WithPeerStateLoopInterval(10 * time.Second)
	opt.Apply(system)
	assert.EqualValues(t, 10*time.Second, system.clusterConfig.PeersStateSyncInterval())
}

func TestWithCoordinatedShutdown(t *testing.T) {
	system := new(actorSystem)
	opt := WithCoordinatedShutdown(&MockShutdownHook{})
	opt.Apply(system)
	assert.EqualValues(t, 1, len(system.shutdownHooks))
}

func TestWithPubSub(t *testing.T) {
	system := new(actorSystem)
	opt := WithPubSub()
	opt.Apply(system)
	assert.True(t, system.pubsubEnabled.Load())
}

func TestWithExtensions(t *testing.T) {
	ext := new(MockExtension)
	system := new(actorSystem)
	opt := WithExtensions(ext)
	opt.Apply(system)
	require.NotEmpty(t, system.extensions)
}

func TestWithEvictionStrategy(t *testing.T) {
	t.Run("When strategy is nil", func(t *testing.T) {
		system := new(actorSystem)
		opt := WithEvictionStrategy(nil, time.Second)
		opt.Apply(system)

		assert.Nil(t, system.evictionStrategy)
	})
	t.Run("When strategy is not nil", func(t *testing.T) {
		system := new(actorSystem)
		strategy, err := NewEvictionStrategy(10, LRU, 1)
		require.NoError(t, err)
		opt := WithEvictionStrategy(strategy, time.Second)
		opt.Apply(system)

		assert.Equal(t, strategy, system.evictionStrategy)
		assert.EqualValues(t, 10, system.evictionStrategy.Limit())
		assert.Equal(t, LRU, system.evictionStrategy.Policy())
	})
}
