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
	Receive(ctx ReceiveContext)
	// PostStop is executed when the actor is shutting down.
	// The execution happens when every messages that have not been processed yet will be processed before the actor shutdowns
	PostStop(ctx context.Context) error
}

// LocalID represents an actor identifier. It is a combination of the type and the actual
// identification number. Example: user-1234 is represented as: Kind=User and Value=1234
type LocalID struct {
	kind string // Kind specifies the type of actor. example: User
	id   string // Value specifies the identifier of the actor: example: 1234
}

// Kind specifies the type of actor. example: User
func (i LocalID) Kind() string {
	return i.kind
}

// ID return the relative id of the actor
func (i LocalID) ID() string {
	return i.id
}
