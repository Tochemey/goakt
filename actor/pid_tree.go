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

	"github.com/tochemey/goakt/v3/internal/collection"
	"github.com/tochemey/goakt/v3/internal/registry"
)

// pidNode represents a node in the PID tree, encapsulating an actor's PID,
// its parent, watchers, watchees, and descendants.
type pidNode struct {
	pid         atomic.Pointer[PID]               // The PID associated with this node.
	parent      atomic.Pointer[PID]               // Parent PID; nil if this is the root node.
	watchers    *collection.Map[string, *PID]     // Actors watching this pidNode.
	watchees    *collection.Map[string, *PID]     // Actors being watched by this pidNode.
	descendants *collection.Map[string, *pidNode] // Descendant nodes of this pidNode.
}

// value returns the PID stored in the node, or nil if not set.
func (n *pidNode) value() *PID {
	if pid := n.pid.Load(); pid != nil {
		return pid
	}
	return nil
}

// tree represents a concurrent-safe tree structure for managing actor PIDs
// and their relationships (parent, descendants, watchers, watchees).
type tree struct {
	sync.RWMutex
	rootNode *pidNode                          // Root node of the tree; nil if no actors are registered.
	pids     *collection.Map[string, *pidNode] // Map of pidNode indexed by their PID string.
	counter  *atomic.Int64                     // Tracks the number of nodes in the tree.
}

// newTree creates and returns a new, initialized PID tree.
func newTree() *tree {
	return &tree{
		pids:    collection.NewMap[string, *pidNode](),
		counter: atomic.NewInt64(0),
		rootNode: &pidNode{
			pid:         atomic.Pointer[PID]{},
			watchers:    collection.NewMap[string, *PID](),
			watchees:    collection.NewMap[string, *PID](),
			descendants: collection.NewMap[string, *pidNode](),
		},
	}
}

// addRootNode adds a root node to the tree with the given PID.
// Returns an error if the PID is nil, NoSender, or already exists.
func (x *tree) addRootNode(pid *PID) error {
	x.Lock()
	defer x.Unlock()

	if pid == nil {
		return errors.New("pid is nil")
	}

	if pid.Equals(NoSender) {
		return errors.New("pid cannot be NoSender")
	}

	if _, exists := x.pids.Get(pid.ID()); exists {
		return errors.New("pid already exists")
	}

	x.rootNode.pid.Store(pid)
	x.pids.Set(pid.ID(), x.rootNode)
	x.counter.Inc()
	return nil
}

// addNode adds a new node with the given PID as a child of the specified parent PID.
// Returns an error if the PID or parent is nil, NoSender, or already exists.
func (x *tree) addNode(parent, pid *PID) error {
	x.Lock()
	defer x.Unlock()

	if pid == nil {
		return errors.New("pid is nil")
	}

	if parent == nil {
		return errors.New("parent pid is nil")
	}

	if parent.Equals(NoSender) {
		return errors.New("parent pid cannot be NoSender")
	}

	// check if the pid already exists
	if _, exists := x.pids.Get(pid.ID()); exists {
		return errors.New("pid already exists")
	}

	pidnode := &pidNode{
		pid:         atomic.Pointer[PID]{},
		watchers:    collection.NewMap[string, *PID](),
		watchees:    collection.NewMap[string, *PID](),
		descendants: collection.NewMap[string, *pidNode](),
	}

	pidnode.pid.Store(pid)
	nodeid := pid.ID()
	parentNode, ok := x.pids.Get(parent.ID())
	if !ok {
		return errors.New("parent pid does not exist")
	}

	// add to the parent's descendants
	parentNode.descendants.Set(nodeid, pidnode)
	parentNode.watchees.Set(nodeid, pid)
	pidnode.watchers.Set(parent.ID(), parent)
	pidnode.parent.Store(parent)

	x.pids.Set(nodeid, pidnode)
	x.counter.Inc()
	return nil
}

// addWatcher registers a watcher PID to watch the specified PID.
// No-op if either PID is nil, NoSender, or not found in the tree.
func (x *tree) addWatcher(pid, watcher *PID) {
	x.Lock()
	defer x.Unlock()

	if pid == nil || watcher == nil {
		return
	}

	if pid.Equals(NoSender) || watcher.Equals(NoSender) {
		return
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

// deleteNode removes the node with the given PID and all its descendants from the tree.
// Cleans up watcher/watchee relationships as well.
func (x *tree) deleteNode(pid *PID) {
	x.Lock()
	defer x.Unlock()

	if pid == nil || pid.Equals(NoSender) {
		return
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok {
		return
	}

	// Recursively remove all descendants first
	var removeDescendants func(*pidNode)
	removeDescendants = func(node *pidNode) {
		for _, descendant := range node.descendants.Values() {
			removeDescendants(descendant)
			// Clean up watcher/watchee relationships for descendant
			descendant.watchers.Range(func(_ string, value *PID) {
				if watcherNode, exists := x.pids.Get(value.ID()); exists {
					watcherNode.watchees.Delete(descendant.pid.Load().ID())
				}
			})
			descendant.watchees.Range(func(_ string, value *PID) {
				if watcheeNode, exists := x.pids.Get(value.ID()); exists {
					watcheeNode.watchers.Delete(descendant.pid.Load().ID())
				}
			})
			// Remove descendant from parent's descendants if applicable
			if descendant.parent.Load() != nil {
				if parentNode, exists := x.pids.Get(descendant.parent.Load().ID()); exists {
					parentNode.descendants.Delete(descendant.pid.Load().ID())
					parentNode.watchees.Delete(descendant.pid.Load().ID())
				}
			}
			x.pids.Delete(descendant.pid.Load().ID())
			x.counter.Dec()
		}
	}
	removeDescendants(node)

	// Clean up watcher/watchee relationships for the node itself
	node.watchers.Range(func(_ string, value *PID) {
		if watcherNode, exists := x.pids.Get(value.ID()); exists {
			watcherNode.watchees.Delete(pid.ID())
		}
	})
	node.watchees.Range(func(_ string, value *PID) {
		if watcheeNode, exists := x.pids.Get(value.ID()); exists {
			watcheeNode.watchers.Delete(pid.ID())
		}
	})

	// Remove from parent's descendants if applicable
	if node.parent.Load() != nil {
		if parentNode, exists := x.pids.Get(node.parent.Load().ID()); exists {
			parentNode.descendants.Delete(pid.ID())
			parentNode.watchees.Delete(pid.ID())
		}
	}

	x.pids.Delete(pid.ID())
	x.counter.Dec()
}

// node retrieves the pidNode for the given PID string ID.
// Returns the node and true if found, otherwise nil and false.
func (x *tree) node(id string) (*pidNode, bool) {
	x.RLock()
	defer x.RUnlock()

	node, ok := x.pids.Get(id)
	if !ok {
		return nil, false
	}
	return node, true
}

// nodes returns a slice of all pidNodes currently in the tree.
func (x *tree) nodes() []*pidNode {
	x.RLock()
	defer x.RUnlock()

	nodes := make([]*pidNode, 0, x.counter.Load())
	x.pids.Range(func(_ string, node *pidNode) {
		nodes = append(nodes, node)
	})

	return nodes
}

// siblings returns all sibling PIDs of the given PID (excluding itself).
// Returns nil if PID is nil, NoSender, or not found.
func (x *tree) siblings(pid *PID) []*PID {
	x.RLock()
	defer x.RUnlock()

	if pid == nil || pid.Equals(NoSender) {
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

	siblings := make([]*PID, 0)
	for _, siblingNode := range parentNode.descendants.Values() {
		siblingPID := siblingNode.pid.Load()
		if siblingPID != nil && !siblingPID.Equals(pid) {
			siblings = append(siblings, siblingPID)
		}
	}

	return siblings
}

// children returns all direct child PIDs of the given PID.
func (x *tree) children(pid *PID) []*PID {
	x.RLock()
	defer x.RUnlock()

	if pid == nil || pid.Equals(NoSender) {
		return nil
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok {
		return nil
	}

	var result []*PID
	for _, descendantNode := range node.descendants.Values() {
		descendantPID := descendantNode.pid.Load()
		if descendantPID != nil {
			result = append(result, descendantPID)
		}
	}

	return result
}

// descendants returns all descendant PIDs of the given PID in the tree.
// Returns nil if PID is nil, NoSender, or not found.
func (x *tree) descendants(pid *PID) []*PID {
	x.RLock()
	defer x.RUnlock()

	if pid == nil || pid.Equals(NoSender) {
		return nil
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok {
		return nil
	}

	var result []*PID
	visited := make(map[string]registry.Unit)
	var fetch func(n *pidNode)
	fetch = func(n *pidNode) {
		for _, descendantNode := range n.descendants.Values() {
			descendantPID := descendantNode.pid.Load()
			if descendantPID != nil {
				if _, seen := visited[descendantPID.ID()]; !seen {
					visited[descendantPID.ID()] = registry.Unit{}
					result = append(result, descendantPID)
					fetch(descendantNode)
				}
			}
		}
	}
	fetch(node)

	return result
}

// reset clears the tree, removing all nodes and resetting the root.
func (x *tree) reset() {
	x.Lock()
	defer x.Unlock()

	x.rootNode = &pidNode{
		pid:         atomic.Pointer[PID]{},
		watchers:    collection.NewMap[string, *PID](),
		watchees:    collection.NewMap[string, *PID](),
		descendants: collection.NewMap[string, *pidNode](),
	}
	x.pids.Reset()
	x.counter.Store(0)
}

// count returns the current number of nodes in the tree.
func (x *tree) count() int64 {
	x.RLock()
	defer x.RUnlock()

	return x.counter.Load()
}

// root returns the PID of the root node, and true if it exists.
func (x *tree) root() (*PID, bool) {
	x.RLock()
	defer x.RUnlock()

	if x.rootNode == nil || x.rootNode.pid.Load() == nil {
		return nil, false
	}
	return x.rootNode.pid.Load(), true
}

// parent returns the parent PID of the given PID, and true if it exists.
func (x *tree) parent(pid *PID) (*PID, bool) {
	x.RLock()
	defer x.RUnlock()

	if pid == nil || pid.Equals(NoSender) {
		return nil, false
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok || node.parent.Load() == nil {
		return nil, false
	}

	return node.parent.Load(), true
}

// watchers returns a slice of PIDs that are watching the given PID.
// Returns nil if PID is nil, NoSender, or not found.
func (x *tree) watchers(pid *PID) []*PID {
	x.RLock()
	defer x.RUnlock()

	if pid == nil || pid.Equals(NoSender) {
		return nil
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok {
		return nil
	}

	watchers := make([]*PID, 0, node.watchers.Len())
	node.watchers.Range(func(_ string, value *PID) {
		watchers = append(watchers, value)
	})

	return watchers
}

// watchees returns a slice of PIDs that the given PID is watching.
// Returns nil if PID is nil, NoSender, or not found.
func (x *tree) watchees(pid *PID) []*PID {
	x.RLock()
	defer x.RUnlock()

	if pid == nil || pid.Equals(NoSender) {
		return nil
	}

	node, ok := x.pids.Get(pid.ID())
	if !ok {
		return nil
	}

	watchees := make([]*PID, 0, node.watchees.Len())
	node.watchees.Range(func(_ string, value *PID) {
		watchees = append(watchees, value)
	})

	return watchees
}
