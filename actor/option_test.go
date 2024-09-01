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
)

func TestOption(t *testing.T) {
	resumeDirective := NewResumeDirective()
	var atomicTrue atomic.Bool
	atomicTrue.Store(true)
	clusterConfig := NewClusterConfig()
	hasher := hash.DefaultHasher()
	testCases := []struct {
		name     string
		option   Option
		expected system
	}{
		{
			name:     "WithExpireActorAfter",
			option:   WithExpireActorAfter(2 * time.Second),
			expected: system{expireActorAfter: 2. * time.Second},
		},
		{
			name:     "WithReplyTimeout",
			option:   WithAskTimeout(2 * time.Second),
			expected: system{askTimeout: 2. * time.Second},
		},
		{
			name:     "WithActorInitMaxRetries",
			option:   WithActorInitMaxRetries(2),
			expected: system{actorInitMaxRetries: 2},
		},
		{
			name:     "WithLogger",
			option:   WithLogger(log.DefaultLogger),
			expected: system{logger: log.DefaultLogger},
		},
		{
			name:     "WithPassivationDisabled",
			option:   WithPassivationDisabled(),
			expected: system{expireActorAfter: -1},
		},
		{
			name:     "WithSupervisorDirective",
			option:   WithSupervisorDirective(resumeDirective),
			expected: system{supervisorDirective: resumeDirective},
		},
		{
			name:     "WithShutdownTimeout",
			option:   WithShutdownTimeout(2 * time.Second),
			expected: system{shutdownTimeout: 2. * time.Second},
		},
		{
			name:     "WithStash",
			option:   WithStash(),
			expected: system{stashEnabled: true},
		},
		{
			name:     "WithPartitionHasher",
			option:   WithPartitionHasher(hasher),
			expected: system{partitionHasher: hasher},
		},
		{
			name:     "WithActorInitTimeout",
			option:   WithActorInitTimeout(2 * time.Second),
			expected: system{actorInitTimeout: 2. * time.Second},
		},
		{
			name:     "WithMetric",
			option:   WithMetric(),
			expected: system{metricEnabled: atomicTrue},
		},
		{
			name:     "WithCluster",
			option:   WithCluster(clusterConfig),
			expected: system{clusterEnabled: atomicTrue, clusterConfig: clusterConfig},
		},
		{
			name:     "WithPeerStateLoopInterval",
			option:   WithPeerStateLoopInterval(2 * time.Second),
			expected: system{peersStateLoopInterval: 2. * time.Second},
		},
		{
			name:     "WithGCInterval",
			option:   WithGCInterval(2 * time.Second),
			expected: system{gcInterval: 2. * time.Second},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg system
			tc.option.Apply(&cfg)
			assert.Equal(t, tc.expected, cfg)
		})
	}
}
