package persistence

import (
	"context"

	"google.golang.org/protobuf/proto"
)

type Command proto.Message
type Event proto.Message
type State proto.Message

type CommandHandler func(ctx context.Context, command Command) (event Event, err error)
type EventHandler func(ctx context.Context, event Event) (state State, err error)
type InitHook func(ctx context.Context) error
type ShutdownHook func(ctx context.Context) error
