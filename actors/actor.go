package actors

import (
	"context"
)

// Actor represents the Actor interface
// This will be implemented by any user who wants to create an actor
type Actor interface {
	// PreStart pre-starts the actor. This function can be used to set up some database connections
	// or some sort of initialization before the actor start processing public
	PreStart(ctx context.Context) error
	// Receive processes any message dropped into the actor mailbox.
	// The receiver of any message can either reply to the sender of the message with a new message or reply to the message synchronously
	// by config the reply of the message. The latter approach is often used when an external service is communicating to the actor.
	// One thing to know is that actor can communicate synchronously as well, just that will hinder the performance of the system.
	Receive(ctx ReceiveContext)
	// PostStop is executed when the actor is shutting down.
	// The execution happens when every message that have not been processed yet will be processed before the actor shutdowns
	PostStop(ctx context.Context) error
}
