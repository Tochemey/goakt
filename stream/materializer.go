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
	"strconv"
	"sync/atomic"

	"github.com/tochemey/goakt/v4/actor"
)

// streamSeq is a process-global counter used to generate unique, lightweight
// stream sub-IDs. A monotonic integer is sufficient for within-process
// correlation (logs, metrics, actor names) and avoids the entropy read and
// string-formatting cost of UUID generation on every materialization.
var streamSeq uint64

// newStreamSubID returns a unique stream identifier string.
func newStreamSubID() string {
	return strconv.FormatUint(atomic.AddUint64(&streamSeq, 1), 10)
}

// completionWrapper wraps any actor.Actor and calls onDone in PostStop.
// The materializer uses this to detect when the sink terminates.
type completionWrapper struct {
	inner  actor.Actor
	onDone func()
}

func (w *completionWrapper) PreStart(ctx *actor.Context) error { return w.inner.PreStart(ctx) }
func (w *completionWrapper) Receive(ctx *actor.ReceiveContext) { w.inner.Receive(ctx) }
func (w *completionWrapper) PostStop(ctx *actor.Context) error {
	err := w.inner.PostStop(ctx)
	w.onDone()
	return err
}

// materialize spawns one actor per stage under a shared stream coordinator,
// wires them together, and returns a StreamHandle. The coordinator acts as
// the supervisor root for all stage actors, forming the hierarchy:
//
//	stream-supervisor/{streamID}
//	  ├── stream-{streamID}-0   (source)
//	  ├── stream-{streamID}-1   (flow …)
//	  └── stream-{streamID}-N   (sink)
//
// The sink sends the initial demand signal on receipt of its stageWire
// message, starting the pipeline.
func materialize(ctx context.Context, system actor.ActorSystem, stages []*stageDesc) (StreamHandle, error) {
	if err := (RunnableGraph{stages: stages}).validate(); err != nil {
		return nil, err
	}

	subID := newStreamSubID()

	// Pre-allocate the handle so we can close over it in the sink wrapper.
	handle := newStreamHandle(subID, nil, nil)

	// Wrap the sink stage so PostStop signals completion.
	sinkIdx := len(stages) - 1
	origSinkMake := stages[sinkIdx].makeActor
	wrappedSinkDesc := *stages[sinkIdx]
	wrappedSinkDesc.makeActor = func(cfg StageConfig) actor.Actor {
		return &completionWrapper{
			inner:  origSinkMake(cfg),
			onDone: func() { handle.signalDone(nil) },
		}
	}

	// Build a local slice with the wrapped sink.
	wrapped := make([]*stageDesc, len(stages))
	copy(wrapped, stages)
	wrapped[sinkIdx] = &wrappedSinkDesc

	// ── Spawn the stream coordinator as the supervisor root ────────────────────
	coord := &streamCoordinator{handle: handle}
	coordName := fmt.Sprintf("stream-supervisor-%s", subID)
	coordPID, err := system.Spawn(ctx, coordName, coord)
	if err != nil {
		handle.signalDone(err)
		return nil, fmt.Errorf("stream: spawn coordinator: %w", err)
	}
	handle.coordPID = coordPID

	// ── Spawn each stage actor as a child of the coordinator ───────────────────
	pids := make([]*actor.PID, len(wrapped))
	for i, desc := range wrapped {
		cfg := desc.config
		cfg.System = system
		// Inject shared metrics pointers so StreamHandle.Metrics() reflects real counts.
		if i == 0 {
			cfg.Metrics = handle.sourceMetrics
		}
		if i == sinkIdx {
			cfg.Metrics = handle.sinkMetrics
		}
		// Inject the stage name into cfg so actors know their own name (for tracer calls).
		if cfg.Name == "" {
			cfg.Name = fmt.Sprintf("stream-%s-%d", subID, i)
		}
		stageName := cfg.Name
		a := desc.makeActor(cfg)

		var spawnOpts []actor.SpawnOption
		if cfg.Mailbox != nil {
			spawnOpts = append(spawnOpts, actor.WithMailbox(cfg.Mailbox))
		} else if cfg.BufferSize > 0 {
			spawnOpts = append(spawnOpts, actor.WithMailbox(actor.NewBoundedMailbox(cfg.BufferSize*2)))
		}
		pid, spawnErr := coordPID.SpawnChild(ctx, stageName, a, spawnOpts...)
		if spawnErr != nil {
			// Shut down already-spawned actors; coordinator cascades to its children.
			_ = coordPID.Shutdown(ctx)
			handle.signalDone(spawnErr)
			return nil, fmt.Errorf("stream: spawn stage %d: %w", i, spawnErr)
		}
		pids[i] = pid
	}

	// ── Wire stages: send each actor its upstream and downstream PIDs ──────────
	n := len(pids)
	for i, pid := range pids {
		var upstream, downstream *actor.PID
		if i > 0 {
			upstream = pids[i-1]
		}
		if i < n-1 {
			downstream = pids[i+1]
		}
		if err := actor.Tell(ctx, pid, &stageWire{
			subID:      subID,
			upstream:   upstream,
			downstream: downstream,
		}); err != nil {
			_ = coordPID.Shutdown(ctx)
			handle.signalDone(err)
			return nil, fmt.Errorf("stream: wire stage %d: %w", i, err)
		}
	}

	// Watch only the sink so the coordinator can detect an unexpected crash
	// (a sink crash that bypasses completionWrapper.PostStop). Source and flow
	// stages shut down as part of the normal completion flow — before the sink's
	// onDone fires — so watching them would produce spurious errors.
	coord.sinkPID = pids[sinkIdx]
	coordPID.Watch(pids[sinkIdx])

	handle.sourcePID = pids[0]
	handle.allPIDs = pids

	return handle, nil
}

// applyFusion combines adjacent fusable flow stages into a single fused stage.
// Source and sink stages are never fused.
func applyFusion(stages []*stageDesc, mode FusionMode) []*stageDesc {
	if mode == FuseNone || len(stages) < 2 {
		return stages
	}
	result := make([]*stageDesc, 0, len(stages))
	i := 0
	for i < len(stages) {
		s := stages[i]
		// Only fuse flow-kind stages with a fuseFn set and Fusion enabled.
		if s.kind != flowKind || s.fuseFn == nil || !s.config.Fusion {
			result = append(result, s)
			i++
			continue
		}
		// Find a run of fusable stages starting at i.
		composed := s.fuseFn
		last := s
		j := i + 1
		for j < len(stages) {
			next := stages[j]
			if next.kind != flowKind || next.fuseFn == nil || !next.config.Fusion {
				break
			}
			// Compose: apply s.fuseFn then next.fuseFn.
			prevComposed := composed
			nextFn := next.fuseFn
			composed = func(v any) (any, bool, error) {
				r, pass, err := prevComposed(v)
				if err != nil || !pass {
					return r, pass, err
				}
				return nextFn(r)
			}
			last = next
			j++
		}
		if j == i+1 {
			// No fusion happened for this stage.
			result = append(result, s)
			i++
			continue
		}
		// Create a fused stage that applies the composed function.
		fusedComposed := composed
		fusedConfig := last.config // use last stage's config
		fusedDesc := &stageDesc{
			id:     newStageID(),
			kind:   flowKind,
			config: fusedConfig,
			makeActor: func(cfg StageConfig) actor.Actor {
				return newFusedFlowActor(fusedComposed, cfg)
			},
		}
		result = append(result, fusedDesc)
		i = j
	}
	return result
}

// materializeAll materializes each pipeline independently and returns a
// multiHandle that aggregates their lifecycles. On any spawn failure the
// already-started pipelines are aborted before the error is returned.
func materializeAll(ctx context.Context, system actor.ActorSystem, pipelines [][]*stageDesc) (StreamHandle, error) {
	handles := make([]StreamHandle, 0, len(pipelines))
	for _, pipeline := range pipelines {
		h, err := materialize(ctx, system, pipeline)
		if err != nil {
			for _, started := range handles {
				started.Abort()
			}
			return nil, err
		}
		handles = append(handles, h)
	}
	return newMultiHandle(handles), nil
}
