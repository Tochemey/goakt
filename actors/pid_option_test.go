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

package actors

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v2/internal/eventstream"
	"github.com/tochemey/goakt/v2/log"
)

func TestPIDOptions(t *testing.T) {
	resumeDirective := NewResumeDirective()
	mailbox := NewUnboundedMailbox()
	var (
		atomicDuration   atomic.Duration
		atomicInt        atomic.Int32
		negativeDuration atomic.Duration
		atomicUint64     atomic.Uint64
		atomicTrue       atomic.Bool
	)
	negativeDuration.Store(-1)
	atomicInt.Store(5)
	atomicDuration.Store(time.Second)
	atomicUint64.Store(10)
	eventsStream := eventstream.New()
	atomicTrue.Store(true)

	testCases := []struct {
		name     string
		option   pidOption
		expected *PID
	}{
		{
			name:     "WithPassivationAfter",
			option:   withPassivationAfter(time.Second),
			expected: &PID{passivateAfter: atomicDuration},
		},
		{
			name:     "WithAskTimeout",
			option:   withAskTimeout(time.Second),
			expected: &PID{askTimeout: atomicDuration},
		},
		{
			name:     "WithInitMaxRetries",
			option:   withInitMaxRetries(5),
			expected: &PID{initMaxRetries: atomicInt},
		},
		{
			name:     "WithLogger",
			option:   withCustomLogger(log.DefaultLogger),
			expected: &PID{logger: log.DefaultLogger},
		},
		{
			name:     "WithSupervisorStrategy",
			option:   withSupervisorDirective(resumeDirective),
			expected: &PID{supervisorDirective: resumeDirective},
		},
		{
			name:     "WithShutdownTimeout",
			option:   withShutdownTimeout(time.Second),
			expected: &PID{shutdownTimeout: atomicDuration},
		},
		{
			name:     "WithPassivationDisabled",
			option:   withPassivationDisabled(),
			expected: &PID{passivateAfter: negativeDuration},
		},
		{
			name:     "withEventsStream",
			option:   withEventsStream(eventsStream),
			expected: &PID{eventsStream: eventsStream},
		},
		{
			name:     "withInitTimeout",
			option:   withInitTimeout(time.Second),
			expected: &PID{initTimeout: atomicDuration},
		},
		{
			name:     "WithMailbox",
			option:   withMailbox(mailbox),
			expected: &PID{mailbox: mailbox},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pid := &PID{}
			tc.option(pid)
			assert.Equal(t, tc.expected, pid)
		})
	}
}
