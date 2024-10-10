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

	"github.com/tochemey/goakt/v2/address"
)

type syncMap struct {
	sync.Map
	counter uint64
}

func newSyncMap() *syncMap {
	return &syncMap{
		counter: 0,
	}
}

// Size returns the number of List
func (m *syncMap) Size() int {
	return int(atomic.LoadUint64(&m.counter))
}

// Get retrieves a pid by its address
func (m *syncMap) Get(address *address.Address) (pid *PID, ok bool) {
	if val, found := m.Load(address.String()); found {
		return val.(*PID), found
	}
	return
}

// Set sets a pid in the map
func (m *syncMap) Set(pid *PID) {
	if pid != nil {
		m.Store(pid.Address().String(), pid)
		atomic.AddUint64(&m.counter, 1)
	}
}

// Remove removes a pid from the map
func (m *syncMap) Remove(addr *address.Address) {
	m.Delete(addr.String())
	atomic.AddUint64(&m.counter, ^uint64(0))
}

// List returns all actors as a slice
func (m *syncMap) List() []*PID {
	var out []*PID
	m.Range(func(_, value interface{}) bool {
		out = append(out, value.(*PID))
		return !(m.Size() == len(out))
	})
	return out
}

// Reset resets the pids map
// nolint
func (m *syncMap) Reset() {
	// TODO: remove this line when migrated to go 1.23
	//m.Clear()
	m.Range(func(key interface{}, value interface{}) bool {
		m.Delete(key)
		return true
	})
	atomic.StoreUint64(&m.counter, 0)
}
