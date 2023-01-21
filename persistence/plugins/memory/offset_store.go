package memory

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hashicorp/go-memdb"
	"github.com/pkg/errors"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/persistence"
	"github.com/tochemey/goakt/telemetry"
)

// OffsetStore implements the offset store interface
// NOTE: NOT RECOMMENDED FOR PRODUCTION CODE because all records are in memory and there is no durability.
// This is recommended for tests or PoC
type OffsetStore struct {
	// specifies the underlying database
	db *memdb.MemDB
	// this is only useful for tests
	keepRecordsAfterDisconnect bool
}

var _ persistence.OffsetStore = &OffsetStore{}

// NewOffsetStore creates an instance of OffsetStore
func NewOffsetStore() *OffsetStore {
	return &OffsetStore{
		keepRecordsAfterDisconnect: false,
	}
}

// Connect connects to the offset store
func (s *OffsetStore) Connect(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "OffsetStore.Connect")
	defer span.End()

	// create an instance of the database
	db, err := memdb.NewMemDB(offsetSchema)
	// handle the eventual error
	if err != nil {
		return err
	}
	// set the journal store underlying database
	s.db = db

	return nil
}

// Disconnect disconnects the offset store
func (s *OffsetStore) Disconnect(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "OffsetStore.Disconnect")
	defer span.End()

	// clear all records
	if !s.keepRecordsAfterDisconnect {
		// spawn a db transaction for read-only
		txn := s.db.Txn(true)

		// free memory resource
		if _, err := txn.DeleteAll(offsetTableName, offsetPK); err != nil {
			txn.Abort()
			return errors.Wrap(err, "failed to free memory resource")
		}
		txn.Commit()
	}
	return nil
}

// WriteOffset writes an offset to the offset store
func (s *OffsetStore) WriteOffset(ctx context.Context, offset *pb.Offset) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "OffsetStore.WriteOffset")
	defer span.End()

	// spawn a db transaction
	txn := s.db.Txn(true)
	// create an offset row
	record := &offsetRow{
		Ordering:       uuid.NewString(),
		ProjectionName: offset.GetProjectionName(),
		PersistenceID:  offset.GetPersistenceId(),
		CurrentOffset:  offset.GetCurrentOffset(),
		LastUpdated:    offset.GetTimestamp(),
	}

	// persist the record
	if err := txn.Insert(offsetTableName, record); err != nil {
		// abort the transaction
		txn.Abort()
		// return the error
		return errors.Wrap(err, "failed to persist offset record on to the offset store")
	}
	// commit the transaction
	txn.Commit()

	return nil
}

// GetCurrentOffset return the offset of a projection
func (s *OffsetStore) GetCurrentOffset(ctx context.Context, projectionID *persistence.ProjectionID) (current *pb.Offset, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "OffsetStore.GetCurrentOffset")
	defer span.End()

	// spawn a db transaction for read-only
	txn := s.db.Txn(false)
	defer txn.Abort()
	// let us fetch the last record
	raw, err := txn.Last(offsetTableName, rowIndex, projectionID.ProjectionName(), projectionID.PersistenceID())
	if err != nil {
		// if the error is not found then return nil
		if err == memdb.ErrNotFound {
			return nil, nil
		}
		return nil, errors.Wrapf(err, "failed to get the current offset for persistenceId=%s given projection=%s", projectionID.PersistenceID(), projectionID.ProjectionName())
	}

	// no record found
	if raw == nil {
		return nil, nil
	}

	// cast the record
	if offsetRow, ok := raw.(*offsetRow); ok {
		current = &pb.Offset{
			PersistenceId:  offsetRow.PersistenceID,
			ProjectionName: offsetRow.ProjectionName,
			CurrentOffset:  offsetRow.CurrentOffset,
			Timestamp:      offsetRow.LastUpdated,
		}
		return
	}

	return nil, fmt.Errorf("failed to get the current offset for persistenceId=%s given projection=%s", projectionID.PersistenceID(), projectionID.ProjectionName())
}
