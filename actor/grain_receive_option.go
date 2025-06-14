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

// GrainReceiveOption provides additional context and options for a grain's Receive method.
//
// It is used to pass metadata about the message delivery, such as the sender's identity.
// This allows grains to access information about the origin of the message when processing requests.
//
// Example usage:
//
//	func (g *MyGrain) Receive(msg proto.Message, option *GrainReceiveOption) (proto.Message, error) {
//	    sender := option.Sender()
//	    // Use sender information if needed
//	    ...
//	}
type GrainReceiveOption struct {
	sender *Identity
}

// Sender returns the identity of the sender of the message, if available.
//
// This can be used by the grain to respond or take action based on the sender.
func (g *GrainReceiveOption) Sender() *Identity {
	return g.sender
}
