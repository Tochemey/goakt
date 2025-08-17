/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package consul

import (
	"context"
	"time"

	"github.com/tochemey/goakt/v3/internal/validation"
)

// Config holds the configuration for the Consul provider
type Config struct {
	// Optional context for operations; nil means background context will be used
	Context context.Context

	// Consul client configuration
	Address    string        // Consul agent address (default: "127.0.0.1:8500")
	Datacenter string        // Consul datacenter
	Token      string        // Consul ACL token
	Timeout    time.Duration // Request timeout (default: 10s)

	ActorSystemName string // Actor system name (used for service discovery)
	Host            string // Host is the actor system host. It is used to register the service in Consul
	PeersPort       int    // PeersPort is the port on which the Actor System peers communicate in the cluster

	// Discovery configuration
	QueryOptions *QueryOptions // Advanced query options

	// Health check configuration
	HealthCheck *HealthCheck
}

var _ validation.Validator = (*Config)(nil)

// Sanitize ensures the configuration is valid and sets defaults.
func (config *Config) Sanitize() {
	if config.Context == nil {
		config.Context = context.Background()
	}

	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}

	// Set query options defaults
	if config.QueryOptions == nil {
		config.QueryOptions = &QueryOptions{
			OnlyPassing: true,
			AllowStale:  false,
			WaitTime:    time.Second,
			Datacenter:  config.Datacenter,
		}
	}

	if config.HealthCheck != nil {
		if config.HealthCheck.Interval == 0 {
			config.HealthCheck.Interval = 10 * time.Second
		}
		if config.HealthCheck.Timeout == 0 {
			config.HealthCheck.Timeout = 3 * time.Second
		}
	}
}

// Validate checks if the configuration is valid.
func (config *Config) Validate() error {
	return validation.New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("ActorSystemName", config.ActorSystemName)).
		AddValidator(validation.NewEmptyStringValidator("Address", config.Address)).
		AddValidator(validation.NewEmptyStringValidator("Host", config.Host)).
		AddAssertion(config.PeersPort > 0, "PeersPort is invalid").
		Validate()
}

// QueryOptions defines options for service discovery queries
type QueryOptions struct {
	OnlyPassing bool          // Only return healthy services
	Near        string        // Sort by distance to this node
	WaitTime    time.Duration // Maximum time to wait for changes
	Datacenter  string        // Datacenter to query
	AllowStale  bool          // Allow stale reads
}

// HealthCheck defines health check configuration
type HealthCheck struct {
	HTTP     string        // HTTP endpoint for health checks
	TCP      string        // TCP address for health checks
	Interval time.Duration // Health check interval (default: 10s)
	Timeout  time.Duration // Health check timeout (default: 3s)
	TTL      time.Duration // TTL for TTL health checks
}
