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

package actors

import (
	"sync"

	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v2/address"
)

type pidMap struct {
	mu       *sync.RWMutex
	size     atomic.Int32
	mappings map[string]*PID
}

func newPIDMap(cap int) *pidMap {
	return &pidMap{
		mappings: make(map[string]*PID, cap),
		mu:       &sync.RWMutex{},
	}
}

// len returns the number of PIDs
func (m *pidMap) len() int {
	return int(m.size.Load())
}

// get retrieves a pid by its address
func (m *pidMap) get(address *address.Address) (pid *PID, ok bool) {
	m.mu.RLock()
	pid, ok = m.mappings[address.String()]
	m.mu.RUnlock()
	return
}

// set sets a pid in the map
func (m *pidMap) set(pid *PID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pid != nil {
		m.mappings[pid.Address().String()] = pid
		m.size.Add(1)
	}
}

// delete removes a pid from the map
func (m *pidMap) delete(addr *address.Address) {
	m.mu.Lock()
	delete(m.mappings, addr.String())
	m.size.Add(-1)
	m.mu.Unlock()
}

// pids returns all actors as a slice
func (m *pidMap) pids() []*PID {
	m.mu.Lock()
	var out []*PID
	for _, prop := range m.mappings {
		out = append(out, prop)
	}
	m.mu.Unlock()
	return out
}

func (m *pidMap) reset() {
	m.mu.Lock()
	m.mappings = make(map[string]*PID)
	m.mu.Unlock()
}
