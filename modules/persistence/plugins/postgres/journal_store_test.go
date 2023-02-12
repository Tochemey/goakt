package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/tochemey/goakt/modules/persistence"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/pkg/postgres"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPostgresJournalStore(t *testing.T) {
	t.Run("testNewJournalStore", func(t *testing.T) {
		config := &postgres.Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     testDatabase,
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		store := NewJournalStore(config)
		assert.NotNil(t, store)
		var p interface{} = store
		_, ok := p.(persistence.JournalStore)
		assert.True(t, ok)
	})
	t.Run("testConnect:happy path", func(t *testing.T) {
		ctx := context.TODO()
		config := &postgres.Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     testDatabase,
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		store := NewJournalStore(config)
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		assert.NoError(t, err)
		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testConnect:database does not exist", func(t *testing.T) {
		ctx := context.TODO()
		config := &postgres.Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     "testDatabase",
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		store := NewJournalStore(config)
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		assert.Error(t, err)
		assert.EqualError(t, err, "failed to ping database connection: pq: database \"testDatabase\" does not exist")
	})
	t.Run("testWriteAndReplayEvents", func(t *testing.T) {
		ctx := context.TODO()
		config := &postgres.Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     testDatabase,
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		store := NewJournalStore(config)
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		require.NoError(t, err)

		db, err := dbHandle(ctx)
		require.NoError(t, err)

		schemaUtil := NewSchemaUtils(db)

		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		state, err := anypb.New(&testpb.Account{})
		assert.NoError(t, err)
		event, err := anypb.New(&testpb.AccountCreated{})
		assert.NoError(t, err)

		ts1 := timestamppb.Now()
		ts2 := timestamppb.Now()

		e1 := &pb.Event{
			PersistenceId:  "persistence-1",
			SequenceNumber: 1,
			IsDeleted:      false,
			Event:          event,
			ResultingState: state,
			Timestamp:      ts1.AsTime().Unix(),
		}

		event, err = anypb.New(&testpb.AccountCredited{})
		assert.NoError(t, err)

		e2 := &pb.Event{
			PersistenceId:  "persistence-1",
			SequenceNumber: 2,
			IsDeleted:      false,
			Event:          event,
			ResultingState: state,
			Timestamp:      ts2.AsTime().Unix(),
		}

		events := []*pb.Event{e1, e2}
		err = store.WriteEvents(ctx, events)
		assert.NoError(t, err)

		persistenceID := "persistence-1"
		max := uint64(4)
		from := uint64(1)
		to := uint64(2)
		replayed, err := store.ReplayEvents(ctx, persistenceID, from, to, max)
		assert.NoError(t, err)
		assert.NotEmpty(t, replayed)
		assert.Len(t, replayed, 2)
		assert.Equal(t, prototext.Format(events[0]), prototext.Format(replayed[0]))
		assert.Equal(t, prototext.Format(events[1]), prototext.Format(replayed[1]))

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testGetLatestEvent", func(t *testing.T) {
		ctx := context.TODO()
		config := &postgres.Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     testDatabase,
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		store := NewJournalStore(config)
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		require.NoError(t, err)

		db, err := dbHandle(ctx)
		require.NoError(t, err)

		schemaUtil := NewSchemaUtils(db)

		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		state, err := anypb.New(&testpb.Account{})
		assert.NoError(t, err)
		event, err := anypb.New(&testpb.AccountCreated{})
		assert.NoError(t, err)

		ts1 := timestamppb.New(time.Now().UTC())
		ts2 := timestamppb.New(time.Now().UTC())

		e1 := &pb.Event{
			PersistenceId:  "persistence-1",
			SequenceNumber: 1,
			IsDeleted:      false,
			Event:          event,
			ResultingState: state,
			Timestamp:      ts1.AsTime().Unix(),
		}

		event, err = anypb.New(&testpb.AccountCredited{})
		assert.NoError(t, err)

		e2 := &pb.Event{
			PersistenceId:  "persistence-1",
			SequenceNumber: 2,
			IsDeleted:      false,
			Event:          event,
			ResultingState: state,
			Timestamp:      ts2.AsTime().Unix(),
		}

		events := []*pb.Event{e1, e2}
		err = store.WriteEvents(ctx, events)
		assert.NoError(t, err)

		persistenceID := "persistence-1"

		actual, err := store.GetLatestEvent(ctx, persistenceID)
		assert.NoError(t, err)
		assert.NotNil(t, actual)

		assert.Equal(t, prototext.Format(e2), prototext.Format(actual))

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
	t.Run("testDeleteEvents", func(t *testing.T) {
		ctx := context.TODO()
		config := &postgres.Config{
			DBHost:     testContainer.Host(),
			DBPort:     testContainer.Port(),
			DBName:     testDatabase,
			DBUser:     testUser,
			DBPassword: testDatabasePassword,
			DBSchema:   testContainer.Schema(),
		}

		store := NewJournalStore(config)
		assert.NotNil(t, store)
		err := store.Connect(ctx)
		require.NoError(t, err)

		db, err := dbHandle(ctx)
		require.NoError(t, err)

		schemaUtil := NewSchemaUtils(db)

		err = schemaUtil.CreateTable(ctx)
		require.NoError(t, err)

		state, err := anypb.New(&testpb.Account{})
		assert.NoError(t, err)
		event, err := anypb.New(&testpb.AccountCreated{})
		assert.NoError(t, err)

		ts1 := timestamppb.New(time.Now().UTC())
		ts2 := timestamppb.New(time.Now().UTC())

		e1 := &pb.Event{
			PersistenceId:  "persistence-1",
			SequenceNumber: 1,
			IsDeleted:      false,
			Event:          event,
			ResultingState: state,
			Timestamp:      ts1.AsTime().Unix(),
		}

		event, err = anypb.New(&testpb.AccountCredited{})
		assert.NoError(t, err)

		e2 := &pb.Event{
			PersistenceId:  "persistence-1",
			SequenceNumber: 2,
			IsDeleted:      false,
			Event:          event,
			ResultingState: state,
			Timestamp:      ts2.AsTime().Unix(),
		}

		events := []*pb.Event{e1, e2}
		err = store.WriteEvents(ctx, events)
		assert.NoError(t, err)

		persistenceID := "persistence-1"

		actual, err := store.GetLatestEvent(ctx, persistenceID)
		assert.NoError(t, err)
		assert.NotNil(t, actual)

		assert.Equal(t, prototext.Format(e2), prototext.Format(actual))

		// let us delete the events
		err = store.DeleteEvents(ctx, persistenceID, uint64(3))
		assert.NoError(t, err)
		actual, err = store.GetLatestEvent(ctx, persistenceID)
		assert.NoError(t, err)
		assert.Nil(t, actual)

		err = schemaUtil.DropTable(ctx)
		assert.NoError(t, err)

		err = store.Disconnect(ctx)
		assert.NoError(t, err)
	})
}
