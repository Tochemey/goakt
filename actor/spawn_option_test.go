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

package actors

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	t.Run("spawn option with passivation after", func(t *testing.T) {
		config := &spawnConfig{}
		second := time.Second
		option := WithPassivateAfter(second)
		option.Apply(config)
		require.Equal(t, &spawnConfig{passivateAfter: &second}, config)
	})
	t.Run("spawn option with long-lived", func(t *testing.T) {
		config := &spawnConfig{}
		option := WithLongLived()
		option.Apply(config)
		require.Equal(t, &spawnConfig{passivateAfter: &longLived}, config)
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
		require.Equal(t, &spawnConfig{disableRelocation: true}, config)
	})
}

func TestNewSpawnConfig(t *testing.T) {
	config := newSpawnConfig()
	require.NotNil(t, config)
	require.Nil(t, config.mailbox)
}
