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
	"sync"

	"github.com/tochemey/goakt/v4/actor"
)

// Sink is a lazy description of a terminal stream stage that consumes
// elements of type T. Nothing executes until the graph is materialized.
type Sink[T any] struct {
	desc *stageDesc
}

// Collector accumulates all elements emitted by a Collect sink.
// Call Items() after the stream's Done channel is closed to retrieve results.
type Collector[T any] struct {
	mu    sync.Mutex
	items []T
	ready chan struct{}
	once  sync.Once
}

func newCollector[T any]() *Collector[T] {
	return &Collector[T]{ready: make(chan struct{})}
}

// markDone signals that the stream has completed and Items() may be called.
func (c *Collector[T]) markDone() {
	c.once.Do(func() { close(c.ready) })
}

// append adds an element (called only from the sink actor — no external callers).
func (c *Collector[T]) append(v T) {
	c.mu.Lock()
	c.items = append(c.items, v)
	c.mu.Unlock()
}

// Items blocks until the stream completes, then returns a copy of all collected elements.
func (c *Collector[T]) Items() []T {
	<-c.ready
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]T, len(c.items))
	copy(out, c.items)
	return out
}

// WithErrorStrategy returns a new Sink that uses the given ErrorStrategy
// for element-level processing errors.
func (s Sink[T]) WithErrorStrategy(strategy ErrorStrategy) Sink[T] {
	newDesc := *s.desc
	newDesc.config.ErrorStrategy = strategy
	prevMake := s.desc.makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor {
		return prevMake(newDesc.config)
	}
	return Sink[T]{desc: &newDesc}
}

// WithRetryConfig returns a new Sink with a custom RetryConfig.
// Only takes effect when ErrorStrategy is Retry.
// MaxAttempts < 1 is coerced to 1.
func (s Sink[T]) WithRetryConfig(rc RetryConfig) Sink[T] {
	if rc.MaxAttempts < 1 {
		rc.MaxAttempts = 1
	}
	newDesc := *s.desc
	newDesc.config.RetryConfig = rc
	prevMake := s.desc.makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor {
		return prevMake(newDesc.config)
	}
	return Sink[T]{desc: &newDesc}
}

// WithMailbox returns a new Sink that uses the given actor.Mailbox for its stage actor.
func (s Sink[T]) WithMailbox(mailbox actor.Mailbox) Sink[T] {
	newDesc := *s.desc
	newDesc.config.Mailbox = mailbox
	prevMake := s.desc.makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor { return prevMake(newDesc.config) }
	return Sink[T]{desc: &newDesc}
}

// WithName returns a new Sink with a custom stage actor name.
func (s Sink[T]) WithName(name string) Sink[T] {
	newDesc := *s.desc
	newDesc.config.Name = name
	prevMake := s.desc.makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor { return prevMake(newDesc.config) }
	return Sink[T]{desc: &newDesc}
}

// WithTags returns a new Sink with tags propagated to metrics and traces.
func (s Sink[T]) WithTags(tags map[string]string) Sink[T] {
	newDesc := *s.desc
	newDesc.config.Tags = tags
	prevMake := s.desc.makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor { return prevMake(newDesc.config) }
	return Sink[T]{desc: &newDesc}
}

// WithTracer returns a new Sink with the given Tracer attached.
func (s Sink[T]) WithTracer(t Tracer) Sink[T] {
	newDesc := *s.desc
	newDesc.config.Tracer = t
	prevMake := s.desc.makeActor
	newDesc.makeActor = func(_ StageConfig) actor.Actor { return prevMake(newDesc.config) }
	return Sink[T]{desc: &newDesc}
}

// ForEach creates a Sink that calls fn for each element.
// Errors returned by fn are handled per the pipeline's ErrorStrategy.
func ForEach[T any](fn func(T)) Sink[T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSinkActor(func(v any) error {
				elem, ok := v.(T)
				if !ok {
					return fmt.Errorf("stream: ForEach got unexpected type %T", v)
				}
				fn(elem)
				return nil
			}, nil, cfg)
		},
		config: config,
	}
	return Sink[T]{desc: desc}
}

// Collect creates a Sink that accumulates all elements into a Collector[T].
// Returns both the Collector (for reading results after completion) and the Sink.
func Collect[T any]() (*Collector[T], Sink[T]) {
	result := newCollector[T]()
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSinkActor(func(v any) error {
				elem, ok := v.(T)
				if !ok {
					return fmt.Errorf("stream: Collect got unexpected type %T", v)
				}
				result.append(elem)
				return nil
			}, result.markDone, cfg)
		},
		config: config,
	}
	return result, Sink[T]{desc: desc}
}

// Fold reduces all elements into a single accumulated value.
// The result is stored in the returned *Result[U] and available after the stream completes.
func Fold[T, U any](zero U, fn func(U, T) U) (*FoldResult[U], Sink[T]) {
	res := &FoldResult[U]{value: zero, ready: make(chan struct{})}
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSinkActor(func(v any) error {
				elem, ok := v.(T)
				if !ok {
					return fmt.Errorf("stream: Fold got unexpected type %T", v)
				}
				res.mu.Lock()
				res.value = fn(res.value, elem)
				res.mu.Unlock()
				return nil
			}, func() { res.once.Do(func() { close(res.ready) }) }, cfg)
		},
		config: config,
	}
	return res, Sink[T]{desc: desc}
}

// FoldResult holds the accumulated result of a Fold sink.
type FoldResult[U any] struct {
	mu    sync.Mutex
	value U
	ready chan struct{}
	once  sync.Once
}

// Value blocks until the stream completes, then returns the folded result.
func (r *FoldResult[U]) Value() U {
	<-r.ready
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.value
}

// ToActor creates a Sink that forwards each element to the given GoAkt actor via Tell.
func ToActor[T any](pid *actor.PID) Sink[T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSinkActor(func(v any) error {
				elem, ok := v.(T)
				if !ok {
					return fmt.Errorf("stream: ToActor got unexpected type %T", v)
				}
				return actor.Tell(context.Background(), pid, elem)
			}, nil, cfg)
		},
		config: config,
	}
	return Sink[T]{desc: desc}
}

// ToActorNamed creates a Sink that resolves an actor by name on each element
// and forwards the element via Tell.
func ToActorNamed[T any](system actor.ActorSystem, name string) Sink[T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSinkActor(func(v any) error {
				elem, ok := v.(T)
				if !ok {
					return fmt.Errorf("stream: ToActorNamed got unexpected type %T", v)
				}
				pid, err := system.ActorOf(context.Background(), name)
				if err != nil {
					return err
				}
				return actor.Tell(context.Background(), pid, elem)
			}, nil, cfg)
		},
		config: config,
	}
	return Sink[T]{desc: desc}
}

// First creates a Sink that captures only the first element, then cancels upstream.
func First[T any]() (*FoldResult[T], Sink[T]) {
	res := &FoldResult[T]{ready: make(chan struct{})}
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newFirstSinkActor(res, cfg)
		},
		config: config,
	}
	return res, Sink[T]{desc: desc}
}

// Ignore creates a Sink that discards all elements.
// Useful when side effects in upstream flows are the goal.
func Ignore[T any]() Sink[T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSinkActor(func(any) error { return nil }, nil, cfg)
		},
		config: config,
	}
	return Sink[T]{desc: desc}
}

// Chan writes each element to ch. If ch is full, backpressure propagates upstream.
func Chan[T any](ch chan<- T) Sink[T] {
	config := defaultStageConfig()
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSinkActor(func(v any) error {
				elem, ok := v.(T)
				if !ok {
					return fmt.Errorf("stream: Chan got unexpected type %T", v)
				}
				ch <- elem // blocks when ch is full, applying natural backpressure
				return nil
			}, func() { close(ch) }, cfg)
		},
		config: config,
	}
	return Sink[T]{desc: desc}
}
