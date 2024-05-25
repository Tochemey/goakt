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

	"github.com/alphadose/haxmap"

	"github.com/tochemey/goakt/v2/internal/types"
)

type prop struct {
	pid   PID
	rtype reflect.Type
}

type pidMap struct {
	mappings *haxmap.Map[string, *prop]
}

func newPIDMap(cap int) *pidMap {
	return &pidMap{
		mappings: haxmap.New[string, *prop](uintptr(cap)),
	}
}

// len returns the number of PIDs
func (m *pidMap) len() int {
	return int(m.mappings.Len())
}

// get retrieves a pid by its address
func (m *pidMap) get(path *Path) (pid PID, ok bool) {
	prop, ok := m.mappings.Get(path.String())
	if prop != nil {
		pid = prop.pid
		ok = true
	}
	return
}

// set sets a pid in the map
func (m *pidMap) set(pid PID) {
	if pid != nil {
		var rtype reflect.Type
		handle := pid.ActorHandle()
		if handle != nil {
			rtype = types.RuntimeTypeOf(handle)
		}

		m.mappings.Set(pid.ActorPath().String(), &prop{
			pid:   pid,
			rtype: rtype,
		})
	}
}

// delete removes a pid from the map
func (m *pidMap) delete(addr *Path) {
	m.mappings.Del(addr.String())
}

// pids returns all actors as a slice
func (m *pidMap) pids() []PID {
	var out []PID
	m.mappings.ForEach(func(_ string, prop *prop) bool {
		if len(out) == int(m.mappings.Len()) {
			return false
		}
		out = append(out, prop.pid)
		return true
	})
	return out
}

func (m *pidMap) props() map[string]*prop {
	out := make(map[string]*prop, m.mappings.Len())
	m.mappings.ForEach(func(key string, prop *prop) bool {
		if len(out) == int(m.mappings.Len()) {
			return false
		}
		out[key] = prop
		return true
	})
	return out
}

func (m *pidMap) close() {
	m.mappings.Clear()
}
