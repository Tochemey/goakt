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

import "sync"

// Node represents the node in the cluster
type Node struct {
	address string
	weight  float64
	mutex   *sync.Mutex
}

// NewNode creates an instance of Node
func NewNode(address string, weight int) *Node {
	return &Node{
		address: address,
		weight:  float64(weight),
		mutex:   &sync.Mutex{},
	}
}

// SetWeight sets the node weight.
// This is thread safe
func (n *Node) SetWeight(weight float64) {
	n.mutex.Lock()
	n.weight = weight
	n.mutex.Unlock()
}

// addr returns the node address
func (n *Node) Address() string {
	n.mutex.Lock()
	address := n.address
	n.mutex.Unlock()
	return address
}

// Weight returns the node weight
func (n *Node) Weight() float64 {
	n.mutex.Lock()
	load := n.weight
	n.mutex.Unlock()
	return load
}
