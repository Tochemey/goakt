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

//go:build scale

// Package benchmark, spawn_scale_test.go: a single-node scale test that
// creates a binary tree of 2²⁰−1 actors, every actor spawning its own two
// children, and reports how long the whole tree takes to build. This is the
// actor-creation workload actor frameworks compare on: creation here is
// concurrent and hierarchical, so it exercises the spawn path under
// contention and at depth, where a sequential spawn benchmark cannot see the
// cost of the actor tree's shared state.
//
// The test is build-tagged behind `scale` so it never runs as part of the
// normal suite. Run it with:
//
//	go test -tags=scale -run TestActorCreationTree -v -timeout 30m ./benchmark/

package benchmark

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
)

const (
	// creationTreeDepth is the depth of the binary tree, root included, so the
	// tree holds 2^creationTreeDepth−1 actors.
	creationTreeDepth = 20

	// creationTreeTimeout bounds the wait for the tree to be built, so a spawn
	// that never completes fails the test with a message instead of hanging
	// until the package timeout.
	creationTreeTimeout = 20 * time.Minute
)

// growSubtree asks a node to build the subtree of the given depth below it,
// itself included. A depth of one makes the node a leaf.
type growSubtree struct {
	depth uint32
}

// subtreeGrown is a child's report to its parent of how many actors its
// finished subtree held, itself included.
type subtreeGrown struct {
	actors uint32
}

// creationTreeResult is the root's report to the test: the number of actors
// in the finished tree, or the first error a spawn in it returned.
type creationTreeResult struct {
	actors uint32
	err    error
}

// creationTreeNode is one actor of the tree. On growSubtree it spawns two
// children and asks each for a subtree one level shallower; once both have
// reported, it adds itself to their counts, reports to its own parent and
// stops. The root reports to the test instead and stays up.
type creationTreeNode struct {
	// parent receives this node's subtreeGrown; nil for the root.
	parent *actor.PID
	// result receives the root's creationTreeResult, and the first spawn
	// error of any node; every node shares it with the root.
	result chan creationTreeResult
	// pending counts the children whose report is still outstanding.
	pending int
	// actors accumulates the sizes of the finished subtrees, plus one for the
	// node itself.
	actors uint32
}

// TestActorCreationTree builds the binary tree from one root actor, waits for
// the root to report the finished tree and reports the wall time of the build
// and the resulting actor-creation rate. It fails when a spawn returns an
// error, when the tree holds a different number of actors than its depth
// implies, or when the build exceeds creationTreeTimeout.
func TestActorCreationTree(t *testing.T) {
	ctx := context.Background()
	system, err := actor.NewActorSystem("creation-tree",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() { _ = system.Stop(ctx) })

	result := make(chan creationTreeResult, 1)
	root, err := system.Spawn(ctx, "root", &creationTreeNode{result: result}, actor.WithLongLived())
	require.NoError(t, err)

	expected := uint32(1)<<creationTreeDepth - 1
	start := time.Now()
	require.NoError(t, actor.Tell(ctx, root, &growSubtree{depth: creationTreeDepth}))

	var built creationTreeResult
	select {
	case built = <-result:
	case <-time.After(creationTreeTimeout):
		t.Fatalf("the tree of %d actors was not built within %s", expected, creationTreeTimeout)
	}

	elapsed := time.Since(start)
	require.NoError(t, built.err, "a spawn in the tree failed")
	require.Equal(t, expected, built.actors, "the tree holds a different number of actors than its depth implies")

	t.Logf("\nreport:\n"+
		"  tree          depth %d, %s actors, each spawned by its parent with WithLongLived()\n"+
		"  wall time     %s to build the whole tree\n"+
		"  creation rate %.0f actors/sec\n"+
		"  reading tips  creation is concurrent across subtrees, so this is bounded by what the spawn path serialises across actors, not by one spawn's latency",
		creationTreeDepth,
		humanCount(int64(expected)),
		elapsed.Round(time.Millisecond),
		float64(expected)/elapsed.Seconds())
}

// PreStart does nothing.
func (x *creationTreeNode) PreStart(*actor.Context) error { return nil }

// Receive grows the subtree on growSubtree and collects the children's
// subtreeGrown reports.
func (x *creationTreeNode) Receive(ctx *actor.ReceiveContext) {
	switch message := ctx.Message().(type) {
	case *growSubtree:
		if message.depth == 1 {
			x.report(ctx, 1)
			return
		}

		x.pending, x.actors = 2, 1
		for _, side := range [2]string{"left", "right"} {
			// A child cannot carry its parent's name, so the depth goes into it.
			name := side + "-" + strconv.FormatUint(uint64(message.depth-1), 10)
			child, err := ctx.Self().SpawnChild(ctx.Context(), name, &creationTreeNode{parent: ctx.Self(), result: x.result}, actor.WithLongLived())
			if err != nil {
				x.fail(err)
				return
			}

			ctx.Tell(child, &growSubtree{depth: message.depth - 1})
		}
	case *subtreeGrown:
		x.actors += message.actors
		if x.pending--; x.pending == 0 {
			x.report(ctx, x.actors)
		}
	}
}

// PostStop does nothing.
func (x *creationTreeNode) PostStop(*actor.Context) error { return nil }

// report sends the size of the finished subtree to the parent and stops the
// node; the root sends it to the test instead and stays up.
func (x *creationTreeNode) report(ctx *actor.ReceiveContext, actors uint32) {
	if x.parent == nil {
		x.result <- creationTreeResult{actors: actors}
		return
	}

	ctx.Tell(x.parent, &subtreeGrown{actors: actors})
	ctx.Shutdown()
}

// fail hands the first spawn error to the test. Later errors are dropped:
// the test fails on the first one and the rest of the tree is never waited
// for.
func (x *creationTreeNode) fail(err error) {
	select {
	case x.result <- creationTreeResult{err: err}:
	default:
	}
}
