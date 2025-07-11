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
}
