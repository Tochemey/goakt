// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package actor

import (
	"errors"
	"sync"

	"go.uber.org/atomic"
)

// pidNode represents a node in the PID tree.
// Fields are protected by the owning tree's mutex; no per-node locking is needed.
type pidNode struct {
	pid         *PID                // PID associated with this node.
	parentNode  *pidNode            // Parent node; nil if root.
	id          string              // Cached pid.ID() — avoids repeated string building.
	name        string              // Cached pid.Name().
	watchers    map[string]*PID     // Actors watching this node (key = watcher ID).
	watchees    map[string]*PID     // Actors this node is watching (key = watchee ID).
	descendants map[string]*pidNode // Direct children (key = child ID).
}

// value returns the PID stored in the node, or nil if not set.
func (n *pidNode) value() *PID {
	return n.pid
}

// newPidNode creates a pidNode with cached id/name fields.
// If pid is nil the cached strings remain empty (used for the initial root node).
func newPidNode(pid *PID) *pidNode {
	n := &pidNode{
		pid:         pid,
		watchers:    make(map[string]*PID),
		watchees:    make(map[string]*PID),
		descendants: make(map[string]*pidNode),
	}
	if pid != nil {
		n.id = pid.ID()
		n.name = pid.Name()
	}
	return n
}

// tree maintains actor relationships in a concurrency-safe structure.
// A single RWMutex protects all internal state, eliminating the need for
// per-map locking and reducing lock contention.
type tree struct {
	mu       sync.RWMutex
	rootNode *pidNode            // Logical root node (its pid may be nil if cleared).
	pids     map[string]*pidNode // Index: PID.ID() -> pidNode.
	names    map[string]*pidNode // Index: PID.Name() -> pidNode.
	counter  *atomic.Int64       // Number of nodes currently registered.
	noSender *PID                // Cached NoSender (set on first root add).
}

// newTree creates and returns a new PID tree.
// Time Complexity: O(1).
// Space Complexity: O(1) (excluding the internal empty maps allocated).
func newTree() *tree {
	return &tree{
		pids:     make(map[string]*pidNode),
		names:    make(map[string]*pidNode),
		counter:  atomic.NewInt64(0),
		rootNode: newPidNode(nil),
	}
}

// addRootNode registers the root PID in the tree.
// Fails if pid is nil, already present, or equals NoSender.
// Time Complexity: O(1) (amortized for map operations).
// Space Complexity: O(1) additional.
func (x *tree) addRootNode(pid *PID) error {
	x.mu.Lock()
	defer x.mu.Unlock()

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

	id := pid.ID()
	if _, exists := x.pids[id]; exists {
		return errors.New("pid already exists")
	}

	name := pid.Name()
	x.rootNode.pid = pid
	x.rootNode.id = id
	x.rootNode.name = name
	x.pids[id] = x.rootNode
	x.names[name] = x.rootNode
	x.counter.Inc()
	return nil
}

// addNode inserts a child PID under the given parent PID.
// Fails if inputs are invalid, parent does not exist, or pid already exists.
// Establishes watcher/watchee links between parent and child.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1) additional (one node plus map entries).
func (x *tree) addNode(parent, pid *PID) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.addNodeLocked(parent, pid)
}

// addNodeLocked is the lock-free core of addNode.
// The caller MUST hold x.mu (write).
func (x *tree) addNodeLocked(parent, pid *PID) error {
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

	id := pid.ID()
	if _, exists := x.pids[id]; exists {
		return errors.New("pid already exists")
	}

	parentID := parent.ID()
	parentNode, ok := x.pids[parentID]
	if !ok {
		return errors.New("parent pid does not exist")
	}

	name := pid.Name()
	childNode := &pidNode{
		pid:         pid,
		parentNode:  parentNode,
		id:          id,
		name:        name,
		watchers:    make(map[string]*PID),
		watchees:    make(map[string]*PID),
		descendants: make(map[string]*pidNode),
	}

	parentNode.descendants[id] = childNode
	parentNode.watchees[id] = pid
	childNode.watchers[parentID] = parent

	x.pids[id] = childNode
	x.names[name] = childNode
	x.counter.Inc()
	return nil
}

// attachNode links an existing pid under the given parent.
// It reestablishes parent/child and watcher/watchee relationships without
// creating a new node or changing the tree size.
func (x *tree) attachNode(parent, pid *PID) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.attachNodeLocked(parent, pid)
}

// attachNodeLocked is the lock-free core of attachNode.
// The caller MUST hold x.mu (write).
func (x *tree) attachNodeLocked(parent, pid *PID) error {
	if pid == nil {
		return errors.New("pid is nil")
	}

	if parent == nil {
		return errors.New("parent pid is nil")
	}

	if parent.Equals(pid.ActorSystem().NoSender()) {
		return errors.New("parent pid cannot be NoSender")
	}

	parentID := parent.ID()
	parentNode, ok := x.pids[parentID]
	if !ok {
		return errors.New("parent pid does not exist")
	}

	childID := pid.ID()
	childNode, ok := x.pids[childID]
	if !ok {
		return errors.New("pid does not exist")
	}

	childNode.parentNode = parentNode
	parentNode.descendants[childID] = childNode
	parentNode.watchees[childID] = pid
	childNode.watchers[parentID] = parent
	return nil
}

// addOrAttachNode ensures pid is linked under parent, adding it when missing.
// Acquires the write lock once to avoid TOCTOU races.
// ref: https://en.wikipedia.org/wiki/Time-of-check_to_time-of-use (TOCTOU)
func (x *tree) addOrAttachNode(parent, pid *PID) error {
	if pid == nil || parent == nil {
		return nil
	}

	if parent.Equals(pid.ActorSystem().NoSender()) {
		return nil
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	id := pid.ID()
	if _, ok := x.pids[id]; ok {
		return x.attachNodeLocked(parent, pid)
	}

	return x.addNodeLocked(parent, pid)
}

// removeWatcher removes the watch relationship: watcher stops watching watchee.
// Silently no-ops if either PID is nil or not found.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1).
func (x *tree) removeWatcher(watchee, watcher *PID) {
	if watchee == nil || watcher == nil {
		return
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	watcheeID := watchee.ID()
	watcherID := watcher.ID()

	// Remove watchee from watcher's watchees list.
	if watcherNode, ok := x.pids[watcherID]; ok {
		delete(watcherNode.watchees, watcheeID)
	}
	// Remove watcher from watchee's watchers list.
	if watcheeNode, ok := x.pids[watcheeID]; ok {
		delete(watcheeNode.watchers, watcherID)
	}
}

// removeDescendant removes a child from a parent node's descendants map.
// Silently no-ops if parentID or childID are not found.
// Time Complexity: O(1).
// Space Complexity: O(1).
func (x *tree) removeDescendant(parentID, childID string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if node, ok := x.pids[parentID]; ok {
		delete(node.descendants, childID)
	}
}

// addWatcher registers watcher to watch pid.
// Silently no-ops if any PID is nil, NoSender, or not found.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1) additional per watcher relationship.
func (x *tree) addWatcher(pid, watcher *PID) {
	x.mu.Lock()
	defer x.mu.Unlock()

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

	pidID := pid.ID()
	pidNode, ok := x.pids[pidID]
	if !ok {
		return
	}

	watcherID := watcher.ID()
	watcherNode, ok := x.pids[watcherID]
	if !ok {
		return
	}

	pidNode.watchers[watcherID] = watcher
	watcherNode.watchees[pidID] = pid
}

// deleteNode removes pid and its entire subtree (all descendants).
// Also cleans all watcher/watchee relationships involving those nodes.
// Time Complexity: O(k + e) where k is number of nodes in the subtree,
// and e is the total number of watcher/watchee edges touching them.
// Space Complexity: O(k) for traversal stacks plus O(1) auxiliary.
// No-ops if pid is nil, NoSender, or unknown.
func (x *tree) deleteNode(pid *PID) {
	x.mu.Lock()
	defer x.mu.Unlock()

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

	id := pid.ID()
	node, ok := x.pids[id]
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
		for _, child := range n.descendants {
			stack = append(stack, child)
		}
	}

	// Process nodes in reverse order so children handled before parents.
	for i := len(post) - 1; i >= 0; i-- {
		n := post[i]
		if n.pid == nil {
			continue
		}

		// Clean watchers: remove this node from each watcher's watchees.
		// Use map keys directly (they are the watcher IDs), avoiding PID.ID() allocations.
		for watcherID := range n.watchers {
			if watcherNode, exists := x.pids[watcherID]; exists {
				delete(watcherNode.watchees, n.id)
			}
		}
		n.watchers = nil

		// Clean watchees: remove this node from each watchee's watchers.
		for watcheeID := range n.watchees {
			if watcheeNode, exists := x.pids[watcheeID]; exists {
				delete(watcheeNode.watchers, n.id)
			}
		}
		n.watchees = nil

		// Unlink from parent using direct node reference (no ID lookup needed).
		if pn := n.parentNode; pn != nil {
			delete(pn.descendants, n.id)
			delete(pn.watchees, n.id)
		}

		delete(x.pids, n.id)
		if current, ok := x.names[n.name]; ok && current == n {
			delete(x.names, n.name)
		}
		n.parentNode = nil
		n.pid = nil
		x.counter.Dec()
	}
}

// node returns the internal pidNode by ID.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1).
func (x *tree) node(id string) (*pidNode, bool) {
	x.mu.RLock()
	n, ok := x.pids[id]
	x.mu.RUnlock()
	return n, ok
}

// nodeByName returns the internal pidNode by actor name.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1).
func (x *tree) nodeByName(name string) (*pidNode, bool) {
	if name == "" {
		return nil, false
	}
	x.mu.RLock()
	node, ok := x.names[name]
	x.mu.RUnlock()
	return node, ok
}

// nodes returns all pidNodes currently registered.
// Time Complexity: O(n) where n is the number of nodes.
// Space Complexity: O(n) for the returned slice.
func (x *tree) nodes() []*pidNode {
	x.mu.RLock()
	defer x.mu.RUnlock()
	result := make([]*pidNode, 0, len(x.pids))
	for _, n := range x.pids {
		result = append(result, n)
	}
	return result
}

// siblings returns all sibling PIDs of pid (excluding pid itself).
// Returns nil if pid is nil/NoSender/unknown; returns empty slice if none.
// Time Complexity: O(d) where d is the number of children of the parent.
// Space Complexity: O(d) for the result slice (worst case).
func (x *tree) siblings(pid *PID) []*PID {
	x.mu.RLock()
	defer x.mu.RUnlock()

	if pid == nil {
		return nil
	}
	if x.noSender != nil && pid.Equals(x.noSender) {
		return nil
	}

	id := pid.ID()
	node, ok := x.pids[id]
	if !ok || node.parentNode == nil {
		return nil
	}

	parentNode := node.parentNode
	size := len(parentNode.descendants)
	if size <= 1 {
		return []*PID{}
	}
	sibs := make([]*PID, 0, size-1)
	for _, s := range parentNode.descendants {
		if sp := s.pid; sp != nil && !sp.Equals(pid) {
			sibs = append(sibs, sp)
		}
	}
	return sibs
}

// children returns the direct children of pid.
// Returns nil if pid is nil/NoSender/unknown; empty slice if no children.
// Time Complexity: O(c) where c is the number of direct children.
// Space Complexity: O(c) for the result slice.
func (x *tree) children(pid *PID) []*PID {
	x.mu.RLock()
	defer x.mu.RUnlock()

	if pid == nil {
		return nil
	}
	if x.noSender != nil && pid.Equals(x.noSender) {
		return nil
	}

	id := pid.ID()
	node, ok := x.pids[id]
	if !ok {
		return nil
	}

	result := make([]*PID, 0, len(node.descendants))
	for _, child := range node.descendants {
		if cp := child.pid; cp != nil {
			result = append(result, cp)
		}
	}
	return result
}

// descendants returns all descendant PIDs of pid (depth-first, no order guarantee).
// Returns nil if pid is nil/NoSender/unknown; empty slice if no descendants.
// Time Complexity: O(k) where k is number of descendants.
// Space Complexity: O(k) for traversal stack + result slice.
func (x *tree) descendants(pid *PID) []*PID {
	x.mu.RLock()
	defer x.mu.RUnlock()

	if pid == nil {
		return nil
	}

	if x.noSender != nil && pid.Equals(x.noSender) {
		return nil
	}

	id := pid.ID()
	node, ok := x.pids[id]
	if !ok {
		return nil
	}

	if len(node.descendants) == 0 {
		return []*PID{}
	}

	stack := make([]*pidNode, 0, len(node.descendants))
	for _, child := range node.descendants {
		stack = append(stack, child)
	}

	result := make([]*PID, 0, len(stack))
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == nil {
			continue
		}
		if p := n.pid; p != nil {
			result = append(result, p)
		}
		for _, c := range n.descendants {
			stack = append(stack, c)
		}
	}
	return result
}

// reset clears the entire tree (all nodes removed) but preserves cached noSender.
// Time Complexity: O(n) in effect (map reset may drop references over n nodes).
// Space Complexity: O(1) additional (old structures become GC candidates).
func (x *tree) reset() {
	x.mu.Lock()
	defer x.mu.Unlock()

	x.rootNode = newPidNode(nil)
	clear(x.pids)
	clear(x.names)
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
	x.mu.RLock()
	defer x.mu.RUnlock()
	if x.rootNode == nil {
		return nil, false
	}
	p := x.rootNode.pid
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
	x.mu.RLock()
	defer x.mu.RUnlock()

	if pid == nil {
		return nil, false
	}
	if x.noSender != nil && pid.Equals(x.noSender) {
		return nil, false
	}

	id := pid.ID()
	node, ok := x.pids[id]
	if !ok {
		return nil, false
	}
	if node.parentNode == nil {
		return nil, false
	}
	pp := node.parentNode.pid
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
	x.mu.RLock()
	defer x.mu.RUnlock()

	if pid == nil {
		return nil
	}

	if x.noSender != nil && pid.Equals(x.noSender) {
		return nil
	}

	id := pid.ID()
	node, ok := x.pids[id]
	if !ok {
		return nil
	}

	list := make([]*PID, 0, len(node.watchers))
	for _, w := range node.watchers {
		list = append(list, w)
	}
	return list
}

// watchees returns the list of PIDs that pid is watching.
// Returns nil if pid is nil/NoSender/unknown.
// Time Complexity: O(w) where w is the number of watchees.
// Space Complexity: O(w) for the result slice.
func (x *tree) watchees(pid *PID) []*PID {
	x.mu.RLock()
	defer x.mu.RUnlock()

	if pid == nil {
		return nil
	}

	if x.noSender != nil && pid.Equals(x.noSender) {
		return nil
	}

	id := pid.ID()
	node, ok := x.pids[id]
	if !ok {
		return nil
	}

	list := make([]*PID, 0, len(node.watchees))
	for _, w := range node.watchees {
		list = append(list, w)
	}
	return list
}
