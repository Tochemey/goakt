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

// GrainFactory defines a function type responsible for creating new Grain instances.
//
// This factory function is used by the goakt actor system to instantiate grains (virtual actors)
// on demand. It receives a context.Context, which can be used for cancellation, deadlines,
// and passing request-scoped values. The function must return a new Grain implementation
// and an error if instantiation fails.
//
// Typical usage is to provide a GrainFactory when registering a grain type with the actor system.
//
// Example:
//
//	func MyGrainFactory(ctx context.Context) (actor.Grain, error) {
//	    return &MyGrain{}, nil
//	}
//
// The returned Grain must be safe for single-threaded access, as the system guarantees
// that only one message is processed at a time per grain instance.
type GrainFactory func(ctx context.Context) (Grain, error)

// Grain defines the contract for grains (virtual actors) in the goakt actor system.
//
// A Grain is a lightweight, virtual actor that encapsulates state and behavior, managed by goakt.
// Grains are automatically activated and deactivated by the system. Each grain instance is uniquely identified
// and processes messages sequentially, ensuring single-threaded execution and simplifying state management.
//
// Implementations must be safe for single-threaded access; concurrent calls are not made to a single grain instance.
//
// Methods:
//
//   - OnActivate: Called when the grain is loaded into memory. Use this to initialize state or resources.
//     Arguments:
//
//   - ctx: context for cancellation and deadlines.
//
//   - props: grain properties and system-level references.
//     Returns:
//
//   - error: non-nil to indicate activation failure (grain will not be activated).
//
//   - OnReceive: Handles an incoming message. Only one call is active at a time per grain instance.
//     Arguments:
//
//   - ctx: GrainContext containing the message, sender, grain identity, and system references.
//     Behavior:
//
//   - Processes the message and updates grain state as needed.
//
//   - Always respect cancellation and deadlines via the context in GrainContext.
//
//   - Do not retain references to the GrainContext or its fields beyond the method scope.
//
//   - OnDeactivate: Called before the grain is removed from memory. Use this to persist state and release resources.
//     Arguments:
//
//   - ctx: context for cancellation and deadlines.
//
//   - props: grain properties and system-level references.
//     Returns:
//
//   - error: non-nil to indicate deactivation failure (system may log or handle the failure).
type Grain interface {
	// OnActivate is called when the grain is loaded into memory.
	// Use this to initialize state or resources.
	//
	// Arguments:
	//   - ctx: context for cancellation and deadlines.
	//   - props: grain properties and system-level references.
	// Returns:
	//   - error: non-nil to indicate activation failure (grain will not be activated).
	OnActivate(ctx context.Context, props *GrainProps) error

	// OnReceive is called when the grain receives a message.
	//
	// Arguments:
	//   - ctx: GrainContext containing the message, sender, grain identity, and system references.
	// Behavior:
	//   - Processes the message and updates grain state as needed.
	//   - Always respect cancellation and deadlines via the context in GrainContext.
	//   - Do not retain references to the GrainContext or its fields beyond the method scope.
	OnReceive(ctx *GrainContext)

	// OnDeactivate is called before the grain is removed from memory.
	// Use this to persist state and release resources.
	//
	// Arguments:
	//   - ctx: context for cancellation and deadlines.
	//   - props: grain properties and system-level references.
	// Returns:
	//   - error: non-nil to indicate deactivation failure (system may log or handle the failure).
	OnDeactivate(ctx context.Context, props *GrainProps) error
}
