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

package stream

import (
	"fmt"

	"github.com/tochemey/goakt/v4/actor"
)

// gnKind classifies a graph builder node.
type gnKind int

const (
	gnSourceKind gnKind = iota
	gnFlowKind
	gnSinkKind
	gnMergeKind // created by MergeInto
)

// graphBuilderNode represents one named node in a Graph topology.
type graphBuilderNode struct {
	name   string
	kind   gnKind
	stages []*stageDesc // pre-compiled descriptors for this node (nil for merge)
	from   []string     // upstream node names
}

// fanOutReg tracks the sharedBroadcast and slot allocation for a fan-out node.
type fanOutReg struct {
	shared   *sharedBroadcast[any]
	nextSlot int
}

// Graph builds non-linear stream topologies using a named-node DSL.
// Nodes are identified by string names and connected explicitly.
//
// Supported topologies:
//   - Linear:   single source → zero or more flows → single sink
//   - Fan-out:  one node → multiple downstream nodes (Broadcast semantics)
//   - Fan-in:   MergeInto creates a merge junction from multiple upstreams
//   - Diamond:  fan-out followed by fan-in
//
// Type-erasure note: the Graph DSL operates on any-typed stages. Use
// Source[any], Flow[any,any], and Sink[any] when constructing nodes.
type Graph struct {
	nodes map[string]*graphBuilderNode
	order []string // insertion order for deterministic compilation
}

// NewGraph creates an empty graph builder.
func NewGraph() *Graph {
	return &Graph{nodes: map[string]*graphBuilderNode{}}
}

// AddSource registers a named source node.
func (g *Graph) AddSource(name string, src Source[any]) *Graph {
	g.addNode(&graphBuilderNode{
		name:   name,
		kind:   gnSourceKind,
		stages: src.stages,
	})
	return g
}

// AddFlow registers a named flow node wired from the named upstream node.
func (g *Graph) AddFlow(name string, flow Flow[any, any], from string) *Graph {
	g.addNode(&graphBuilderNode{
		name:   name,
		kind:   gnFlowKind,
		stages: []*stageDesc{flow.desc},
		from:   []string{from},
	})
	return g
}

// AddSink registers a named sink node wired from the named upstream node.
func (g *Graph) AddSink(name string, sink Sink[any], from string) *Graph {
	g.addNode(&graphBuilderNode{
		name:   name,
		kind:   gnSinkKind,
		stages: []*stageDesc{sink.desc},
		from:   []string{from},
	})
	return g
}

// MergeInto creates a fan-in (merge) node named into that consumes elements
// from all the listed upstream nodes. Upstream elements are interleaved in
// arrival order (non-deterministic), and the merge completes when all
// upstreams have completed.
func (g *Graph) MergeInto(into string, from ...string) *Graph {
	g.addNode(&graphBuilderNode{
		name: into,
		kind: gnMergeKind,
		from: from,
	})
	return g
}

// Build validates the graph and compiles it into a RunnableGraph.
// Returns ErrInvalidGraph if the graph has no sources or sinks, if a node
// references an unknown upstream, or if the graph contains a cycle.
func (g *Graph) Build() (RunnableGraph, error) {
	if err := g.validateGraph(); err != nil {
		return RunnableGraph{}, err
	}
	pipelines, err := g.compile()
	if err != nil {
		return RunnableGraph{}, err
	}
	if len(pipelines) == 1 {
		return RunnableGraph{stages: pipelines[0]}, nil
	}
	return RunnableGraph{pipelines: pipelines}, nil
}

func (g *Graph) addNode(n *graphBuilderNode) {
	g.nodes[n.name] = n
	g.order = append(g.order, n.name)
}

func (g *Graph) validateGraph() error {
	for _, node := range g.nodes {
		for _, from := range node.from {
			if _, ok := g.nodes[from]; !ok {
				return fmt.Errorf("stream: graph node %q references unknown node %q", node.name, from)
			}
		}
	}
	srcCount, sinkCount := 0, 0
	for _, node := range g.nodes {
		switch node.kind {
		case gnSourceKind:
			srcCount++
		case gnSinkKind:
			sinkCount++
		}
	}
	if srcCount == 0 {
		return fmt.Errorf("%w: graph has no sources", ErrInvalidGraph)
	}
	if sinkCount == 0 {
		return fmt.Errorf("%w: graph has no sinks", ErrInvalidGraph)
	}
	return nil
}

// compile produces one []*stageDesc pipeline per sink node, ordered by insertion.
func (g *Graph) compile() ([][]*stageDesc, error) {
	// Count how many distinct downstream nodes reference each node.
	downCount := map[string]int{}
	for _, node := range g.nodes {
		seen := map[string]bool{}
		for _, from := range node.from {
			if !seen[from] {
				downCount[from]++
				seen[from] = true
			}
		}
	}

	// Determine topological order (upstream before downstream).
	topoOrder, err := g.topologicalOrder()
	if err != nil {
		return nil, err
	}

	// Build sharedBroadcast registrations for fan-out nodes in topo order.
	// Processing upstream fan-out nodes first ensures that when we build the
	// srcChain for a downstream fan-out, we can correctly substitute upstream
	// fan-out nodes with their broadcast slots.
	fanOuts := map[string]*fanOutReg{}
	for _, name := range topoOrder {
		if downCount[name] <= 1 {
			continue
		}
		srcChain, err := g.buildOwnChain(name, fanOuts)
		if err != nil {
			return nil, fmt.Errorf("stream: building fan-out chain for %q: %w", name, err)
		}
		n := downCount[name]
		fanOuts[name] = &fanOutReg{shared: newSharedBroadcast[any](n, srcChain)}
	}

	// Compile one pipeline per sink, in insertion order.
	var pipelines [][]*stageDesc
	for _, name := range g.order {
		node := g.nodes[name]
		if node.kind != gnSinkKind {
			continue
		}
		chain, err := g.buildChain(node.from[0], fanOuts)
		if err != nil {
			return nil, fmt.Errorf("stream: compiling pipeline for sink %q: %w", name, err)
		}
		pipeline := append(chain, node.stages...)
		pipelines = append(pipelines, pipeline)
	}
	return pipelines, nil
}

// topologicalOrder returns node names in upstream-before-downstream order.
// Returns an error if the graph contains a cycle.
func (g *Graph) topologicalOrder() ([]string, error) {
	// visiting tracks nodes on the current DFS recursion stack (gray nodes).
	// visited tracks nodes whose full subtree has been explored (black nodes).
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var order []string
	var visit func(name string) error
	visit = func(name string) error {
		if visited[name] {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("%w: cycle detected at node %q", ErrInvalidGraph, name)
		}
		visiting[name] = true
		node := g.nodes[name]
		for _, from := range node.from {
			if err := visit(from); err != nil {
				return err
			}
		}
		delete(visiting, name)
		visited[name] = true
		order = append(order, name)
		return nil
	}
	for _, name := range g.order {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// buildChain builds the stage chain that produces elements up to (not including)
// the named node's own stages, substituting fan-out nodes with broadcast slots.
func (g *Graph) buildChain(name string, fanOuts map[string]*fanOutReg) ([]*stageDesc, error) {
	if reg, ok := fanOuts[name]; ok {
		slot := reg.nextSlot
		reg.nextSlot++
		return []*stageDesc{makeBroadcastSlotDesc(reg.shared, slot)}, nil
	}
	return g.buildOwnChain(name, fanOuts)
}

// buildOwnChain builds the stage chain INCLUDING the named node's own stages,
// without treating the node itself as a fan-out (used when computing the
// srcStages for a sharedBroadcast).
func (g *Graph) buildOwnChain(name string, fanOuts map[string]*fanOutReg) ([]*stageDesc, error) {
	node := g.nodes[name]
	switch node.kind {
	case gnSourceKind:
		out := make([]*stageDesc, len(node.stages))
		copy(out, node.stages)
		return out, nil
	case gnFlowKind:
		up, err := g.buildChain(node.from[0], fanOuts)
		if err != nil {
			return nil, err
		}
		return append(up, node.stages...), nil
	case gnMergeKind:
		desc, err := g.buildMergeDesc(node, fanOuts)
		if err != nil {
			return nil, err
		}
		return []*stageDesc{desc}, nil
	default:
		return nil, fmt.Errorf("stream: unexpected node kind %d for node %q in upstream chain", node.kind, name)
	}
}

// buildMergeDesc creates a mergeSourceActor stageDesc for a gnMergeKind node.
func (g *Graph) buildMergeDesc(node *graphBuilderNode, fanOuts map[string]*fanOutReg) (*stageDesc, error) {
	subStages := make([][]*stageDesc, len(node.from))
	for i, from := range node.from {
		chain, err := g.buildChain(from, fanOuts)
		if err != nil {
			return nil, err
		}
		subStages[i] = chain
	}
	captured := subStages
	return &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newMergeSourceActor[any](captured, cfg)
		},
		config: defaultStageConfig(),
	}, nil
}

// makeBroadcastSlotDesc creates a stageDesc for one slot of a sharedBroadcast.
func makeBroadcastSlotDesc[T any](shared *sharedBroadcast[T], slot int) *stageDesc {
	return &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return &broadcastSlotActor[T]{shared: shared, slot: slot, config: cfg}
		},
		config: defaultStageConfig(),
	}
}
