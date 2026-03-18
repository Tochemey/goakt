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

// Package stream internal tests for RunnableGraph validation edge cases.
package stream

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
)

func makeStageDesc(kind stageKind) *stageDesc {
	return &stageDesc{
		id:   newStageID(),
		kind: kind,
		makeActor: func(_ StageConfig) actor.Actor {
			return newSinkActor(func(any) error { return nil }, nil, defaultStageConfig())
		},
		config: defaultStageConfig(),
	}
}

func TestValidate_FirstStageNotSource(t *testing.T) {
	// A 2-stage graph whose first stage is a flow (not a source) should fail.
	flow := makeStageDesc(flowKind)
	sink := makeStageDesc(sinkKind)
	g := RunnableGraph{stages: []*stageDesc{flow, sink}}
	err := g.validate()
	require.Error(t, err)
}

func TestValidate_LastStageNotSink(t *testing.T) {
	// A 2-stage graph whose last stage is a flow (not a sink) should fail.
	src := makeStageDesc(sourceKind)
	flow := makeStageDesc(flowKind)
	g := RunnableGraph{stages: []*stageDesc{src, flow}}
	err := g.validate()
	require.Error(t, err)
}
