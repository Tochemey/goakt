package persistence

import (
	"context"

	"google.golang.org/protobuf/proto"
)

type Command proto.Message
type Event proto.Message
type State proto.Message

type PersistentActor interface {
	InitialState(state State)
	HandleCommand(ctx context.Context, command Command) (event Event, err error)
	HandleEvent(ctx context.Context, event Event) (state State, err error)
}

type persistentActor[S State] struct {
	initialState  S
	journalStore  JournalStore
	snapshotStore SnapshotStore
}
