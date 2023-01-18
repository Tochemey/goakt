package persistence

// PersistentID implementation specifies a persistent actor unique identifier
// Each implementation needs to define what is the kind of the persistent actor
// and the actual id of the given persistent actor
type PersistentID interface {
	// Kind defines the kind of actor it is. This in combination with the ID uniquely identifies the
	// event sourced actor. For instance Users, Accounts can all be used as kind.
	Kind() string
	// ID defines the id that will be used in the event journal.
	// This helps track the event sourced actor in the events store.
	ID() string
}
