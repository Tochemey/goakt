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

	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/persistence"
)

// VirtualContext provides the execution context for virtual actors.
//
// VirtualContext encapsulates the actor's current state, the state store for persistence operations,
// and the Go context in which the actor operates. It is passed to actor lifecycle and message handling
// methods to provide access to state management, cancellation, deadlines, and other contextual information.
//
// # Usage
//
// VirtualContext is guaranteed to be available during each actor processing phase (activation, deactivation,
// and message handling). It should be treated as immutable and not retained beyond the scope of the current
// operation.
//
// Typical use cases include:
//   - Accessing the actor's persistent state for reading or modification
//   - Performing state persistence operations via the StateStore
//   - Observing cancellation signals or deadlines from the context
//
// # Example
//
//	func (a *MyActor) HandleMessage(ctx *actor.VirtualContext, msg proto.Message) (proto.Message, error) {
//	    state := ctx.State()
//	    store := ctx.StateStore()
//	    goCtx := ctx.Context()
//	    // ... actor logic ...
//	}
//
// See package documentation for more details on actor execution context and state management.
type VirtualContext struct {
	ctx        context.Context
	stateStore persistence.StateStore
	logger     log.Logger
}

// Context returns the Go context associated with this virtual actor process context.
//
// The returned context provides access to cancellation signals, deadlines, and other
// request-scoped values that may be relevant during the actor's execution. Use this
// context for any long-running or asynchronous operations to ensure proper cancellation.
func (v *VirtualContext) Context() context.Context {
	return v.ctx
}

// StateStore returns the state store used for persistence operations.
//
// The StateStore provides access to the underlying storage mechanism for reading and writing
// the actor's state. Use this to persist any state changes made during the actor's execution.
func (v *VirtualContext) StateStore() persistence.StateStore {
	return v.stateStore
}

// Logger returns the logger associated with this virtual actor context.
//
// The logger can be used to log messages related to the actor's lifecycle,
// message handling, and any other relevant events. It is configured according to
// the actor system's logging settings and can be used for structured logging.
// Use the logger to capture important events, errors, and debug information during
// the actor's execution.
//
// It is recommended to use structured logging where possible to facilitate
// better observability and debugging.
func (v *VirtualContext) Logger() log.Logger {
	return v.logger
}
