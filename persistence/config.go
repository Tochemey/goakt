package persistence

// PersistentConfig defines the persistent actor config
type PersistentConfig[T State] struct {
	InitialState   T
	JournalStore   JournalStore
	SnapshotStore  SnapshotStore
	CommandHandler CommandHandler
	EventHandler   EventHandler
	InitHook       InitHook
	ShutdownHook   ShutdownHook
}
