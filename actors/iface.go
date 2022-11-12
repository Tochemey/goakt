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

// actorRef defines the various actions one can perform on a given actor
type actorRef interface {
	// Send sends a given message to the actor in a fire-and-forget pattern
	Send(ctx context.Context, message proto.Message) error
	// SendReply sends a given message to the actor and expect a reply in return
	SendReply(ctx context.Context, message proto.Message) (proto.Message, error)
	// Shutdown gracefully shuts down the given actor
	Shutdown(ctx context.Context)
	// IsReady returns true when the actor is alive ready to process messages and false
	// when the actor is stopped or not started at all
	IsReady(ctx context.Context) bool
	// TotalProcessed returns the total number of messages processed by the actor
	// at a given point in time while the actor heart is still beating
	TotalProcessed(ctx context.Context) uint64
	// ErrorsCount returns the total number of panic attacks that occur while the actor is processing messages
	// at a given point in time while the actor heart is still beating
	ErrorsCount(ctx context.Context) uint64
}

// ActorSystem defines the contract of an actor system
type ActorSystem interface {
	// Name returns the actor system name
	Name() string
	// NodeAddr returns the node where the actor system is running
	NodeAddr() string
	// Actors returns the list of Actors that are alive in the actor system
	Actors() []*ActorRef
	// Start starts the actor system
	Start(ctx context.Context) error
	// Stop stops the actor system
	Stop(ctx context.Context) error
	// Spawn creates an actor in the system
	Spawn(ctx context.Context, kind string, actor Actor) *ActorRef
}
