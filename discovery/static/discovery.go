/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package static

import (
	"github.com/tochemey/goakt/v2/discovery"
)

// Discovery represents the static discovery provider
type Discovery struct {
	config *Config
}

// enforce compilation error
var _ discovery.Provider = &Discovery{}

// NewDiscovery creates an instance of the static discovery provider
func NewDiscovery(config *Config) *Discovery {
	d := &Discovery{
		config: config,
	}

	return d
}

// ID returns the discovery provider identifier
func (d *Discovery) ID() string {
	return "static"
}

// Initialize the discovery provider
func (d *Discovery) Initialize() error {
	return d.config.Validate()
}

// Register registers this node to a service discovery directory.
func (d *Discovery) Register() error {
	return nil
}

// Deregister removes this node from a service discovery directory.
func (d *Discovery) Deregister() error {
	return nil
}

// Close closes the provider
func (d *Discovery) Close() error {
	return nil
}

// DiscoverPeers returns a list of known nodes.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	return d.config.Hosts, nil
}
