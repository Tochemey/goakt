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
	"errors"
	"sync"

	gerrors "github.com/tochemey/goakt/v4/errors"
)

type stashState struct {
	box    *UnboundedMailbox
	locker sync.Mutex
}

// stash adds the current message to the stash buffer. ctx is still
// linked into the main mailbox via its intrusive `next`, so a clone
// is enqueued instead — the original completes its main-mailbox turn.
func (pid *PID) stash(ctx *ReceiveContext) error {
	state := pid.stashState
	if state == nil || state.box == nil {
		return gerrors.ErrStashBufferNotSet
	}
	return state.box.Enqueue(cloneContext(ctx))
}

// unstash pops the oldest stashed message and re-enters it into the
// main mailbox. The dequeued context is the stash mailbox's new
// sentinel, so a clone is re-enqueued instead of the original.
func (pid *PID) unstash() error {
	state := pid.stashState
	if state == nil || state.box == nil {
		return gerrors.ErrStashBufferNotSet
	}

	received := state.box.Dequeue()
	if received == nil {
		return errors.New("stash buffer may be closed")
	}
	pid.doReceive(cloneContext(received))
	return nil
}

// unstashAll unstashes all messages from the stash buffer and prepends in the mailbox
// (it keeps the messages in the same order as received, unstashing older messages before newer).
func (pid *PID) unstashAll() error {
	state := pid.stashState
	if state == nil || state.box == nil {
		return gerrors.ErrStashBufferNotSet
	}

	state.locker.Lock()
	for !state.box.IsEmpty() {
		if received := state.box.Dequeue(); received != nil {
			pid.doReceive(cloneContext(received))
		}
	}
	state.locker.Unlock()
	return nil
}
