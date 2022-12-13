package actors

import (
	"context"
)

// Actor represents the Actor interface
// This will be implemented by any user who wants to create an actor
type Actor interface {
	// PreStart pre-starts the actor. This function can be used to set up some database connections
	// or some sort of initialization before the actor start processing messages
	PreStart(ctx context.Context) error
	// Receive processes any message dropped into the actor mailbox without a reply
	Receive(ctx MessageContext)
	// PostStop is executed when the actor is shutting down.
	// The execution happens when every messages that have not been processed yet will be processed before the actor shutdowns
	PostStop(ctx context.Context) error
}

// ID represents an actor identifier. It is a combination of the type and the actual
// identification number. Example: user-1234 is represented as: Kind=User and Value=1234
type ID struct {
	Kind  string // Kind specifies the type of actor. example: User
	Value string // Value specifies the identifier of the actor: example: 1234
}
