package memory

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/persistence"
	"google.golang.org/protobuf/proto"
)

func TestMemoryOffsetStore(t *testing.T) {
	t.Run("testNew", func(t *testing.T) {
		store := NewOffsetStore()
		assert.NotNil(t, store)
		var p interface{} = store
		_, ok := p.(persistence.OffsetStore)
		assert.True(t, ok)
	})
	t.Run("testConnect", func(t *testing.T) {
		ctx := context.TODO()
		store := NewOffsetStore()
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testWriteOffset", func(t *testing.T) {
		ctx := context.TODO()

		store := NewOffsetStore()
		assert.NotNil(t, store)
		require.NoError(t, store.Connect(ctx))

		persistenceID := uuid.NewString()
		projectionName := "DB_WRITER"
		timestamp := time.Now().UnixMilli()

		offset := &pb.Offset{
			PersistenceId:  persistenceID,
			ProjectionName: projectionName,
			CurrentOffset:  15,
			Timestamp:      timestamp,
		}

		require.NoError(t, store.WriteOffset(ctx, offset))

		err := store.Disconnect(ctx)
		assert.NoError(t, err)
	})

	t.Run("testGetCurrentOffset: happy path", func(t *testing.T) {
		ctx := context.TODO()

		store := NewOffsetStore()
		assert.NotNil(t, store)
		require.NoError(t, store.Connect(ctx))

		persistenceID := uuid.NewString()
		projectionName := "DB_WRITER"
		timestamp := time.Now().UnixMilli()

		offset := &pb.Offset{
			PersistenceId:  persistenceID,
			ProjectionName: projectionName,
			CurrentOffset:  15,
			Timestamp:      timestamp,
		}

		require.NoError(t, store.WriteOffset(ctx, offset))

		offset = &pb.Offset{
			PersistenceId:  persistenceID,
			ProjectionName: projectionName,
			CurrentOffset:  24,
			Timestamp:      timestamp,
		}

		require.NoError(t, store.WriteOffset(ctx, offset))

		actual, err := store.GetCurrentOffset(ctx, persistence.NewProjectionID(projectionName, persistenceID))
		assert.NoError(t, err)
		assert.NotNil(t, actual)
		assert.True(t, proto.Equal(offset, actual))

		assert.NoError(t, store.Disconnect(ctx))
	})
	t.Run("testGetCurrentOffset: not found", func(t *testing.T) {
		ctx := context.TODO()

		store := NewOffsetStore()
		assert.NotNil(t, store)
		require.NoError(t, store.Connect(ctx))

		persistenceID := uuid.NewString()
		projectionName := "DB_WRITER"

		actual, err := store.GetCurrentOffset(ctx, persistence.NewProjectionID(projectionName, persistenceID))
		assert.NoError(t, err)
		assert.Nil(t, actual)

		assert.NoError(t, store.Disconnect(ctx))
	})
}
