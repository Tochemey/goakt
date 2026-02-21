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

package actor

import (
	"time"

	"github.com/tochemey/goakt/v4/internal/pointer"
)

// clusterSingletonConfig holds configuration options for cluster singleton actors.
type clusterSingletonConfig struct {
	// role defines the role required for the node to spawn the singleton actor.
	role *string
	// spawnTimeout defines the maximum duration to wait for the singleton to be spawned.
	spawnTimeout time.Duration
	// waitInterval defines the interval between spawn status checks.
	waitInterval time.Duration
	// numberOfRetries defines the number of retries to check for singleton spawn status.
	numberOfRetries int
}

// newClusterSingletonConfig creates a new cluster singleton configuration.
func newClusterSingletonConfig(opts ...ClusterSingletonOption) *clusterSingletonConfig {
	csc := &clusterSingletonConfig{
		spawnTimeout:    30 * time.Second,
		waitInterval:    500 * time.Millisecond,
		numberOfRetries: 5,
	}
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

// WithSingletonSpawnTimeout sets the maximum amount of time `SpawnSingleton` will spend
// retrying before giving up.
//
// Notes:
//   - Values <= 0 are ignored and the existing/default timeout is kept.
//   - This timeout is an overall upper bound; retry attempts (see
//     `WithSingletonSpawnWaitInterval` and `WithSingletonSpawnRetries`) may cause
//     `SpawnSingleton` to return earlier if retries are exhausted.
//   - Default: 30 seconds.
func WithSingletonSpawnTimeout(timeout time.Duration) ClusterSingletonOption {
	return func(c *clusterSingletonConfig) {
		if timeout > 0 {
			c.spawnTimeout = timeout
		}
	}
}

// WithSingletonSpawnWaitInterval sets the base delay between `SpawnSingleton` retry attempts.
//
// Notes:
//   - Values <= 0 are ignored and the existing/default interval is kept.
//   - Used together with `WithSingletonSpawnRetries` to define the retry budget.
//     Approximate retry window: `tries * interval` (best-effort; scheduling can add jitter).
//   - Default: 500 milliseconds.
func WithSingletonSpawnWaitInterval(interval time.Duration) ClusterSingletonOption {
	return func(c *clusterSingletonConfig) {
		if interval > 0 {
			c.waitInterval = interval
		}
	}
}

// WithSingletonSpawnRetries sets the maximum number of `SpawnSingleton` attempts before giving up.
//
// Notes:
//   - Values <= 0 are ignored and the existing/default retry count is kept.
//   - Combined with `WithSingletonSpawnWaitInterval`, this controls how long `SpawnSingleton`
//     will keep retrying (roughly `tries * interval`), up to the overall timeout set by
//     `WithSingletonSpawnTimeout`.
//   - Default: 5.
func WithSingletonSpawnRetries(retries int) ClusterSingletonOption {
	return func(c *clusterSingletonConfig) {
		if retries > 0 {
			c.numberOfRetries = retries
		}
	}
}
