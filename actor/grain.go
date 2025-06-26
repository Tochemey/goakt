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

import "context"

// Grain defines the contract for grains (virtual actors) in the goakt actor system.
//
// A Grain is a lightweight, virtual actor that encapsulates state and behavior, managed by goakt.
// Grains are automatically activated and deactivated by the system, providing location transparency and efficient resource usage.
// Each grain instance is uniquely identified and processes messages sequentially, ensuring single-threaded execution and simplifying state management.
//
// # Lifecycle Methods
//
//   - OnActivate: Called when the grain is loaded into memory. Use this to initialize state or resources.
//   - OnDeactivate: Called before the grain is removed from memory. Use this to persist state and release resources.
//
// # Message Handling
//
//   - OnReceive: Handles an incoming message. Only one call is active at a time per grain instance.
//
// # Implementation Notes
//
//   - Always respect the provided context for cancellation and deadlines.
//   - Do not retain references to context or message instances beyond the method scope.
//   - Use the Dependencies method (if implemented) to declare external dependencies required by the grain.
//
// Any type intended to act as a grain within the goakt actor system should implement this interface.
//
// Methods:
//   - OnActivate: Initialize state or resources when the grain is loaded.
//   - OnReceive: Process incoming messages and update state. Single-threaded per grain instance.
//   - OnDeactivate: Persist state and release resources before the grain is removed.
type Grain interface {
	// OnActivate is called when the grain is loaded into memory.
	// Use this to initialize state or resources.
	//
	// The provided context contains cancellation and deadline information.
	// The actorSystem provides system-level references.
	// Return an error to indicate activation failure. If an error is returned,
	// the grain will not be activated.
	OnActivate(ctx context.Context, props *GrainProps) error

	// OnReceive is called when the grain receives a message.
	//
	// This method is invoked for each incoming message to the grain instance.
	// The provided GrainContext contains the message, sender information, grain identity,
	// and references to the actor system.
	//
	// Implement this method to define how the grain processes messages and updates its state.
	// Message processing is single-threaded per grain instance, ensuring that only one
	// OnReceive call is active at a time.
	//
	// Notes:
	//   - Always respect cancellation and deadlines via the context in GrainContext.
	//   - Do not retain references to the GrainContext or its fields beyond the method scope.
	//   - Use this method for both command and query message handling.
	OnReceive(ctx *GrainContext)

	// OnDeactivate is called before the grain is removed from memory.
	// Use this to persist state and release resources.
	//
	// The provided context contains cancellation and deadline information.
	// The actorSystem provides system-level references.
	// Return an error to indicate deactivation failure. If an error is returned,
	// the system may log or handle the failure as appropriate.
	OnDeactivate(ctx context.Context, props *GrainProps) error
}
