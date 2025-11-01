/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actor

import "github.com/tochemey/goakt/v3/internal/pointer"

// clusterSingletonConfig holds configuration options for cluster singleton actors.
type clusterSingletonConfig struct {
	// role defines the role required for the node to spawn the singleton actor.
	role *string
}

// newClusterSingletonConfig creates a new cluster singleton configuration.
func newClusterSingletonConfig(opts ...ClusterSingletonOption) *clusterSingletonConfig {
	csc := &clusterSingletonConfig{}
	for _, opt := range opts {
		opt(csc)
	}
	return csc
}

// Role returns the role required for the node to spawn the singleton actor.
func (x clusterSingletonConfig) Role() *string {
	return x.role
}

// ClusterSingletonOption defines a function type for configuring cluster singleton actors.
type ClusterSingletonOption func(*clusterSingletonConfig)

// WithSingletonRole pins the singleton to cluster members that advertise the specified role.
//
// When a role is provided, the actor system picks the oldest node in the cluster that reports
// the role and spawns (or relocates) the singleton there. Nodes without the role will never
// host the singleton; when no matching members exist, `SpawnSingleton` returns an error.
//
// Passing the empty string is a no-op and leaves the singleton eligible for placement on the
// overall oldest cluster member.
//
// Example:
//
//	if err := system.SpawnSingleton(
//		ctx,
//		"scheduler",
//		NewSchedulerActor(),
//		WithSingletonRole("control-plane"),
//	); err != nil {
//		return err
//	}
func WithSingletonRole(role string) ClusterSingletonOption {
	return func(c *clusterSingletonConfig) {
		if role != "" {
			c.role = pointer.To(role)
		}
	}
}
