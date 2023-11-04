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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tochemey/goakt/eventstream"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

func TestPIDOptions(t *testing.T) {
	mailbox := newReceiveContextBuffer(10)
	var (
		atomicDuration   atomic.Duration
		atomicInt        atomic.Int32
		negativeDuration atomic.Duration
		atomicUint64     atomic.Uint64
	)
	negativeDuration.Store(-1)
	atomicInt.Store(5)
	atomicDuration.Store(time.Second)
	atomicUint64.Store(10)
	eventsStream := eventstream.New()

	testCases := []struct {
		name     string
		option   pidOption
		expected *pid
	}{
		{
			name:     "WithPassivationAfter",
			option:   withPassivationAfter(time.Second),
			expected: &pid{passivateAfter: atomicDuration},
		},
		{
			name:     "WithSendReplyTimeout",
			option:   withSendReplyTimeout(time.Second),
			expected: &pid{replyTimeout: atomicDuration},
		},
		{
			name:     "WithInitMaxRetries",
			option:   withInitMaxRetries(5),
			expected: &pid{initMaxRetries: atomicInt},
		},
		{
			name:     "WithLogger",
			option:   withCustomLogger(log.DefaultLogger),
			expected: &pid{logger: log.DefaultLogger},
		},
		{
			name:     "WithSupervisorStrategy",
			option:   withSupervisorStrategy(RestartDirective),
			expected: &pid{supervisorStrategy: RestartDirective},
		},
		{
			name:     "WithShutdownTimeout",
			option:   withShutdownTimeout(time.Second),
			expected: &pid{shutdownTimeout: atomicDuration},
		},
		{
			name:     "WithPassivationDisabled",
			option:   withPassivationDisabled(),
			expected: &pid{passivateAfter: negativeDuration},
		},
		{
			name:     "WithMailboxSize",
			option:   withMailboxSize(10),
			expected: &pid{mailboxSize: 10},
		},
		{
			name:     "WithMailbox",
			option:   withMailbox(mailbox),
			expected: &pid{mailbox: mailbox},
		},
		{
			name:     "WithStash",
			option:   withStash(10),
			expected: &pid{stashCapacity: atomicUint64},
		},
		{
			name:     "withEventsStream",
			option:   withEventsStream(eventsStream),
			expected: &pid{eventsStream: eventsStream},
		},
		{
			name:     "withInitTimeout",
			option:   withInitTimeout(time.Second),
			expected: &pid{initTimeout: atomicDuration},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pid := &pid{}
			tc.option(pid)
			assert.Equal(t, tc.expected, pid)
		})
	}
}
