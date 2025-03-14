/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actor

import "errors"

// stash adds the current message to the stash buffer
func (pid *PID) stash(ctx *ReceiveContext) error {
	if pid.stashBox == nil {
		return ErrStashBufferNotSet
	}
	return pid.stashBox.Enqueue(ctx)
}

// unstash unstashes the oldest message in the stash and prepends to the mailbox
func (pid *PID) unstash() error {
	if pid.stashBox == nil {
		return ErrStashBufferNotSet
	}

	received := pid.stashBox.Dequeue()
	if received == nil {
		return errors.New("stash buffer may be closed")
	}
	pid.doReceive(received)
	return nil
}

// unstashAll unstashes all messages from the stash buffer and prepends in the mailbox
// (it keeps the messages in the same order as received, unstashing older messages before newer).
func (pid *PID) unstashAll() error {
	if pid.stashBox == nil {
		return ErrStashBufferNotSet
	}

	pid.stashLocker.Lock()
	for !pid.stashBox.IsEmpty() {
		if received := pid.stashBox.Dequeue(); received != nil {
			pid.doReceive(received)
		}
	}
	pid.stashLocker.Unlock()
	return nil
}
