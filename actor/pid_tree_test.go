/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/internal/collection"
)

func MockPID(system ActorSystem, name string, port int) *PID {
	return &PID{
		address: address.New(name, system.Name(), "host", port),
		system:  system,
	}
}

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
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), system: actorSystem}
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
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), system: actorSystem}
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
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), system: actorSystem}
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
		pid := &PID{address: address.New("root", "TestSys", "host", 0), system: actorSystem}
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
	pid := &PID{address: address.New("root-delete", "TestSys", "host", ports[0]), system: actorSystem}
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
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), system: actorSystem}
		siblings := tree.siblings(pid)
		require.Empty(t, siblings)
	})

	// add test for pid has no parent
	t.Run("pid has no parent", func(t *testing.T) {
		pid := &PID{address: address.New("no_parent", "TestSys", "host", 0), system: actorSystem}
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
	actorSystem, _ := NewActorSystem("TestSys")
	t.Run("nil pid", func(t *testing.T) {
		descendants := tree.descendants(nil)
		require.Empty(t, descendants)
	})

	t.Run("pid not in tree", func(t *testing.T) {
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), system: actorSystem}
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
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), system: actorSystem}
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
		pid := &PID{address: address.New("not_in_tree", "TestSys", "host", 0), system: actorSystem}
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
