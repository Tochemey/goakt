/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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
	"math/rand"
	"sync"
	"time"
)

// Random helps pick a node at random
type Random struct {
	locker sync.Mutex
	nodes  []string
	rnd    *rand.Rand
}

var _ Balancer = (*Random)(nil)

// NewRandom creates an instance of Random balancer
func NewRandom(nodes ...string) *Random {
	return &Random{
		nodes:  nodes,
		locker: sync.Mutex{},
		rnd:    rand.New(rand.NewSource(time.Now().UTC().UnixNano())), //nolint:gosec
	}
}

// Next returns the next node in the pool
func (x *Random) Next() string {
	x.locker.Lock()
	defer x.locker.Unlock()
	return x.nodes[x.rnd.Intn(len(x.nodes))]
}
