package memory

import "github.com/hashicorp/go-memdb"

// offsetRow represent the offset entry in the offset store
type offsetRow struct {
	// Ordering basically used as PK
	Ordering string
	// ProjectionName is the projection name
	ProjectionName string
	// PersistenceID is the persistence ID
	PersistenceID string
	// CurrentOffset is the current offset
	CurrentOffset uint64
	// Specifies the last update time
	LastUpdated int64
}

const (
	offsetTableName     = "offsets"
	offsetPK            = "id"
	currentOffsetIndex  = "currentOffset"
	projectionNameIndex = "projectionName"
	rowIndex            = "rowIndex"
)

var (
	// offsetSchema defines the offset schema
	offsetSchema = &memdb.DBSchema{
		Tables: map[string]*memdb.TableSchema{
			offsetTableName: {
				Name: offsetTableName,
				Indexes: map[string]*memdb.IndexSchema{
					offsetPK: {
						Name:         offsetPK,
						AllowMissing: false,
						Unique:       true,
						Indexer: &memdb.StringFieldIndex{
							Field:     "Ordering",
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
					currentOffsetIndex: {
						Name:         currentOffsetIndex,
						AllowMissing: false,
						Unique:       false,
						Indexer: &memdb.UintFieldIndex{
							Field: "CurrentOffset",
						},
					},
					projectionNameIndex: {
						Name:         projectionNameIndex,
						AllowMissing: false,
						Unique:       false,
						Indexer: &memdb.StringFieldIndex{
							Field: "ProjectionName",
						},
					},
					rowIndex: {
						Name:         rowIndex,
						AllowMissing: false,
						Unique:       true,
						Indexer: &memdb.CompoundIndex{
							Indexes: []memdb.Indexer{
								&memdb.StringFieldIndex{
									Field:     "ProjectionName",
									Lowercase: false,
								},
								&memdb.StringFieldIndex{
									Field:     "PersistenceID",
									Lowercase: false,
								},
							},
							AllowMissing: false,
						},
					},
				},
			},
		},
	}
)
