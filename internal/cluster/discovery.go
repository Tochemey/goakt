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

package cluster

import (
	golog "log"
	"strings"

	"github.com/buraksezer/olric/pkg/service_discovery"
	"github.com/pkg/errors"

	"github.com/tochemey/goakt/v2/discovery"
)

// discoveryProvider wraps the Cluster engine discovery and implements
// service_discovery.ServiceDiscovery
type discoveryProvider struct {
	provider discovery.Provider
	log      *golog.Logger
}

// enforce compilation error
var _ service_discovery.ServiceDiscovery = &discoveryProvider{}

// Initialize implementation
func (d *discoveryProvider) Initialize() error {
	if d.provider == nil {
		return errors.New("discovery provider is not set")
	}

	if err := d.provider.Initialize(); err != nil {
		if !errors.Is(err, discovery.ErrAlreadyInitialized) {
			return err
		}
	}

	return nil
}

// SetConfig implementation
func (d *discoveryProvider) SetConfig(c map[string]any) error {
	id, ok := c["id"]
	if !ok {
		return errors.New("discovery provider id is not set")
	}

	idVal := id.(string)
	if !strings.EqualFold(idVal, d.provider.ID()) {
		return errors.New("invalid discovery provider id")
	}

	return nil
}

// SetLogger implementation
func (d *discoveryProvider) SetLogger(l *golog.Logger) {
	d.log = l
}

// Register implementation
func (d *discoveryProvider) Register() error {
	if d.provider == nil {
		return errors.New("discovery provider is not set")
	}
	if err := d.provider.Register(); err != nil {
		if !errors.Is(err, discovery.ErrAlreadyRegistered) {
			return err
		}
	}

	return nil
}

// Deregister implementation
func (d *discoveryProvider) Deregister() error {
	if d.provider == nil {
		return errors.New("discovery provider is not set")
	}
	return d.provider.Deregister()
}

// DiscoverPeers implementation
func (d *discoveryProvider) DiscoverPeers() ([]string, error) {
	if d.provider == nil {
		return nil, errors.New("discovery provider is not set")
	}

	return d.provider.DiscoverPeers()
}

// Close implementation
func (d *discoveryProvider) Close() error {
	return d.provider.Close()
}
