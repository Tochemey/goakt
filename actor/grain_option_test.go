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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
)

func TestGrainOptions(t *testing.T) {
	t.Run("WithGrainInitMaxRetries", func(t *testing.T) {
		config := &grainConfig{}
		option := WithGrainInitMaxRetries(5)
		option(config)
		require.EqualValues(t, 5, config.initMaxRetries.Load())
	})

	t.Run("WithGrainInitTimeout", func(t *testing.T) {
		config := &grainConfig{}
		option := WithGrainInitTimeout(10 * time.Second)
		option(config)
		require.Equal(t, 10*time.Second, config.initTimeout.Load())
	})

	t.Run("WithGrainDeactivateAfter", func(t *testing.T) {
		config := &grainConfig{}
		option := WithGrainDeactivateAfter(15 * time.Minute)
		option(config)
		require.Equal(t, 15*time.Minute, config.deactivateAfter)
	})

	t.Run("WithLongLivedGrain", func(t *testing.T) {
		config := &grainConfig{}
		option := WithLongLivedGrain()
		option(config)
		require.Equal(t, time.Duration(-1), config.deactivateAfter)
	})
	t.Run("With valid dependency", func(t *testing.T) {
		config := &grainConfig{}
		dependency := NewMockDependency("id", "user", "email")
		option := WithGrainDependencies(dependency)
		option(config)
		require.NotEmpty(t, config.dependencies)
		require.Len(t, config.dependencies.Values(), 1)
	})
	t.Run("With dependencies validation", func(t *testing.T) {
		config := &grainConfig{}
		dependency := NewMockDependency("$omeN@me", "user", "email")
		option := WithGrainDependencies(dependency)
		option(config)
		err := config.Validate()
		require.Error(t, err)
	})

	t.Run("With Local Activation strategy", func(t *testing.T) {
		config := &grainConfig{}
		option := WithActivationStrategy(LocalActivation)
		option(config)
		require.Equal(t, LocalActivation, config.activationStrategy)
	})

	t.Run("With RoundRobin Activation strategy", func(t *testing.T) {
		config := &grainConfig{}
		option := WithActivationStrategy(RoundRobinActivation)
		option(config)
		require.Equal(t, RoundRobinActivation, config.activationStrategy)
	})

	t.Run("With Random Activation strategy", func(t *testing.T) {
		config := &grainConfig{}
		option := WithActivationStrategy(RandomActivation)
		option(config)
		require.Equal(t, RandomActivation, config.activationStrategy)
	})

	t.Run("With LeastLoad Activation strategy", func(t *testing.T) {
		config := &grainConfig{}
		option := WithActivationStrategy(LeastLoadActivation)
		option(config)
		require.Equal(t, LeastLoadActivation, config.activationStrategy)
	})
	t.Run("With Mailbox capacity", func(t *testing.T) {
		config := new(grainConfig)
		option := WithGrainMailboxCapacity(10)
		option(config)
		require.EqualValues(t, 10, config.capacity)
	})
	t.Run("With Relocation Disabled", func(t *testing.T) {
		config := new(grainConfig)
		option := WithGrainDisableRelocation()
		option(config)
		require.True(t, config.disableRelocation)
	})
	t.Run("With Eager Relocation", func(t *testing.T) {
		config := new(grainConfig)
		option := WithGrainEagerRelocation()
		option(config)
		require.True(t, config.eagerRelocation)
	})
	t.Run("Eager and Disable Relocation conflict is rejected", func(t *testing.T) {
		config := newGrainConfig(WithGrainDisableRelocation(), WithGrainEagerRelocation())
		err := config.Validate()
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrGrainRelocationConflict)
	})
	t.Run("Default grain config is lazy relocation", func(t *testing.T) {
		config := newGrainConfig()
		require.NoError(t, config.Validate())
		require.False(t, config.eagerRelocation)
		require.False(t, config.disableRelocation)
	})
}

func TestWithGrainReentrancy(t *testing.T) {
	t.Run("sets and validates the policy", func(t *testing.T) {
		policy := reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll), reentrancy.WithMaxInFlight(4))
		config := newGrainConfig(WithGrainReentrancy(policy))
		require.Same(t, policy, config.reentrancy)
		require.NoError(t, config.Validate())
	})

	t.Run("rejects an invalid mode", func(t *testing.T) {
		config := newGrainConfig(WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.Mode(99)))))
		require.ErrorIs(t, config.Validate(), gerrors.ErrInvalidReentrancyMode)
	})
}

func TestNewGrainPIDBuildsReentrancyState(t *testing.T) {
	ctx := t.Context()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	identity := &GrainIdentity{kind: "Kind", name: "configured"}
	policy := reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant), reentrancy.WithMaxInFlight(7))

	pid := newGrainPID(identity, NewMockGrain(), sys, newGrainConfig(WithGrainReentrancy(policy)))
	reentrant := pid.reentrancy.Load()
	require.NotNil(t, reentrant)
	require.Equal(t, reentrancy.StashNonReentrant, reentrant.getMode())
	require.EqualValues(t, 7, reentrant.maxInFlight.Load())
	require.NotNil(t, pid.responses)

	// An Off policy behaves exactly like no policy.
	pid = newGrainPID(identity, NewMockGrain(), sys, newGrainConfig(WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.Off)))))
	require.Nil(t, pid.reentrancy.Load())
	require.NotNil(t, pid.responses)
}
