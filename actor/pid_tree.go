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
	"hash/maphash"
	"sync"
	"sync/atomic"
)

// treeShardCount is the number of shards of each tree index. It is a power of
// two so that a key's hash picks its shard with a mask.
const treeShardCount = 64

// errNodeAlreadyExists reports that a PID with the same ID is already
// registered in the tree. completeSpawn relies on this sentinel to detect a
// duplicate insertion and return the canonical instance instead.
var errNodeAlreadyExists = errors.New("pid already exists")

// treeHashSeed seeds the hash that picks the shard of an index key.
var treeHashSeed = maphash.MakeSeed()

// pidNode represents a node in the PID tree.
//
// Locking. The tree has no lock of its own. Each index shard has one, and each
// node has mu, which protects the node's relationships: parentNode, watchers,
// watchees and descendants. The rules that keep the locks free of cycles are:
//
//   - A shard lock is a leaf: nothing else is taken while it is held.
//   - A node lock may be held while a shard lock is taken.
//   - Two node locks are held at once in two cases. Adding a new node locks the
//     new node, then its parent: the new node is not yet in any index, so
//     nothing else can hold its lock. Adding or removing a watch edge locks its
//     two nodes in the order of their IDs (lockPair), so two edge operations
//     on the same pair never wait on each other in a cycle. Nothing else takes
//     a second node lock.
//
// The pid field is an atomic pointer because nodes are handed out without any
// lock (node, nodeByName) and deleteNode clears it when the actor leaves the
// tree. A node whose pid is nil has left the tree, or never joined it, and
// every mutation of a relationship checks that under the node's lock, so no
// edge is ever added to a node that is being removed.
type pidNode struct {
	pid atomic.Pointer[PID] // PID associated with this node (atomic for safe lock-free reads).
	// id, name and qualifiedName cache the PID's identifiers. They are set
	// before the node is published in an index and never change afterwards,
	// so removing the node never reads the PID.
	id            string // Cached pid.ID().
	name          string // Cached pid.Name().
	qualifiedName string // Cached pid.qualifiedName(), the node's key in the qualifiedNames index.
	// namePrev and nameNext link the nodes that share this node's name, in
	// insertion order, so that unlinking a node on removal costs O(1)
	// whatever the number of actors with the same name. The names index
	// points at the newest node of each name. All nodes of one name hash to
	// the same names shard, whose lock protects these links; use linkName and
	// removeName.
	namePrev, nameNext *pidNode
	// mu protects parentNode, watchers, watchees and descendants, and makes
	// clearing pid atomic with the removal of the node from the indexes.
	mu          sync.Mutex
	parentNode  *pidNode            // Parent node; nil if root or removed.
	watchers    []*PID              // Actors watching this node. A small slice, nil until the first watcher, scanned linearly (a handful of entries at most); use putWatcher/deleteWatcher.
	watchees    map[string]*PID     // Actors this node is watching (key = watchee ID). Nil until the first watchee; use putLazy to insert.
	descendants map[string]*pidNode // Direct children (key = child ID). Nil until the first child; use putLazy to insert.
}

// treeIndexShard is one shard of a treeIndex: a map under its own lock.
type treeIndexShard struct {
	mu    sync.RWMutex
	nodes map[string]*pidNode
}

// treeIndex maps a key to a node. It is split into treeShardCount shards that
// each carry their own lock, so lookups and updates of different keys do not
// wait on each other; a key always lands in the same shard, so a check and an
// insert of one key under that shard's lock are atomic.
type treeIndex struct {
	shards [treeShardCount]treeIndexShard
}

// removedNode is a node deleteSubtree took out of the tree, with the
// relationships it held at that moment, which are cleaned once the whole
// subtree is out.
type removedNode struct {
	node     *pidNode
	parent   *pidNode
	watchers []*PID
	watchees map[string]*PID
}

// tree maintains actor relationships in a concurrency-safe structure: three
// sharded indexes over pidNode values, by ID, by name and by qualified name,
// and the relationships kept on the nodes themselves. There is no tree-wide
// lock: a spawn, a stop or a watch takes the locks of the nodes and index
// shards it touches, so actors in different parts of the tree are created and
// stopped in parallel. See pidNode for the lock order.
type tree struct {
	pids           treeIndex               // Index: PID.ID() -> pidNode.
	names          treeIndex               // Index: PID.Name() -> the most recently added node still in the tree with that name; see pidNode.namePrev.
	qualifiedNames treeIndex               // Index: the qualified name of an actor, its ancestors' names and its own joined by '/', -> pidNode. Every node shares this system's host and port, so the qualified name identifies a node as uniquely as its ID, and lookups by it build no address.
	rootNode       atomic.Pointer[pidNode] // Logical root node (its pid may be nil if cleared).
	counter        atomic.Int64            // Number of nodes currently registered.
	noSender       atomic.Pointer[PID]     // Cached NoSender (set on first root add).
}

// value returns the PID stored in the node, or nil if cleared by deleteNode.
// Safe to call without holding any lock.
func (n *pidNode) value() *PID {
	return n.pid.Load()
}

// pidInTree returns the PID a node lookup resolved. A node is handed out without
// a lock and deleteNode clears its PID when the actor leaves the tree, so a
// node found a moment ago may hold no PID any more: that is reported as not
// found. Callers that need the PID go through the tree's pid lookups rather
// than reading value() themselves.
func pidInTree(node *pidNode, found bool) (*PID, bool) {
	if !found {
		return nil, false
	}

	pid := node.value()
	return pid, pid != nil
}

// newPidNode creates a pidNode with cached id/name fields.
// If pid is nil the cached strings remain empty (used for the initial root node).
// The watchers slice and the watchees and descendants maps stay nil until
// their first insert; all read paths treat the nil values as empty.
func newPidNode(pid *PID) *pidNode {
	n := &pidNode{}
	n.pid.Store(pid)

	if pid != nil {
		n.id = pid.ID()
		n.name = pid.Name()
		n.qualifiedName = pid.qualifiedName()
	}

	return n
}

// putLazy inserts key/value into the map pointed to by m, allocating the map
// on first use.
func putLazy[V any](m *map[string]V, key string, value V) {
	if *m == nil {
		*m = make(map[string]V, 1)
	}

	(*m)[key] = value
}

// putWatcher records w as a watcher in the slice pointed to by list, allocating
// the backing array on first use. If an entry with the same w.ID() is already
// present it replaces that element, reproducing the old watchers map's
// overwrite-by-key so a re-added watcher never duplicates; otherwise it appends.
// PID.ID() returns a cached string, so the linear scan allocates nothing.
func putWatcher(list *[]*PID, w *PID) {
	id := w.ID()
	for i, existing := range *list {
		if existing.ID() == id {
			(*list)[i] = w
			return
		}
	}

	*list = append(*list, w)
}

// deleteWatcher removes the watcher whose ID() equals id from the slice pointed
// to by list. Watcher order is irrelevant (the old map kept none), so it removes
// the match by swapping the last element into its place and truncating. It nils
// out the vacated tail slot so the removed *PID is not retained, and sets the
// slice back to nil once it becomes empty so an actor with no watchers holds no
// backing array. Absent id is a no-op.
func deleteWatcher(list *[]*PID, id string) {
	s := *list
	for i, w := range s {
		if w.ID() != id {
			continue
		}

		last := len(s) - 1
		s[i] = s[last]
		s[last] = nil

		if last == 0 {
			*list = nil
			return
		}

		*list = s[:last]
		return
	}
}

// init allocates the map of every shard.
func (x *treeIndex) init() {
	for i := range x.shards {
		x.shards[i].nodes = make(map[string]*pidNode)
	}
}

// shard returns the shard that holds key.
func (x *treeIndex) shard(key string) *treeIndexShard {
	return &x.shards[maphash.String(treeHashSeed, key)&(treeShardCount-1)]
}

// get returns the node registered under key.
func (x *treeIndex) get(key string) (*pidNode, bool) {
	s := x.shard(key)
	s.mu.RLock()
	n, ok := s.nodes[key]
	s.mu.RUnlock()
	return n, ok
}

// put registers n under key, replacing any node registered there.
func (x *treeIndex) put(key string, n *pidNode) {
	s := x.shard(key)
	s.mu.Lock()
	s.nodes[key] = n
	s.mu.Unlock()
}

// putIfAbsent registers n under key unless a node is already registered there,
// and reports whether it did.
func (x *treeIndex) putIfAbsent(key string, n *pidNode) bool {
	s := x.shard(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.nodes[key]; exists {
		return false
	}

	s.nodes[key] = n
	return true
}

// remove drops the entry of key when it is n. An entry that another node took
// over since n was registered belongs to that node and is left in place.
func (x *treeIndex) remove(key string, n *pidNode) {
	s := x.shard(key)
	s.mu.Lock()
	if current, ok := s.nodes[key]; ok && current == n {
		delete(s.nodes, key)
	}
	s.mu.Unlock()
}

// each calls fn with every registered node, one shard at a time under that
// shard's read lock. Nodes added or removed while another shard is visited may
// or may not be seen.
func (x *treeIndex) each(fn func(*pidNode)) {
	for i := range x.shards {
		s := &x.shards[i]
		s.mu.RLock()

		for _, n := range s.nodes {
			fn(n)
		}

		s.mu.RUnlock()
	}
}

// clear drops every entry of every shard.
func (x *treeIndex) clear() {
	for i := range x.shards {
		s := &x.shards[i]
		s.mu.Lock()
		clear(s.nodes)
		s.mu.Unlock()
	}
}

// newTree creates and returns a new PID tree.
// Time Complexity: O(1).
// Space Complexity: O(1) (excluding the internal empty maps allocated).
func newTree() *tree {
	x := &tree{}
	x.pids.init()
	x.names.init()
	x.qualifiedNames.init()
	x.rootNode.Store(newPidNode(nil))
	return x
}

// addRootNode registers the root PID in the tree.
// Fails if pid is nil, already present, or equals NoSender.
// Time Complexity: O(1) (amortized for map operations).
// Space Complexity: O(1) additional.
func (x *tree) addRootNode(pid *PID) error {
	if pid == nil {
		return errors.New("pid is nil")
	}

	// Cache NoSender once.
	x.noSender.CompareAndSwap(nil, pid.ActorSystem().NoSender())
	if pid.Equals(x.noSender.Load()) {
		return errors.New("pid cannot be NoSender")
	}

	id := pid.ID()
	if _, exists := x.pids.get(id); exists {
		return errors.New("pid already exists")
	}

	root := x.rootNode.Load()
	root.mu.Lock()
	defer root.mu.Unlock()

	root.id = id
	root.name = pid.Name()
	root.qualifiedName = pid.qualifiedName()
	root.pid.Store(pid)
	x.pids.put(id, root)
	x.linkName(root)
	x.qualifiedNames.put(root.qualifiedName, root)
	x.counter.Add(1)
	return nil
}

// addNode inserts a child PID under the given parent PID.
// Fails if inputs are invalid, parent does not exist, or pid already exists.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1) additional.
func (x *tree) addNode(parent, pid *PID) error {
	if pid == nil {
		return errors.New("pid is nil")
	}

	if parent == nil {
		return errors.New("parent pid is nil")
	}

	// Ensure NoSender cached (in case addRootNode was not yet called—defensive).
	x.noSender.CompareAndSwap(nil, pid.ActorSystem().NoSender())
	if parent.Equals(x.noSender.Load()) {
		return errors.New("parent pid cannot be NoSender")
	}

	if _, exists := x.pids.get(pid.ID()); exists {
		return errNodeAlreadyExists
	}

	parentNode, ok := x.pids.get(parent.ID())
	if !ok {
		return errors.New("parent pid does not exist")
	}

	return x.addNodeUnder(parentNode, parent, pid, false)
}

// addOrAttachNode ensures pid is linked under parent, adding it when missing.
// A parent that is stopping is refused under the parent's lock, the lock its
// freeChildren reads the children under, so there is no window between the
// check and the insert.
// ref: https://en.wikipedia.org/wiki/Time-of-check_to_time-of-use (TOCTOU)
func (x *tree) addOrAttachNode(parent, pid *PID) error {
	if pid == nil || parent == nil {
		return nil
	}

	if parent.Equals(pid.ActorSystem().NoSender()) {
		return nil
	}

	if parentStopping(parent) {
		return errors.New("parent pid is stopping")
	}

	parentNode, ok := x.pids.get(parent.ID())
	if !ok {
		return errors.New("parent pid does not exist")
	}

	if existing, ok := x.pids.get(pid.ID()); ok {
		return x.attachNode(parentNode, existing, parent, pid)
	}

	return x.addNodeUnder(parentNode, parent, pid, true)
}

// removeWatcher removes the watch relationship: watcher stops watching watchee.
// Silently no-ops if either PID is nil or not found.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1).
func (x *tree) removeWatcher(watchee, watcher *PID) {
	if watchee == nil || watcher == nil {
		return
	}

	watcheeID := watchee.ID()
	watcherID := watcher.ID()
	watcherNode, _ := x.pids.get(watcherID)
	watcheeNode, _ := x.pids.get(watcheeID)

	switch {
	case watcherNode != nil && watcheeNode != nil:
		lockPair(watcherNode, watcheeNode)
		delete(watcherNode.watchees, watcheeID)
		deleteWatcher(&watcheeNode.watchers, watcherID)
		unlockPair(watcherNode, watcheeNode)
	case watcherNode != nil:
		watcherNode.mu.Lock()
		delete(watcherNode.watchees, watcheeID)
		watcherNode.mu.Unlock()
	case watcheeNode != nil:
		watcheeNode.mu.Lock()
		deleteWatcher(&watcheeNode.watchers, watcherID)
		watcheeNode.mu.Unlock()
	}
}

// removeDescendant removes a child from a parent node's descendants map.
// Silently no-ops if parentID or childID are not found.
// Time Complexity: O(1).
// Space Complexity: O(1).
func (x *tree) removeDescendant(parentID, childID string) {
	if node, ok := x.pids.get(parentID); ok {
		node.mu.Lock()
		delete(node.descendants, childID)
		node.mu.Unlock()
	}
}

// addWatcher registers watcher to watch pid.
// Silently no-ops if any PID is nil, NoSender, not found, or no longer in the
// tree. The two ends of the relationship are written under the locks of both
// nodes, so a concurrent removal of the edge or of either actor sees the edge
// whole or not at all.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1) additional per watcher relationship.
func (x *tree) addWatcher(pid, watcher *PID) {
	if pid == nil || watcher == nil {
		return
	}

	if x.isNoSender(pid) || x.isNoSender(watcher) {
		return
	}

	pidID := pid.ID()
	pidNode, ok := x.pids.get(pidID)
	if !ok {
		return
	}

	watcherID := watcher.ID()
	watcherNode, ok := x.pids.get(watcherID)
	if !ok {
		return
	}

	lockPair(pidNode, watcherNode)
	defer unlockPair(pidNode, watcherNode)

	if pidNode.pid.Load() == nil || watcherNode.pid.Load() == nil {
		return
	}

	putWatcher(&pidNode.watchers, watcher)
	putLazy(&watcherNode.watchees, pidID, pid)
}

// deleteNode removes pid and its entire subtree (all descendants).
// Also cleans all watcher/watchee relationships involving those nodes.
// Time Complexity: O(k + e) where k is number of nodes in the subtree,
// and e is the total number of watcher/watchee edges touching them.
// Space Complexity: O(k) for the snapshots of the subtree's relationships.
// No-ops if pid is nil, NoSender, unknown, or no longer the PID its node
// holds: a node that a later incarnation of the same name took over belongs to
// that incarnation and is left in place.
// It reports whether the node was removed.
func (x *tree) deleteNode(pid *PID) bool {
	if pid == nil || x.isNoSender(pid) {
		return false
	}

	node, ok := x.pids.get(pid.ID())
	if !ok || node.pid.Load() != pid {
		return false
	}

	return x.deleteSubtree(node, pid)
}

// node returns the internal pidNode by ID.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1).
func (x *tree) node(id string) (*pidNode, bool) {
	return x.pids.get(id)
}

// nodeByName returns the internal pidNode by actor name. When several nodes
// share the name, which children of different parents may, it returns the most
// recently added one.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1).
func (x *tree) nodeByName(name string) (*pidNode, bool) {
	if name == "" {
		return nil, false
	}

	return x.names.get(name)
}

// nodeByQualifiedName returns the internal pidNode by the qualified name of its
// actor: the name of a top-level actor, parent/child for a child. It resolves
// exactly one node, unlike nodeByName.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1).
func (x *tree) nodeByQualifiedName(qualifiedName string) (*pidNode, bool) {
	return x.qualifiedNames.get(qualifiedName)
}

// pidOf returns the PID of the actor registered under id. It reports false
// when no node carries id or when the actor has left the tree, so a PID it
// returns with true is never nil.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1).
func (x *tree) pidOf(id string) (*PID, bool) {
	return pidInTree(x.node(id))
}

// pidByName returns the PID of the actor called name, the most recently added
// one when several share it (see nodeByName). It reports false when no node
// carries the name or when the actor has left the tree.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1).
func (x *tree) pidByName(name string) (*PID, bool) {
	return pidInTree(x.nodeByName(name))
}

// pidByQualifiedName returns the PID of the actor whose qualified name is
// qualifiedName (see nodeByQualifiedName). It reports false when no node
// carries the name or when the actor has left the tree.
// Time Complexity: O(1) (amortized).
// Space Complexity: O(1).
func (x *tree) pidByQualifiedName(qualifiedName string) (*PID, bool) {
	return pidInTree(x.nodeByQualifiedName(qualifiedName))
}

// pidList returns the PID of every actor in the tree. The list is collected
// one index shard at a time, so an actor that joins or leaves the tree while
// another shard is read may or may not be in it; a node whose actor left the
// tree while the nodes were collected is skipped.
// Time Complexity: O(n) where n is the number of nodes.
// Space Complexity: O(n) for the returned slice.
func (x *tree) pidList() []*PID {
	result := make([]*PID, 0, x.counter.Load())
	x.pids.each(func(n *pidNode) {
		if pid := n.value(); pid != nil {
			result = append(result, pid)
		}
	})

	return result
}

// siblings returns all sibling PIDs of pid (excluding pid itself).
// Returns nil if pid is nil/NoSender/unknown; returns empty slice if none.
// Time Complexity: O(d) where d is the number of children of the parent.
// Space Complexity: O(d) for the result slice (worst case).
func (x *tree) siblings(pid *PID) []*PID {
	node, ok := x.lookup(pid)
	if !ok {
		return nil
	}

	node.mu.Lock()
	parentNode := node.parentNode
	node.mu.Unlock()

	if parentNode == nil {
		return nil
	}

	parentNode.mu.Lock()
	defer parentNode.mu.Unlock()

	size := len(parentNode.descendants)
	if size <= 1 {
		return []*PID{}
	}

	sibs := make([]*PID, 0, size-1)
	for _, s := range parentNode.descendants {
		if sp := s.pid.Load(); sp != nil && !sp.Equals(pid) {
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
	node, ok := x.lookup(pid)
	if !ok {
		return nil
	}

	node.mu.Lock()
	defer node.mu.Unlock()

	result := make([]*PID, 0, len(node.descendants))
	for _, child := range node.descendants {
		if cp := child.pid.Load(); cp != nil {
			result = append(result, cp)
		}
	}

	return result
}

// descendants returns all descendant PIDs of pid (depth-first, no order guarantee).
// Returns nil if pid is nil/NoSender/unknown; empty slice if no descendants.
// Each node's children are read under that node's lock, so the result is a
// consistent view of every node but not of the subtree as a whole.
// Time Complexity: O(k) where k is number of descendants.
// Space Complexity: O(k) for traversal stack + result slice.
func (x *tree) descendants(pid *PID) []*PID {
	node, ok := x.lookup(pid)
	if !ok {
		return nil
	}

	stack := childNodes(node)
	if len(stack) == 0 {
		return []*PID{}
	}

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

		stack = append(stack, childNodes(n)...)
	}

	return result
}

// reset clears the entire tree (all nodes removed) but preserves cached noSender.
// Time Complexity: O(n) in effect (map reset may drop references over n nodes).
// Space Complexity: O(1) additional (old structures become GC candidates).
func (x *tree) reset() {
	x.rootNode.Store(newPidNode(nil))
	x.pids.clear()
	x.names.clear()
	x.qualifiedNames.clear()
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
	root := x.rootNode.Load()
	if root == nil {
		return nil, false
	}

	p := root.pid.Load()
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
	node, ok := x.lookup(pid)
	if !ok {
		return nil, false
	}

	node.mu.Lock()
	parentNode := node.parentNode
	node.mu.Unlock()

	if parentNode == nil {
		return nil, false
	}

	pp := parentNode.pid.Load()
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
	node, ok := x.lookup(pid)
	if !ok {
		return nil
	}

	node.mu.Lock()
	defer node.mu.Unlock()

	list := make([]*PID, len(node.watchers))
	copy(list, node.watchers)
	return list
}

// watchees returns the list of PIDs that pid is watching.
// Returns nil if pid is nil/NoSender/unknown.
// Time Complexity: O(w) where w is the number of watchees.
// Space Complexity: O(w) for the result slice.
func (x *tree) watchees(pid *PID) []*PID {
	node, ok := x.lookup(pid)
	if !ok {
		return nil
	}

	node.mu.Lock()
	defer node.mu.Unlock()

	list := make([]*PID, 0, len(node.watchees))
	for _, w := range node.watchees {
		list = append(list, w)
	}

	return list
}

// addNodeUnder creates the node of pid and links it under parentNode. The new
// node's lock is held until it is in every index and in its parent's maps, so
// a stop of the new actor that races its spawn waits for the spawn to finish
// linking rather than removing a half-linked node. The parent's lock is taken
// inside it, which is the one place two node locks nest (see pidNode).
// With refuseStopping, a parent that is stopping or passivating is refused
// under the parent's lock, the lock its freeChildren reads the children under.
func (x *tree) addNodeUnder(parentNode *pidNode, parent, pid *PID, refuseStopping bool) error {
	id := pid.ID()
	child := &pidNode{
		parentNode:    parentNode,
		id:            id,
		name:          pid.Name(),
		qualifiedName: pid.qualifiedName(),
		watchers:      []*PID{parent},
	}
	child.pid.Store(pid)

	child.mu.Lock()
	defer child.mu.Unlock()

	parentNode.mu.Lock()
	if refuseStopping && parentStopping(parent) {
		parentNode.mu.Unlock()
		return errors.New("parent pid is stopping")
	}

	if parentNode.pid.Load() == nil {
		parentNode.mu.Unlock()
		return errors.New("parent pid does not exist")
	}

	if !x.pids.putIfAbsent(id, child) {
		parentNode.mu.Unlock()
		return errNodeAlreadyExists
	}

	putLazy(&parentNode.descendants, id, child)
	putLazy(&parentNode.watchees, id, pid)
	parentNode.mu.Unlock()

	x.linkName(child)
	x.qualifiedNames.put(child.qualifiedName, child)
	x.counter.Add(1)
	return nil
}

// attachNode links the existing node of pid under parentNode again, which a
// restart does. It reestablishes parent/child and watcher/watchee relationships
// without creating a new node or changing the tree size. The parent is written
// first and the child second, each under its own lock; a child that left the
// tree in between is taken out of the parent again, and a parent that leaves
// the tree in between removes the child with the rest of its subtree.
func (x *tree) attachNode(parentNode, childNode *pidNode, parent, pid *PID) error {
	childID := pid.ID()

	parentNode.mu.Lock()
	if parentStopping(parent) {
		parentNode.mu.Unlock()
		return errors.New("parent pid is stopping")
	}

	if parentNode.pid.Load() == nil {
		parentNode.mu.Unlock()
		return errors.New("parent pid does not exist")
	}

	putLazy(&parentNode.descendants, childID, childNode)
	putLazy(&parentNode.watchees, childID, pid)
	parentNode.mu.Unlock()

	childNode.mu.Lock()
	if childNode.pid.Load() == nil {
		childNode.mu.Unlock()
		parentNode.mu.Lock()
		delete(parentNode.descendants, childID)
		delete(parentNode.watchees, childID)
		parentNode.mu.Unlock()
		return errors.New("pid does not exist")
	}

	childNode.parentNode = parentNode
	putWatcher(&childNode.watchers, parent)
	childNode.mu.Unlock()
	return nil
}

// deleteSubtree removes root and its subtree. owner is the PID root must still
// hold; a descendant is removed whatever PID it holds. Under each node's lock
// it takes the node out of every index and clears its PID, so from then on no
// lookup resolves the node and no relationship is added to it; a child being
// linked under the node at that moment finishes first, because the link holds
// the child's lock, and is then removed with the rest of the subtree. The
// relationships the nodes held are cleaned afterwards without the node locks,
// children before parents.
func (x *tree) deleteSubtree(root *pidNode, owner *PID) bool {
	removed := make([]removedNode, 0, 8)
	stack := make([]*pidNode, 0, 8)
	stack = append(stack, root)

	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		n.mu.Lock()
		current := n.pid.Load()
		if current == nil || (n == root && current != owner) {
			n.mu.Unlock()
			if n == root {
				return false
			}

			continue
		}

		x.pids.remove(n.id, n)
		x.removeName(n)
		x.qualifiedNames.remove(n.qualifiedName, n)
		n.pid.Store(nil)

		removed = append(removed, removedNode{node: n, parent: n.parentNode, watchers: n.watchers, watchees: n.watchees})
		for _, child := range n.descendants {
			stack = append(stack, child)
		}

		n.parentNode, n.watchers, n.watchees, n.descendants = nil, nil, nil, nil
		n.mu.Unlock()
	}

	// Parents were taken out before their children, so the reverse order
	// cleans children first.
	for i := len(removed) - 1; i >= 0; i-- {
		r := removed[i]

		// Clean watchers: remove this node from each watcher's watchees.
		// w.ID() is cached, so the scan allocates nothing.
		for _, w := range r.watchers {
			if watcherNode, exists := x.pids.get(w.ID()); exists {
				watcherNode.mu.Lock()
				delete(watcherNode.watchees, r.node.id)
				watcherNode.mu.Unlock()
			}
		}

		// Clean watchees: remove this node from each watchee's watchers.
		for watcheeID := range r.watchees {
			if watcheeNode, exists := x.pids.get(watcheeID); exists {
				watcheeNode.mu.Lock()
				deleteWatcher(&watcheeNode.watchers, r.node.id)
				watcheeNode.mu.Unlock()
			}
		}

		if r.parent != nil {
			r.parent.mu.Lock()
			delete(r.parent.descendants, r.node.id)
			delete(r.parent.watchees, r.node.id)
			r.parent.mu.Unlock()
		}

		x.counter.Add(-1)
	}

	return true
}

// removeName drops n from the name index, leaving the other nodes that share
// its name in place, and deletes the entry once no node carries the name.
func (x *tree) removeName(n *pidNode) {
	s := x.names.shard(n.name)
	s.mu.Lock()
	defer s.mu.Unlock()

	newest, indexed := s.nodes[n.name]
	if n.namePrev == nil && n.nameNext == nil && newest != n {
		return // not linked
	}

	if n.namePrev != nil {
		n.namePrev.nameNext = n.nameNext
	}

	if n.nameNext != nil {
		n.nameNext.namePrev = n.namePrev
	}

	if indexed && newest == n {
		if n.namePrev == nil {
			delete(s.nodes, n.name)
		} else {
			s.nodes[n.name] = n.namePrev
		}
	}

	n.namePrev, n.nameNext = nil, nil
}

// linkName appends n to the list of nodes that share its name and makes it the
// node a lookup by that name returns.
func (x *tree) linkName(n *pidNode) {
	s := x.names.shard(n.name)
	s.mu.Lock()
	defer s.mu.Unlock()

	if newest, ok := s.nodes[n.name]; ok {
		newest.nameNext = n
		n.namePrev = newest
	}

	s.nodes[n.name] = n
}

// isNoSender reports whether pid is the system's NoSender, from the cached
// value or, before the root is added, from pid's own system.
func (x *tree) isNoSender(pid *PID) bool {
	noSender := x.noSender.Load()
	if noSender == nil {
		noSender = pid.ActorSystem().NoSender()
	}

	return pid.Equals(noSender)
}

// lookup returns the node of pid. It reports false for a nil pid, for the
// cached NoSender and for a pid the tree does not know.
func (x *tree) lookup(pid *PID) (*pidNode, bool) {
	if pid == nil {
		return nil, false
	}

	if noSender := x.noSender.Load(); noSender != nil && pid.Equals(noSender) {
		return nil, false
	}

	return x.pids.get(pid.ID())
}

// childNodes returns the direct child nodes of n, read under n's lock.
func childNodes(n *pidNode) []*pidNode {
	n.mu.Lock()
	defer n.mu.Unlock()

	if len(n.descendants) == 0 {
		return nil
	}

	children := make([]*pidNode, 0, len(n.descendants))
	for _, child := range n.descendants {
		children = append(children, child)
	}

	return children
}

// lockPair locks a and b, two nodes of the tree, in the order of their IDs,
// and locks a node passed twice once, so that two edge operations on the same
// pair take the locks in the same order. Both must be in the tree: nodes the
// tree knows have distinct IDs, which is what makes the order total.
func lockPair(a, b *pidNode) {
	if a == b {
		a.mu.Lock()
		return
	}

	if a.id > b.id {
		a, b = b, a
	}

	a.mu.Lock()
	b.mu.Lock()
}

// unlockPair releases the locks lockPair took on a and b.
func unlockPair(a, b *pidNode) {
	a.mu.Unlock()
	if a != b {
		b.mu.Unlock()
	}
}

// parentStopping reports whether parent has begun to stop or passivate, in
// which case no child may be linked under it: the parent frees the children
// it reads under its node lock, and a child linked once the stop has begun
// would outlive it unsupervised.
func parentStopping(parent *PID) bool {
	return parent.isStateSet(stoppingState) || parent.isStateSet(passivatingState)
}
