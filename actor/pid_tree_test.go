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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/internal/collection"
)

func TestTree(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ports := dynaport.Get(1)
		a := &PID{address: address.New("a", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		b := &PID{address: address.New("b", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		c := &PID{address: address.New("c", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		d := &PID{address: address.New("d", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		e := &PID{address: address.New("e", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		f := &PID{address: address.New("f", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}

		tree := newTree()

		// add the root node
		err := tree.addRootNode(a)
		require.NoError(t, err)

		// add node b as a child of a
		err = tree.addNode(a, b)
		require.NoError(t, err)

		// add node c as a child of b
		err = tree.addNode(b, c)
		require.NoError(t, err)

		// add node d as a child of b
		err = tree.addNode(b, d)
		require.NoError(t, err)

		// add node e as a child of the root node a
		err = tree.addNode(a, e)
		require.NoError(t, err)

		// add node f as a child of tree root node a
		err = tree.addNode(a, f)
		require.NoError(t, err)

		// get the direct children of node a
		children := tree.children(a)
		require.Len(t, children, 3)
		expected := []string{"b", "e", "f"}
		actual := make([]string, len(children))
		for i, child := range children {
			actual[i] = child.Name()
		}
		require.ElementsMatch(t, expected, actual)

		// add e as a watcher of b
		tree.addWatcher(b, e)

		// get the watcher of node b
		// this should return e as the only watcher of b
		watchers := tree.watchers(b)
		require.Len(t, watchers, 2)
		expected = []string{"a", "e"}
		actual = make([]string, len(watchers))
		for i, watcher := range watchers {
			actual[i] = watcher.Name()
		}
		require.ElementsMatch(t, expected, actual)

		// get the watchees of node e
		// this should return b as the only watchee of e
		watchees := tree.watchees(e)
		require.Len(t, watchees, 1)
		expected = []string{"b"}
		actual = make([]string, len(watchees))
		for i, watchee := range watchees {
			actual[i] = watchee.Name()
		}
		require.ElementsMatch(t, expected, actual)

		// get all the nodes in the tree
		// this should return all the nodes in the tree
		nodes := tree.nodes()
		require.Len(t, nodes, 6)
		expected = []string{"a", "b", "c", "d", "e", "f"}
		actual = make([]string, len(nodes))
		for i, node := range nodes {
			actual[i] = node.pid.Load().Name()
		}
		require.ElementsMatch(t, expected, actual)

		// get all the descendants of the root node a
		descendants := tree.descendants(a)
		require.Len(t, descendants, 5)
		expected = []string{"b", "c", "d", "e", "f"}
		actual = make([]string, len(descendants))
		for i, descendant := range descendants {
			actual[i] = descendant.Name()
		}
		require.ElementsMatch(t, expected, actual)

		// get all the siblings of node b
		siblings := tree.siblings(b)
		require.Len(t, siblings, 2)

		expected = []string{"e", "f"}
		actual = make([]string, len(siblings))
		for i, sibling := range siblings {
			actual[i] = sibling.Name()
		}
		require.ElementsMatch(t, expected, actual)

		// get all the siblings of node c
		siblings = tree.siblings(c)
		require.Len(t, siblings, 1)
		expected = []string{"d"}
		actual = make([]string, len(siblings))
		for i, sibling := range siblings {
			actual[i] = sibling.Name()
		}

		require.ElementsMatch(t, expected, actual)

		// get all the descendants of node b
		descendants = tree.descendants(b)
		require.Len(t, descendants, 2)
		expected = []string{"c", "d"}
		actual = make([]string, len(descendants))
		for i, descendant := range descendants {
			actual[i] = descendant.Name()
		}

		require.ElementsMatch(t, expected, actual)

		// get the parent of node c
		parent, ok := tree.parent(c)
		require.True(t, ok)
		require.Equal(t, b.Name(), parent.Name())

		// delete node b
		tree.deleteNode(b)
		require.NoError(t, err)

		// get all the descendants of node a
		descendants = tree.descendants(a)
		require.Len(t, descendants, 2)
		expected = []string{"e", "f"}
		actual = make([]string, len(descendants))
		for i, descendant := range descendants {
			actual[i] = descendant.Name()
		}
		require.ElementsMatch(t, expected, actual)

		// get all the nodes in the tree
		nodes = tree.nodes()
		require.Len(t, nodes, 3)
		expected = []string{"a", "e", "f"}
		actual = make([]string, len(nodes))
		for i, node := range nodes {
			actual[i] = node.pid.Load().Name()
		}
		require.ElementsMatch(t, expected, actual)

		// get the tree count
		count := tree.count()
		require.EqualValues(t, 3, count)

		// get node e
		eid := e.ID()
		node, ok := tree.node(eid)
		require.True(t, ok)
		require.Equal(t, e.Name(), node.pid.Load().Name())

		// get root node
		root, ok := tree.root()
		require.True(t, ok)
		require.Equal(t, a.Name(), root.Name())

		tree.reset()
	})
}
func TestAddNode(t *testing.T) {
	ports := dynaport.Get(2)
	a := &PID{address: address.New("a", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
	b := &PID{address: address.New("b", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}

	tree := newTree()
	require.NoError(t, tree.addRootNode(a))

	t.Run("pid is nil", func(t *testing.T) {
		err := tree.addNode(a, nil)
		require.Error(t, err)
		require.EqualError(t, err, "pid is nil")
	})

	t.Run("parent is nil", func(t *testing.T) {
		err := tree.addNode(nil, b)
		require.Error(t, err)
		require.EqualError(t, err, "parent pid is nil")
	})

	t.Run("pid already exists in tree", func(t *testing.T) {
		// Add b as child of a
		require.NoError(t, tree.addNode(a, b))
		// Try to add b again
		err := tree.addNode(a, b)
		require.Error(t, err)
		require.EqualError(t, err, "pid already exists")
	})
}

func TestAddWatcher(t *testing.T) {
	ports := dynaport.Get(3)
	a := &PID{address: address.New("a", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
	b := &PID{address: address.New("b", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
	c := &PID{address: address.New("c", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}

	tree := newTree()
	t.Cleanup(tree.reset)

	require.NoError(t, tree.addRootNode(a))
	require.NoError(t, tree.addNode(a, b))
	require.NoError(t, tree.addNode(a, c))

	t.Run("pid is nil", func(t *testing.T) {
		tree.addWatcher(nil, b)
		// Should not panic or add anything
		require.Empty(t, tree.watchers(nil))
	})

	t.Run("watcher is nil", func(t *testing.T) {
		tree.addWatcher(b, nil)
		require.NotContains(t, tree.watchers(b), nil)
	})

	t.Run("pid is NoSender", func(t *testing.T) {
		tree.addWatcher(NoSender, b)
		require.NotContains(t, tree.watchers(NoSender), b)
	})

	t.Run("watcher is NoSender", func(t *testing.T) {
		tree.addWatcher(b, NoSender)
		require.NotContains(t, tree.watchers(b), NoSender)
	})

	t.Run("pid does not exist in tree", func(t *testing.T) {
		d := &PID{address: address.New("d", "TestSys", "host", ports[1]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		tree.addWatcher(d, b)
		require.Empty(t, tree.watchers(d))
	})

	t.Run("watcher does not exist in tree", func(t *testing.T) {
		d := &PID{address: address.New("d", "TestSys", "host", ports[1]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		tree.addWatcher(b, d)
		require.NotContains(t, tree.watchers(b), d)
	})

	t.Run("happy path", func(t *testing.T) {
		tree.addWatcher(b, c)
		watchers := tree.watchers(b)
		names := make([]string, len(watchers))
		for i, w := range watchers {
			names[i] = w.Name()
		}
		require.Contains(t, names, "a")
		require.Contains(t, names, "c")
		// c should have b as a watchee
		watchees := tree.watchees(c)
		watcheeNames := make([]string, len(watchees))
		for i, w := range watchees {
			watcheeNames[i] = w.Name()
		}
		require.Contains(t, watcheeNames, "b")
	})
}

func TestWatchers(t *testing.T) {
	tree := newTree()
	t.Run("nil pid", func(t *testing.T) {
		watchers := tree.watchers(nil)
		require.Empty(t, watchers)
	})
	t.Run("pid not in tree", func(t *testing.T) {
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		watchers := tree.watchers(pid)
		require.Empty(t, watchers)
	})

	t.Cleanup(tree.reset)
}

func TestWatchees(t *testing.T) {
	tree := newTree()
	t.Run("nil pid", func(t *testing.T) {
		watchees := tree.watchees(nil)
		require.Empty(t, watchees)
	})
	t.Run("pid not in tree", func(t *testing.T) {
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		watchees := tree.watchees(pid)
		require.Empty(t, watchees)
	})

	t.Cleanup(tree.reset)
}

func TestParent(t *testing.T) {
	tree := newTree()
	t.Run("nil pid", func(t *testing.T) {
		parent, ok := tree.parent(nil)
		require.False(t, ok)
		require.Nil(t, parent)
	})
	t.Run("pid not in tree", func(t *testing.T) {
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		parent, ok := tree.parent(pid)
		require.False(t, ok)
		require.Nil(t, parent)
	})

	t.Cleanup(tree.reset)
}

func TestRoo(t *testing.T) {
	tree := newTree()
	t.Run("empty tree", func(t *testing.T) {
		root, ok := tree.root()
		require.False(t, ok)
		require.Nil(t, root)
	})

	t.Run("tree with root", func(t *testing.T) {
		pid := &PID{address: address.New("root", "TestSys", "host", 0), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		require.NoError(t, tree.addRootNode(pid))
		root, ok := tree.root()
		require.True(t, ok)
		require.Equal(t, pid.Name(), root.Name())
	})

	t.Cleanup(tree.reset)
}

func TestSiblings(t *testing.T) {
	tree := newTree()
	t.Run("nil pid", func(t *testing.T) {
		siblings := tree.siblings(nil)
		require.Empty(t, siblings)
	})

	t.Run("pid not in tree", func(t *testing.T) {
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		siblings := tree.siblings(pid)
		require.Empty(t, siblings)
	})

	// add test for pid has no parent
	t.Run("pid has no parent", func(t *testing.T) {
		pid := &PID{address: address.New("no_parent", "TestSys", "host", 0), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		pidnode := &pidNode{
			pid:         atomic.Pointer[PID]{},
			watchers:    collection.NewMap[string, *PID](),
			watchees:    collection.NewMap[string, *PID](),
			descendants: collection.NewMap[string, *pidNode](),
		}
		pidnode.pid.Store(pid)

		tree.pids.Set(pid.ID(), pidnode)
		siblings := tree.siblings(pid)
		require.Empty(t, siblings)
	})

	t.Cleanup(tree.reset)
}
func TestDescendants(t *testing.T) {
	tree := newTree()
	t.Run("nil pid", func(t *testing.T) {
		descendants := tree.descendants(nil)
		require.Empty(t, descendants)
	})

	t.Run("pid not in tree", func(t *testing.T) {
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		descendants := tree.descendants(pid)
		require.Empty(t, descendants)
	})

	t.Cleanup(tree.reset)
}

func TestDeleteNode(t *testing.T) {
	tree := newTree()
	t.Run("nil pid", func(t *testing.T) {
		require.NotPanics(t, func() {
			tree.deleteNode(nil)
		})
	})

	t.Run("pid not in tree", func(t *testing.T) {
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		require.NotPanics(t, func() {
			tree.deleteNode(pid)
		})
	})

	t.Cleanup(tree.reset)
}

func TestChildren(t *testing.T) {
	tree := newTree()
	t.Run("nil pid", func(t *testing.T) {
		children := tree.children(nil)
		require.Empty(t, children)
	})

	t.Run("pid not in tree", func(t *testing.T) {
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
		children := tree.children(pid)
		require.Empty(t, children)
	})

	t.Cleanup(tree.reset)
}
