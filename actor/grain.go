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
)

const (
	identitySeparator = "/"
)

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
//   - ReceiveSync: Handles an incoming message synchronously and returns a response. Only one call is active at a time per grain instance.
//   - ReceiveAsync: Handles an incoming message asynchronously. Only one call is active at a time per grain instance.
//
// # Implementation Notes
//
//   - Always respect the provided context for cancellation and deadlines.
//   - Do not retain references to context or message instances beyond the method scope.
//   - Use the Dependencies method (if implemented) to declare external dependencies required by the grain.
//
// Any type intended to act as a grain within the goakt actor system should implement this interface.
type Grain interface {
	// OnActivate is called when the grain is loaded into memory.
	// Use this to initialize state or resources.
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
