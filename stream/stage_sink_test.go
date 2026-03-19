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

// Package stream internal tests for the sinkActor error-strategy paths.
package stream

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
)

// newInternalTestSystem creates a disposable ActorSystem for internal tests.
func newInternalTestSystem(t *testing.T) actor.ActorSystem {
	t.Helper()
	sys, err := actor.NewActorSystem("stream-internal", actor.WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(context.Background()))
	t.Cleanup(func() { _ = sys.Stop(context.Background()) })
	return sys
}

// errSink creates a Sink[int] whose consumeFn errors on the element equal to `bad`.
func errSink(bad int, strategy ErrorStrategy) Sink[int] {
	sentinel := errors.New("bad element")
	config := defaultStageConfig()
	config.ErrorStrategy = strategy
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSinkActor(func(v any) error {
				if v.(int) == bad {
					return sentinel
				}
				return nil
			}, nil, cfg)
		},
		config: config,
	}
	return Sink[int]{desc: desc}
}

// collectSink returns a Sink[int] that accumulates received ints plus a done signal.
func collectSink(strategy ErrorStrategy) (*[]int, Sink[int]) {
	out := &[]int{}
	sentinel := errors.New("bad element")
	config := defaultStageConfig()
	config.ErrorStrategy = strategy
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSinkActor(func(v any) error {
				n := v.(int)
				if n == 99 {
					return sentinel
				}
				*out = append(*out, n)
				return nil
			}, nil, cfg)
		},
		config: config,
	}
	return out, Sink[int]{desc: desc}
}

func TestSinkActor_ErrorResume_SkipsElement(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	out, sink := collectSink(Resume)
	handle, err := Of(1, 99, 3).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	<-handle.Done()
	assert.NoError(t, handle.Err())
	// Element 99 is skipped; 1 and 3 are collected.
	assert.Equal(t, []int{1, 3}, *out)
}

func TestSinkActor_ErrorRetry_Succeeds(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	attempts := 0
	config := defaultStageConfig()
	config.ErrorStrategy = Retry
	collected := &[]int{}
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSinkActor(func(v any) error {
				n := v.(int)
				if n == 2 && attempts == 0 {
					attempts++
					return errors.New("first attempt")
				}
				*collected = append(*collected, n)
				return nil
			}, nil, cfg)
		},
		config: config,
	}
	handle, err := Of(1, 2, 3).To(Sink[int]{desc: desc}).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.Equal(t, []int{1, 2, 3}, *collected)
}

func TestSinkActor_ErrorRetry_Fails(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	handle, err := Of(1, 2, 3).To(errSink(2, Retry)).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	// Stream should terminate after retry failure.
}

func TestSinkActor_ErrorFailFast(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	handle, err := Of(1, 2, 3).To(errSink(2, FailFast)).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
}

func TestSinkActor_StreamError_FromUpstream(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	// When an upstream flow errors (FailFast), it sends streamError to the sink.
	handle, err := Via(
		Of(1, 2, 3),
		TryMap(func(n int) (int, error) {
			if n == 1 {
				return 0, errors.New("upstream error")
			}
			return n, nil
		}),
	).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
}

func TestSinkActor_ErrorSupervise(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	// Supervise currently behaves like FailFast: stream terminates on error.
	handle, err := Of(1, 2, 3).To(errSink(2, Supervise)).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
}

func TestSink_WithErrorStrategy(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	// Verify the public WithErrorStrategy on Sink[T].
	out, sink := collectSink(FailFast)
	sinkResume := sink.WithErrorStrategy(Resume)
	// sink uses FailFast; sinkResume uses Resume but operates on a new instance.
	_ = out

	handle, err := Of(1, 99, 3).To(sinkResume).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
}
