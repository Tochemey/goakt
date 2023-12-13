/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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

package actors

// stash adds the current message to the stash buffer
func (p *pid) stash(ctx ReceiveContext) error {
	// return an error when the stash buffer is not set
	if p.stashBuffer == nil {
		return ErrStashBufferNotSet
	}

	p.stashSemaphore.Lock()
	defer p.stashSemaphore.Unlock()

	// add the message to stash buffer
	p.stashBuffer.Send(ctx)
	p.stashSize.Inc()
	return nil
}

// unstash unstashes the oldest message in the stash and prepends to the mailbox
func (p *pid) unstash() error {
	// return an error when the stash buffer is not set
	if p.stashBuffer == nil {
		return ErrStashBufferNotSet
	}

	p.stashSemaphore.Lock()
	defer p.stashSemaphore.Unlock()

	// grab the message from the stash buffer. Ignore the error when the mailbox is empty
	select {
	case received, ok := <-p.stashBuffer.Iterator():
		if ok {
			// send it to the mailbox processing
			p.doReceive(received)
			p.stashSize.Dec()
		}
	default:
	}
	return nil
}

// unstashAll unstashes all messages from the stash buffer and prepends in the mailbox
// (it keeps the messages in the same order as received, unstashing older messages before newer).
func (p *pid) unstashAll() error {
	// return an error when the stash buffer is not set
	if p.stashBuffer == nil {
		return ErrStashBufferNotSet
	}

	p.stashSemaphore.Lock()
	defer p.stashSemaphore.Unlock()

	for {
		// grab the message from the stash buffer. Ignore the error when the mailbox is empty
		select {
		case received, ok := <-p.stashBuffer.Iterator():
			if ok {
				// send it to the mailbox processing
				p.doReceive(received)
				p.stashSize.Dec()
			}
		default:
			return nil
		}
	}
}
