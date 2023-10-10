/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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

package discovery

// ServiceDiscovery defines the cluster service discovery
type ServiceDiscovery struct {
	// provider specifies the discovery provider
	provider Provider
	// config specifies the discovery config
	config Config
}

// NewServiceDiscovery creates an instance of ServiceDiscovery
func NewServiceDiscovery(provider Provider, config Config) *ServiceDiscovery {
	return &ServiceDiscovery{
		provider: provider,
		config:   config,
	}
}

// Provider returns the service discovery provider
func (s ServiceDiscovery) Provider() Provider {
	return s.provider
}

// Config returns the service discovery config
func (s ServiceDiscovery) Config() Config {
	return s.config
}
