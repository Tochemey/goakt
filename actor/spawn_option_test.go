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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/supervisor"
)

func TestSpawnOption(t *testing.T) {
	t.Run("spawn option with mailbox", func(t *testing.T) {
		mailbox := NewUnboundedMailbox()
		config := &spawnConfig{}
		option := WithMailbox(mailbox)
		option.Apply(config)
		require.Equal(t, &spawnConfig{mailbox: mailbox}, config)
	})
	t.Run("spawn option with supervisor strategy", func(t *testing.T) {
		config := &spawnConfig{}
		supervisor := supervisor.NewSupervisor(supervisor.WithStrategy(supervisor.OneForOneStrategy))
		option := WithSupervisor(supervisor)
		option.Apply(config)
		require.Equal(t, &spawnConfig{supervisor: supervisor}, config)
	})
	t.Run("spawn option with singleton", func(t *testing.T) {
		config := &spawnConfig{}
		spec := &singletonSpec{
			SpawnTimeout: 10 * time.Second,
			WaitInterval: 500 * time.Millisecond,
			MaxRetries:   3,
		}
		option := withSingleton(spec)
		option.Apply(config)
		require.Equal(t, &spawnConfig{asSingleton: true, singletonSpec: spec}, config)
	})
	t.Run("spawn option with relocation disabled", func(t *testing.T) {
		config := &spawnConfig{}
		option := WithRelocationDisabled()
		option.Apply(config)
		require.Equal(t, &spawnConfig{relocatable: false}, config)
	})
	t.Run("spawn option with stashing", func(t *testing.T) {
		config := &spawnConfig{}
		option := WithStashing()
		option.Apply(config)
		require.Equal(t, &spawnConfig{enableStash: true}, config)
	})
	t.Run("spawn option with reentrancy", func(t *testing.T) {
		config := &spawnConfig{}
		option := WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll), reentrancy.WithMaxInFlight(4)))
		option.Apply(config)
		require.NotNil(t, config.reentrancy)
		require.Equal(t, reentrancy.AllowAll, config.reentrancy.Mode())
		require.Equal(t, 4, config.reentrancy.MaxInFlight())
	})
	t.Run("spawn option with reentrancy max in flight clamp", func(t *testing.T) {
		config := &spawnConfig{}
		option := WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll), reentrancy.WithMaxInFlight(-1)))
		option.Apply(config)
		require.NotNil(t, config.reentrancy)
		require.Equal(t, 0, config.reentrancy.MaxInFlight())
	})
	t.Run("spawn option with placement", func(t *testing.T) {
		config := &spawnConfig{}
		option := WithPlacement(RoundRobin)
		option.Apply(config)
		require.Equal(t, &spawnConfig{placement: RoundRobin}, config)
	})
	t.Run("spawn option with role", func(t *testing.T) {
		config := &spawnConfig{}
		option := WithRole("api")
		option.Apply(config)
		require.Equal(t, &spawnConfig{role: new("api")}, config)
	})

	t.Run("spawn option with host and port", func(t *testing.T) {
		config := &spawnConfig{}
		option := WithHostAndPort("localhost", 8080)
		option.Apply(config)
		require.Equal(t, &spawnConfig{host: new("localhost"), port: new(8080)}, config)
	})

	t.Run("spawn option with host and port validation", func(t *testing.T) {
		config := &spawnConfig{}
		option := WithHostAndPort("localhost", -1)
		option.Apply(config)
		err := config.Validate()
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrInvalidTCPAddress)
	})
}

func TestNewSpawnConfig(t *testing.T) {
	config := newSpawnConfig()
	require.NotNil(t, config)
	require.Nil(t, config.mailbox)
}

func TestSpawnConfig(t *testing.T) {
	t.Run("With valid dependency", func(t *testing.T) {
		config := &spawnConfig{}
		dependency := NewMockDependency("id", "user", "email")
		option := WithDependencies(dependency)
		option.Apply(config)
		require.Equal(t, &spawnConfig{dependencies: []extension.Dependency{dependency}}, config)
	})
	t.Run("With dependencies validation", func(t *testing.T) {
		config := newSpawnConfig()
		dependency := NewMockDependency("$omeN@me", "user", "email")
		option := WithDependencies(dependency)
		option.Apply(config)
		err := config.Validate()
		require.Error(t, err)
	})
	t.Run("spawn option with long-lived", func(t *testing.T) {
		config := &spawnConfig{}
		option := WithLongLived()
		option.Apply(config)
		require.IsType(t, new(passivation.LongLivedStrategy), config.passivationStrategy)
	})
	t.Run("With invalid passivation strategy", func(t *testing.T) {
		config := newSpawnConfig()
		option := WithPassivationStrategy(&MockFakePassivationStrategy{})
		option.Apply(config)
		err := config.Validate()
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrInvalidPassivationStrategy)
	})
	t.Run("With invalid reentrancy mode", func(t *testing.T) {
		config := newSpawnConfig()
		option := WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.Mode(99))))
		option.Apply(config)
		err := config.Validate()
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrInvalidReentrancyMode)
	})
}

func TestSpawnConfigClone(t *testing.T) {
	t.Run("clones every field without overrides", func(t *testing.T) {
		mailbox := NewUnboundedMailbox()
		sup := supervisor.NewSupervisor(supervisor.WithStrategy(supervisor.OneForOneStrategy))
		dep := NewMockDependency("dep-1", "user", "email")
		strategy := passivation.NewLongLivedStrategy()
		reent := reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll), reentrancy.WithMaxInFlight(2))

		original := newSpawnConfig(
			WithMailbox(mailbox),
			WithSupervisor(sup),
			WithDependencies(dep),
			WithStashing(),
			WithPassivationStrategy(strategy),
			WithReentrancy(reent),
			WithRole("api"),
			WithHostAndPort("localhost", 8080),
			WithPlacement(Random),
			WithRelocationDisabled(),
		)
		withSingleton(&singletonSpec{SpawnTimeout: 5 * time.Second, WaitInterval: time.Second, MaxRetries: 2}).Apply(original)

		cloned := original.clone()

		require.NotSame(t, original, cloned)
		require.Equal(t, original, cloned)
	})

	t.Run("mutating cloned scalar pointers does not affect the source", func(t *testing.T) {
		original := newSpawnConfig(
			WithRole("api"),
			WithHostAndPort("localhost", 8080),
		)

		cloned := original.clone()

		require.NotSame(t, original.role, cloned.role)
		require.NotSame(t, original.host, cloned.host)
		require.NotSame(t, original.port, cloned.port)

		*cloned.role = "worker"
		*cloned.host = "remote"
		*cloned.port = 9090

		require.Equal(t, "api", *original.role)
		require.Equal(t, "localhost", *original.host)
		require.Equal(t, 8080, *original.port)
	})

	t.Run("dependencies slice is reallocated", func(t *testing.T) {
		dep := NewMockDependency("dep-1", "user", "email")
		original := newSpawnConfig(WithDependencies(dep))

		cloned := original.clone()

		require.Equal(t, original.dependencies, cloned.dependencies)
		require.NotEqual(t,
			fmt.Sprintf("%p", original.dependencies),
			fmt.Sprintf("%p", cloned.dependencies),
		)
	})

	t.Run("singletonSpec is reallocated", func(t *testing.T) {
		original := newSpawnConfig()
		withSingleton(&singletonSpec{SpawnTimeout: 5 * time.Second, WaitInterval: time.Second, MaxRetries: 2}).Apply(original)

		cloned := original.clone()

		require.NotSame(t, original.singletonSpec, cloned.singletonSpec)
		cloned.singletonSpec.MaxRetries = 99
		require.Equal(t, int32(2), original.singletonSpec.MaxRetries)
	})

	t.Run("applies overrides on the clone only", func(t *testing.T) {
		original := newSpawnConfig(
			WithRole("api"),
			WithPlacement(Random),
		)

		newSup := supervisor.NewSupervisor(supervisor.WithStrategy(supervisor.OneForOneStrategy))
		cloned := original.clone(
			WithRole("worker"),
			WithPlacement(LeastLoad),
			WithSupervisor(newSup),
		)

		require.Equal(t, "worker", *cloned.role)
		require.Equal(t, LeastLoad, cloned.placement)
		require.Same(t, newSup, cloned.supervisor)

		require.Equal(t, "api", *original.role)
		require.Equal(t, Random, original.placement)
		require.Nil(t, original.supervisor)
	})

	t.Run("clones a zero-value config", func(t *testing.T) {
		original := &spawnConfig{}

		cloned := original.clone()

		require.NotSame(t, original, cloned)
		require.Equal(t, original, cloned)
	})
}
