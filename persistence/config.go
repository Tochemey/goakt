package persistence

// PersistentConfig defines the persistent actor config
type PersistentConfig[T State] struct {
	Kind           string
	PersistentID   string
	InitialState   T
	JournalStore   JournalStore
	CommandHandler CommandHandler[T]
	EventHandler   EventHandler[T]
	InitHook       InitHook
	ShutdownHook   ShutdownHook
}
