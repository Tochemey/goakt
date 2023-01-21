package memory

import (
	"github.com/hashicorp/go-memdb"
)

// Journal helps create the journal schema
// This matches the RDBMS counter-part.
type Journal struct {
	// PersistenceID is the persistence ID
	PersistenceID string
	// SequenceNumber
	SequenceNumber uint64
	// Specifies whether the journal is deleted
	IsDeleted bool
	// Specifies the event byte array
	EventPayload []byte
	// Specifies the event manifest
	EventManifest string
	// Specifies the state payload
	StatePayload []byte
	// Specifies the state manifest
	StateManifest string
	// Specifies time the record has been persisted
	Timestamp int64
}

const (
	tableName              = "event_journal"
	primaryKey             = "pk"
	isDeletedIndexName     = "deletion"
	persistenceIdIndexName = "persistenceId"
)

var (
	// journalSchema defines the journal schema
	journalSchema = &memdb.DBSchema{
		Tables: map[string]*memdb.TableSchema{
			"event_journal": {
				Name: tableName,
				Indexes: map[string]*memdb.IndexSchema{
					primaryKey: {
						Name:         primaryKey,
						AllowMissing: false,
						Unique:       true,
						Indexer: &memdb.CompoundIndex{
							Indexes: []memdb.Indexer{
								&memdb.StringFieldIndex{
									Field:     "PersistenceID",
									Lowercase: false,
								},
								&memdb.StringFieldIndex{
									Field:     "SequenceNumber",
									Lowercase: false,
								},
							},
							AllowMissing: false,
						},
					},
					isDeletedIndexName: {
						Name:         isDeletedIndexName,
						AllowMissing: false,
						Unique:       false,
						Indexer: &memdb.StringFieldIndex{
							Field:     "IsDeleted",
							Lowercase: false,
						},
					},
					persistenceIdIndexName: {
						Name:         persistenceIdIndexName,
						AllowMissing: false,
						Unique:       false,
						Indexer: &memdb.StringFieldIndex{
							Field:     "PersistenceID",
							Lowercase: false,
						},
					},
				},
			},
		},
	}
)
