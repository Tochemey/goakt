package postgres

import (
	"context"
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/eventsourcing/storage"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/pkg/postgres"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/anypb"
)

var (
	columns = []string{
		"persistence_id",
		"sequence_number",
		"is_deleted",
		"event_payload",
		"event_manifest",
		"state_payload",
		"state_manifest",
		"timestamp",
	}

	tableName = "event_journal"
)

// EventStore implements the EventStore interface
// and helps persist events in a Postgres database
type EventStore struct {
	db postgres.IDatabase
	sb sq.StatementBuilderType
	// insertBatchSize represents the chunk of data to bulk insert.
	// This helps avoid the postgres 65535 parameter limit.
	// This is necessary because Postgres uses a 32-bit int for binding input parameters and
	// is not able to track anything larger.
	// Note: Change this value when you know the size of data to bulk insert at once. Otherwise, you
	// might encounter the postgres 65535 parameter limit error.
	insertBatchSize int
}

// make sure the PostgresEventStore implements the EventStore interface
var _ storage.EventStore = &EventStore{}

// NewEventStore creates a new instance of PostgresEventStore
func NewEventStore(config *postgres.Config) *EventStore {
	// create the underlying db connection
	db := postgres.New(config)
	return &EventStore{
		db:              db,
		sb:              sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
		insertBatchSize: 500,
	}
}

// Connect connects to the underlying postgres database
func (p *EventStore) Connect(ctx context.Context) error {
	return p.db.Connect(ctx)
}

// Disconnect disconnects from the underlying postgres database
func (p *EventStore) Disconnect(ctx context.Context) error {
	return p.db.Disconnect(ctx)
}

// WriteEvents writes a bunch of events into the underlying postgres database
func (p *EventStore) WriteEvents(ctx context.Context, events []*pb.Event) error {
	// check whether the journals list is empty
	if len(events) == 0 {
		// do nothing
		return nil
	}

	// let us begin a database transaction to make sure we atomically write those events into the database
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	// return the error in case we are unable to get a database transaction
	if err != nil {
		return errors.Wrap(err, "failed to obtain a database transaction")
	}

	// start creating the sql statement for insertion
	statement := p.sb.Insert(tableName).Columns(columns...)
	for index, event := range events {
		var (
			eventManifest string
			eventBytes    []byte
			stateManifest string
			stateBytes    []byte
		)

		// serialize the event and resulting state
		eventBytes, _ = proto.Marshal(event.GetEvent())
		stateBytes, _ = proto.Marshal(event.GetResultingState())

		// grab the manifest
		eventManifest = string(event.GetEvent().ProtoReflect().Descriptor().FullName())
		stateManifest = string(event.GetResultingState().ProtoReflect().Descriptor().FullName())

		// build the insertion values
		statement = statement.Values(
			event.GetPersistenceId(),
			event.GetSequenceNumber(),
			event.GetIsDeleted(),
			eventBytes,
			eventManifest,
			stateBytes,
			stateManifest,
			event.GetTimestamp(),
		)

		if (index+1)%p.insertBatchSize == 0 || index == len(events)-1 {
			// get the SQL statement to run
			query, args, err := statement.ToSql()
			// handle the error while generating the SQL
			if err != nil {
				return errors.Wrap(err, "unable to build sql insert statement")
			}
			// insert into the table
			_, execErr := tx.ExecContext(ctx, query, args...)
			if execErr != nil {
				// attempt to roll back the transaction and log the error in case there is an error
				if err = tx.Rollback(); err != nil {
					return errors.Wrap(err, "unable to rollback db transaction")
				}
				// return the main error
				return errors.Wrap(execErr, "failed to record events")
			}

			// reset the statement for the next bulk
			statement = p.sb.Insert(tableName).Columns(columns...)
		}
	}

	// commit the transaction
	if commitErr := tx.Commit(); commitErr != nil {
		// return the commit error in case there is one
		return errors.Wrap(commitErr, "failed to record events")
	}
	// every looks good
	return nil
}

// DeleteEvents deletes events from the postgres up to a given sequence number (inclusive)
func (p *EventStore) DeleteEvents(ctx context.Context, persistenceID string, toSequenceNumber uint64) error {
	// create the database delete statement
	statement := p.sb.
		Delete(tableName).
		Where(sq.Eq{"persistence_id": persistenceID}).
		Where(sq.LtOrEq{"sequence_number": toSequenceNumber})

	// get the sql statement and the arguments
	query, args, err := statement.ToSql()
	if err != nil {
		return errors.Wrap(err, "failed to build the delete events sql statement")
	}

	// execute the sql statement
	if _, err := p.db.Exec(ctx, query, args...); err != nil {
		return errors.Wrap(err, "failed to delete events from the database")
	}

	return nil
}

// ReplayEvents fetches events for a given persistence ID from a given sequence number(inclusive) to a given sequence number(inclusive)
func (p *EventStore) ReplayEvents(ctx context.Context, persistenceID string, fromSequenceNumber, toSequenceNumber uint64, max uint64) ([]*pb.Event, error) {
	// create the database select statement
	statement := p.sb.
		Select(columns...).
		From(tableName).
		Where(sq.Eq{"persistence_id": persistenceID}).
		Where(sq.GtOrEq{"sequence_number": fromSequenceNumber}).
		Where(sq.LtOrEq{"sequence_number": toSequenceNumber}).
		OrderBy("sequence_number ASC").
		Limit(max)

	// get the sql statement and the arguments
	query, args, err := statement.ToSql()
	if err != nil {
		return nil, errors.Wrap(err, "failed to build the select sql statement")
	}

	// create the ds to hold the database record
	type row struct {
		PersistenceID  string
		SequenceNumber uint64
		IsDeleted      bool
		EventPayload   []byte
		EventManifest  string
		StatePayload   []byte
		StateManifest  string
		Timestamp      int64
	}

	// execute the query against the database
	var rows []*row
	err = p.db.SelectAll(ctx, &rows, query, args...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch the events from the database")
	}

	events := make([]*pb.Event, 0, max)
	for _, row := range rows {
		// unmarshal the event and the state
		evt, err := p.toProto(row.EventManifest, row.EventPayload)
		if err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal the journal event")
		}
		state, err := p.toProto(row.StateManifest, row.StatePayload)
		if err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal the journal state")
		}
		// create the event and add it to the list of events
		events = append(events, &pb.Event{
			PersistenceId:  row.PersistenceID,
			SequenceNumber: row.SequenceNumber,
			IsDeleted:      row.IsDeleted,
			Event:          evt,
			ResultingState: state,
			Timestamp:      row.Timestamp,
		})
	}

	return events, nil
}

// GetLatestEvent fetches the latest event
func (p *EventStore) GetLatestEvent(ctx context.Context, persistenceID string) (*pb.Event, error) {
	// create the database select statement
	statement := p.sb.
		Select(columns...).
		From(tableName).
		Where(sq.Eq{"persistence_id": persistenceID}).
		OrderBy("sequence_number DESC").
		Limit(1)

	// get the sql statement and the arguments
	query, args, err := statement.ToSql()
	if err != nil {
		return nil, errors.Wrap(err, "failed to build the select sql statement")
	}

	// create the ds to hold the database record
	type row struct {
		PersistenceID  string
		SequenceNumber uint64
		IsDeleted      bool
		EventPayload   []byte
		EventManifest  string
		StatePayload   []byte
		StateManifest  string
		Timestamp      int64
	}

	// execute the query against the database
	data := new(row)
	err = p.db.Select(ctx, data, query, args...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch the latest event from the database")
	}

	// check whether we do have data
	if data.PersistenceID == "" {
		return nil, nil
	}

	// unmarshal the event and the state
	evt, err := p.toProto(data.EventManifest, data.EventPayload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal the journal event")
	}
	state, err := p.toProto(data.StateManifest, data.StatePayload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal the journal state")
	}

	return &pb.Event{
		PersistenceId:  data.PersistenceID,
		SequenceNumber: data.SequenceNumber,
		IsDeleted:      data.IsDeleted,
		Event:          evt,
		ResultingState: state,
		Timestamp:      data.Timestamp,
	}, nil
}

// toProto converts a byte array given its manifest into a valid proto message
func (p *EventStore) toProto(manifest string, bytea []byte) (*anypb.Any, error) {
	mt, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(manifest))
	if err != nil {
		return nil, err
	}

	pm := mt.New().Interface()
	err = proto.Unmarshal(bytea, pm)
	if err != nil {
		return nil, err
	}

	if cast, ok := pm.(*anypb.Any); ok {
		return cast, nil
	}
	return nil, fmt.Errorf("failed to unpack message=%s", manifest)
}
