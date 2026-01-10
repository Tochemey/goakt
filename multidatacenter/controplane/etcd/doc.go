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

// Package etcd provides a Multi-DC ControlPlane implementation backed by etcd.
//
// # Overview
//
// This package uses etcd (v3 API) as the coordination and metadata store for
// GoAkt's multi-datacenter features. It is intended for data center discovery and
// coordination (e.g. membership/registry data, coordination state), not for
// storing application payloads.
//
// # Data model
//
// The implementation persists small pieces of control-plane metadata under a
// configurable key prefix and relies on etcd primitives such as leases and
// watches to keep membership and coordination state up to date.
//
// Operational notes
//
//   - An etcd cluster must be reachable by all participating nodes.
//   - Authentication and TLS configuration (if enabled) are handled via the
//     etcd client configuration used by this ControlPlane.
//   - Key prefix isolation is recommended when multiple GoAkt clusters share the
//     same etcd cluster.
//   - Any breaking changes to stored keys/values are handled by this package;
//     treat stored data as internal to GoAkt.
//
// Package etcd is safe for concurrent use unless documented otherwise by a
// specific type.
package etcd
