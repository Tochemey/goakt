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

import (
	"context"
)

// Actor defines the core interface for an actor in the system's concurrency model.
//
// Actors are lightweight, isolated units of computation that communicate exclusively
// via message passing. Each actor has its own mailbox and processes messages sequentially,
// ensuring thread safety without requiring explicit synchronization.
//
// Structs implementing this interface must be **immutable**—all fields should be private (unexported)
// and initialized via the `PreStart` hook. This immutability guarantees safe concurrent access
// and avoids race conditions.
//
// The lifecycle of an actor follows three main phases:
//  1. PreStart – Setup logic before message handling begins
//  2. Receive – Core message handling loop
//  3. PostStop – Cleanup logic after the actor is stopped
//
// Actors are typically managed by a runtime system or actor framework that handles
// their scheduling, supervision, restarts, and message delivery.
type Actor interface {
	// PreStart is invoked once before the actor begins processing any messages.
	//
	// Use this hook to perform one-time setup operations such as:
	//   - Initializing internal state or caches
	//   - Establishing connections to external services (e.g., databases, APIs)
	//   - Registering the actor with discovery mechanisms or registries
	//
	// If an error is returned, the actor will fail to start and will not process messages.
	// The actor runtime may choose to restart or escalate the failure depending on its supervision strategy.
	PreStart(ctx context.Context) error

	// Receive handles all messages sent to the actor's mailbox.
	//
	// This method is the primary entry point for processing messages. It is invoked
	// sequentially per actor instance and may:
	//   - Send replies to other actors
	//   - Forward or delegate messages
	//   - Spawn child actors for further processing
	//
	// Message handling should be efficient and non-blocking. Long-running or blocking
	// operations should be offloaded to separate goroutines to preserve system responsiveness.
	Receive(ctx *ReceiveContext)

	// PostStop is invoked after the actor has processed its final message and is about to terminate.
	//
	// Use this hook to perform cleanup tasks such as:
	//   - Flushing buffers or committing final state
	//   - Closing network connections or file handles
	//   - Unsubscribing from event streams or deregistering from discovery systems
	//
	// This method is guaranteed to be called once, even if `PreStart` failed or `Receive` panicked.
	PostStop(ctx context.Context) error
}
