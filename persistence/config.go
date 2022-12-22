package persistence

// PersistentConfig defines the persistent actor config
type PersistentConfig struct {
	Kind           string
	PersistentID   string
	JournalStore   JournalStore
	CommandHandler CommandHandler
	EventHandler   EventHandler
	InitHook       InitHook
	ShutdownHook   ShutdownHook
}
