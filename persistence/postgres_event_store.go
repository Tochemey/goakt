package persistence

import (
	"context"
	"database/sql"

	sq "github.com/Masterminds/squirrel"
	"github.com/pkg/errors"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/pkg/postgres"
	"google.golang.org/protobuf/proto"
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

// PostgresEventStore implements the EventStore interface
// and helps persist events in a Postgres database
type PostgresEventStore struct {
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
var _ EventStore = &PostgresEventStore{}

// NewPostgresEventStore creates a new instance of PostgresEventStore
func NewPostgresEventStore(config *postgres.Config) *PostgresEventStore {
	// create the underlying db connection
	db := postgres.New(config)
	return &PostgresEventStore{
		db:              db,
		sb:              sq.StatementBuilderType{},
		insertBatchSize: 500,
	}
}

// Connect connects to the underlying postgres database
func (p *PostgresEventStore) Connect(ctx context.Context) error {
	return p.db.Connect(ctx)
}

// Disconnect disconnects from the underlying postgres database
func (p *PostgresEventStore) Disconnect(ctx context.Context) error {
	return p.db.Disconnect(ctx)
}

// WriteEvents writes a bunch of events into the underlying postgres database
func (p *PostgresEventStore) WriteEvents(ctx context.Context, events []*pb.Event) error {
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
			event.GetTimestamp().AsTime().UTC(),
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
func (p *PostgresEventStore) DeleteEvents(ctx context.Context, persistenceID string, toSequenceNumber uint64) error {
	//TODO implement me
	panic("implement me")
}

// ReplayEvents fetches events for a given persistence ID from a given sequence number(inclusive) to a given sequence number(inclusive)
func (p *PostgresEventStore) ReplayEvents(ctx context.Context, persistenceID string, fromSequenceNumber, toSequenceNumber uint64) ([]*pb.Event, error) {
	//TODO implement me
	panic("implement me")
}

// GetLatestEvent fetches the latest event
func (p *PostgresEventStore) GetLatestEvent(ctx context.Context, persistenceID string) (*pb.Event, error) {
	//TODO implement me
	panic("implement me")
}
