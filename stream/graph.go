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
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/tochemey/goakt/v4/actor"
)

// stageKind classifies a stage in the stream DAG.
type stageKind int8

const (
	sourceKind stageKind = iota
	flowKind
	sinkKind
)

// stageDesc is an immutable description of one stage.
// It is assembled into a RunnableGraph by Source.To() and materialized into
// an actor when RunnableGraph.Run() is called.
type stageDesc struct {
	id   string
	kind stageKind
	// makeActor is called once per materialization to create the stage actor.
	// config is the final, builder-modified StageConfig at spawn time.
	makeActor func(config StageConfig) actor.Actor
	config    StageConfig
	// fuseFn is set for stateless stages (Map, Filter) and enables stage fusion.
	// Returns (result, pass, err). When pass is false, the element is filtered out.
	// When err is non-nil, the element failed processing.
	fuseFn func(any) (any, bool, error)
}

// newStageID returns a short 8-character identifier derived from a UUID v4.
// A truncated prefix is used instead of the full 36-character UUID because stage
// IDs only need to be unique within a single materialized graph (not globally),
// and shorter IDs keep actor names, log lines, and metrics labels compact.
func newStageID() string {
	return uuid.New().String()[:8]
}

// FusionMode controls whether adjacent stateless stages are fused into a single actor.
type FusionMode int

const (
	// FuseStateless fuses adjacent stateless stages (Map, Filter). This is the default.
	FuseStateless FusionMode = iota
	// FuseNone disables stage fusion (useful for debugging or profiling individual stages).
	FuseNone
	// FuseAggressive fuses all fusable adjacent stages including those with buffering.
	FuseAggressive
)

// RunnableGraph is a fully assembled stream pipeline ready for execution.
// It is a value type — the same graph may be Run() multiple times to
// produce independent stream instances.
//
// A RunnableGraph may contain either a single linear pipeline (stages) or
// multiple pipelines produced by the Graph DSL (pipelines). Run dispatches
// to the appropriate materializer automatically.
type RunnableGraph struct {
	stages     []*stageDesc   // non-nil for a single linear pipeline
	pipelines  [][]*stageDesc // non-nil for multi-pipeline graphs (Graph DSL)
	fusionMode FusionMode
}

// WithFusion returns a new RunnableGraph with the given fusion mode applied.
func (g RunnableGraph) WithFusion(mode FusionMode) RunnableGraph {
	g.fusionMode = mode
	return g
}

// Run materializes the graph within the given ActorSystem.
// It spawns one actor per stage, wires them together, and starts the demand
// signal from the sink. Returns a StreamHandle for lifecycle control and
// metrics, or an error if spawning fails.
func (g RunnableGraph) Run(ctx context.Context, system actor.ActorSystem) (StreamHandle, error) {
	if len(g.pipelines) > 0 {
		fused := make([][]*stageDesc, len(g.pipelines))
		for i, p := range g.pipelines {
			fused[i] = applyFusion(p, g.fusionMode)
		}
		return materializeAll(ctx, system, fused)
	}
	return materialize(ctx, system, applyFusion(g.stages, g.fusionMode))
}

// validate checks that the graph has at least a source and a sink.
func (g RunnableGraph) validate() error {
	if len(g.stages) < 2 {
		return ErrInvalidGraph
	}
	if g.stages[0].kind != sourceKind {
		return fmt.Errorf("stream: first stage must be a source, got kind %d", g.stages[0].kind)
	}
	if g.stages[len(g.stages)-1].kind != sinkKind {
		return fmt.Errorf("stream: last stage must be a sink, got kind %d", g.stages[len(g.stages)-1].kind)
	}
	return nil
}
