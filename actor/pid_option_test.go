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
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/metric"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/supervisor"
)

func TestPIDOptions(t *testing.T) {
	t.Run("AsSingleton", func(t *testing.T) {
		pid := &PID{}
		asSingleton()(pid)
		assert.True(t, pid.isStateSet(singletonState))
	})

	t.Run("AsSystemActor", func(t *testing.T) {
		pid := &PID{}
		asSystemActor()(pid)
		assert.True(t, pid.isStateSet(systemState))
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

func TestWithSingletonSpec(t *testing.T) {
	pid := &PID{}
	spec := &singletonSpec{
		SpawnTimeout: time.Second,
		WaitInterval: 500 * time.Millisecond,
		MaxRetries:   3,
	}
	option := withSingletonSpec(spec)
	option(pid)
	require.Equal(t, spec, pid.singletonSpec())
}

func TestPIDSettingsApply(t *testing.T) {
	t.Run("applies every setting as its option would", func(t *testing.T) {
		system, err := NewActorSystem("TestSys")
		require.NoError(t, err)
		manager := newPassivationManager(system.Logger())
		timeout := 3 * time.Second
		sup := supervisor.NewSupervisor()
		mailbox := NewUnboundedMailbox()
		provider := metric.NewProvider()
		strategy := passivation.NewMessageCountBasedStrategy(10)

		pid := &PID{}
		pid.setState(relocationState, true)
		settings := pidSettings{
			actorSystem:         system,
			initMaxRetries:      7,
			initTimeout:         &timeout,
			passivationManager:  manager,
			metricProvider:      provider,
			supervisor:          sup,
			mailbox:             mailbox,
			passivationStrategy: strategy,
			relocationDisabled:  true,
		}
		settings.apply(pid)

		assert.Same(t, system, pid.actorSystem)
		assert.Same(t, system.(*actorSystem).dispatcher, pid.dispatcher)
		assert.EqualValues(t, 7, pid.initMaxRetries.Load())
		assert.Same(t, &timeout, pid.initTimeout.Load())
		assert.Same(t, manager, pid.passivationManager)
		assert.Same(t, provider, pid.metricProvider())
		assert.Same(t, sup, pid.supervisor)
		assert.Same(t, mailbox, pid.mailbox)
		assert.Same(t, strategy, pid.passivationStrategy)
		assert.True(t, pid.msgCountPassivation.Load())
		assert.False(t, pid.isStateSet(relocationState))
	})

	t.Run("skips the settings that are unset", func(t *testing.T) {
		strategy := passivation.NewLongLivedStrategy()
		pid := &PID{}
		pid.setState(relocationState, true)
		settings := pidSettings{passivationStrategy: strategy}
		settings.apply(pid)

		assert.Nil(t, pid.supervisor, "a nil supervisor is left for newPID's default")
		assert.Nil(t, pid.mailbox, "a nil mailbox is left for the embedded one")
		assert.Nil(t, pid.companion, "a nil metric provider attaches no companion")
		assert.Nil(t, pid.initTimeout.Load())
		assert.Same(t, strategy, pid.passivationStrategy)
		assert.False(t, pid.msgCountPassivation.Load())
		assert.True(t, pid.isStateSet(relocationState), "relocation stays enabled unless disabled")
	})
}
