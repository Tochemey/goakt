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
	"sync/atomic"
)

type pidMap struct {
	mu   *sync.RWMutex
	size atomic.Int64
	pids map[string]PID
}

func newPIDMap(cap int) *pidMap {
	return &pidMap{
		mu:   &sync.RWMutex{},
		pids: make(map[string]PID, cap),
	}
}

// Len returns the number of PIDs
func (m *pidMap) Len() int {
	return int(m.size.Load())
}

// Get retrieves a pid by its address
func (m *pidMap) Get(path *Path) (pid PID, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pid, ok = m.pids[path.String()]
	return
}

// Set sets a pid in the map
func (m *pidMap) Set(pid PID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pid != nil {
		m.pids[pid.ActorPath().String()] = pid
		m.size.Add(1)
	}
}

// Delete removes a pid from the map
func (m *pidMap) Delete(addr *Path) {
	m.mu.Lock()
	delete(m.pids, addr.String())
	m.size.Add(-1)
	m.mu.Unlock()
}

// List returns all actors as a slice
func (m *pidMap) List() []PID {
	m.mu.Lock()
	var out []PID
	for _, actor := range m.pids {
		out = append(out, actor)
	}
	m.mu.Unlock()
	return out
}
