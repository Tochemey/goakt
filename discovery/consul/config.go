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

// Config defines the configuration options for the Consul provider.
//
// It controls how the provider connects to Consul, registers the service,
// and configures discovery and health-check behavior.
type Config struct {
	// Context specifies the execution context for Consul operations.
	// If nil, context.Background() will be used.
	Context context.Context
	// Address is the address of the Consul agent to connect to.
	// Default: "127.0.0.1:8500"
	Address string
	// Datacenter specifies the Consul datacenter to use.
	// If empty, the agent's default datacenter is used.
	Datacenter string
	// Token is the Consul ACL token used for authenticated requests.
	Token string
	// Timeout specifies the maximum duration for Consul requests.
	// Default: 10s
	Timeout time.Duration
	// ActorSystemName is the name of the actor system.
	// It is used as the service identifier when registering with Consul.
	ActorSystemName string
	// Host is the hostname or IP address of the actor system.
	// It is used to register the service in Consul.
	Host string
	// DiscoveryPort is the TCP port on which the actor system listens
	// for service discovery requests.
	DiscoveryPort int
	// QueryOptions specifies advanced options for Consul queries.
	// May be nil for default behavior.
	QueryOptions *QueryOptions
	// HealthCheck configures the Consul health check for the registered service.
	// May be nil to disable health checks.
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
			OnlyPassing: false,
			AllowStale:  false,
			WaitTime:    time.Second,
			Datacenter:  config.Datacenter,
		}
	}

	if config.HealthCheck == nil {
		config.HealthCheck = defaultHealthCheck()
	}
}

// Validate checks if the configuration is valid.
func (config *Config) Validate() error {
	return validation.New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("ActorSystemName", config.ActorSystemName)).
		AddValidator(validation.NewEmptyStringValidator("Address", config.Address)).
		AddValidator(validation.NewEmptyStringValidator("Host", config.Host)).
		AddAssertion(config.DiscoveryPort > 0, "DiscoveryPort is invalid").
		Validate()
}

// QueryOptions defines advanced options for Consul service discovery queries.
//
// These options control how results are filtered, sorted, and fetched
// from Consul's catalog.
type QueryOptions struct {
	// OnlyPassing specifies whether to return only services with a passing health check.
	// If false, all services (healthy or not) may be returned.
	OnlyPassing bool

	// Near specifies a node name to sort results by network distance to that node.
	// If empty, no distance-based sorting is applied.
	Near string

	// WaitTime is the maximum duration to wait for changes when using Consul blocking queries.
	// If zero, the default agent wait time is used.
	WaitTime time.Duration

	// Datacenter specifies the Consul datacenter to query.
	// If empty, the agent's default datacenter is used.
	Datacenter string

	// AllowStale indicates whether stale results are acceptable.
	// When true, results may be served from follower nodes, improving availability
	// at the cost of potentially outdated data.
	AllowStale bool
}

// HealthCheck defines the configuration of a Consul health check
// associated with a registered service.
//
// Health checks allow Consul to automatically mark services as unhealthy
// if they fail within the configured thresholds.
type HealthCheck struct {
	// Interval is the frequency at which Consul performs the health check.
	// Default: 10s
	Interval time.Duration

	// Timeout is the maximum duration Consul waits for a health check response.
	// If the timeout is exceeded, the check is considered failed.
	// Default: 3s
	Timeout time.Duration
}

func defaultHealthCheck() *HealthCheck {
	return &HealthCheck{
		Interval: 10 * time.Second,
		Timeout:  3 * time.Second,
	}
}
