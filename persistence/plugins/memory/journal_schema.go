package memory

import (
	"github.com/hashicorp/go-memdb"
)

// journal represents the journal entry
// This matches the RDBMS counter-part.
type journal struct {
	// Ordering basically used as PK
	Ordering string
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
	journalTableName    = "event_journal"
	journalPK           = "id"
	isDeletedIndex      = "deletion"
	persistenceIDIndex  = "persistenceId"
	sequenceNumberIndex = "sequenceNumber"
)

var (
	// journalSchema defines the journal schema
	journalSchema = &memdb.DBSchema{
		Tables: map[string]*memdb.TableSchema{
			journalTableName: {
				Name: journalTableName,
				Indexes: map[string]*memdb.IndexSchema{
					journalPK: {
						Name:         journalPK,
						AllowMissing: false,
						Unique:       true,
						Indexer: &memdb.StringFieldIndex{
							Field:     "Ordering",
							Lowercase: false,
						},
					},
					isDeletedIndex: {
						Name:         isDeletedIndex,
						AllowMissing: false,
						Unique:       false,
						Indexer: &memdb.StringFieldIndex{
							Field:     "IsDeleted",
							Lowercase: false,
						},
					},
					persistenceIDIndex: {
						Name:         persistenceIDIndex,
						AllowMissing: false,
						Unique:       false,
						Indexer: &memdb.StringFieldIndex{
							Field:     "PersistenceID",
							Lowercase: false,
						},
					},
					sequenceNumberIndex: {
						Name:         sequenceNumberIndex,
						AllowMissing: false,
						Unique:       false,
						Indexer: &memdb.UintFieldIndex{
							Field: "SequenceNumber",
						},
					},
				},
			},
		},
	}
)
