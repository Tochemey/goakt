package persistence

// ProjectionID is composed by  the projection name and a given persistence ID
type ProjectionID struct {
	// the projection projectionName. This must be unique within an actor system
	// the same name is used in all projection IDs for a given a projection
	projectionName string
	// specifies the actual persistence ID
	// TODO decide whether to use the interface instead of the real entity ID
	persistenceID string
}

// NewProjectionID creates an instance of the ProjectionID given the name and a persistence ID
func NewProjectionID(name string, persistenceID string) *ProjectionID {
	return &ProjectionID{
		projectionName: name,
		persistenceID:  persistenceID,
	}
}

// ProjectionName returns the projection name of a ProjectionID
// The projection name is shared across multiple instances of ProjectionID with different persistence ID
func (x ProjectionID) ProjectionName() string {
	return x.projectionName
}

// PersistenceID returns the persistence ID of a ProjectionID
func (x ProjectionID) PersistenceID() string {
	return x.persistenceID
}
