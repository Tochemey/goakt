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

package datacenter

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/log"
)

func TestConfigDefaults(t *testing.T) {
	config := NewConfig()
	require.NotNil(t, config.Logger)
	require.Equal(t, DefaultHeartbeatInterval, config.HeartbeatInterval)
	require.Equal(t, DefaultCacheRefreshInterval, config.CacheRefreshInterval)
	require.Equal(t, DefaultMaxCacheStaleness, config.MaxCacheStaleness)
	require.Equal(t, DefaultLeaderCheckInterval, config.LeaderCheckInterval)
	require.Equal(t, DefaultJitterRatio, config.JitterRatio)
	require.Equal(t, DefaultMaxBackoff, config.MaxBackoff)
	require.Equal(t, DefaultRequestTimeout, config.RequestTimeout)
	require.True(t, config.WatchEnabled)
	require.True(t, config.FailOnStaleCache) // default to strict consistency
}

func TestConfigSanitizeDefaults(t *testing.T) {
	config := &Config{}
	config.Sanitize()

	require.Equal(t, log.DefaultLogger, config.Logger)
	require.Equal(t, DefaultHeartbeatInterval, config.HeartbeatInterval)
	require.Equal(t, DefaultCacheRefreshInterval, config.CacheRefreshInterval)
	require.Equal(t, DefaultMaxCacheStaleness, config.MaxCacheStaleness)
	require.Equal(t, DefaultLeaderCheckInterval, config.LeaderCheckInterval)
	require.Equal(t, DefaultJitterRatio, config.JitterRatio)
	require.Equal(t, DefaultMaxBackoff, config.MaxBackoff)
	require.Equal(t, DefaultRequestTimeout, config.RequestTimeout)
}

func TestConfigValidate(t *testing.T) {
	validConfig := func() *Config {
		return &Config{
			Logger:               log.DiscardLogger,
			ControlPlane:         &MockControlPlane{},
			DataCenter:           DataCenter{Name: "dc-1"},
			HeartbeatInterval:    time.Second,
			CacheRefreshInterval: time.Second,
			MaxCacheStaleness:    time.Second,
			LeaderCheckInterval:  time.Second,
			JitterRatio:          0.1,
			MaxBackoff:           time.Second,
			WatchEnabled:         true,
			RequestTimeout:       time.Second,
		}
	}

	t.Run("valid", func(t *testing.T) {
		require.NoError(t, validConfig().Validate())
	})

	t.Run("missing control plane", func(t *testing.T) {
		config := validConfig()
		config.ControlPlane = nil
		require.Error(t, config.Validate())
	})

	t.Run("missing data center name", func(t *testing.T) {
		config := validConfig()
		config.DataCenter = DataCenter{}
		require.Error(t, config.Validate())
	})

	t.Run("invalid heartbeat interval", func(t *testing.T) {
		config := validConfig()
		config.HeartbeatInterval = 0
		require.Error(t, config.Validate())
	})

	t.Run("invalid refresh interval", func(t *testing.T) {
		config := validConfig()
		config.CacheRefreshInterval = 0
		require.Error(t, config.Validate())
	})

	t.Run("invalid max staleness", func(t *testing.T) {
		config := validConfig()
		config.MaxCacheStaleness = 0
		require.Error(t, config.Validate())
	})

	t.Run("invalid leader check interval", func(t *testing.T) {
		config := validConfig()
		config.LeaderCheckInterval = 0
		require.Error(t, config.Validate())
	})

	t.Run("invalid jitter ratio", func(t *testing.T) {
		config := validConfig()
		config.JitterRatio = 0.75
		require.Error(t, config.Validate())
	})

	t.Run("invalid max backoff", func(t *testing.T) {
		config := validConfig()
		config.MaxBackoff = 0
		require.Error(t, config.Validate())
	})

	t.Run("invalid request timeout", func(t *testing.T) {
		config := validConfig()
		config.RequestTimeout = 0
		require.Error(t, config.Validate())
	})
}
