package persistence

import "context"

type OffsetStore interface {
	WriteOffset(ctx context.Context) error
}
