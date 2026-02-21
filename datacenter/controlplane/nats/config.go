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

package nats

import (
	"context"
	"strings"
	"time"

	"github.com/tochemey/goakt/v4/internal/validation"
)

const defaultBucket = "goakt_datacenters"

// Config holds configuration for the NATS JetStream control plane provider.
type Config struct {
	// Context specifies the execution context for NATS operations.
	// If nil, context.Background() will be used.
	Context context.Context
	// URL is the NATS server URL (e.g. nats://127.0.0.1:4222).
	URL string
	// Bucket is the JetStream KeyValue bucket name for datacenter records.
	// Must be alphanumeric, dashes, or underscores. Defaults to goakt_datacenters.
	Bucket string
	// TTL is the time-to-live for keys; each Put/Update resets the key age.
	// Used as bucket TTL for liveness.
	TTL time.Duration
	// Timeout sets the timeout for NATS/JetStream operations.
	Timeout time.Duration
	// ConnectTimeout sets the timeout for establishing the NATS connection.
	ConnectTimeout time.Duration
}

var _ validation.Validator = (*Config)(nil)

// Validate implements validation.Validator.
func (c *Config) Validate() error {
	return validation.New(validation.FailFast()).
		AddAssertion(strings.TrimSpace(c.URL) != "", "URL must not be empty").
		AddAssertion(c.TTL >= time.Second, "TTL must be at least 1s").
		AddAssertion(c.Timeout > 0, "Timeout must be greater than 0").
		AddAssertion(c.ConnectTimeout > 0, "ConnectTimeout must be greater than 0").
		Validate()
}

// Sanitize sets defaults for empty fields.
func (c *Config) Sanitize() {
	if c.Context == nil {
		c.Context = context.Background()
	}
	if strings.TrimSpace(c.Bucket) == "" {
		c.Bucket = defaultBucket
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 5 * time.Second
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
}
