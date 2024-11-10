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

package client

// BalancerStrategy defines the Client weight balancer strategy
type BalancerStrategy int

const (
	// RoundRobinStrategy uses the round-robin algorithm to determine the appropriate node
	RoundRobinStrategy BalancerStrategy = iota
	// RandomStrategy uses the random algorithm to determine the appropriate node
	RandomStrategy
	// LeastLoadStrategy choses among a pool of nodes the node that has the less weight
	// at the time of the execution
	LeastLoadStrategy
)

// Balancer helps locate the right node to channel Client request to
type Balancer interface {
	// Set sets the balancer nodes pool
	Set(nodes ...*Node)
	// Next returns the appropriate weight-balanced node to use
	Next() *Node
}
