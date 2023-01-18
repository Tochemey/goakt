package persistence

import (
	"context"

	pb "github.com/tochemey/goakt/pb/goakt/v1"
)

// OffsetStore defines the contract needed to persist persistence entities
// events offsets
type OffsetStore interface {
	// Connect connects to the offset store
	Connect(ctx context.Context) error
	// Disconnect disconnects the offset store
	Disconnect(ctx context.Context) error
	// WriteOffset writes the current offset of the event consumed for a given projection ID
	WriteOffset(ctx context.Context, offset *pb.Offset) error
	// GetCurrentOffset returns the current offset of a given projection ID
	GetCurrentOffset(ctx context.Context, projectionID *ProjectionID) (current *pb.Offset, err error)
	// GetLatestOffset returns the latest offset a given projection ID
	GetLatestOffset(ctx context.Context, projectionID *ProjectionID) (latest *pb.Offset, err error)
}
