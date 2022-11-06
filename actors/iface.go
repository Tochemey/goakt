package actors

import (
	"context"

	"google.golang.org/protobuf/proto"
)

// Actor represents the Actor interface
// This will be implemented by any user who wants to create an actor
type Actor interface {
	// Init initialize the actor. This function can be used to set up some database connections
	// or some sort of initialization before the actor init processing messages
	Init(ctx context.Context) error
	// Receive processes any message dropped into the actor mailbox without a reply
	Receive(ctx context.Context, message proto.Message) error
	// ReceiveReply processes any message dropped into the actor mailbox with a reply
	ReceiveReply(ctx context.Context, message proto.Message) (proto.Message, error)
	// Stop gracefully shuts down the given actor
	Stop(ctx context.Context)
}

// actorRef defines the various actions one can perform on a given actor
type actorRef interface {
	// Send sends a given message to the actor in a fire-and-forget pattern
	Send(ctx context.Context, message proto.Message) error
	// SendReply sends a given message to the actor and expect a reply in return
	SendReply(ctx context.Context, message proto.Message) (proto.Message, error)
	// Shutdown gracefully shuts down the given actor
	Shutdown(ctx context.Context)
	// Heartbeat returns true when the actor is alive ready to process messages and false
	// when the actor is stopped or not started at all
	Heartbeat() bool
}
