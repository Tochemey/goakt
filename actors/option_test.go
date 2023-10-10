/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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
	"github.com/tochemey/goakt/hash"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/telemetry"
	"go.uber.org/atomic"
)

func TestOptions(t *testing.T) {
	tel := telemetry.New()
	mailbox := newReceiveContextBuffer(10)
	var atomicTrue atomic.Bool
	atomicTrue.Store(true)
	hasher := hash.DefaultHasher()
	testCases := []struct {
		name     string
		option   Option
		expected actorSystem
	}{
		{
			name:     "WithExpireActorAfter",
			option:   WithExpireActorAfter(2 * time.Second),
			expected: actorSystem{expireActorAfter: 2. * time.Second},
		},
		{
			name:     "WithReplyTimeout",
			option:   WithReplyTimeout(2 * time.Second),
			expected: actorSystem{replyTimeout: 2. * time.Second},
		},
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
			name:     "WithPassivationDisabled",
			option:   WithPassivationDisabled(),
			expected: actorSystem{expireActorAfter: -1},
		},
		{
			name:     "WithSupervisorStrategy",
			option:   WithSupervisorStrategy(RestartDirective),
			expected: actorSystem{supervisorStrategy: RestartDirective},
		},
		{
			name:     "WithRemoting",
			option:   WithRemoting("localhost", 3100),
			expected: actorSystem{remotingEnabled: atomicTrue, remotingPort: 3100, remotingHost: "localhost"},
		},
		{
			name:     "WithShutdownTimeout",
			option:   WithShutdownTimeout(2 * time.Second),
			expected: actorSystem{shutdownTimeout: 2. * time.Second},
		},
		{
			name:     "WithTelemetry",
			option:   WithTelemetry(tel),
			expected: actorSystem{telemetry: tel},
		},
		{
			name:     "WithMailboxSize",
			option:   WithMailboxSize(10),
			expected: actorSystem{mailboxSize: 10},
		},
		{
			name:     "WithMailbox",
			option:   WithMailbox(mailbox),
			expected: actorSystem{mailbox: mailbox},
		},
		{
			name:     "WithStash",
			option:   WithStash(10),
			expected: actorSystem{stashBuffer: 10},
		},
		{
			name:     "WithPartitionHasher",
			option:   WithPartitionHasher(hasher),
			expected: actorSystem{partitionHasher: hasher},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg actorSystem
			tc.option.Apply(&cfg)
			assert.Equal(t, tc.expected, cfg)
		})
	}
}
