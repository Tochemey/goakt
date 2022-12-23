package persistence

import (
	"context"

	"google.golang.org/protobuf/proto"
)

type Command proto.Message
type Event proto.Message
type State proto.Message

// PersistentBehavior defines a persistence behavior
type PersistentBehavior[T State] interface {
	Kind() string
	PersistenceID() string
	InitialState() T
	HandleCommand(ctx context.Context, command Command, priorState T) (event Event, err error)
	HandleEvent(ctx context.Context, event Event, priorState T) (state T, err error)
}
