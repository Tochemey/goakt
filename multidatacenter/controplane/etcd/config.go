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

package etcd

import (
	"context"
	"crypto/tls"
	"strings"
	"time"

	"github.com/tochemey/goakt/v3/internal/validation"
)

const defaultNamespace = "/goakt/multidc/dcs"

// Config holds configuration for the etcd control plane provider.
type Config struct {
	// Context specifies the execution context for etcd operations.
	// If nil, context.Background() will be used.
	Context context.Context
	// Endpoints is a list of etcd cluster endpoints.
	Endpoints []string
	// Namespace scopes the control plane keys. Defaults to /goakt/multidc/dcs.
	Namespace string
	// TTL is the time-to-live for liveness leases.
	TTL time.Duration
	// TLS configures client TLS (optional).
	TLS *tls.Config
	// DialTimeout sets the timeout for establishing etcd connections.
	DialTimeout time.Duration
	// Timeout sets the timeout for etcd operations.
	Timeout time.Duration
	// Username sets the etcd authentication user (optional).
	Username string
	// Password sets the etcd authentication password (optional).
	Password string
}

var _ validation.Validator = (*Config)(nil)

// Validate implements validation.Validator.
func (c *Config) Validate() error {
	return validation.New(validation.FailFast()).
		AddAssertion(len(c.Endpoints) > 0, "Endpoints must not be empty").
		AddAssertion(c.TTL >= time.Second, "TTL must be at least 1s").
		AddAssertion(c.DialTimeout > 0, "DialTimeout must be greater than 0").
		AddAssertion(c.Timeout > 0, "Timeout must be greater than 0").
		Validate()
}

func (c *Config) Sanitize() {
	if c.Context == nil {
		c.Context = context.Background()
	}

	if strings.TrimSpace(c.Namespace) == "" {
		c.Namespace = defaultNamespace
	}

	if c.DialTimeout == 0 {
		c.DialTimeout = 5 * time.Second
	}

	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
}
