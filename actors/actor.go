/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

// Actor represents the Actor interface
// This will be implemented by any user who wants to create an actor
// Any implementation must immutable which means all fields must be private(unexported).
// Only make use the PreStart hook to set the initial values.
type Actor interface {
	// PreStart pre-starts the actor. This function can be used to set up some database connections
	// or some sort of initialization before the actor start processing messages
	// when the initialization failed the actor will not be started.
	// Use this function to set any fields that will be needed before the actor starts.
	// This hook helps set the default values needed by any fields of the actor.
	PreStart(ctx context.Context) error
	// Receive processes any message dropped into the actor mailbox.
	// The receiver of any message can either reply to the sender of the message with a new message or reply to the message synchronously
	// by config the reply of the message. The latter approach is often used when an external service is communicating to the actor.
	// One thing to know is that actor can communicate synchronously as well, just that will hinder the performance of the system.
	Receive(ctx *ReceiveContext)
	// PostStop is executed when the actor is shutting down.
	// The execution happens when every message that have not been processed yet will be processed before the actor shutdowns
	// This help free-up resources
	PostStop(ctx context.Context) error
}
