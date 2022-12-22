package persistence

import (
	"context"

	"google.golang.org/protobuf/proto"
)

type Command proto.Message
type Event proto.Message
type State proto.Message

type CommandHandler[T State] func(ctx context.Context, command Command, priorState T) (event Event, err error)
type EventHandler[T State] func(ctx context.Context, event Event, priorState T) (state T, err error)
type InitHook func(ctx context.Context) error
type ShutdownHook func(ctx context.Context) error
