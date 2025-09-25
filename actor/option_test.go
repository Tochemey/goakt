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
	gtls "github.com/tochemey/goakt/v3/tls"
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
		{
			name:   "WithPublishStateTimeout",
			option: WithPublishStateTimeout(2 * time.Second),
			check: func(t *testing.T, sys *actorSystem) {
				assert.Equal(t, 2*time.Second, sys.publishStateTimeout)
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

// TestEffectivePublishStateTimeout validates the resolution/capping logic of
// (*actorSystem).effectivePublishStateTimeout().
func TestEffectivePublishStateTimeout(t *testing.T) {
	type fields struct {
		shutdown time.Duration
		publish  time.Duration
	}

	// helper reproducing the resolution logic for derived expectations
	deriveExpected := func(st, pt time.Duration) time.Duration {
		if st <= 0 {
			// No shutdown budget: just return the raw publish timeout (even if zero)
			return pt
		}
		if pt <= 0 {
			d := DefaultPublishStateTimeout
			if d <= 0 {
				d = st / 2
				if d == 0 {
					d = st
				}
			}
			if d > st {
				return st
			}
			return d
		}
		if pt > st {
			return st
		}
		return pt
	}

	now := time.Now() // just to avoid lint complaints about time import if unused

	_ = now // keep linter happy in case all durations are constants

	tests := []struct {
		name   string
		fields fields
	}{
		{
			name:   "no shutdown timeout, publish set",
			fields: fields{shutdown: 0, publish: 15 * time.Second},
		},
		{
			name:   "no shutdown timeout, publish zero (returns zero)",
			fields: fields{shutdown: 0, publish: 0},
		},
		{
			name:   "shutdown set, publish unset derives from default within cap",
			fields: fields{shutdown: 1 * time.Minute, publish: 0},
		},
		{
			name:   "shutdown smaller than default, publish unset -> capped to shutdown",
			fields: fields{shutdown: 30 * time.Second, publish: 0},
		},
		{
			name:   "publish greater than shutdown -> capped",
			fields: fields{shutdown: 1 * time.Minute, publish: 90 * time.Second},
		},
		{
			name:   "publish equals shutdown -> returns publish",
			fields: fields{shutdown: 1 * time.Minute, publish: 1 * time.Minute},
		},
		{
			name:   "publish less than shutdown -> returns publish",
			fields: fields{shutdown: 2 * time.Minute, publish: 30 * time.Second},
		},
		{
			name:   "negative publish timeout derives from default logic",
			fields: fields{shutdown: 45 * time.Second, publish: -1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sys := &actorSystem{
				shutdownTimeout:     tc.fields.shutdown,
				publishStateTimeout: tc.fields.publish,
			}
			got := sys.effectivePublishStateTimeout()
			want := deriveExpected(tc.fields.shutdown, tc.fields.publish)
			if got != want {
				t.Fatalf("effectivePublishStateTimeout() = %v, want %v (shutdown=%v publish=%v default=%v)",
					got, want, tc.fields.shutdown, tc.fields.publish, DefaultPublishStateTimeout)
			}
		})
	}
}
