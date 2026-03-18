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
//
// Source and flow stages terminate as part of normal completion flow (before the
// sink's PostStop fires the onDone callback), so they are NOT watched. Only the
// sink is watched.
type streamCoordinator struct {
	handle  *streamHandleImpl
	sinkPID *actor.PID // watched for unexpected early termination
}

func (c *streamCoordinator) PreStart(_ *actor.Context) error { return nil }

// Receive handles Terminated messages from watched actors.
// GoAkt automatically delivers Terminated to the parent coordinator for every
// child spawned via SpawnChild when that child stops — regardless of Watch calls.
// Source and flow stages stop as part of normal completion flow, so we only
// act on Terminated when the message is for the sink PID and the stream has
// not yet signaled completion (i.e. an unexpected sink crash).
func (c *streamCoordinator) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *actor.Terminated:
		// Only care about the sink; ignore normal source/flow shutdowns.
		if c.sinkPID == nil || !msg.ActorPath().Equals(c.sinkPID.Path()) {
			return
		}
		select {
		case <-c.handle.done:
			// Stream already completed normally — ignore.
		default:
			c.handle.signalDone(fmt.Errorf("stream: sink %s terminated unexpectedly", msg.ActorPath()))
		}
	default:
		rctx.Unhandled()
	}
}

func (c *streamCoordinator) PostStop(_ *actor.Context) error { return nil }
