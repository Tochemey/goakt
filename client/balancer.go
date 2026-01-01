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

package client

// BalancerStrategy defines the strategy used by the Client to determine
// how to distribute actors across available nodes.
//
// These strategies are used to influence actor placement when spawning
// with balancing enabled, allowing for flexible and optimized load distribution.
type BalancerStrategy int

const (
	// RoundRobinStrategy distributes actors evenly across nodes by cycling
	// through the available nodes in a round-robin fashion.
	RoundRobinStrategy BalancerStrategy = iota

	// RandomStrategy selects a node at random from the pool of available nodes.
	// This strategy can help achieve quick distribution without maintaining state.
	RandomStrategy

	// LeastLoadStrategy selects the node with the lowest current weight or load
	// at the time of actor placement. This strategy aims to optimize resource
	// utilization by placing actors on underutilized nodes.
	LeastLoadStrategy
)

// Balancer helps locate the right node to channel Client request to
type Balancer interface {
	// Set sets the balancer nodes pool
	Set(nodes ...*Node)
	// Next returns the appropriate weight-balanced node to use
	Next() *Node
}
