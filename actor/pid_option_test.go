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
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/internal/eventstream"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/passivation"
)

func TestPIDOptions(t *testing.T) {
	mailbox := NewUnboundedMailbox()
	supervisor := NewSupervisor(WithStrategy(OneForAllStrategy))
	var (
		atomicDuration   atomic.Duration
		atomicInt        atomic.Int32
		negativeDuration atomic.Duration
		atomicUint64     atomic.Uint64
		atomicTrue       atomic.Bool
		atomicFalse      atomic.Bool
	)
	negativeDuration.Store(-1)
	atomicInt.Store(5)
	atomicDuration.Store(time.Second)
	atomicUint64.Store(10)
	eventsStream := eventstream.New()
	atomicTrue.Store(true)
	atomicFalse.Store(false)
	strategy := passivation.NewLongLivedStrategy()

	testCases := []struct {
		name     string
		option   pidOption
		expected *PID
	}{
		{
			name:     "WithPassivationStrategy",
			option:   withPassivationStrategy(strategy),
			expected: &PID{passivationStrategy: strategy},
		},
		{
			name:     "WithInitMaxRetries",
			option:   withInitMaxRetries(5),
			expected: &PID{initMaxRetries: atomicInt},
		},
		{
			name:     "WithLogger",
			option:   withCustomLogger(log.DebugLogger),
			expected: &PID{logger: log.DebugLogger},
		},
		{
			name:     "WithSupervisor",
			option:   withSupervisor(supervisor),
			expected: &PID{supervisor: supervisor},
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
		{
			name:   "AsSingleton",
			option: asSingleton(),
			expected: &PID{
				isSingleton: atomicTrue,
			},
		},
		{
			name:     "withRelocationDisabled",
			option:   withRelocationDisabled(),
			expected: &PID{relocatable: atomicFalse},
		},
		{
			name:   "AsSystemActor",
			option: asSystemActor(),
			expected: &PID{
				isSystem: atomicTrue,
			},
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

func TestWithDependencies(t *testing.T) {
	pid := &PID{}
	dependency := NewMockDependency("id", "user", "email")
	option := withDependencies(dependency)
	option(pid)
	require.NotEmpty(t, pid.Dependencies())
	actual := pid.Dependency("id")
	require.True(t, reflect.DeepEqual(actual, dependency))
}
