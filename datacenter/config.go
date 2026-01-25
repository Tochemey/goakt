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
	"time"

	"github.com/tochemey/goakt/v3/internal/validation"
	"github.com/tochemey/goakt/v3/log"
)

const (
	// DefaultHeartbeatInterval defines the default cadence for DC liveness heartbeats.
	DefaultHeartbeatInterval = 10 * time.Second
	// DefaultCacheRefreshInterval defines the default polling interval for refreshing DC cache data.
	DefaultCacheRefreshInterval = 10 * time.Second
	// DefaultMaxCacheStaleness defines the default maximum acceptable age for cached DC data.
	DefaultMaxCacheStaleness = 30 * time.Second
	// DefaultLeaderCheckInterval defines how often leadership is rechecked for manager ownership.
	DefaultLeaderCheckInterval = 5 * time.Second
	// DefaultRequestTimeout defines the default timeout for control plane operations.
	DefaultRequestTimeout = 3 * time.Second
	// DefaultJitterRatio defines the default jitter ratio applied to periodic loops.
	DefaultJitterRatio = 0.1
	// DefaultMaxBackoff defines the default cap for exponential backoff on errors.
	DefaultMaxBackoff = 30 * time.Second
)

// Config holds multi-DC runtime configuration.
type Config struct {
	// Logger receives internal manager logs. Defaults to log.DefaultLogger.
	Logger log.Logger
	// ControlPlane is the multi-DC control plane implementation.
	ControlPlane ControlPlane
	// DataCenter describes the local DC metadata.
	DataCenter DataCenter
	// Endpoints are the remoting addresses (host:port) that this data center advertises to
	// the multi-DC control plane. They are the only way other data centers can reach this DC
	// for cross-DC messaging and spawning. Each element must be a valid TCP address in "host:port"
	// form, representing a node's remoting listen address.
	//
	// How they are used:
	//
	//   - Messaging: Other DCs use these addresses to discover and talk to actors and grains
	//     in this DC. For example, PID.DiscoverActor and grain Tell/Ask iterate over the
	//     target DC's Endpoints (from the cached DataCenterRecord), contact remoting at each
	//     host:port to resolve or deliver messages, and use the first successful result.
	//
	//   - Spawning: When another DC spawns an actor in this DC via SpawnOn with WithDataCenter,
	//     the runtime picks one of this DC's Endpoints at random and sends RemoteSpawn to that
	//     node. So the list you configure here defines where cross-DC spawns land: a single
	//     endpoint (e.g. the DC leader only) sends all such spawns to one node; multiple
	//     endpoints spread them across nodes.
	//
	// This value is stored in the control plane as DataCenterRecord.Endpoints when the local DC
	// registers (typically by the DC leader). Choose addresses that are reachable from other DCs
	// and that correspond to nodes running the actor system with remoting enabled.
	//
	// Example with one node (leader only): []string{"192.168.1.1:8080"}.
	// Example with three nodes: []string{"192.168.1.1:8080", "192.168.1.2:8080", "192.168.1.3:8080"}.
	Endpoints []string
	// HeartbeatInterval controls how often the local DC renews its liveness.
	HeartbeatInterval time.Duration
	// CacheRefreshInterval controls how often the DC registry cache is refreshed.
	CacheRefreshInterval time.Duration
	// MaxCacheStaleness bounds how old cached DC data may be before routing is restricted.
	MaxCacheStaleness time.Duration
	// LeaderCheckInterval controls how often leader status is reevaluated for manager ownership.
	LeaderCheckInterval time.Duration
	// JitterRatio applies +/- jitter to periodic loops (0 uses DefaultJitterRatio).
	JitterRatio float64
	// MaxBackoff caps exponential backoff when loop operations fail.
	MaxBackoff time.Duration
	// WatchEnabled enables watch-based cache refresh when supported by the control plane.
	WatchEnabled bool
	// RequestTimeout bounds control plane API calls.
	RequestTimeout time.Duration
}

var _ validation.Validator = (*Config)(nil)

// NewConfig returns a Config populated with defaults.
func NewConfig() *Config {
	return &Config{
		Logger:               log.DefaultLogger,
		HeartbeatInterval:    DefaultHeartbeatInterval,
		CacheRefreshInterval: DefaultCacheRefreshInterval,
		MaxCacheStaleness:    DefaultMaxCacheStaleness,
		LeaderCheckInterval:  DefaultLeaderCheckInterval,
		JitterRatio:          DefaultJitterRatio,
		MaxBackoff:           DefaultMaxBackoff,
		WatchEnabled:         true,
		RequestTimeout:       DefaultRequestTimeout,
	}
}

// Validate implements validation.Validator.
func (c *Config) Validate() error {
	gerr := validation.New(validation.FailFast()).
		AddAssertion(c.ControlPlane != nil, "ControlPlane is required").
		AddValidator(validation.NewEmptyStringValidator("Name", c.DataCenter.Name)).
		AddAssertion(len(c.Endpoints) > 0, "Endpoints must not be empty").
		AddAssertion(c.HeartbeatInterval > 0, "HeartbeatInterval must be greater than 0").
		AddAssertion(c.CacheRefreshInterval > 0, "CacheRefreshInterval must be greater than 0").
		AddAssertion(c.MaxCacheStaleness > 0, "MaxCacheStaleness must be greater than 0").
		AddAssertion(c.LeaderCheckInterval > 0, "LeaderCheckInterval must be greater than 0").
		AddAssertion(c.JitterRatio >= 0 && c.JitterRatio <= 0.5, "JitterRatio must be between 0 and 0.5").
		AddAssertion(c.MaxBackoff > 0, "MaxBackoff must be greater than 0").
		AddAssertion(c.RequestTimeout > 0, "RequestTimeout must be greater than 0").
		Validate()

	if gerr != nil {
		return gerr
	}

	// validate the endpoints
	for _, endpoint := range c.Endpoints {
		if err := validation.NewTCPAddressValidator(endpoint).Validate(); err != nil {
			return err
		}
	}

	return nil
}

// Sanitize fills zero-value fields with sensible defaults.
func (c *Config) Sanitize() {
	if c.Logger == nil {
		c.Logger = log.DefaultLogger
	}

	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = DefaultHeartbeatInterval
	}

	if c.CacheRefreshInterval == 0 {
		c.CacheRefreshInterval = DefaultCacheRefreshInterval
	}

	if c.MaxCacheStaleness == 0 {
		c.MaxCacheStaleness = DefaultMaxCacheStaleness
	}

	if c.LeaderCheckInterval == 0 {
		c.LeaderCheckInterval = DefaultLeaderCheckInterval
	}

	if c.JitterRatio == 0 {
		c.JitterRatio = DefaultJitterRatio
	}

	if c.MaxBackoff == 0 {
		c.MaxBackoff = DefaultMaxBackoff
	}

	if c.RequestTimeout == 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
}
