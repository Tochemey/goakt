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

package actors

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/tochemey/goakt/v3/internal/collection/slice"
)

// pidTree represents the entire actors Tree structure
type pidTree struct {
	nodes     shardedMap
	parents   shardedMap
	nodePool  *sync.Pool
	valuePool *sync.Pool
	size      atomic.Int64
	rootNode  *pidNode
}

// newTree creates a new instance of the actors Tree
func newTree() *pidTree {
	return &pidTree{
		nodes:   newShardedMap(),
		parents: newShardedMap(),
		nodePool: &sync.Pool{
			New: func() any {
				return &pidNode{
					Descendants: slice.New[*pidNode](),
					Watchees:    slice.New[*pidNode](),
					Watchers:    slice.New[*pidNode](),
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
// The first node that is created without a parent becomes the defacto root node
func (x *pidTree) AddNode(parent, child *PID) error {
	var (
		parentNode *pidNode
		ok         bool
	)

	// check whether the node to be added is a root node
	if parent == nil && x.rootNode != nil {
		return errors.New("root node already set")
	}

	// validate parent node existence
	if parent != nil && !parent.Equals(NoSender) {
		parentNode, ok = x.GetNode(parent.ID())
		if !ok || parentNode == nil {
			return fmt.Errorf("parent node=(%s) does not exist", parent.ID())
		}
	}

	// create a new node from the pool
	newNode := x.nodePool.Get().(*pidNode)
	x.resetNode(newNode, child.ID())

	// create a pidValue using the pool and set its data
	val := x.valuePool.Get().(*pidValue)
	val.data = child

	// store the value atomically in the node
	newNode.SetValue(val)

	// store the node in the tree
	x.nodes.Store(child.ID(), newNode)

	// when parentNode is defined
	if parentNode != nil {
		x.addChild(parentNode, newNode)
		x.updateAncestors(parent.ID(), child.ID())
	}

	// only set the root node when parent is nil
	if parentNode == nil {
		// set the given node as root node
		x.rootNode = newNode
	}

	x.size.Add(1)
	return nil
}

// AddWatcher adds a watcher to the given node. Make sure to check the existence of both PID
// before watching because this call will do nothing when the watcher and the watched node do not exist in
// the tree
func (x *pidTree) AddWatcher(node, watcher *PID) {
	currentNode, currentOk := x.GetNode(node.ID())
	watcherNode, watcherOk := x.GetNode(watcher.ID())
	if !currentOk || !watcherOk || currentNode == nil {
		return
	}

	watcherNode.Watchees.Append(currentNode)
	currentNode.Watchers.Append(watcherNode)
}

// Ancestors retrieves all ancestors nodes of a given node
func (x *pidTree) Ancestors(pid *PID) ([]*pidNode, bool) {
	ancestorIDs, ok := x.ancestors(pid.ID())
	if !ok {
		return nil, false
	}

	var ancestors []*pidNode
	for _, ancestorID := range ancestorIDs {
		if ancestor, ok := x.GetNode(ancestorID); ok {
			ancestors = append(ancestors, ancestor)
		}
	}
	return ancestors, true
}

// ParentAt returns a given PID direct parent
func (x *pidTree) ParentAt(pid *PID, level int) (*pidNode, bool) {
	return x.ancestorAt(pid, level)
}

// Descendants retrieves all descendants of the node with the given ID.
func (x *pidTree) Descendants(pid *PID) ([]*pidNode, bool) {
	node, ok := x.GetNode(pid.ID())
	if !ok {
		return nil, false
	}

	return collectDescendants(node), true
}

// Siblings returns a slice of pidNode that are the siblings of the given node.
// If the node is the root (i.e. has no parent), it returns an empty slice.
// It returns (siblings, true) on success, or (nil, false) if an error occur
func (x *pidTree) Siblings(pid *PID) ([]*pidNode, bool) {
	// get the direct parent of the given node
	parentNode, ok := x.ParentAt(pid, 0)
	if !ok {
		return nil, false
	}

	// defensive programming
	if parentNode == nil {
		return nil, false
	}

	// here we are only fetching the first level children
	children := parentNode.Descendants.Items()
	var siblings []*pidNode
	for _, child := range children {
		if !child.GetValue().Equals(pid) {
			siblings = append(siblings, child)
		}
	}
	return siblings, true
}

// DeleteNode deletes a node and all its descendants
func (x *pidTree) DeleteNode(pid *PID) {
	node, ok := x.GetNode(pid.ID())
	if !ok {
		return
	}

	// remove the node from its parent's Children slice
	if ancestors, ok := x.parents.Load(pid.ID()); ok && len(ancestors.([]string)) > 0 {
		parentID := ancestors.([]string)[0]
		if parent, found := x.GetNode(parentID); found {
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
		x.nodes.Delete(n.ID)
		x.parents.Delete(n.ID)
		x.nodePool.Put(n)
		x.size.Add(-1)
	}

	deleteChildren(node)
}

// GetNode retrieves a node by its ID
func (x *pidTree) GetNode(id string) (*pidNode, bool) {
	value, ok := x.nodes.Load(id)
	if !ok {
		return nil, false
	}
	node, ok := value.(*pidNode)
	return node, ok
}

// Nodes retrieves all nodes in the tree efficiently
func (x *pidTree) Nodes() []*pidNode {
	if x.Size() == 0 {
		return nil
	}
	var nodes []*pidNode
	x.nodes.Range(func(_, value any) {
		node := value.(*pidNode)
		nodes = append(nodes, node)
	})
	return nodes
}

// Size returns the current number of nodes in the tree
func (x *pidTree) Size() int64 {
	return x.size.Load()
}

// Reset clears all nodes and parents, resetting the tree to an empty state
func (x *pidTree) Reset() {
	x.nodes.Reset()   // Reset nodes map
	x.parents.Reset() // Reset parents map
	x.size.Store(0)
}

// ancestors returns the list of ancestor nodes
func (x *pidTree) ancestors(id string) ([]string, bool) {
	if value, ok := x.parents.Load(id); ok {
		return value.([]string), true
	}
	return nil, false
}

// addChild safely appends a child to a parent's Children slice using atomic operations.
func (x *pidTree) addChild(parent *pidNode, child *pidNode) {
	parent.Descendants.Append(child)
	parent.Watchees.Append(child)
	child.Watchers.Append(parent)
}

// updateAncestors updates the parent/ancestor relationships.
func (x *pidTree) updateAncestors(parentID, childID string) {
	switch ancestors, ok := x.ancestors(parentID); {
	case ok:
		x.parents.Store(childID, append([]string{parentID}, ancestors...))
	default:
		x.parents.Store(childID, []string{parentID})
	}
}

// filterOutChild removes the node with the given ID from the Children slice.
func filterOutChild(children *slice.Slice[*pidNode], childID string) *slice.Slice[*pidNode] {
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
	output := slice.New[*pidNode]()

	var recursive func(*pidNode)
	recursive = func(currentNode *pidNode) {
		for _, child := range currentNode.Descendants.Items() {
			output.Append(child)
			recursive(child)
		}
	}

	recursive(node)
	return output.Items()
}

// ancestorAt retrieves the ancestor at the specified level (0 for parent, 1 for grandparent, etc.)
func (x *pidTree) ancestorAt(pid *PID, level int) (*pidNode, bool) {
	ancestors, ok := x.ancestors(pid.ID())
	if ok && len(ancestors) > level {
		return x.GetNode(ancestors[level])
	}
	return nil, false
}

func (x *pidTree) resetNode(node *pidNode, id string) {
	node.ID = id
	node.Descendants.Reset()
	node.Watchees.Reset()
	node.Watchers.Reset()
}
