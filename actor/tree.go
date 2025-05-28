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
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/tochemey/goakt/v3/internal/collection"
)

// tree represents the entire actors Tree structure
type tree struct {
	nodeShards   shards
	parentShards shards
	nodePool     *sync.Pool
	valuePool    *sync.Pool
	counter      atomic.Int64
	rootNode     *treeNode
}

// newTree creates a new instance of the actors Tree
func newTree() *tree {
	return &tree{
		nodeShards:   newShards(),
		parentShards: newShards(),
		nodePool: &sync.Pool{
			New: func() any {
				return &treeNode{
					Descendants: collection.NewList[*treeNode](),
					Watchees:    collection.NewList[*treeNode](),
					Watchers:    collection.NewList[*treeNode](),
				}
			},
		},
		valuePool: &sync.Pool{
			New: func() any {
				return &value{}
			},
		},
	}
}

// addNode adds a new node to the tree under a given parent
// The first node that is created without a parent becomes the defacto root node
func (x *tree) addNode(parent, child *PID) error {
	var (
		parentNode *treeNode
		ok         bool
	)

	// check whether the node to be added is a root node
	if parent == nil && x.rootNode != nil {
		return errors.New("root node already set")
	}

	// validate parent node existence
	if parent != nil && !parent.Equals(NoSender) {
		parentNode, ok = x.node(parent.ID())
		if !ok || parentNode == nil {
			return fmt.Errorf("parent node=(%s) does not exist", parent.ID())
		}
	}

	// create a new node from the pool
	newNode := x.nodePool.Get().(*treeNode)
	x.resetNode(newNode, child.ID())

	// create a value using the pool and set its data
	val := x.valuePool.Get().(*value)
	val._data = child

	// store the value atomically in the node
	newNode.setValue(val)

	// store the node in the tree
	x.nodeShards.Store(child.ID(), newNode)

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

	x.counter.Add(1)
	return nil
}

// addWatcher adds a watcher to the given node. Make sure to check the existence of both PID
// before watching because this call will do nothing when the watcher and the watched node do not exist in
// the tree
func (x *tree) addWatcher(node, watcher *PID) {
	currentNode, currentOk := x.node(node.ID())
	watcherNode, watcherOk := x.node(watcher.ID())
	if !currentOk || !watcherOk || currentNode == nil {
		return
	}

	watcherNode.Watchees.Append(currentNode)
	currentNode.Watchers.Append(watcherNode)
}

// ancestors retrieves all ancestorIDs nodesMap of a given node
func (x *tree) ancestors(pid *PID) ([]*treeNode, bool) {
	ancestorIDs, ok := x.ancestorIDs(pid.ID())
	if !ok {
		return nil, false
	}

	var ancestors []*treeNode
	for _, ancestorID := range ancestorIDs {
		if ancestor, ok := x.node(ancestorID); ok {
			ancestors = append(ancestors, ancestor)
		}
	}
	return ancestors, true
}

// parentAt returns a given PID direct parent
func (x *tree) parentAt(pid *PID, level int) (*treeNode, bool) {
	return x.ancestorAt(pid, level)
}

// descendants retrieves all descendants of the node with the given ID.
func (x *tree) descendants(pid *PID) ([]*treeNode, bool) {
	node, ok := x.node(pid.ID())
	if !ok {
		return nil, false
	}

	return collectDescendants(node), true
}

// siblings returns a slice of treeNode that are the siblings of the given node.
// If the node is the root (i.e. has no parent), it returns an empty slice.
// It returns (siblings, true) on success, or (nil, false) if an error occur
func (x *tree) siblings(pid *PID) ([]*treeNode, bool) {
	// get the direct parent of the given node
	parentNode, ok := x.parentAt(pid, 0)
	if !ok || parentNode == nil {
		return nil, false
	}

	// here we are only fetching the first level children
	children := parentNode.Descendants.Items()
	var siblings []*treeNode
	for _, child := range children {
		if !child.value().Equals(pid) {
			siblings = append(siblings, child)
		}
	}
	return siblings, true
}

// deleteNode deletes a node and all its descendants
func (x *tree) deleteNode(pid *PID) {
	node, ok := x.node(pid.ID())
	if !ok {
		return
	}

	// remove the node from its parent's Children slice
	if ancestors, ok := x.parentShards.Load(pid.ID()); ok && len(ancestors.([]string)) > 0 {
		parentID := ancestors.([]string)[0]
		if parent, found := x.node(parentID); found {
			children := filterOutChild(parent.Descendants, pid.ID())
			parent.Descendants.Reset()
			parent.Descendants.AppendMany(children.Items()...)
		}
	}

	// recursive function to delete a node and its descendants
	var deleteChildren func(n *treeNode)
	deleteChildren = func(n *treeNode) {
		for index, child := range n.Descendants.Items() {
			n.Descendants.Delete(index)
			deleteChildren(child)
		}
		// delete node from maps and pool
		x.nodeShards.Delete(n.ID)
		x.parentShards.Delete(n.ID)
		x.nodePool.Put(n)
		x.counter.Add(-1)
	}

	deleteChildren(node)
}

// node retrieves a node by its ID
func (x *tree) node(id string) (*treeNode, bool) {
	value, ok := x.nodeShards.Load(id)
	if !ok {
		return nil, false
	}
	node, ok := value.(*treeNode)
	return node, ok
}

// nodes retrieves all nodes in the tree efficiently
func (x *tree) nodes() []*treeNode {
	if x.length() == 0 {
		return nil
	}
	var nodes []*treeNode
	x.nodeShards.Range(func(_, value any) {
		node := value.(*treeNode)
		nodes = append(nodes, node)
	})
	return nodes
}

// length returns the current number of nodesMap in the tree
func (x *tree) length() int64 {
	return x.counter.Load()
}

// reset clears all nodes and parents, resetting the tree to an empty state
func (x *tree) reset() {
	x.nodeShards.Reset()   // Reset nodesMap map
	x.parentShards.Reset() // Reset parents map
	x.counter.Store(0)
}

// ancestorIDs returns the list of ancestor nodesMap
func (x *tree) ancestorIDs(id string) ([]string, bool) {
	if value, ok := x.parentShards.Load(id); ok {
		return value.([]string), true
	}
	return nil, false
}

// addChild safely appends a child to a parent's Children slice using atomic operations.
func (x *tree) addChild(parent *treeNode, child *treeNode) {
	parent.Descendants.Append(child)
	parent.Watchees.Append(child)
	child.Watchers.Append(parent)
}

// updateAncestors updates the parent/ancestor relationships.
func (x *tree) updateAncestors(parentID, childID string) {
	switch ancestors, ok := x.ancestorIDs(parentID); {
	case ok:
		x.parentShards.Store(childID, append([]string{parentID}, ancestors...))
	default:
		x.parentShards.Store(childID, []string{parentID})
	}
}

// filterOutChild removes the node with the given ID from the Children slice.
func filterOutChild(children *collection.List[*treeNode], childID string) *collection.List[*treeNode] {
	for i, child := range children.Items() {
		if child.ID == childID {
			children.Delete(i)
			return children
		}
	}
	return children
}

// collectDescendants collects all the descendants and grand children
func collectDescendants(node *treeNode) []*treeNode {
	output := collection.NewList[*treeNode]()

	var recursive func(*treeNode)
	recursive = func(currentNode *treeNode) {
		for _, child := range currentNode.Descendants.Items() {
			output.Append(child)
			recursive(child)
		}
	}

	recursive(node)
	return output.Items()
}

// ancestorAt retrieves the ancestor at the specified level (0 for parent, 1 for grandparent, etc.)
func (x *tree) ancestorAt(pid *PID, level int) (*treeNode, bool) {
	ancestors, ok := x.ancestorIDs(pid.ID())
	if ok && len(ancestors) > level {
		return x.node(ancestors[level])
	}
	return nil, false
}

func (x *tree) resetNode(node *treeNode, id string) {
	node.ID = id
	node.Descendants.Reset()
	node.Watchees.Reset()
	node.Watchers.Reset()
}
