package persistence

// PersistentConfig defines the persistent actor config
type PersistentConfig[T State] struct {
	InitialState   T
	Kind           string
	PersistentID   string
	JournalStore   JournalStore
	SnapshotStore  SnapshotStore
	SnapshotAfter  uint64
	CommandHandler CommandHandler
	EventHandler   EventHandler
	InitHook       InitHook
	ShutdownHook   ShutdownHook
}
