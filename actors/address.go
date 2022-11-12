package actors

import (
	"fmt"
	"strings"
)

// Address is the actor address in the actor system
type Address string

const (
	protocol = "goakt"
)

// GetAddress returns a unique address for a given actor in a given actor system.
// kind represents the type of actor and id represents an entity of that type of actor
// example: kind=User, id=john therefore the address is goakt://sys@hostA:9000/user/john
func GetAddress(system ActorSystem, kind, id string) Address {
	// construct the given address string value
	addr := fmt.Sprintf("%s://sys@%s/%s/%s", protocol, system.NodeAddr(), strings.ToLower(kind), id)
	// return the newly created address
	return Address(addr)
}
