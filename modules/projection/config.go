package projection

import (
	"github.com/tochemey/goakt/log"
	persistence2 "github.com/tochemey/goakt/modules/persistence"
)

// Config defines a projection config
type Config struct {
	// Name specifies the projection Name
	Name string
	// Logger specifies the logger
	Logger log.Logger
	// Handler specifies the projection handler
	Handler Handler
	// JournalStore specifies the journal store for reading events
	JournalStore persistence2.JournalStore
	// OffsetStore specifies the offset store to commit offsets
	OffsetStore persistence2.OffsetStore
	// Specifies the recovery setting
	RecoverySetting *RecoverySetting
}
