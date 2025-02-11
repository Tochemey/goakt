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

package actors

import (
	"context"
)

// Actor defines the interface for an actor in the system.
// Any struct implementing this interface must be immutable, meaning all fields should be private (unexported).
// Use the PreStart hook to initialize any required fields or resources.
type Actor interface {
	// PreStart is called before the actor starts processing messages.
	// It can be used for initialization tasks such as setting up database connections or other dependencies.
	// If this function returns an error, the actor will not start.
	PreStart(ctx context.Context) error
	// Receive handles messages delivered to the actor's mailbox.
	// The actor can respond to messages asynchronously by sending a reply or synchronously by configuring the reply within the message.
	// While synchronous communication is possible, it can impact system performance and should be used with caution.
	Receive(ctx *ReceiveContext)
	// PostStop is called when the actor is shutting down.
	// It ensures that any remaining messages in the mailbox are processed before termination.
	// Use this hook to release resources, close connections, or perform cleanup tasks.
	PostStop(ctx context.Context) error
}
