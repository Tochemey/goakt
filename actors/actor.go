package actors

import (
	"context"
)

// Actor represents the Actor interface
// This will be implemented by any user who wants to create an actor
type Actor interface {
	// ID returns the unique identifier of an actor
	ID() string
	// Init initialize the actor. This function can be used to set up some database connections
	// or some sort of initialization before the actor init processing messages
	Init(ctx context.Context) error
	// Receive processes any message dropped into the actor mailbox without a reply
	Receive(message Message) error
	// Stop gracefully shuts down the given actor
	Stop(ctx context.Context)
}
