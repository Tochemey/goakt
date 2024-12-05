/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/tochemey/goakt/v2/internal/slice"
)

// pidTree represents the entire actors Tree structure
type pidTree struct {
	nodes     *shardedMap
	parents   *shardedMap
	nodePool  *sync.Pool
	valuePool *sync.Pool
	size      atomic.Int64
}

// newTree creates a new instance of the actors Tree
func newTree() *pidTree {
	return &pidTree{
		nodes:   newShardedMap(),
		parents: newShardedMap(),
		nodePool: &sync.Pool{
			New: func() any {
				return &pidNode{
					Descendants: slice.NewThreadSafe[*pidNode](),
					Watchees:    slice.NewThreadSafe[*pidNode](),
					Watchers:    slice.NewThreadSafe[*pidNode](),
				}
			},
		},
		valuePool: &sync.Pool{
			New: func() any {
				return &pidValue{}
			},
		},
	}
}

// AddNode adds a new node to the tree under a given parent
func (t *pidTree) AddNode(parent, child *PID) error {
	var (
		parentNode *pidNode
		ok         bool
	)
	// validate parent node existence
	if !parent.Equals(NoSender) {
		parentNode, ok = t.getNode(parent.ID())
		if !ok || parentNode == nil {
			return fmt.Errorf("parent node=(%s) does not exist", parent.ID())
		}
	}

	// create a new node from the pool
	newNode := t.nodePool.Get().(*pidNode)
	newNode.ID = child.ID()
	newNode.Descendants.Reset()
	newNode.Watchers.Reset()
	newNode.Watchees.Reset()

	// create a pidValue using the pool and set its data
	val := t.valuePool.Get().(*pidValue)
	val.data = child

	// store the value atomically in the node
	newNode.SetValue(val)

	// store the node in the tree
	t.nodes.Store(child.ID(), newNode)

	// when parentNode is defined
	if parentNode != nil {
		t.addChild(parentNode, newNode)
		t.updateAncestors(parent.ID(), child.ID())
	}

	t.size.Add(1)
	return nil
}

// AddWatcher adds a watcher to the given node. Make sure to check the existence of both PID
// before watching because this call will do nothing when the watcher and the watched node do not exist in
// the tree
func (t *pidTree) AddWatcher(node, watcher *PID) {
	currentNode, ok := t.getNode(node.ID())
	if !ok || currentNode == nil {
		return
	}

	watcherNode, ok := t.getNode(watcher.ID())
	if !ok {
		return
	}

	watcherNode.Watchees.Append(currentNode)
	currentNode.Watchers.Append(watcherNode)
}

// Ancestors retrieves all ancestors nodes of a given node
func (t *pidTree) Ancestors(pid *PID) ([]*pidNode, bool) {
	ancestorIDs, ok := t.getAncestors(pid.ID())
	if !ok {
		return nil, false
	}

	var ancestors []*pidNode
	for _, ancestorID := range ancestorIDs {
		if ancestor, ok := t.getNode(ancestorID); ok {
			ancestors = append(ancestors, ancestor)
		}
	}
	return ancestors, true
}

// Parent returns a given PID direct parent
func (t *pidTree) Parent(pid *PID) (*pidNode, bool) {
	ancestors, ok := t.getAncestors(pid.ID())
	if ok && len(ancestors) > 0 {
		return t.getNode(ancestors[0])
	}
	return nil, false
}

// GrandParent retrieves the grandparent of a node
func (t *pidTree) GrandParent(pid *PID) (*pidNode, bool) {
	ancestors, ok := t.getAncestors(pid.ID())
	if ok && len(ancestors) > 1 {
		return t.getNode(ancestors[1])
	}
	return nil, false
}

// GreatGrandParent retrieves the great-grand-parent of a node
func (t *pidTree) GreatGrandParent(pid *PID) (*pidNode, bool) {
	ancestors, ok := t.getAncestors(pid.ID())
	if ok && len(ancestors) > 2 {
		return t.getNode(ancestors[2])
	}
	return nil, false
}

// Descendants retrieves all descendants of the node with the given ID.
func (t *pidTree) Descendants(pid *PID) ([]*pidNode, bool) {
	node, ok := t.GetNode(pid.ID())
	if !ok {
		return nil, false
	}

	return collectDescendants(node), true
}

// DeleteNode deletes a node and all its descendants
func (t *pidTree) DeleteNode(pid *PID) {
	node, ok := t.getNode(pid.ID())
	if !ok {
		return
	}

	// remove the node from its parent's Children slice
	if ancestors, ok := t.parents.Load(pid.ID()); ok && len(ancestors.([]string)) > 0 {
		parentID := ancestors.([]string)[0]
		if parent, found := t.GetNode(parentID); found {
			children := filterOutChild(parent.Descendants, pid.ID())
			parent.Descendants.Reset()
			parent.Descendants.AppendMany(children.Items()...)
		}
	}

	// recursive function to delete a node and its descendants
	var deleteChildren func(n *pidNode)
	deleteChildren = func(n *pidNode) {
		for index, child := range n.Descendants.Items() {
			n.Descendants.Delete(index)
			deleteChildren(child)
		}
		// delete node from maps and pool
		t.nodes.Delete(n.ID)
		t.parents.Delete(n.ID)
		t.nodePool.Put(n)
		t.size.Add(-1)
	}

	deleteChildren(node)
}

// GetNode retrieves a node by its ID
func (t *pidTree) GetNode(id string) (*pidNode, bool) {
	return t.getNode(id)
}

// Nodes retrieves all nodes in the tree efficiently
func (t *pidTree) Nodes() []*pidNode {
	var nodes []*pidNode
	t.nodes.Range(func(_, value any) bool {
		node := value.(*pidNode)
		nodes = append(nodes, node)
		return true
	})
	return nodes
}

// Size returns the current number of nodes in the tree
func (t *pidTree) Size() int64 {
	return t.size.Load()
}

// Reset clears all nodes and parents, resetting the tree to an empty state
func (t *pidTree) Reset() {
	t.nodes.Reset()   // Reset nodes map
	t.parents.Reset() // Reset parents map
	t.size.Store(0)
}

// getNode is a helper to get a node from sync.Map
func (t *pidTree) getNode(id string) (*pidNode, bool) {
	if value, ok := t.nodes.Load(id); ok {
		return value.(*pidNode), true
	}
	return nil, false
}

// getAncestors returns the list of ancestor nodes
func (t *pidTree) getAncestors(id string) ([]string, bool) {
	if value, ok := t.parents.Load(id); ok {
		return value.([]string), true
	}
	return nil, false
}

// addChild safely appends a child to a parent's Children slice using atomic operations.
func (t *pidTree) addChild(parent *pidNode, child *pidNode) {
	parent.Descendants.Append(child)
	parent.Watchees.Append(child)
	child.Watchers.Append(parent)
}

// updateAncestors updates the parent/ancestor relationships.
func (t *pidTree) updateAncestors(parentID, childID string) {
	if ancestors, ok := t.getAncestors(parentID); ok {
		t.parents.Store(childID, append([]string{parentID}, ancestors...))
	} else {
		t.parents.Store(childID, []string{parentID})
	}
}

// filterOutChild removes the node with the given ID from the Children slice.
func filterOutChild(children *slice.ThreadSafe[*pidNode], childID string) *slice.ThreadSafe[*pidNode] {
	for i, child := range children.Items() {
		if child.ID == childID {
			children.Delete(i)
			return children
		}
	}
	return children
}

// collectDescendants collects all the descendants and grand children
func collectDescendants(node *pidNode) []*pidNode {
	output := slice.NewThreadSafe[*pidNode]()

	var collectRecursive func(*pidNode)
	collectRecursive = func(currentNode *pidNode) {
		for _, child := range currentNode.Descendants.Items() {
			output.Append(child)
			collectRecursive(child)
		}
	}

	collectRecursive(node)
	return output.Items()
}
