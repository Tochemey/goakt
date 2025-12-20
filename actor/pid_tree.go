/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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
	"errors"
	"sync"

	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/internal/ds"
)

// pidNode represents a node in the PID tree.
type pidNode struct {
	pid         atomic.Pointer[PID]       // PID associated with this node.
	parent      atomic.Pointer[PID]       // Parent PID; nil if root.
	watchers    *ds.Map[string, *PID]     // Actors watching this node.
	watchees    *ds.Map[string, *PID]     // Actors this node is watching.
	descendants *ds.Map[string, *pidNode] // Direct children.
}

// value returns the PID stored in the node, or nil if not set.
func (n *pidNode) value() *PID {
	return n.pid.Load()
}

// tree maintains actor relationships in a concurrency-safe structure.
type tree struct {
	sync.RWMutex
	rootNode *pidNode                  // Logical root node (its pid may be nil if cleared).
	pids     *ds.Map[string, *pidNode] // Index: PID.ID() -> pidNode.
	names    *ds.Map[string, *pidNode] // Index: PID.Name() -> pidNode.
	counter  *atomic.Int64             // Number of nodes currently registered.
	noSender *PID                      // Cached NoSender (set on first root add).
}

// newTree creates and returns a new PID tree.
// Time Complexity: O(1).
// Space Complexity: O(1) (excluding the internal empty maps allocated).
func newTree() *tree {
	return &tree{
		pids:    ds.NewMap[string, *pidNode](),
		names:   ds.NewMap[string, *pidNode](),
		counter: atomic.NewInt64(0),
		rootNode: &pidNode{
			pid:         atomic.Pointer[PID]{},
			watchers:    ds.NewMap[string, *PID](),
			watchees:    ds.NewMap[string, *PID](),
			descendants: ds.NewMap[string, *pidNode](),
		},
	}
}

// addRootNode registers the root PID in the tree.
// Fails if pid is nil, already present, or equals NoSender.
// Time Complexity: O(1) (amortized for map operations).
// Space Complexity: O(1) additional.
func (x *tree) addRootNode(pid *PID) error {
	x.Lock()
	defer x.Unlock()

	if pid == nil {
		return errors.New("pid is nil")
	}

	// Cache NoSender once.
	if x.noSender == nil {
		x.noSender = pid.ActorSystem().NoSender()
	}
	if pid.Equals(x.noSender) {
		return errors.New("pid cannot be NoSender")
	}
	if _, exists := x.pids.Get(pid.ID()); exists {
		return errors.New("pid already exists")
	}

	x.rootNode.pid.Store(pid)
	x.pids.Set(pid.ID(), x.rootNode)
	x.names.Set(pid.Name(), x.rootNode)
	x.counter.Inc()
	return nil
}

// addNode inserts a child PID under the given parent PID.
// Fails if inputs are invalid, parent does not exist, or pid already exists.
// Establishes watcher/watchee links between parent and child.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1) additional (one node plus map entries).
func (x *tree) addNode(parent, pid *PID) error {
	x.Lock()
	defer x.Unlock()

	if pid == nil {
		return errors.New("pid is nil")
	}

	if parent == nil {
		return errors.New("parent pid is nil")
	}

	// Ensure NoSender cached (in case addRootNode was not yet called—defensive).
	if x.noSender == nil {
		x.noSender = pid.ActorSystem().NoSender()
	}

	if parent.Equals(x.noSender) {
		return errors.New("parent pid cannot be NoSender")
	}

	if _, exists := x.pids.Get(pid.ID()); exists {
		return errors.New("pid already exists")
	}

	parentNode, ok := x.pids.Get(parent.ID())
	if !ok {
		return errors.New("parent pid does not exist")
	}

	childNode := &pidNode{
		pid:         atomic.Pointer[PID]{},
		watchers:    ds.NewMap[string, *PID](),
		watchees:    ds.NewMap[string, *PID](),
		descendants: ds.NewMap[string, *pidNode](),
	}
	childNode.pid.Store(pid)
	childNode.parent.Store(parent)

	id := pid.ID()
	parentNode.descendants.Set(id, childNode)
	parentNode.watchees.Set(id, pid)
	childNode.watchers.Set(parent.ID(), parent)

	x.pids.Set(id, childNode)
	x.names.Set(pid.Name(), childNode)
	x.counter.Inc()
	return nil
}

// addWatcher registers watcher to watch pid.
// Silently no-ops if any PID is nil, NoSender, or not found.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1) additional per watcher relationship.
func (x *tree) addWatcher(pid, watcher *PID) {
	x.Lock()
	defer x.Unlock()

	if pid == nil || watcher == nil {
		return
	}

	if x.noSender != nil {
		if pid.Equals(x.noSender) || watcher.Equals(x.noSender) {
			return
		}
	} else {
		// Fallback (should be rare).
		noSender := pid.ActorSystem().NoSender()
		if pid.Equals(noSender) || watcher.Equals(noSender) {
			return
		}
	}

	pidNode, ok := x.pids.Get(pid.ID())
	if !ok {
		return
	}

	watcherNode, ok := x.pids.Get(watcher.ID())
	if !ok {
		return
	}

	pidNode.watchers.Set(watcher.ID(), watcher)
	watcherNode.watchees.Set(pid.ID(), pid)
}

// deleteNode removes pid and its entire subtree (all descendants).
// Also cleans all watcher/watchee relationships involving those nodes.
// Time Complexity: O(k + e) where k is number of nodes in the subtree,
// and e is the total number of watcher/watchee edges touching them.
// Space Complexity: O(k) for traversal stacks plus O(1) auxiliary.
// No-ops if pid is nil, NoSender, or unknown.
func (x *tree) deleteNode(pid *PID) {
	x.Lock()
	defer x.Unlock()

	if pid == nil {
		return
	}

	if x.noSender != nil && pid.Equals(x.noSender) {
		return
	} else if x.noSender == nil { // Defensive fallback.
		noSender := pid.ActorSystem().NoSender()
		if pid.Equals(noSender) {
			return
		}
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok {
		return
	}

	// Iterative stack for subtree traversal (post-order style via two slices).
	stack := make([]*pidNode, 0, 8)
	post := make([]*pidNode, 0, 8)
	stack = append(stack, node)

	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == nil {
			continue
		}
		post = append(post, n)
		n.descendants.Range(func(_ string, child *pidNode) {
			stack = append(stack, child)
		})
	}

	// Process nodes in reverse order so children handled before parents.
	for i := len(post) - 1; i >= 0; i-- {
		n := post[i]
		p := n.pid.Load()
		if p == nil {
			continue
		}
		name := p.Name()

		// Clean watchers.
		n.watchers.Range(func(_ string, w *PID) {
			if watcherNode, exists := x.pids.Get(w.ID()); exists {
				watcherNode.watchees.Delete(p.ID())
			}
		})
		n.watchers.Reset()

		// Clean watchees.
		n.watchees.Range(func(_ string, w *PID) {
			if watcheeNode, exists := x.pids.Get(w.ID()); exists {
				watcheeNode.watchers.Delete(p.ID())
			}
		})
		n.watchees.Reset()

		// Unlink from parent.
		if parentPID := n.parent.Load(); parentPID != nil {
			if parentNode, exists := x.pids.Get(parentPID.ID()); exists {
				parentNode.descendants.Delete(p.ID())
				parentNode.watchees.Delete(p.ID())
			}
		}

		x.pids.Delete(p.ID())
		if current, ok := x.names.Get(name); ok && current == n {
			x.names.Delete(name)
		}
		n.parent.Store(nil)
		n.pid.Store(nil)
		x.counter.Dec()
	}
}

// node returns the internal pidNode by ID.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1).
func (x *tree) node(id string) (*pidNode, bool) {
	x.RLock()
	defer x.RUnlock()
	n, ok := x.pids.Get(id)
	return n, ok
}

func (x *tree) nodeByName(name string) (*pidNode, bool) {
	if name == "" {
		return nil, false
	}
	node, ok := x.names.Get(name)
	return node, ok
}

// nodes returns all pidNodes currently registered.
// Time Complexity: O(n) where n is the number of nodes.
// Space Complexity: O(n) for the returned slice.
func (x *tree) nodes() []*pidNode {
	x.RLock()
	defer x.RUnlock()
	result := make([]*pidNode, 0, x.counter.Load())
	x.pids.Range(func(_ string, n *pidNode) {
		result = append(result, n)
	})
	return result
}

// siblings returns all sibling PIDs of pid (excluding pid itself).
// Returns nil if pid is nil/NoSender/unknown; returns empty slice if none.
// Time Complexity: O(d) where d is the number of children of the parent.
// Space Complexity: O(d) for the result slice (worst case).
func (x *tree) siblings(pid *PID) []*PID {
	x.RLock()
	defer x.RUnlock()

	if pid == nil {
		return nil
	}
	if x.noSender != nil && pid.Equals(x.noSender) {
		return nil
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok || node.parent.Load() == nil {
		return nil
	}
	parentNode, ok := x.pids.Get(node.parent.Load().ID())
	if !ok {
		return nil
	}

	size := parentNode.descendants.Len()
	if size <= 1 {
		return []*PID{}
	}
	sibs := make([]*PID, 0, size-1)
	parentNode.descendants.Range(func(_ string, s *pidNode) {
		if sp := s.pid.Load(); sp != nil && !sp.Equals(pid) {
			sibs = append(sibs, sp)
		}
	})
	return sibs
}

// children returns the direct children of pid.
// Returns nil if pid is nil/NoSender/unknown; empty slice if no children.
// Time Complexity: O(c) where c is the number of direct children.
// Space Complexity: O(c) for the result slice.
func (x *tree) children(pid *PID) []*PID {
	x.RLock()
	defer x.RUnlock()

	if pid == nil {
		return nil
	}
	if x.noSender != nil && pid.Equals(x.noSender) {
		return nil
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok {
		return nil
	}

	result := make([]*PID, 0, node.descendants.Len())
	node.descendants.Range(func(_ string, child *pidNode) {
		if cp := child.pid.Load(); cp != nil {
			result = append(result, cp)
		}
	})
	return result
}

// descendants returns all descendant PIDs of pid (depth-first, no order guarantee).
// Returns nil if pid is nil/NoSender/unknown; empty slice if no descendants.
// Time Complexity: O(k) where k is number of descendants.
// Space Complexity: O(k) for traversal stack + result slice.
func (x *tree) descendants(pid *PID) []*PID {
	x.RLock()
	defer x.RUnlock()

	if pid == nil {
		return nil
	}

	if x.noSender != nil && pid.Equals(x.noSender) {
		return nil
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok {
		return nil
	}

	if node.descendants.Len() == 0 {
		return []*PID{}
	}

	stack := make([]*pidNode, 0, node.descendants.Len())
	node.descendants.Range(func(_ string, child *pidNode) {
		stack = append(stack, child)
	})

	result := make([]*PID, 0, len(stack))
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == nil {
			continue
		}
		if p := n.pid.Load(); p != nil {
			result = append(result, p)
		}
		n.descendants.Range(func(_ string, c *pidNode) {
			stack = append(stack, c)
		})
	}
	return result
}

// reset clears the entire tree (all nodes removed) but preserves cached noSender.
// Time Complexity: O(n) in effect (map reset may drop references over n nodes).
// Space Complexity: O(1) additional (old structures become GC candidates).
func (x *tree) reset() {
	x.Lock()
	defer x.Unlock()

	x.rootNode = &pidNode{
		pid:         atomic.Pointer[PID]{},
		watchers:    ds.NewMap[string, *PID](),
		watchees:    ds.NewMap[string, *PID](),
		descendants: ds.NewMap[string, *pidNode](),
	}
	x.pids.Reset()
	x.names.Reset()
	x.counter.Store(0)
	// Keep cached noSender (still valid for same ActorSystem instances).
}

// count returns number of registered nodes (lock-free).
// Time Complexity: O(1).
// Space Complexity: O(1).
func (x *tree) count() int64 {
	return x.counter.Load()
}

// root returns the root PID if set.
// Time Complexity: O(1).
// Space Complexity: O(1).
func (x *tree) root() (*PID, bool) {
	x.RLock()
	defer x.RUnlock()
	if x.rootNode == nil {
		return nil, false
	}
	p := x.rootNode.pid.Load()
	if p == nil {
		return nil, false
	}
	return p, true
}

// parent returns the parent PID of pid.
// Returns (nil,false) if pid is nil/NoSender/unknown or has no parent.
// Time Complexity: O(1).
// Space Complexity: O(1).
func (x *tree) parent(pid *PID) (*PID, bool) {
	x.RLock()
	defer x.RUnlock()

	if pid == nil {
		return nil, false
	}
	if x.noSender != nil && pid.Equals(x.noSender) {
		return nil, false
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok {
		return nil, false
	}
	pp := node.parent.Load()
	if pp == nil {
		return nil, false
	}
	return pp, true
}

// watchers returns the list of PIDs watching pid.
// Returns nil if pid is nil/NoSender/unknown.
// Time Complexity: O(w) where w is the number of watchers.
// Space Complexity: O(w) for the result slice.
func (x *tree) watchers(pid *PID) []*PID {
	x.RLock()
	defer x.RUnlock()

	if pid == nil {
		return nil
	}

	if x.noSender != nil && pid.Equals(x.noSender) {
		return nil
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok {
		return nil
	}

	list := make([]*PID, 0, node.watchers.Len())
	node.watchers.Range(func(_ string, w *PID) {
		list = append(list, w)
	})
	return list
}

// watchees returns the list of PIDs that pid is watching.
// Returns nil if pid is nil/NoSender/unknown.
// Time Complexity: O(w) where w is the number of watchees.
// Space Complexity: O(w) for the result slice.
func (x *tree) watchees(pid *PID) []*PID {
	x.RLock()
	defer x.RUnlock()

	if pid == nil {
		return nil
	}

	if x.noSender != nil && pid.Equals(x.noSender) {
		return nil
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok {
		return nil
	}

	list := make([]*PID, 0, node.watchees.Len())
	node.watchees.Range(func(_ string, w *PID) {
		list = append(list, w)
	})
	return list
}
