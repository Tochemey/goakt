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

import (
	"sync"
	"sync/atomic"
)

// RoundRobin implements the round-robin algorithm
// to pick a particular nodes in the cluster
type RoundRobin struct {
	locker *sync.Mutex
	nodes  []*Node
	next   uint32
}

var _ Balancer = (*RoundRobin)(nil)

// NewRoundRobin creates an instance of RoundRobin
func NewRoundRobin() *RoundRobin {
	return &RoundRobin{
		nodes:  make([]*Node, 0),
		locker: &sync.Mutex{},
	}
}

// Set sets the balancer nodes pool
func (x *RoundRobin) Set(nodes ...*Node) {
	x.locker.Lock()
	x.nodes = nodes
	x.locker.Unlock()
}

// Next returns the next node in the pool
func (x *RoundRobin) Next() *Node {
	x.locker.Lock()
	defer x.locker.Unlock()
	n := atomic.AddUint32(&x.next, 1)
	return x.nodes[(int(n)-1)%len(x.nodes)]
}
