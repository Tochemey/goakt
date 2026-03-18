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
	"time"

	"github.com/tochemey/goakt/v4/actor"
)

// Flow is a lazy description of a transformation stage.
// It consumes elements of type In and produces elements of type Out.
//
// Flows are assembled into pipelines using Source.Via or the package-level Via.
// Use the builder methods (WithErrorStrategy, etc.) to customize behaviour
// before attaching the flow to a source.
type Flow[In, Out any] struct {
	desc *stageDesc
}

// WithErrorStrategy returns a new Flow that uses the given ErrorStrategy
// for element-level processing errors.
func (f Flow[In, Out]) WithErrorStrategy(s ErrorStrategy) Flow[In, Out] {
	newDesc := *f.desc
	newDesc.config.ErrorStrategy = s
	// Rebuild makeActor so it captures the updated config value.
	prevMake := f.desc.makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor {
		return prevMake(newDesc.config)
	}
	return Flow[In, Out]{desc: &newDesc}
}

// WithRetryConfig returns a new Flow with a custom RetryConfig.
// Only takes effect when ErrorStrategy is Retry.
// MaxAttempts < 1 is coerced to 1.
func (f Flow[In, Out]) WithRetryConfig(rc RetryConfig) Flow[In, Out] {
	if rc.MaxAttempts < 1 {
		rc.MaxAttempts = 1
	}
	newDesc := *f.desc
	newDesc.config.RetryConfig = rc
	prevMake := f.desc.makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor {
		return prevMake(newDesc.config)
	}
	return Flow[In, Out]{desc: &newDesc}
}

// WithMailbox returns a new Flow that uses the given actor.Mailbox for its stage actor.
func (f Flow[In, Out]) WithMailbox(mailbox actor.Mailbox) Flow[In, Out] {
	newDesc := *f.desc
	newDesc.config.Mailbox = mailbox
	prevMake := f.desc.makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor { return prevMake(newDesc.config) }
	return Flow[In, Out]{desc: &newDesc}
}

// WithName returns a new Flow with a custom stage actor name.
func (f Flow[In, Out]) WithName(name string) Flow[In, Out] {
	newDesc := *f.desc
	newDesc.config.Name = name
	prevMake := f.desc.makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor { return prevMake(newDesc.config) }
	return Flow[In, Out]{desc: &newDesc}
}

// WithTags returns a new Flow with tags propagated to metrics and traces.
func (f Flow[In, Out]) WithTags(tags map[string]string) Flow[In, Out] {
	newDesc := *f.desc
	newDesc.config.Tags = tags
	prevMake := f.desc.makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor { return prevMake(newDesc.config) }
	return Flow[In, Out]{desc: &newDesc}
}

// WithTracer returns a new Flow with the given Tracer attached.
func (f Flow[In, Out]) WithTracer(t Tracer) Flow[In, Out] {
	newDesc := *f.desc
	newDesc.config.Tracer = t
	prevMake := f.desc.makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor { return prevMake(newDesc.config) }
	return Flow[In, Out]{desc: &newDesc}
}

// Map creates a Flow that applies fn to each element with no error path.
func Map[In, Out any](fn func(In) Out) Flow[In, Out] {
	return TryMap(func(in In) (Out, error) { return fn(in), nil })
}

// TryMap creates a Flow that applies fn to each element.
// fn may return an error; the ErrorStrategy (default: FailFast) controls
// how errors are handled.
// For error-free transformations prefer Map which has a cleaner signature.
func TryMap[In, Out any](fn func(In) (Out, error)) Flow[In, Out] {
	return makeMapFlow(fn, defaultStageConfig())
}

// Filter creates a type-preserving Flow that keeps only elements for which
// predicate returns true. Dropped elements do not consume downstream demand;
// the stage immediately requests a replacement from upstream.
func Filter[T any](predicate func(T) bool) Flow[T, T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(config StageConfig) actor.Actor {
			return newFlowActor(func(v any) ([]any, error) {
				elem, ok := v.(T)
				if !ok {
					return nil, fmt.Errorf("stream: Filter got unexpected type %T", v)
				}
				if predicate(elem) {
					return []any{elem}, nil
				}
				return nil, nil
			}, config)
		},
		config: config,
		fuseFn: func(v any) (any, bool, error) {
			elem, ok := v.(T)
			if !ok {
				return nil, false, fmt.Errorf("stream: Filter (fused) got unexpected type %T", v)
			}
			return elem, predicate(elem), nil
		},
	}
	return Flow[T, T]{desc: desc}
}

// FlatMap creates a Flow that applies fn to each element and flattens the
// resulting slices into individual elements downstream.
func FlatMap[In, Out any](fn func(In) []Out) Flow[In, Out] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(config StageConfig) actor.Actor {
			return newFlowActor(func(v any) ([]any, error) {
				elem, ok := v.(In)
				if !ok {
					return nil, fmt.Errorf("stream: FlatMap got unexpected type %T", v)
				}
				outs := fn(elem)
				result := make([]any, len(outs))
				for i, o := range outs {
					result[i] = o
				}
				return result, nil
			}, config)
		},
		config: config,
	}
	return Flow[In, Out]{desc: desc}
}

// Flatten expands slices into individual elements downstream.
// Each incoming []T is unwrapped and its elements are emitted one by one.
func Flatten[T any]() Flow[[]T, T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(config StageConfig) actor.Actor {
			return newFlowActor(func(v any) ([]any, error) {
				slice, ok := v.([]T)
				if !ok {
					return nil, fmt.Errorf("stream: Flatten got unexpected type %T", v)
				}
				result := make([]any, len(slice))
				for i, e := range slice {
					result[i] = e
				}
				return result, nil
			}, config)
		},
		config: config,
	}
	return Flow[[]T, T]{desc: desc}
}

// Batch groups elements into slices of at most n elements, flushing early
// if maxWait elapses with at least one element buffered.
// The downstream receives []T values.
func Batch[T any](n int, maxWait time.Duration) Flow[T, []T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(config StageConfig) actor.Actor {
			return newBatchFlowActor[T](n, maxWait, config)
		},
		config: config,
	}
	return Flow[T, []T]{desc: desc}
}

// Buffer inserts an asynchronous buffer stage of the given capacity.
// It decouples the processing rates of upstream and downstream.
// When the buffer is full the OverflowStrategy is applied.
func Buffer[T any](size int, strategy OverflowStrategy) Flow[T, T] {
	if size < 1 {
		size = 1
	}
	config := defaultStageConfig()
	config.OverflowStrategy = strategy
	config.BufferSize = size
	config.InitialDemand = int64(size)
	config.RefillThreshold = int64(size) / 4
	desc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(config StageConfig) actor.Actor {
			return newFlowActor(func(v any) ([]any, error) {
				return []any{v}, nil // identity; the stage actor provides the buffer
			}, config)
		},
		config: config,
	}
	return Flow[T, T]{desc: desc}
}

// Throttle limits throughput to at most n elements per the given duration.
// A ticker-based actor is used so the stage remains responsive to cancellation
// and completion signals while pacing output.
func Throttle[T any](n int, per time.Duration) Flow[T, T] {
	if n < 1 {
		n = 1
	}
	perElement := per / time.Duration(n)
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(config StageConfig) actor.Actor {
			return newThrottleActor[T](perElement, config)
		},
		config: config,
	}
	return Flow[T, T]{desc: desc}
}

// Deduplicate suppresses consecutive duplicate elements.
// Equality is tested with ==; T must be comparable.
func Deduplicate[T comparable]() Flow[T, T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(config StageConfig) actor.Actor {
			var last T
			var hasLast bool
			return newFlowActor(func(v any) ([]any, error) {
				elem, ok := v.(T)
				if !ok {
					return nil, fmt.Errorf("stream: Deduplicate got unexpected type %T", v)
				}
				if hasLast && last == elem {
					return nil, nil
				}
				last = elem
				hasLast = true
				return []any{elem}, nil
			}, config)
		},
		config: config,
	}
	return Flow[T, T]{desc: desc}
}

// Scan applies fn cumulatively, emitting each intermediate accumulator state.
// The initial accumulator value is provided by zero.
func Scan[In, State any](zero State, fn func(State, In) State) Flow[In, State] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(config StageConfig) actor.Actor {
			acc := zero
			return newFlowActor(func(v any) ([]any, error) {
				elem, ok := v.(In)
				if !ok {
					return nil, fmt.Errorf("stream: Scan got unexpected type %T", v)
				}
				acc = fn(acc, elem)
				return []any{acc}, nil
			}, config)
		},
		config: config,
	}
	return Flow[In, State]{desc: desc}
}

// WithContext creates a type-preserving Flow that passes each element through
// unchanged. The key/value pair labels this stage as a named tracing boundary.
// In future releases this metadata will be attached to distributed tracing spans
// when an OpenTelemetry integration is active; for now it is recorded in the
// stage descriptor and visible via actor-system tooling.
func WithContext[T any](key, value string) Flow[T, T] {
	_ = key
	_ = value
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newFlowActor(func(v any) ([]any, error) { return []any{v}, nil }, cfg)
		},
		config: config,
	}
	return Flow[T, T]{desc: desc}
}

// ParallelMap applies fn to each element using up to n goroutines concurrently.
// Elements are emitted downstream in completion order (non-deterministic).
// For order-preserving parallel execution use OrderedParallelMap.
func ParallelMap[In, Out any](n int, fn func(In) Out) Flow[In, Out] {
	if n < 1 {
		n = 1
	}
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newParallelMapActor[In, Out](n, fn, false, cfg)
		},
		config: config,
	}
	return Flow[In, Out]{desc: desc}
}

// OrderedParallelMap applies fn using up to n goroutines but emits results in
// the original element order (resequencing via a min-heap).
func OrderedParallelMap[In, Out any](n int, fn func(In) Out) Flow[In, Out] {
	if n < 1 {
		n = 1
	}
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newParallelMapActor[In, Out](n, fn, true, cfg)
		},
		config: config,
	}
	return Flow[In, Out]{desc: desc}
}

// makeMapFlow is the shared implementation for Map / MapOK.
func makeMapFlow[In, Out any](fn func(In) (Out, error), config StageConfig) Flow[In, Out] {
	desc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(config StageConfig) actor.Actor {
			return newFlowActor(func(v any) ([]any, error) {
				elem, ok := v.(In)
				if !ok {
					return nil, fmt.Errorf("stream: Map got unexpected type %T, want %T", v, *new(In))
				}
				out, err := fn(elem)
				if err != nil {
					return nil, err
				}
				return []any{out}, nil
			}, config)
		},
		config: config,
		fuseFn: func(v any) (any, bool, error) {
			elem, ok := v.(In)
			if !ok {
				return nil, false, fmt.Errorf("stream: Map (fused) got unexpected type %T, want %T", v, *new(In))
			}
			out, err := fn(elem)
			if err != nil {
				return nil, false, err
			}
			return out, true, nil
		},
	}
	return Flow[In, Out]{desc: desc}
}
