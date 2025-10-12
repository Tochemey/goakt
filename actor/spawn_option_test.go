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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/passivation"
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
		supervisor := NewSupervisor(WithStrategy(OneForOneStrategy))
		option := WithSupervisor(supervisor)
		option.Apply(config)
		require.Equal(t, &spawnConfig{supervisor: supervisor}, config)
	})
	t.Run("spawn option with singleton", func(t *testing.T) {
		config := &spawnConfig{}
		option := withSingleton()
		option.Apply(config)
		require.Equal(t, &spawnConfig{asSingleton: true}, config)
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
		require.Equal(t, &spawnConfig{role: "api"}, config)
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
	t.Run("spawn option with passivation after", func(t *testing.T) {
		config := &spawnConfig{}
		second := time.Second
		option := WithPassivateAfter(second)
		option.Apply(config)
		require.IsType(t, new(passivation.TimeBasedStrategy), config.passivationStrategy)
	})
	t.Run("spawn option with long-lived", func(t *testing.T) {
		config := &spawnConfig{}
		option := WithLongLived()
		option.Apply(config)
		require.IsType(t, new(passivation.LongLivedStrategy), config.passivationStrategy)
	})
	t.Run("With invalid passivation strategy", func(t *testing.T) {
		config := newSpawnConfig()
		option := WithPassivationStrategy(&MockInvalidPassivationStrategy{})
		option.Apply(config)
		err := config.Validate()
		require.Error(t, err)
		require.ErrorIs(t, err, errors.ErrInvalidPassivationStrategy)
	})
}
