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

package actor

import (
	"sync"
	"time"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/refusal"
)

// lateGrainMessages holds, per deactivated grain instance, the messages that
// reached it after it deactivated and wait to be sent to a fresh activation.
// Each entry is a copy of the message's GrainContext, detached from the pool
// because the mailbox recycles the original. It lives on the actor system so
// grainPID keeps its fixed size. A queue exists only while it has messages.
type lateGrainMessages struct {
	mu     sync.Mutex
	queues map[*grainPID][]*GrainContext
}

// push appends late to pid's queue. It reports true when pid had no queue,
// in which case the caller must start forwarding it.
func (x *lateGrainMessages) push(pid *grainPID, late *GrainContext) bool {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.queues == nil {
		x.queues = make(map[*grainPID][]*GrainContext)
	}

	queue, exists := x.queues[pid]
	x.queues[pid] = append(queue, late)
	return !exists
}

// pop removes and returns the oldest late message of pid. When the queue is
// empty it is deleted and pop returns nil.
func (x *lateGrainMessages) pop(pid *grainPID) *GrainContext {
	x.mu.Lock()
	defer x.mu.Unlock()

	queue := x.queues[pid]
	if len(queue) == 0 {
		delete(x.queues, pid)
		return nil
	}

	late := queue[0]
	queue[0] = nil
	x.queues[pid] = queue[1:]
	return late
}

// redirectLateMessage handles a message that reached this instance after it
// deactivated. A node that is shutting down refuses it, marked as a node
// refusal (refusal.Mark) because the message has not run; otherwise it is sent to a
// fresh activation and the reply goes back to the original caller.
func (pid *grainPID) redirectLateMessage(grainContext *GrainContext) {
	system := pid.actorSystem
	if system.isStopping() {
		grainContext.Err(refusal.Mark(gerrors.ErrSystemShuttingDown))
		return
	}

	late := &GrainContext{
		ctx:            grainContext.ctx,
		self:           grainContext.self,
		actorSystem:    grainContext.actorSystem,
		message:        grainContext.message,
		response:       grainContext.response,
		err:            grainContext.err,
		synchronous:    grainContext.synchronous,
		oneWay:         grainContext.oneWay,
		requestID:      grainContext.requestID,
		requestReplyTo: grainContext.requestReplyTo,
		timeout:        grainContext.timeout,
		deadline:       grainContext.deadline,
	}

	if system.getLateGrainMessages().push(pid, late) {
		go pid.forwardLateMessages()
	}
}

// forwardLateMessages sends this instance's late messages to a fresh
// activation, one at a time, so they keep their arrival order.
func (pid *grainPID) forwardLateMessages() {
	queue := pid.actorSystem.getLateGrainMessages()
	for late := queue.pop(pid); late != nil; late = queue.pop(pid) {
		late.forwardLate()
	}
}

// forwardLate delivers this late message to a fresh activation and relays the
// outcome to the original caller through the usual reply methods.
func (x *GrainContext) forwardLate() {
	// the sender gave up while the message waited: no activation for it
	if x.expired() {
		x.Err(gerrors.ErrRequestTimeout)
		return
	}

	// a reentrant ask travels as an envelope; the new activation replies to
	// the caller itself
	if x.requestID != "" {
		envelope := &commands.AsyncRequest{CorrelationID: x.requestID, ReplyTo: x.requestReplyTo, Message: x.message, Deadline: x.deadline}
		if err := x.actorSystem.deliverAsyncEnvelope(x.ctx, x.self, envelope); err != nil {
			x.Err(err)
		}
		return
	}

	mode := grainTell
	switch {
	case x.synchronous:
		mode = grainAsk
	case x.oneWay:
		mode = grainOneWay
	}

	reply, err := x.actorSystem.localSendGrain(x.ctx, x.self, x.message, x.lateSendTimeout(), mode)
	switch {
	case err != nil:
		x.Err(err)
	case x.synchronous:
		x.Response(reply)
	default:
		x.NoErr()
	}
}

// lateSendTimeout returns how long a late forward may wait for the fresh
// activation: for an ask the time left until its sender stops waiting, so the
// forward does not start the timeout over; otherwise the caller's own timeout,
// else the time left on its context, else the default grain request timeout.
func (x *GrainContext) lateSendTimeout() time.Duration {
	if x.deadline != 0 {
		return untilAskDeadline(x.deadline)
	}

	if x.timeout > 0 {
		return x.timeout
	}

	if deadline, ok := x.ctx.Deadline(); ok {
		return time.Until(deadline)
	}

	return DefaultGrainRequestTimeout
}
