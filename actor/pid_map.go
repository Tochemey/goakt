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

package actor

import (
	csmap "github.com/mhmtszr/concurrent-swiss-map"
	"github.com/zeebo/xxh3"
)

type pidMap struct {
	mappings *csmap.CsMap[string, *PID]
}

func newPIDMap(cap int) *pidMap {
	m := csmap.Create[string, *PID](
		csmap.WithShardCount[string, *PID](32),
		csmap.WithCustomHasher[string, *PID](func(key string) uint64 {
			return xxh3.Hash([]byte(key))
		}),
		csmap.WithSize[string, *PID](uint64(cap)),
	)
	return &pidMap{
		mappings: m,
	}
}

// len returns the number of PIDs
func (m *pidMap) len() int {
	return m.mappings.Count()
}

// get retrieves a pid by its address
func (m *pidMap) get(path *Path) (pid *PID, ok bool) {
	return m.mappings.Load(path.String())
}

// set sets a pid in the map
func (m *pidMap) set(pid *PID) {
	m.mappings.Store(pid.ActorPath().String(), pid)
}

// delete removes a pid from the map
func (m *pidMap) delete(addr *Path) {
	m.mappings.Delete(addr.String())
}

// pids returns all actors as a slice
func (m *pidMap) pids() []*PID {
	var out []*PID
	m.mappings.Range(func(k string, v *PID) bool {
		out = append(out, v)
		return m.mappings.Count() == len(out)
	})
	return out
}

func (m *pidMap) reset() {
	m.mappings.Clear()
}
