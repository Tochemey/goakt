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

// RoutingStrategy defines how a router actor forwards incoming messages to its routees.
//
// Available strategies:
//   - RoundRobinRouting:
//     Distributes messages one at a time to each routee in sequence.
//     Useful for balancing uniform, stateless workloads.
//     Example:
//     // creates a router that round-robins messages across 5 workers
//     r := newRouter(5, &MyWorker{}, logger, WithRoutingStrategy(RoundRobinRouting))
//   - RandomRouting:
//     Chooses a routee at random for every message.
//     Useful when uneven load patterns are acceptable or desired.
//     Example:
//     r := newRouter(10, &MyWorker{}, logger, WithRoutingStrategy(RandomRouting))
//   - FanOutRouting:
//     Broadcasts each message to all active routees concurrently.
//     Useful for pub/sub, cache invalidation, or multi-sink processing.
//     Example:
//     r := newRouter(3, &EventConsumer{}, logger, WithRoutingStrategy(FanOutRouting))
//   - ConsistentHashRouting:
//     Routes messages with the same key to the same routee using a consistent hash ring.
//     Requires a KeyExtractor provided via WithConsistentHashRouter. Useful for sticky
//     sessions, per-entity routing, or partitioned caches.
//     Example:
//     r := newRouter(5, &MyWorker{}, logger, WithConsistentHashRouter(myExtractor))
//
// Note: If a routee stops, it is removed from the internal map and no longer receives messages.
type RoutingStrategy int

const (
	// RoundRobinRouting sends each incoming message to the next routee in order,
	// cycling back to the first after the last. Provides even distribution.
	RoundRobinRouting RoutingStrategy = iota
	// RandomRouting selects a routee uniformly at random for each message.
	RandomRouting
	// FanOutRouting broadcasts every message to all currently available routees.
	FanOutRouting
	// ConsistentHashRouting routes messages with the same key (as determined by a
	// KeyExtractor) to the same routee. Adding or removing routees only remaps
	// keys that were assigned to the changed node; all other mappings are stable.
	ConsistentHashRouting
)

// MessageRoutingKeyExtractor derives a routing key from a message for consistent-hash routing.
// Return an empty string to fall back to random routing for that message.
type MessageRoutingKeyExtractor func(msg any) string
