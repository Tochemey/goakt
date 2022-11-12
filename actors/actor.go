package actors

import (
	"context"

	"google.golang.org/protobuf/proto"
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
	Receive(ctx context.Context, message proto.Message) error
	// ReceiveReply processes any message dropped into the actor mailbox with a reply
	ReceiveReply(ctx context.Context, message proto.Message) (proto.Message, error)
	// Stop gracefully shuts down the given actor
	Stop(ctx context.Context)
}
