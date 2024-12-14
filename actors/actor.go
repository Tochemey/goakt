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

	"google.golang.org/protobuf/proto"
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

// PersistentActor represents a type of Actor that persists its full state after processing each command instead of using event sourcing.
// This type of Actor keeps its current state in memory during command handling and based upon the command response
// persists its full state into a durable store. The store can be a SQL or NoSQL database.
// The whole concept is given the current state of the actor and a command produce a new state with a higher version as shown in this
// diagram: (State, Command) => State
// PersistentActor reacts to commands which result in a new version of the actor state. Only the latest version of the actor actorState is
// persisted to the durable store. There is no concept of history regarding the actor state since this is not an event sourced actor.
// However, one can rely on the version number of the actor state and exactly know how the actor state has evolved overtime.
// State actor version number are numerically incremented by the command handler which means it is imperative that the newer version of the state is greater than the current version by one.
// If the new actor state version does not meet the set requirement the given actor will be put into a suspended state.
//
// PersistentActor contrary to normal actor during a restart procedure will attempt to recover its state whenever available from the durable state.
// During a normal shutdown process, it will persist its current state to the durable store prior to shutting down.
// This behavior help maintain some consistency accross the actor state evolution.
//
// Note: PersistentActor aside these particular characteristics are like any other actor in GoAkt.
type PersistentActor interface {
	// EmptyState defines the actor initial state.
	// This state will always come with a version zero.
	EmptyState() proto.Message
	// PreStart pre-starts the actor. This function can be used to set up some database connections
	// or some sort of initialization before the actor start processing messages
	// when the initialization failed the actor will not be started.
	// Use this function to set any fields that will be needed before the actor starts.
	// This hook helps set the default values needed by any fields of the actor.
	PreStart(ctx context.Context) error
	// PostStop is executed when the actor is shutting down.
	// The execution happens when every message that have not been processed yet will be processed before the actor shutdowns
	// This help free-up resources
	PostStop(ctx context.Context) error
	// Receive processes every command sent to the persistent actor. One needs to use the command and the currentState sent to produce a command response.
	// Every command sent to the persistent actor expects a response within a timeframe that must be defined when creating the persistent actor.
	// When a command response is not received within the given time period the persistent actor will be put in a suspended state.
	// Remember that a suspended actor can be either restarted or killed.
	// Ths defines how to handle each incoming command,
	// which validations must be applied, and finally, whether a resulting state will be persisted depending upon the PersistentResponse.
	// They encode the business rules of your durable state actor and act as a guardian of the actor consistency.
	// The command handler must first validate that the incoming command can be applied to the current model state.
	// Any decision should be solely based on the data passed in the command and the state of the PersistentContext.
	// In case of successful validation and processing , the new state will be stored in the durable store depending upon the PersistentResponse response.
	Receive(ctx *PersistentContext) *PersistentResponse
}
