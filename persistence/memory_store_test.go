package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNewMemoryStore(t *testing.T) {
	store := NewMemoryStore()
	assert.NotNil(t, store)
	var p interface{} = store
	_, ok := p.(JournalStore)
	assert.True(t, ok)
}

func TestMemoryStore_Connect(t *testing.T) {
	ctx := context.TODO()
	store := NewMemoryStore()
	assert.NotNil(t, store)
	err := store.Connect(ctx)
	assert.NoError(t, err)
}

func TestMemoryStore_WriteJournals(t *testing.T) {
	ctx := context.TODO()
	state := &emptypb.Empty{}
	marshaledState, err := anypb.New(state)
	assert.NoError(t, err)
	event := &emptypb.Empty{}
	marshaledEvent, err := anypb.New(event)
	assert.NoError(t, err)

	timestamp := timestamppb.Now()

	journal := &pb.Journal{
		PersistenceId:  "persistence-1",
		SequenceNumber: 1,
		IsDeleted:      false,
		Event:          marshaledEvent,
		ResultingState: marshaledState,
		Timestamp:      timestamp,
	}

	store := NewMemoryStore()
	assert.NotNil(t, store)

	err = store.WriteJournals(ctx, []*pb.Journal{journal})
	assert.NoError(t, err)

	// fetch the data we insert back
	actual, err := store.GetLatestJournal(ctx, "persistence-1")
	assert.NoError(t, err)
	assert.NotNil(t, actual)
	assert.True(t, proto.Equal(journal, actual))

	err = store.Disconnect(ctx)
	assert.NoError(t, err)
}

func TestMemoryStore_DeleteJournals(t *testing.T) {
	ctx := context.TODO()
	state := &emptypb.Empty{}
	marshaledState, err := anypb.New(state)
	assert.NoError(t, err)
	event := &emptypb.Empty{}
	marshaledEvent, err := anypb.New(event)
	assert.NoError(t, err)

	timestamp := timestamppb.Now()

	journal := &pb.Journal{
		PersistenceId:  "persistence-1",
		SequenceNumber: 1,
		IsDeleted:      false,
		Event:          marshaledEvent,
		ResultingState: marshaledState,
		Timestamp:      timestamp,
	}

	store := NewMemoryStore()
	assert.NotNil(t, store)

	err = store.WriteJournals(ctx, []*pb.Journal{journal})
	assert.NoError(t, err)

	// fetch the data we insert back
	actual, err := store.GetLatestJournal(ctx, "persistence-1")
	assert.NoError(t, err)
	assert.NotNil(t, actual)
	assert.True(t, proto.Equal(journal, actual))

	// delete the journal
	err = store.DeleteJournals(ctx, "persistence-1", 2)
	assert.NoError(t, err)

	actual, err = store.GetLatestJournal(ctx, "persistence-1")
	assert.NoError(t, err)
	assert.Nil(t, actual)

	err = store.Disconnect(ctx)
	assert.NoError(t, err)
}

func TestMemoryStore_ReplayJournals(t *testing.T) {
	ctx := context.TODO()
	state := &emptypb.Empty{}
	marshaledState, err := anypb.New(state)
	assert.NoError(t, err)
	event := &emptypb.Empty{}
	marshaledEvent, err := anypb.New(event)
	assert.NoError(t, err)

	timestamp := timestamppb.Now()

	count := 10
	journals := make([]*pb.Journal, count)
	for i := 0; i < count; i++ {
		seqNr := i + 1
		journals[i] = &pb.Journal{
			PersistenceId:  "persistence-1",
			SequenceNumber: uint64(seqNr),
			IsDeleted:      false,
			Event:          marshaledEvent,
			ResultingState: marshaledState,
			Timestamp:      timestamp,
		}
	}

	store := NewMemoryStore()
	assert.NotNil(t, store)

	err = store.WriteJournals(ctx, journals)
	assert.NoError(t, err)

	from := uint64(3)
	to := uint64(6)

	actual, err := store.ReplayJournals(ctx, "persistence-1", from, to)
	assert.NoError(t, err)
	assert.NotEmpty(t, actual)
	assert.Len(t, actual, 4)

	err = store.Disconnect(ctx)
	assert.NoError(t, err)
}
