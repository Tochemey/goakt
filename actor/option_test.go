/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v2/hash"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/telemetry"
)

func TestOption(t *testing.T) {
	resumeDirective := NewResumeDirective()
	tel := telemetry.New()
	var atomicTrue atomic.Bool
	atomicTrue.Store(true)
	clusterConfig := NewClusterConfig()
	hasher := hash.DefaultHasher()
	testCases := []struct {
		name     string
		option   Option
		expected ActorSystem
	}{
		{
			name:     "WithExpireActorAfter",
			option:   WithExpireActorAfter(2 * time.Second),
			expected: ActorSystem{expireActorAfter: 2. * time.Second},
		},
		{
			name:     "WithReplyTimeout",
			option:   WithAskTimeout(2 * time.Second),
			expected: ActorSystem{askTimeout: 2. * time.Second},
		},
		{
			name:     "WithActorInitMaxRetries",
			option:   WithActorInitMaxRetries(2),
			expected: ActorSystem{actorInitMaxRetries: 2},
		},
		{
			name:     "WithLogger",
			option:   WithLogger(log.DefaultLogger),
			expected: ActorSystem{logger: log.DefaultLogger},
		},
		{
			name:     "WithPassivationDisabled",
			option:   WithPassivationDisabled(),
			expected: ActorSystem{expireActorAfter: -1},
		},
		{
			name:     "WithSupervisorDirective",
			option:   WithSupervisorDirective(resumeDirective),
			expected: ActorSystem{supervisorDirective: resumeDirective},
		},
		{
			name:     "WithShutdownTimeout",
			option:   WithShutdownTimeout(2 * time.Second),
			expected: ActorSystem{shutdownTimeout: 2. * time.Second},
		},
		{
			name:     "WithStash",
			option:   WithStash(),
			expected: ActorSystem{stashEnabled: true},
		},
		{
			name:     "WithPartitionHasher",
			option:   WithPartitionHasher(hasher),
			expected: ActorSystem{partitionHasher: hasher},
		},
		{
			name:     "WithActorInitTimeout",
			option:   WithActorInitTimeout(2 * time.Second),
			expected: ActorSystem{actorInitTimeout: 2. * time.Second},
		},
		{
			name:     "WithMetric",
			option:   WithMetric(),
			expected: ActorSystem{metricEnabled: atomicTrue},
		},
		{
			name:     "WithCluster",
			option:   WithCluster(clusterConfig),
			expected: ActorSystem{clusterEnabled: atomicTrue, clusterConfig: clusterConfig},
		},
		{
			name:     "WithPeerStateLoopInterval",
			option:   WithPeerStateSyncInterval(2 * time.Second),
			expected: ActorSystem{peersStateSyncInterval: 2. * time.Second},
		},
		{
			name:     "WithJanitorInterval",
			option:   WithJanitorInterval(2 * time.Second),
			expected: ActorSystem{gcInterval: 2. * time.Second},
		},
		{
			name:     "WithTelemetry",
			option:   WithTelemetry(tel),
			expected: ActorSystem{telemetry: tel},
		},
		{
			name:     "WithHost",
			option:   WithHost("127.0.0.1"),
			expected: ActorSystem{host: "127.0.0.1"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg ActorSystem
			tc.option.Apply(&cfg)
			assert.Equal(t, tc.expected, cfg)
		})
	}
}
