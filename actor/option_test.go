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
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/hash"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	sup "github.com/tochemey/goakt/v4/supervisor"
	gtls "github.com/tochemey/goakt/v4/tls"
)

func TestOption(t *testing.T) {
	clusterConfig := NewClusterConfig()
	hasher := hash.DefaultHasher()
	// nolint
	tlsConfig := &tls.Config{}
	tlsInfo := &gtls.Info{
		ClientConfig: tlsConfig,
		ServerConfig: tlsConfig,
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
			name:   "WithLogger_nil_uses_discard",
			option: WithLogger(nil),
			check: func(t *testing.T, sys *actorSystem) {
				assert.Equal(t, log.DiscardLogger, sys.logger)
			},
		},
		{
			name:   "WithLoggingDisabled",
			option: WithLoggingDisabled(),
			check: func(t *testing.T, sys *actorSystem) {
				assert.Equal(t, log.DiscardLogger, sys.logger)
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
				assert.Equal(t, tlsConfig, sys.tlsInfo.ServerConfig)
				assert.Equal(t, tlsConfig, sys.tlsInfo.ClientConfig)
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

func TestWithMetrics(t *testing.T) {
	system := new(actorSystem)
	opt := WithMetrics()
	opt.Apply(system)

	assert.NotNil(t, system.metricProvider)
}

func TestWithDefaultSupervisor(t *testing.T) {
	t.Run("When supervisor is nil", func(t *testing.T) {
		system := new(actorSystem)
		existing := sup.NewSupervisor(sup.WithStrategy(sup.OneForAllStrategy))
		system.defaultSupervisor = existing

		opt := WithDefaultSupervisor(nil)
		opt.Apply(system)

		assert.Same(t, existing, system.defaultSupervisor)
	})

	t.Run("When supervisor is not nil", func(t *testing.T) {
		system := new(actorSystem)
		custom := sup.NewSupervisor(sup.WithStrategy(sup.OneForAllStrategy))

		opt := WithDefaultSupervisor(custom)
		opt.Apply(system)

		assert.Same(t, custom, system.defaultSupervisor)
	})
}
