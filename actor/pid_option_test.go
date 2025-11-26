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

	"github.com/tochemey/goakt/v3/internal/eventstream"
	"github.com/tochemey/goakt/v3/internal/metric"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/passivation"
)

func TestPIDOptions(t *testing.T) {
	t.Run("WithPassivationStrategy", func(t *testing.T) {
		strategy := passivation.NewLongLivedStrategy()
		pid := &PID{}
		withPassivationStrategy(strategy)(pid)
		assert.Equal(t, strategy, pid.passivationStrategy)
	})

	t.Run("WithInitMaxRetries", func(t *testing.T) {
		pid := &PID{}
		withInitMaxRetries(5)(pid)
		assert.Equal(t, int32(5), pid.initMaxRetries.Load())
	})

	t.Run("WithLogger", func(t *testing.T) {
		pid := &PID{}
		withCustomLogger(log.DebugLogger)(pid)
		assert.Equal(t, log.DebugLogger, pid.logger)
	})

	t.Run("WithSupervisor", func(t *testing.T) {
		supervisor := NewSupervisor()
		pid := &PID{}
		withSupervisor(supervisor)(pid)
		assert.Equal(t, supervisor, pid.supervisor)
	})

	t.Run("withEventsStream", func(t *testing.T) {
		stream := eventstream.New()
		pid := &PID{}
		withEventsStream(stream)(pid)
		assert.Equal(t, stream, pid.eventsStream)
	})

	t.Run("withInitTimeout", func(t *testing.T) {
		pid := &PID{}
		withInitTimeout(time.Second)(pid)
		assert.Equal(t, time.Second, pid.initTimeout.Load())
	})

	t.Run("WithMailbox", func(t *testing.T) {
		box := NewUnboundedMailbox()
		pid := &PID{}
		withMailbox(box)(pid)
		assert.Equal(t, box, pid.mailbox)
	})

	t.Run("AsSingleton", func(t *testing.T) {
		pid := &PID{}
		asSingleton()(pid)
		assert.True(t, pid.isFlagEnabled(isSingletonFlag))
	})

	t.Run("withRelocationDisabled", func(t *testing.T) {
		pid := &PID{}
		withRelocationDisabled()(pid)
		assert.False(t, pid.isFlagEnabled(isRelocatableFlag))
	})

	t.Run("AsSystemActor", func(t *testing.T) {
		pid := &PID{}
		asSystemActor()(pid)
		assert.True(t, pid.isFlagEnabled(isSystemFlag))
	})

	t.Run("withMetricProvider", func(t *testing.T) {
		pid := &PID{}
		metricProvider := metric.New()
		withMeterProvider(metricProvider)(pid)
		assert.Equal(t, metricProvider, pid.metricProvider)
	})
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

func TestWithRole(t *testing.T) {
	pid := &PID{}
	option := withRole("payments")
	option(pid)
	role := pid.Role()
	require.NotNil(t, role)
	assert.Equal(t, "payments", *role)
}
