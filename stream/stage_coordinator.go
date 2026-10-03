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
	"sync/atomic"

	"github.com/tochemey/goakt/v4/actor"
)

// streamCoordinator is the supervisor root of a materialized stream pipeline.
// Every stage actor is spawned as a child of the coordinator (via SpawnChild),
// giving the stream its own subtree in the actor hierarchy:
//
//	stream-supervisor/{streamID}
//	  ├── stream-{streamID}-0   (source)
//	  ├── stream-{streamID}-1   (flow)
//	  └── stream-{streamID}-2   (sink)
//
// The coordinator's primary responsibilities are:
//  1. Provide a named root for the pipeline subtree so Abort() can cascade via
//     a single Shutdown() call.
//  2. Detect when the SINK terminates before the stream has signaled completion
//     (i.e. a crash in the sink that bypasses the completionWrapper) and signal
//     the StreamHandle so callers are not left hanging.
//  3. Stop itself once the stream has ended, so a finished stream leaves no
//     actor behind. The StreamHandle does not depend on the coordinator:
//     Done, Err and Metrics read state held by the handle.
//
// Source and flow stages terminate as part of normal completion flow (before the
// sink's PostStop fires the onDone callback), so they are NOT watched. Only the
// sink is watched.
type streamCoordinator struct {
	handle *streamHandleImpl
	// sinkPID is the sink stage, watched for unexpected early termination.
	// Atomic because the materializer stores it after the stages are spawned,
	// from a goroutine other than the one running Receive.
	sinkPID atomic.Pointer[actor.PID]
	// sinkStopped is set once the sink's Terminated has been handled. From
	// then on the coordinator stops as soon as its last stage has stopped.
	// Only Receive reads and writes it.
	sinkStopped bool
}

func (c *streamCoordinator) PreStart(_ *actor.Context) error { return nil }

// Receive handles Terminated messages from the stage actors.
// GoAkt automatically delivers Terminated to the parent coordinator for every
// child spawned via SpawnChild when that child stops — regardless of Watch calls.
//
// The sink's Terminated marks the end of the stream. If the handle has not
// been signaled by then, the sink crashed: the coordinator signals the error
// and shuts down at once, which stops the stages that would otherwise wait
// for a sink that is gone. After a regular end the remaining stages are
// stopping on their own as completion or cancellation reaches them; the
// coordinator waits for the last of them before it shuts down, because
// stopping a stage discards its mailbox and a stage must be left to handle
// a pending streamCancel (a fan-out slot, for instance, tells its hub).
func (c *streamCoordinator) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *actor.Terminated:
		sinkPID := c.sinkPID.Load()
		if sinkPID != nil && msg.ActorPath().Equals(sinkPID.Path()) {
			c.sinkStopped = true
			select {
			case <-c.handle.done:
				// Stream already completed normally.
			default:
				c.handle.signalDone(fmt.Errorf("stream: sink %s terminated unexpectedly", msg.ActorPath()))
				rctx.Shutdown()
				return
			}
		}

		if c.sinkStopped && rctx.Self().ChildrenCount() == 0 {
			rctx.Shutdown()
		}

	default:
		rctx.Unhandled()
	}
}

func (c *streamCoordinator) PostStop(_ *actor.Context) error { return nil }
