package actors

import (
	"fmt"

	"github.com/google/uuid"
)

// Path is a unique path to an actor
type Path struct {
	// specifies the Address under which this path can be reached
	address *Address
	// specifies the name of the actor that this path refers to.
	name string
	// specifies the internal unique id of the actor that this path refer to.
	id uuid.UUID
	// specifies the path for the parent actor.
	parent *Path
}

// NewPath creates an immutable Path
func NewPath(name string, address *Address) *Path {
	// create the instance and return it
	return &Path{
		address: address,
		name:    name,
		id:      uuid.New(),
	}
}

// Parent returns the parent path
func (a *Path) Parent() *Path {
	return a.parent
}

// WithParent sets the parent actor path and returns a new path
// This function is immutable
func (a *Path) WithParent(parent *Path) *Path {
	newPath := NewPath(a.name, a.address)
	newPath.parent = parent
	return newPath
}

// Address return the actor path address
func (a *Path) Address() *Address {
	return a.address
}

// Name returns the name of the actor that this path refers to.
func (a *Path) Name() string {
	return a.name
}

// ID returns the internal unique id of the actor that this path refer to.
func (a *Path) ID() uuid.UUID {
	return a.id
}

// String returns the string representation of an actorPath
func (a *Path) String() string {
	return fmt.Sprintf("%s/%s", a.address.String(), a.name)
}
