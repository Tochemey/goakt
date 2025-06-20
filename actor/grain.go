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

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/extension"
)

const (
	identitySeparator = "/"
)

// Grain defines the contract for grains (virtual actors) in the actor system.
//
// A Grain is a lightweight, virtual actor that encapsulates state and behavior, managed by the goakt actor system.
// Grains are automatically activated and deactivated by the system, providing location transparency and efficient resource usage.
// Each grain instance is uniquely identified and processes messages sequentially, ensuring single-threaded execution and simplifying state management.
//
// ## Key Features
//   - **Activation/Deactivation:** Grains are activated on demand and deactivated when idle, conserving resources.
//   - **Location Transparency:** Grains are addressed by unique identity, not by network address or process.
//   - **Single-threaded Execution:** Each grain processes one message at a time, avoiding concurrency issues.
//   - **Lifecycle Hooks:** Grains can initialize and clean up resources using activation and deactivation hooks.
//   - **Dependency Injection:** Grains can declare dependencies that are injected by the runtime.
//
// ## Implementation Guidelines
//   - Implement `OnActivate` to initialize state or resources when the grain is loaded.
//   - Implement `OnDeactivate` to persist state and clean up resources before removal.
//   - Implement `ReceiveSync` to process incoming messages synchronously and return a response.
//   - Implement `ReceiveAsync` to process incoming messages asynchronously.
//   - Always respect the provided context for cancellation and deadlines.
//   - Do not retain references to context or message instances beyond the method scope.
//   - Use the `Dependencies` method to declare external dependencies required by the grain.
//
// ## Example
//
//	type MyGrain struct{}
//
//	func (g *MyGrain) OnActivate(ctx *GrainContext) error {
//	    // Load state or initialize resources
//	    return nil
//	}
//
//	func (g *MyGrain) OnDeactivate(ctx *GrainContext) error {
//	    // Persist state or clean up resources
//	    return nil
//	}
//
//	func (g *MyGrain) Dependencies() []extension.Dependency {
//	    // Return required dependencies
//	    return nil
//	}
//
//	func (g *MyGrain) ReceiveSync(ctx context.Context, message proto.Message) (proto.Message, error) {
//	    // Handle the incoming message and return a response
//	    return NewGrainResponse("ok"), nil
//	}
//
//	func (g *MyGrain) ReceiveAsync(ctx context.Context, message proto.Message) error {
//	    // Handle the incoming message asynchronously
//	    return nil
//	}
//
// The Grain interface should be implemented by any type intended to act as a grain within the goakt actor system.
type Grain interface {
	// OnActivate is called when the grain is loaded into memory.
	// Use this to load state or initialize resources.
	//
	// The provided GrainContext contains grain identity and system references.
	// Return an error to indicate activation failure. If an error is returned,
	// the grain will not be activated.
	OnActivate(ctx *GrainContext) error

	// OnDeactivate is called before the grain is removed from memory.
	// Use this to persist state and release resources.
	//
	// The provided GrainContext contains grain identity and system references.
	// Return an error to indicate deactivation failure. If an error is returned,
	// the system may log or handle the failure as appropriate.
	OnDeactivate(ctx *GrainContext) error

	// Dependencies returns a slice of external dependencies required by the grain.
	// These dependencies are injected by the actor system at activation time.
	// Return nil or an empty slice if no dependencies are required.
	Dependencies() []extension.Dependency

	// ReceiveSync processes an incoming message for the grain synchronously.
	// Returns a response or error. Only one call is active at a time per grain instance.
	//
	// Parameters:
	//   - ctx: context for cancellation and deadlines.
	//   - message: the incoming message to process.
	//
	// Returns:
	//   - proto.Message: the response message.
	//   - error: any error encountered during processing.
	ReceiveSync(ctx context.Context, message proto.Message) (proto.Message, error)

	// ReceiveAsync processes an incoming message for the grain asynchronously.
	// Returns an error if processing fails. Only one call is active at a time per grain instance.
	//
	// Parameters:
	//   - ctx: context for cancellation and deadlines.
	//   - message: the incoming message to process.
	//
	// Returns:
	//   - error: any error encountered during processing.
	ReceiveAsync(ctx context.Context, message proto.Message) error
}
