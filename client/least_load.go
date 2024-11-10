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
	"cmp"
	"slices"
	"sync"
)

// LeastLoad uses the LeastLoadStrategy to pick the next available node
// in the pool
type LeastLoad struct {
	locker *sync.Mutex
	nodes  []*Node
}

var _ Balancer = (*LeastLoad)(nil)

// NewLeastLoad creates an instance of LeastLoad
func NewLeastLoad() *LeastLoad {
	return &LeastLoad{
		nodes:  make([]*Node, 0),
		locker: &sync.Mutex{},
	}
}

// Set sets the balancer nodes pool
func (x *LeastLoad) Set(nodes ...*Node) {
	x.locker.Lock()
	x.nodes = nodes
	x.locker.Unlock()
}

// Next returns the next node in the pool
func (x *LeastLoad) Next() *Node {
	x.locker.Lock()
	defer x.locker.Unlock()
	slices.SortStableFunc(x.nodes, func(a, b *Node) int {
		return cmp.Compare(a.Weight(), b.Weight())
	})
	return x.nodes[0]
}
