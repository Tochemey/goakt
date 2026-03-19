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
	"sync"
	"sync/atomic"

	"github.com/tochemey/goakt/v4/actor"
)

// StreamHandle is the live runtime handle for a materialized stream.
// It provides lifecycle control and observability.
type StreamHandle interface {
	// ID returns the unique identifier of this stream instance.
	// Useful for correlating logs, metrics, and traces across stages.
	ID() string
	// Done returns a channel that is closed when the stream terminates,
	// either by normal completion or by an error.
	Done() <-chan struct{}
	// Err returns the terminal error, if any. Returns nil on normal completion.
	// Valid only after Done() is closed.
	Err() error
	// Stop signals an orderly shutdown. In-flight elements are drained before
	// stages are stopped. Blocks until the stream has terminated or the context
	// deadline is exceeded.
	Stop(ctx context.Context) error
	// Abort immediately terminates all stage actors, discarding buffered elements.
	Abort()
	// Metrics returns a snapshot of live stream metrics.
	Metrics() StreamMetrics
}

// streamHandleImpl is the concrete StreamHandle returned by RunnableGraph.Run().
type streamHandleImpl struct {
	done      chan struct{}
	closeOnce sync.Once
	termErr   atomic.Value // holds error

	subID       string
	source      *actor.PID   // first stage — receives streamCancel on Stop
	coordinator *actor.PID   // supervisor coordinator — shut down on Abort (cascades to stages)
	stageActors []*actor.PID // stage actors — fallback shutdown when coordinator is nil

	// per-stage metric collectors, populated by stage actors
	sinkMetrics   *stageMetrics
	sourceMetrics *stageMetrics
}

func newStreamHandle(subID string, source *actor.PID, all []*actor.PID) *streamHandleImpl {
	return &streamHandleImpl{
		done:          make(chan struct{}),
		subID:         subID,
		source:        source,
		stageActors:   all,
		sinkMetrics:   &stageMetrics{},
		sourceMetrics: &stageMetrics{},
	}
}

// signalDone closes the done channel exactly once and records the first error.
func (h *streamHandleImpl) signalDone(err error) {
	h.closeOnce.Do(func() {
		if err != nil {
			h.termErr.Store(err)
		}
		close(h.done)
	})
}

func (h *streamHandleImpl) ID() string { return h.subID }

func (h *streamHandleImpl) Done() <-chan struct{} { return h.done }

func (h *streamHandleImpl) Err() error {
	v := h.termErr.Load()
	if v == nil {
		return nil
	}
	return v.(error)
}

func (h *streamHandleImpl) Stop(ctx context.Context) error {
	if h.source == nil {
		// No pipeline to drain.
		return nil
	}
	_ = actor.Tell(ctx, h.source, &streamCancel{subID: h.subID})
	// Wait for the stream to fully drain, respecting the caller's context deadline.
	select {
	case <-h.done:
		return h.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *streamHandleImpl) Abort() {
	// Signal the error before shutting down actors so the first call to
	// signalDone (which may come from the completion wrapper's PostStop)
	// carries ErrStreamCanceled.
	h.signalDone(ErrStreamCanceled)
	if h.coordinator != nil {
		// Shutting down the coordinator cascades to all stage children.
		_ = h.coordinator.Shutdown(context.Background())
		return
	}
	for _, pid := range h.stageActors {
		_ = pid.Shutdown(context.Background())
	}
}

func (h *streamHandleImpl) Metrics() StreamMetrics {
	parts := []StreamMetrics{
		h.sourceMetrics.snapshot(),
		h.sinkMetrics.snapshot(),
	}
	return aggregateMetrics(parts)
}

// multiHandle aggregates the lifecycles of N independent stream handles
// produced by Graph DSL compilation. Done closes when ALL sub-handles are done;
// Stop and Abort are fanned out concurrently; Metrics aggregates all subs.
type multiHandle struct {
	handles []StreamHandle
	id      string
	done    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	termErr error
}

func newMultiHandle(handles []StreamHandle) *multiHandle {
	h := &multiHandle{
		handles: handles,
		id:      newStreamSubID(),
		done:    make(chan struct{}),
	}
	go h.waitAll()
	return h
}

func (h *multiHandle) waitAll() {
	var wg sync.WaitGroup
	wg.Add(len(h.handles))
	for _, sh := range h.handles {
		sh := sh
		go func() {
			defer wg.Done()
			<-sh.Done()
			if err := sh.Err(); err != nil {
				h.mu.Lock()
				if h.termErr == nil {
					h.termErr = err
				}
				h.mu.Unlock()
			}
		}()
	}
	wg.Wait()
	h.once.Do(func() { close(h.done) })
}

func (h *multiHandle) ID() string            { return h.id }
func (h *multiHandle) Done() <-chan struct{} { return h.done }

func (h *multiHandle) Err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.termErr
}

func (h *multiHandle) Stop(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(len(h.handles))
	errs := make([]error, len(h.handles))
	for i, sh := range h.handles {
		i, sh := i, sh
		go func() {
			defer wg.Done()
			errs[i] = sh.Stop(ctx)
		}()
	}
	wg.Wait()
	select {
	case <-h.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return h.Err()
}

func (h *multiHandle) Abort() {
	for _, sh := range h.handles {
		sh.Abort()
	}
}

func (h *multiHandle) Metrics() StreamMetrics {
	parts := make([]StreamMetrics, len(h.handles))
	for i, sh := range h.handles {
		parts[i] = sh.Metrics()
	}
	return aggregateMetrics(parts)
}
