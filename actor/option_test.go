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
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/hash"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
)

func TestOption(t *testing.T) {
	var atomicTrue atomic.Bool
	var atomicFalse atomic.Bool
	atomicFalse.Store(false)
	atomicTrue.Store(true)
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
		name     string
		option   Option
		expected actorSystem
	}{
		{
			name:     "WithActorInitMaxRetries",
			option:   WithActorInitMaxRetries(2),
			expected: actorSystem{actorInitMaxRetries: 2},
		},
		{
			name:     "WithLogger",
			option:   WithLogger(log.DefaultLogger),
			expected: actorSystem{logger: log.DefaultLogger},
		},
		{
			name:     "WithShutdownTimeout",
			option:   WithShutdownTimeout(2 * time.Second),
			expected: actorSystem{shutdownTimeout: 2. * time.Second},
		},
		{
			name:     "WithPartitionHasher",
			option:   WithPartitionHasher(hasher),
			expected: actorSystem{partitionHasher: hasher},
		},
		{
			name:     "WithActorInitTimeout",
			option:   WithActorInitTimeout(2 * time.Second),
			expected: actorSystem{actorInitTimeout: 2. * time.Second},
		},
		{
			name:     "WithCluster",
			option:   WithCluster(clusterConfig),
			expected: actorSystem{clusterEnabled: atomicTrue, clusterConfig: clusterConfig},
		},
		{
			name:     "WithPeerStateLoopInterval",
			option:   WithPeerStateLoopInterval(2 * time.Second),
			expected: actorSystem{peersStateLoopInterval: 2. * time.Second},
		},
		{
			name:     "WithTLS",
			option:   WithTLS(tlsInfo),
			expected: actorSystem{serverTLS: tlsConfig, clientTLS: tlsConfig},
		},
		{
			name:     "WithRemote",
			option:   WithRemote(remoteConfig),
			expected: actorSystem{remotingEnabled: atomicTrue, remoteConfig: remoteConfig},
		},
		{
			name:     "WithoutRelocation",
			option:   WithoutRelocation(),
			expected: actorSystem{enableRelocation: atomicFalse},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var system actorSystem
			tc.option.Apply(&system)
			assert.Equal(t, tc.expected, system)
		})
	}
}

func TestWithCoordinatedShutdown(t *testing.T) {
	system := new(actorSystem)
	shutdownHook := func(context.Context) error { return nil }
	opt := WithCoordinatedShutdown(shutdownHook)
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
	ext := new(mockExtension)
	system := new(actorSystem)
	opt := WithExtensions(ext)
	opt.Apply(system)
	require.NotEmpty(t, system.extensions)
}
