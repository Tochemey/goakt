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
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/tochemey/goakt/internal/types"
)

type prop struct {
	pid   PID
	rtype reflect.Type
}

type pidMap struct {
	mu       *sync.RWMutex
	size     atomic.Int64
	propsMap map[string]*prop
}

func newPIDMap(cap int) *pidMap {
	return &pidMap{
		mu:       &sync.RWMutex{},
		propsMap: make(map[string]*prop, cap),
	}
}

// len returns the number of PIDs
func (m *pidMap) len() int {
	return int(m.size.Load())
}

// get retrieves a pid by its address
func (m *pidMap) get(path *Path) (pid PID, ok bool) {
	m.mu.RLock()
	prop, ok := m.propsMap[path.String()]
	if prop != nil {
		pid = prop.pid
		ok = true
	}
	m.mu.RUnlock()
	return
}

// set sets a pid in the map
func (m *pidMap) set(pid PID) {
	m.mu.Lock()
	if pid != nil {
		var rtype reflect.Type
		handle := pid.ActorHandle()
		if handle != nil {
			rtype = types.RuntimeTypeOf(handle)
		}

		m.propsMap[pid.ActorPath().String()] = &prop{
			pid:   pid,
			rtype: rtype,
		}
		m.size.Add(1)
		m.mu.Unlock()
	}
}

// delete removes a pid from the map
func (m *pidMap) delete(addr *Path) {
	m.mu.Lock()
	delete(m.propsMap, addr.String())
	m.size.Add(-1)
	m.mu.Unlock()
}

// pids returns all actors as a slice
func (m *pidMap) pids() []PID {
	m.mu.Lock()
	var out []PID
	for _, prop := range m.propsMap {
		out = append(out, prop.pid)
	}
	m.mu.Unlock()
	return out
}

func (m *pidMap) props() map[string]*prop {
	m.mu.Lock()
	out := m.propsMap
	m.mu.Unlock()
	return out
}
