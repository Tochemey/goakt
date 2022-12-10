package actors

import (
	"context"
)

// Actor represents the Actor interface
// This will be implemented by any user who wants to create an actor
type Actor interface {
	// ID returns the unique identifier of an actor
	ID() string
	// PreStart pre-starts the actor. This function can be used to set up some database connections
	// or some sort of initialization before the actor start processing messages
	PreStart(ctx context.Context) error
	// Receive processes any message dropped into the actor mailbox without a reply
	Receive(message Message) error
	// PostStop is executed when the actor is shutting down.
	// The execution happens when every messages that have not been processed yet will be processed before the actor shutdowns
	PostStop(ctx context.Context) error
}
