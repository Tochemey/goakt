/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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
 *
 */

package actors

import "context"

// DurableStateBehavior defines the interface of a durable state actor / entity behavior to store the full state after processing each command.
// The current state is always stored in the durable state store. Since only the latest state is stored, there is no history of changes.
// The durable state actor engine would read that state and store it in memory. After processing of the command is finished, the new state will be stored in the durable state store.
// The processing of the next command will not start until the state has been successfully stored in the durable state store.
type DurableStateBehavior interface {
	// EmptyState returns the durable state actor empty state
	EmptyState() ActorState
	// PreStartHook pre-starts the actor. This function can be used to set up some database connections
	// or some sort of initialization before the actor start processing messages
	// when the initialization failed the actor will not be started.
	// Use this function to set any fields that will be needed before the actor starts.
	// This hook helps set the default values needed by any fields of the actor.
	PreStartHook(ctx context.Context) error
	// PostStopHook is executed when the actor is shutting down.
	// The execution happens when every message that have not been processed yet will be processed before the actor shutdowns
	// This help free-up resources
	PostStopHook(ctx context.Context) error
	// Handle helps handle commands received by the durable state actor. The command handlers define how to handle each incoming command,
	// which validations must be applied, and finally, whether a resulting state will be persisted depending upon the StatefulEffect
	// They encode the business rules of your durable state actor and act as a guardian of the actor consistency.
	// The command handler must first validate that the incoming command can be applied to the current model state.
	//  Any decision should be solely based on the data passed in the command and the state of the Behavior.
	// In case of successful validation and processing , the new state will be stored in the durable store depending upon the StatefulEffect response
	Handle(ctx context.Context, command Command, priorState *DurableState) CommandResponse
}
