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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v3/address"
)

func TestTree(t *testing.T) {
	ports := dynaport.Get(1)
	pid0 := &PID{address: address.New("pid0", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
	pid1 := &PID{address: address.New("pid1", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
	pid2 := &PID{address: address.New("pid2", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
	pid3 := &PID{address: address.New("pid3", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
	pid4 := &PID{address: address.New("pid4", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}
	pid5 := &PID{address: address.New("pid5", "TestSys", "host", ports[0]), fieldsLocker: &sync.RWMutex{}, stopLocker: &sync.Mutex{}}

	tree := newTree()

	// create pid0 as the root node
	require.NoError(t, tree.addNode(NoSender, pid0)) // pid0 has no parent

	require.NoError(t, tree.addNode(pid0, pid1)) // pid1 has pid0 as parent
	require.NoError(t, tree.addNode(pid0, pid2)) // pid0 is parent of pid2
	require.NoError(t, tree.addNode(pid1, pid3)) // pid1 is parent of pid3
	require.Error(t, tree.addNode(pid4, pid5))   // this will error because pid4 does not exist on the tree
	require.NoError(t, tree.addNode(pid3, pid4)) // pid3 is parent of pid4

	tree.addWatcher(pid3, pid0)
	tree.addWatcher(pid4, pid5)

	pid4Ancestors, ok := tree.ancestors(pid4)
	require.True(t, ok)
	require.NotEmpty(t, pid4Ancestors)
	require.Len(t, pid4Ancestors, 3)
	pid4Parent, ok := tree.parentAt(pid4, 0)
	require.True(t, ok)
	require.NotNil(t, pid4Parent)
	require.True(t, pid4Parent.value().Equals(pid3))
	pid4GParent, ok := tree.parentAt(pid4, 1)
	require.True(t, ok)
	require.True(t, pid4GParent.value().Equals(pid1))
	pid4GGParent, ok := tree.parentAt(pid4, 2)
	require.True(t, ok)
	require.NotNil(t, pid4GGParent)

	// grab the siblings of pid2
	siblings, ok := tree.siblings(pid2)
	require.True(t, ok)
	require.NotEmpty(t, siblings)
	require.Len(t, siblings, 1)
	sibling := siblings[0]
	require.True(t, sibling.value().Equals(pid1))

	require.EqualValues(t, 5, tree.length())

	node, ok := tree.node(pid5.ID())
	require.False(t, ok)
	require.Nil(t, node)

	tree.deleteNode(pid1)
	require.EqualValues(t, 2, tree.length())
	require.Len(t, tree.nodes(), 2)

	node, ok = tree.node(pid1.ID())
	require.False(t, ok)
	require.Nil(t, node)

	node, ok = tree.node(pid3.ID())
	require.False(t, ok)
	require.Nil(t, node)

	node, ok = tree.node(pid4.ID())
	require.False(t, ok)
	require.Nil(t, node)

	tree.reset()
	require.Empty(t, tree.nodes())
	assert.EqualValues(t, 0, tree.length())
}
