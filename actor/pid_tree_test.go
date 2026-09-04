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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/address"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
)

func TestTree(t *testing.T) {
	ports := dynaport.Get(1)
	actorSystem, _ := NewActorSystem("TestSys")

	a := MockPID(actorSystem, "a", ports[0])
	b := MockPID(actorSystem, "b", ports[0])
	c := MockPID(actorSystem, "c", ports[0])
	d := MockPID(actorSystem, "d", ports[0])
	e := MockPID(actorSystem, "e", ports[0])
	f := MockPID(actorSystem, "f", ports[0])

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
		actual[i] = node.name
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
		actual[i] = node.name
	}
	require.ElementsMatch(t, expected, actual)

	// get the tree count
	count := tree.count()
	require.EqualValues(t, 3, count)

	// get node e
	eid := e.ID()
	node, ok := tree.node(eid)
	require.True(t, ok)
	require.Equal(t, e.Name(), node.name)

	// get root node
	root, ok := tree.root()
	require.True(t, ok)
	require.Equal(t, a.Name(), root.Name())

	tree.reset()
}
func TestAddNode(t *testing.T) {
	ports := dynaport.Get(2)
	actorSystem, _ := NewActorSystem("TestSys")
	a := MockPID(actorSystem, "a", ports[0])
	b := MockPID(actorSystem, "b", ports[0])

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
	actorSystem, _ := NewActorSystem("TestSys")
	a := MockPID(actorSystem, "a", ports[0])
	b := MockPID(actorSystem, "b", ports[0])
	c := MockPID(actorSystem, "c", ports[0])
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

	t.Run("pid does not exist in tree", func(t *testing.T) {
		d := MockPID(actorSystem, "d", ports[1])
		tree.addWatcher(d, b)
		require.Empty(t, tree.watchers(d))
	})

	t.Run("watcher does not exist in tree", func(t *testing.T) {
		d := MockPID(actorSystem, "d", ports[2])
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
		actorSystem, _ := NewActorSystem("TestSys")
		addr := address.New("not_in_tree", "TestSys", "host", 0)
		pid := &PID{address: addr, path: newPath(addr), actorSystem: actorSystem}
		watchers := tree.watchers(pid)
		require.Empty(t, watchers)
	})

	t.Cleanup(tree.reset)
}

func TestWatchees(t *testing.T) {
	tree := newTree()
	actorSystem, _ := NewActorSystem("TestSys")
	t.Run("nil pid", func(t *testing.T) {
		watchees := tree.watchees(nil)
		require.Empty(t, watchees)
	})
	t.Run("pid not in tree", func(t *testing.T) {
		addr := address.New("not_in_tree", "TestSys", "host", 0)
		pid := &PID{address: addr, path: newPath(addr), actorSystem: actorSystem}
		watchees := tree.watchees(pid)
		require.Empty(t, watchees)
	})

	t.Cleanup(tree.reset)
}

func TestParent(t *testing.T) {
	tree := newTree()
	actorSystem, _ := NewActorSystem("TestSys")
	t.Run("nil pid", func(t *testing.T) {
		parent, ok := tree.parent(nil)
		require.False(t, ok)
		require.Nil(t, parent)
	})
	t.Run("pid not in tree", func(t *testing.T) {
		addr := address.New("not_in_tree", "TestSys", "host", 0)
		pid := &PID{address: addr, path: newPath(addr), actorSystem: actorSystem}
		parent, ok := tree.parent(pid)
		require.False(t, ok)
		require.Nil(t, parent)
	})

	t.Cleanup(tree.reset)
}

func TestRoot(t *testing.T) {
	tree := newTree()
	actorSystem, _ := NewActorSystem("TestSys")
	t.Run("empty tree", func(t *testing.T) {
		root, ok := tree.root()
		require.False(t, ok)
		require.Nil(t, root)
	})

	t.Run("tree with root", func(t *testing.T) {
		addr := address.New("root", "TestSys", "host", 0)
		pid := &PID{address: addr, path: newPath(addr), actorSystem: actorSystem}
		require.NoError(t, tree.addRootNode(pid))
		root, ok := tree.root()
		require.True(t, ok)
		require.Equal(t, pid.Name(), root.Name())
	})

	t.Cleanup(tree.reset)
}

func TestDeleteRootNodeClearsRoot(t *testing.T) {
	ports := dynaport.Get(1)
	actorSystem, _ := NewActorSystem("TestSys")
	tree := newTree()
	addr := address.New("root-delete", "TestSys", "host", ports[0])
	pid := &PID{address: addr, path: newPath(addr), actorSystem: actorSystem}
	require.NoError(t, tree.addRootNode(pid))

	tree.deleteNode(pid)

	root, ok := tree.root()
	require.False(t, ok)
	require.Nil(t, root)
	require.Zero(t, tree.count())
}

func TestSiblings(t *testing.T) {
	tree := newTree()
	actorSystem, _ := NewActorSystem("TestSys")
	t.Run("nil pid", func(t *testing.T) {
		siblings := tree.siblings(nil)
		require.Empty(t, siblings)
	})

	t.Run("pid not in tree", func(t *testing.T) {
		addr := address.New("not_in_tree", "TestSys", "host", 0)
		pid := &PID{address: addr, path: newPath(addr), actorSystem: actorSystem}
		siblings := tree.siblings(pid)
		require.Empty(t, siblings)
	})

	// add test for pid has no parent
	t.Run("pid has no parent", func(t *testing.T) {
		addr := address.New("no_parent", "TestSys", "host", 0)
		pid := &PID{address: addr, path: newPath(addr), actorSystem: actorSystem}
		pidnode := newPidNode(pid)

		tree.pids[pid.ID()] = pidnode
		siblings := tree.siblings(pid)
		require.Empty(t, siblings)
	})

	t.Cleanup(tree.reset)
}
func TestDescendants(t *testing.T) {
	tree := newTree()
	actorSystem, _ := NewActorSystem("TestSys")
	t.Run("nil pid", func(t *testing.T) {
		descendants := tree.descendants(nil)
		require.Empty(t, descendants)
	})

	t.Run("pid not in tree", func(t *testing.T) {
		addr := address.New("not_in_tree", "TestSys", "host", 0)
		pid := &PID{address: addr, path: newPath(addr), actorSystem: actorSystem}
		descendants := tree.descendants(pid)
		require.Empty(t, descendants)
	})

	t.Cleanup(tree.reset)
}

func TestDeleteNode(t *testing.T) {
	tree := newTree()
	actorSystem, _ := NewActorSystem("TestSys")
	t.Run("nil pid", func(t *testing.T) {
		require.NotPanics(t, func() {
			tree.deleteNode(nil)
		})
	})

	t.Run("pid not in tree", func(t *testing.T) {
		addr := address.New("not_in_tree", "TestSys", "host", 0)
		pid := &PID{address: addr, path: newPath(addr), actorSystem: actorSystem}
		require.NotPanics(t, func() {
			tree.deleteNode(pid)
		})
	})

	t.Cleanup(tree.reset)
}

func TestChildren(t *testing.T) {
	tree := newTree()
	actorSystem, _ := NewActorSystem("TestSys")
	t.Run("nil pid", func(t *testing.T) {
		children := tree.children(nil)
		require.Empty(t, children)
	})

	t.Run("pid not in tree", func(t *testing.T) {
		addr := address.New("not_in_tree", "TestSys", "host", 0)
		pid := &PID{address: addr, path: newPath(addr), actorSystem: actorSystem}
		children := tree.children(pid)
		require.Empty(t, children)
	})

	t.Cleanup(tree.reset)
}

func TestAddRootNodeValidation(t *testing.T) {
	system, _ := NewActorSystem("TestSys")
	impl, ok := system.(*actorSystem)
	require.True(t, ok)
	impl.noSender = MockPID(system, "nosender", 0)
	noSender := impl.noSender

	t.Run("pid is nil", func(t *testing.T) {
		tree := newTree()
		err := tree.addRootNode(nil)
		require.Error(t, err)
		require.EqualError(t, err, "pid is nil")
	})

	t.Run("pid is NoSender", func(t *testing.T) {
		tree := newTree()
		tree.noSender = noSender
		err := tree.addRootNode(noSender)
		require.Error(t, err)
		require.EqualError(t, err, "pid cannot be NoSender")
	})

	t.Run("duplicate pid", func(t *testing.T) {
		tree := newTree()
		root := MockPID(system, "root", 1)
		require.NoError(t, tree.addRootNode(root))
		err := tree.addRootNode(root)
		require.Error(t, err)
		require.EqualError(t, err, "pid already exists")
	})
}

func TestAddNodeParentValidation(t *testing.T) {
	system, _ := NewActorSystem("TestSys")
	impl, ok := system.(*actorSystem)
	require.True(t, ok)
	impl.noSender = MockPID(system, "nosender", 0)
	noSender := impl.noSender

	t.Run("parent is NoSender", func(t *testing.T) {
		tree := newTree()
		root := MockPID(system, "root", 1)
		child := MockPID(system, "child", 2)
		require.NoError(t, tree.addRootNode(root))
		tree.noSender = noSender
		err := tree.addNode(noSender, child)
		require.Error(t, err)
		require.EqualError(t, err, "parent pid cannot be NoSender")
	})

	t.Run("parent pid does not exist", func(t *testing.T) {
		tree := newTree()
		parent := MockPID(system, "missing", 3)
		child := MockPID(system, "child", 4)
		err := tree.addNode(parent, child)
		require.Error(t, err)
		require.EqualError(t, err, "parent pid does not exist")
		require.Equal(t, noSender, tree.noSender)
	})
}

func TestTreeNoSenderGuards(t *testing.T) {
	system, _ := NewActorSystem("TestSys")
	impl, ok := system.(*actorSystem)
	require.True(t, ok)
	impl.noSender = MockPID(system, "nosender", 0)
	tree := newTree()
	root := MockPID(system, "root", 1)
	child := MockPID(system, "child", 2)

	require.NoError(t, tree.addRootNode(root))
	require.NoError(t, tree.addNode(root, child))

	noSender := system.NoSender()

	require.Nil(t, tree.children(noSender))
	require.Nil(t, tree.descendants(noSender))
	require.Nil(t, tree.watchers(noSender))
	require.Nil(t, tree.watchees(noSender))
	require.Nil(t, tree.siblings(noSender))

	parent, ok := tree.parent(noSender)
	require.False(t, ok)
	require.Nil(t, parent)

	countBefore := tree.count()
	tree.deleteNode(noSender)
	require.Equal(t, countBefore, tree.count())
}

func TestAddWatcherNoSenderFallback(t *testing.T) {
	system, _ := NewActorSystem("TestSys")
	impl, ok := system.(*actorSystem)
	require.True(t, ok)
	impl.noSender = MockPID(system, "nosender", 0)
	tree := newTree()
	root := MockPID(system, "root", 1)
	child := MockPID(system, "child", 2)
	sibling := MockPID(system, "sibling", 3)

	require.NoError(t, tree.addRootNode(root))
	require.NoError(t, tree.addNode(root, child))
	require.NoError(t, tree.addNode(root, sibling))

	watchers := tree.watchers(child)
	require.Len(t, watchers, 1)
	require.Equal(t, root.Name(), watchers[0].Name())

	tree.noSender = nil
	tree.addWatcher(child, system.NoSender())

	watchers = tree.watchers(child)
	require.Len(t, watchers, 1)
	require.Equal(t, root.Name(), watchers[0].Name())
}

func TestDeleteNodeCleansRelationships(t *testing.T) {
	system, _ := NewActorSystem("TestSys")
	impl, ok := system.(*actorSystem)
	require.True(t, ok)
	impl.noSender = MockPID(system, "nosender", 0)
	tree := newTree()
	root := MockPID(system, "root", 1)
	child := MockPID(system, "child", 2)
	grandChild := MockPID(system, "grandchild", 3)
	sibling := MockPID(system, "sibling", 4)

	require.NoError(t, tree.addRootNode(root))
	require.NoError(t, tree.addNode(root, child))
	require.NoError(t, tree.addNode(child, grandChild))
	require.NoError(t, tree.addNode(root, sibling))

	tree.addWatcher(child, sibling)

	getNames := func(pids []*PID) []string {
		result := make([]string, len(pids))
		for i, pid := range pids {
			result[i] = pid.Name()
		}
		return result
	}

	require.ElementsMatch(t, []string{root.Name(), sibling.Name()}, getNames(tree.watchers(child)))
	require.ElementsMatch(t, []string{child.Name()}, getNames(tree.watchees(sibling)))

	tree.deleteNode(child)

	require.Empty(t, tree.watchees(sibling))
	children := tree.children(root)
	require.Len(t, children, 1)
	require.Equal(t, sibling.Name(), children[0].Name())
	require.EqualValues(t, 2, tree.count())
}

func TestDeleteNodeNoSender(t *testing.T) {
	system, _ := NewActorSystem("TestSys")
	impl, ok := system.(*actorSystem)
	require.True(t, ok)
	impl.noSender = MockPID(system, "nosender", 0)
	tree := newTree()
	root := MockPID(system, "root", 1)
	require.NoError(t, tree.addRootNode(root))

	noSender := system.NoSender()
	tree.deleteNode(noSender)
	require.EqualValues(t, 1, tree.count())

	tree.noSender = nil
	tree.deleteNode(noSender)
	require.EqualValues(t, 1, tree.count())
}

func TestSiblingsReturnsEmptyWhenSingleChild(t *testing.T) {
	system, _ := NewActorSystem("TestSys")
	impl, ok := system.(*actorSystem)
	require.True(t, ok)
	impl.noSender = MockPID(system, "nosender", 0)
	tree := newTree()
	root := MockPID(system, "root", 1)
	onlyChild := MockPID(system, "only", 2)
	require.NoError(t, tree.addRootNode(root))
	require.NoError(t, tree.addNode(root, onlyChild))

	siblings := tree.siblings(onlyChild)
	require.NotNil(t, siblings)
	require.Empty(t, siblings)
}

func TestChildrenReturnsEmptySlice(t *testing.T) {
	system, _ := NewActorSystem("TestSys")
	impl, ok := system.(*actorSystem)
	require.True(t, ok)
	impl.noSender = MockPID(system, "nosender", 0)
	tree := newTree()
	root := MockPID(system, "root", 1)
	child := MockPID(system, "child", 2)
	require.NoError(t, tree.addRootNode(root))
	require.NoError(t, tree.addNode(root, child))

	children := tree.children(child)
	require.NotNil(t, children)
	require.Empty(t, children)
}

func TestDescendantsReturnsEmptySlice(t *testing.T) {
	system, _ := NewActorSystem("TestSys")
	impl, ok := system.(*actorSystem)
	require.True(t, ok)
	impl.noSender = MockPID(system, "nosender", 0)
	tree := newTree()
	root := MockPID(system, "root", 1)
	child := MockPID(system, "child", 2)
	require.NoError(t, tree.addRootNode(root))
	require.NoError(t, tree.addNode(root, child))

	descendants := tree.descendants(child)
	require.NotNil(t, descendants)
	require.Empty(t, descendants)
}

func TestTreeNodeLookupMissing(t *testing.T) {
	tree := newTree()
	_, ok := tree.node("missing")
	require.False(t, ok)
}

func TestNodeByName(t *testing.T) {
	newTreeWithNodes := func(t *testing.T) (*tree, *PID, *PID) {
		t.Helper()
		system, _ := NewActorSystem("TestSys")
		tree := newTree()
		root := MockPID(system, "root", 1)
		child := MockPID(system, "child", 2)
		require.NoError(t, tree.addRootNode(root))
		require.NoError(t, tree.addNode(root, child))
		t.Cleanup(tree.reset)
		return tree, root, child
	}

	t.Run("empty name", func(t *testing.T) {
		tree := newTree()
		node, ok := tree.nodeByName("")
		require.False(t, ok)
		require.Nil(t, node)
	})

	t.Run("missing name", func(t *testing.T) {
		tree, _, _ := newTreeWithNodes(t)
		node, ok := tree.nodeByName("missing")
		require.False(t, ok)
		require.Nil(t, node)
	})

	t.Run("root name", func(t *testing.T) {
		tree, root, _ := newTreeWithNodes(t)
		node, ok := tree.nodeByName(root.Name())
		require.True(t, ok)
		require.NotNil(t, node)
		require.Equal(t, root.ID(), node.id)
	})

	t.Run("child name", func(t *testing.T) {
		tree, _, child := newTreeWithNodes(t)
		node, ok := tree.nodeByName(child.Name())
		require.True(t, ok)
		require.NotNil(t, node)
		require.Equal(t, child.ID(), node.id)
	})

	t.Run("deleted name", func(t *testing.T) {
		tree, _, child := newTreeWithNodes(t)
		tree.deleteNode(child)
		node, ok := tree.nodeByName(child.Name())
		require.False(t, ok)
		require.Nil(t, node)
	})
}

func TestTreeResetPreservesNoSender(t *testing.T) {
	system, _ := NewActorSystem("TestSys")
	impl, ok := system.(*actorSystem)
	require.True(t, ok)
	impl.noSender = MockPID(system, "nosender", 0)
	tree := newTree()
	root := MockPID(system, "root", 1)
	child := MockPID(system, "child", 2)

	require.NoError(t, tree.addRootNode(root))
	require.NoError(t, tree.addNode(root, child))
	require.NotNil(t, tree.noSender)
	noSender := tree.noSender

	tree.reset()

	require.EqualValues(t, 0, tree.count())
	require.Equal(t, noSender, tree.noSender)
	rootPID, ok := tree.root()
	require.False(t, ok)
	require.Nil(t, rootPID)
}

// TestLazyMapsNewNodeCarriesNoMaps verifies that a fresh pidNode does not
// allocate its watchers, watchees, and descendants maps, and that every read
// path treats the nil maps as empty.
func TestLazyMapsNewNodeCarriesNoMaps(t *testing.T) {
	ports := dynaport.Get(1)
	actorSystem, _ := NewActorSystem("TestSys")
	pid := MockPID(actorSystem, "lazy", ports[0])

	node := newPidNode(pid)
	require.Nil(t, node.watchers)
	require.Nil(t, node.watchees)
	require.Nil(t, node.descendants)

	tree := newTree()
	require.NoError(t, tree.addRootNode(pid))

	// Reads over the nil maps behave as reads over empty maps.
	require.Empty(t, tree.watchers(pid))
	require.Empty(t, tree.watchees(pid))
	require.Empty(t, tree.children(pid))
	require.Empty(t, tree.descendants(pid))
	require.Empty(t, tree.siblings(pid))
}

// TestLazyMapsChildAllocatesOnlyTouchedMaps verifies that spawning a child
// materializes exactly the maps the relationship writes: the parent's
// descendants and watchees, and the child's watchers. The child's own
// watchees and descendants must stay nil.
func TestLazyMapsChildAllocatesOnlyTouchedMaps(t *testing.T) {
	ports := dynaport.Get(1)
	actorSystem, _ := NewActorSystem("TestSys")
	parent := MockPID(actorSystem, "parent", ports[0])
	child := MockPID(actorSystem, "child", ports[0])

	tree := newTree()
	require.NoError(t, tree.addRootNode(parent))
	require.NoError(t, tree.addNode(parent, child))

	parentNode, ok := tree.node(parent.ID())
	require.True(t, ok)
	require.Len(t, parentNode.descendants, 1)
	require.Len(t, parentNode.watchees, 1)
	require.Nil(t, parentNode.watchers)

	childNode, ok := tree.node(child.ID())
	require.True(t, ok)
	require.Len(t, childNode.watchers, 1)
	require.Nil(t, childNode.watchees)
	require.Nil(t, childNode.descendants)
}

// TestLazyMapsWatchUnwatchOnBareNode exercises addWatcher and removeWatcher
// against sibling nodes whose watcher and watchee maps were never touched
// before, covering the lazy-allocation path of both maps.
func TestLazyMapsWatchUnwatchOnBareNode(t *testing.T) {
	ports := dynaport.Get(1)
	actorSystem, _ := NewActorSystem("TestSys")
	root := MockPID(actorSystem, "root", ports[0])
	watched := MockPID(actorSystem, "watched", ports[0])
	watcher := MockPID(actorSystem, "watcher", ports[0])

	tree := newTree()
	require.NoError(t, tree.addRootNode(root))
	require.NoError(t, tree.addNode(root, watched))
	require.NoError(t, tree.addNode(root, watcher))

	// The watcher node has no watchees map until the watch registers.
	watcherNode, ok := tree.node(watcher.ID())
	require.True(t, ok)
	require.Nil(t, watcherNode.watchees)

	tree.addWatcher(watched, watcher)

	watchers := tree.watchers(watched)
	require.Len(t, watchers, 2)
	require.Len(t, tree.watchees(watcher), 1)

	tree.removeWatcher(watched, watcher)
	require.Len(t, tree.watchers(watched), 1)
	require.Empty(t, tree.watchees(watcher))

	// Removing a watch that was never registered stays a no-op on nil maps.
	other := MockPID(actorSystem, "other", ports[0])
	require.NoError(t, tree.addNode(root, other))
	tree.removeWatcher(other, watcher)
	require.Empty(t, tree.watchees(watcher))
}

// TestLazyMapsDeleteMixedSubtree deletes a subtree in which some nodes carry
// materialized maps and some are bare leaves with nil maps, then verifies the
// tree is consistent and the parent can spawn again.
func TestLazyMapsDeleteMixedSubtree(t *testing.T) {
	ports := dynaport.Get(1)
	actorSystem, _ := NewActorSystem("TestSys")
	root := MockPID(actorSystem, "root", ports[0])
	branch := MockPID(actorSystem, "branch", ports[0])
	leaf := MockPID(actorSystem, "leaf", ports[0])
	observer := MockPID(actorSystem, "observer", ports[0])

	tree := newTree()
	require.NoError(t, tree.addRootNode(root))
	require.NoError(t, tree.addNode(root, branch))
	require.NoError(t, tree.addNode(branch, leaf))
	require.NoError(t, tree.addNode(root, observer))

	// branch has a descendants map, leaf does not; observer watches leaf so
	// deletion must also clean a lazily created watch relationship.
	tree.addWatcher(leaf, observer)

	tree.deleteNode(branch)

	_, ok := tree.node(branch.ID())
	require.False(t, ok)
	_, ok = tree.node(leaf.ID())
	require.False(t, ok)
	require.Empty(t, tree.watchees(observer))
	require.Len(t, tree.children(root), 1)

	// The parent spawns a replacement under the same tree without issue.
	replacement := MockPID(actorSystem, "replacement", ports[0])
	require.NoError(t, tree.addNode(root, replacement))
	require.Len(t, tree.children(root), 2)
}

// TestLazyMapsReattachExistingNode covers attachNodeLocked through
// addOrAttachNode: relinking an existing node must lazily materialize the new
// parent's maps and the child's watchers map exactly as a fresh add does.
func TestLazyMapsReattachExistingNode(t *testing.T) {
	ports := dynaport.Get(1)
	actorSystem, _ := NewActorSystem("TestSys")
	root := MockPID(actorSystem, "root", ports[0])
	first := MockPID(actorSystem, "first", ports[0])
	second := MockPID(actorSystem, "second", ports[0])

	tree := newTree()
	require.NoError(t, tree.addRootNode(root))
	require.NoError(t, tree.addNode(root, first))
	require.NoError(t, tree.addNode(root, second))

	// Relink second under first; first's descendants map does not exist yet.
	firstNode, ok := tree.node(first.ID())
	require.True(t, ok)
	require.Nil(t, firstNode.descendants)

	require.NoError(t, tree.addOrAttachNode(first, second))
	require.Len(t, tree.children(first), 1)

	watchers := tree.watchers(second)
	names := make([]string, len(watchers))
	for i, w := range watchers {
		names[i] = w.Name()
	}
	require.Contains(t, names, "first")
}

// TestPutWatcher covers the watchers-slice insert helper: distinct watchers
// append, and re-adding an entry with the same ID overwrites in place rather
// than duplicating, reproducing the old watchers map's overwrite-by-key.
func TestPutWatcher(t *testing.T) {
	ports := dynaport.Get(1)
	system, _ := NewActorSystem("TestSys")
	a := MockPID(system, "a", ports[0])
	b := MockPID(system, "b", ports[0])

	var list []*PID

	// Distinct watchers append in order.
	putWatcher(&list, a)
	putWatcher(&list, b)
	require.Len(t, list, 2)
	require.Equal(t, a.ID(), list[0].ID())
	require.Equal(t, b.ID(), list[1].ID())

	// Re-adding the same instance overwrites its slot, no duplicate.
	putWatcher(&list, a)
	require.Len(t, list, 2)

	// A different *PID that shares a's ID replaces the stored element.
	aClone := &PID{address: a.address, path: a.path, actorSystem: system}
	require.Equal(t, a.ID(), aClone.ID())
	putWatcher(&list, aClone)
	require.Len(t, list, 2)
	require.Same(t, aClone, list[0])
}

// TestDeleteWatcher covers the watchers-slice removal helper: the matching
// entry is removed, an absent ID is a no-op, and removing the last entry resets
// the slice to nil rather than leaving an empty backing array.
func TestDeleteWatcher(t *testing.T) {
	ports := dynaport.Get(1)
	system, _ := NewActorSystem("TestSys")
	a := MockPID(system, "a", ports[0])
	b := MockPID(system, "b", ports[0])
	c := MockPID(system, "c", ports[0])

	var list []*PID
	putWatcher(&list, a)
	putWatcher(&list, b)

	// An absent ID leaves the slice untouched.
	deleteWatcher(&list, c.ID())
	require.Len(t, list, 2)

	// Removing a present entry drops exactly that one.
	deleteWatcher(&list, a.ID())
	require.Len(t, list, 1)
	require.Equal(t, b.ID(), list[0].ID())

	// Removing the last entry resets the slice to nil, not an empty slice.
	deleteWatcher(&list, b.ID())
	require.Nil(t, list)
}

// TestWatchersSliceLifecycle drives the watchers slice through the tree: two
// watchers register on one node, watchers() returns both, and removing them one
// at a time leaves the node with a nil watchers slice and no backing array.
func TestWatchersSliceLifecycle(t *testing.T) {
	ports := dynaport.Get(1)
	system, _ := NewActorSystem("TestSys")
	root := MockPID(system, "root", ports[0])
	watched := MockPID(system, "watched", ports[0])
	watcher := MockPID(system, "watcher", ports[0])

	tree := newTree()
	t.Cleanup(tree.reset)
	require.NoError(t, tree.addRootNode(root))
	require.NoError(t, tree.addNode(root, watched))
	require.NoError(t, tree.addNode(root, watcher))

	// watched carries root as its parent-watcher; add a second watcher.
	tree.addWatcher(watched, watcher)

	names := make([]string, 0, 2)
	for _, w := range tree.watchers(watched) {
		names = append(names, w.Name())
	}
	require.ElementsMatch(t, []string{"root", "watcher"}, names)

	node, ok := tree.node(watched.ID())
	require.True(t, ok)
	require.Len(t, node.watchers, 2)

	// Remove the two watchers one at a time; the slice empties back to nil.
	tree.removeWatcher(watched, watcher)
	require.Len(t, tree.watchers(watched), 1)

	tree.removeWatcher(watched, root)
	require.Empty(t, tree.watchers(watched))
	require.Nil(t, node.watchers)
}
