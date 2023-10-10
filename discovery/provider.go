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

// Provider helps discover other running actor system in a cloud environment
type Provider interface {
	// ID returns the discovery name
	ID() string
	// Initialize initializes the plugin: registers some internal data structures, clients etc.
	Initialize() error
	// Register registers this node to a service discovery directory.
	Register() error
	// Deregister removes this node from a service discovery directory.
	Deregister() error
	// SetConfig registers the underlying discovery options
	SetConfig(config Config) error
	// DiscoverPeers returns a list of known nodes.
	DiscoverPeers() ([]string, error)
}
