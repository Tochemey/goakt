package actors

import (
	"fmt"

	"github.com/google/uuid"
	pb "github.com/tochemey/goakt/messages/v1"
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
func (p *Path) Parent() *Path {
	return p.parent
}

// WithParent sets the parent actor path and returns a new path
// This function is immutable
func (p *Path) WithParent(parent *Path) *Path {
	newPath := NewPath(p.name, p.address)
	newPath.parent = parent
	return newPath
}

// Address returns the address of the path
func (p *Path) Address() *Address {
	return p.address
}

// Name returns the name of the actor that this path refers to.
func (p *Path) Name() string {
	return p.name
}

// ID returns the internal unique id of the actor that this path refer to.
func (p *Path) ID() uuid.UUID {
	return p.id
}

// String returns the string representation of an actorPath
func (p *Path) String() string {
	return fmt.Sprintf("%s/%s", p.address.String(), p.name)
}

// RemoteAddress returns the remote from path
func (p *Path) RemoteAddress() *pb.Address {
	// only returns a remote address when we are in a remote scope otherwise return nil
	if !p.address.IsRemote() {
		return nil
	}
	return &pb.Address{
		Host: p.address.Host(),
		Port: int32(p.address.Port()),
		Name: p.Name(),
		Id:   p.ID().String(),
	}
}
