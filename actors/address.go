package actors

import (
	"bytes"
	"strconv"

	"github.com/pkg/errors"
)

const (
	protocol = "goakt"
)

var (
	ErrLocalAddress = errors.New("address is a local address")
)

// Address represents the physical location under which an Actor can be
// reached. Examples are local addresses, identified by the ActorSystem’s name,
// and remote addresses, identified by protocol, host and port.
type Address struct {
	// host is the host address
	host string
	// port is the port number
	port int
	// system is the actor system name
	system string
	// protocol is the protocol
	protocol string
}

// NewAddress creates an instance of Address
func NewAddress(system string, host string, port int) *Address {
	return &Address{
		host:     host,
		port:     port,
		system:   system,
		protocol: protocol,
	}
}

// WithHost sets the hosts of a given Address and returns a new instance of the address
func (a *Address) WithHost(host string) (*Address, error) {
	// only allow this operation if the given address is a remote one
	if a.IsLocal() {
		return nil, ErrLocalAddress
	}
	return NewAddress(a.System(), host, a.Port()), nil
}

// WithPort sets the port of a given Address and returns a new instance of the address
func (a *Address) WithPort(port int) (*Address, error) {
	// only allow this operation if the given address is a remote one
	if a.IsLocal() {
		return nil, ErrLocalAddress
	}
	return NewAddress(a.System(), a.Host(), port), nil
}

// WithSystem sets the actor system of a given Address and returns a new instance of the address
func (a *Address) WithSystem(system string) *Address {
	return NewAddress(system, a.Host(), a.Port())
}

// Host returns the host
func (a *Address) Host() string {
	return a.host
}

// Port returns the port number
func (a *Address) Port() int {
	return a.port
}

// System returns the actor system name
func (a *Address) System() string {
	return a.system
}

// Protocol returns the protocol
func (a *Address) Protocol() string {
	return a.protocol
}

// HostPort returns the host and port in the following string format
// @host:port
func (a *Address) HostPort() string {
	// create a bytes buffer instance
	buf := bytes.NewBuffer(nil)
	// write the host and port value when it is remote
	if a.IsRemote() {
		buf.WriteString(a.host)
		buf.WriteByte(':')
		buf.WriteString(strconv.Itoa(a.port))
	}

	// return the constructed string
	return buf.String()
}

// String returns the canonical String representation of this Address formatted as:
// `protocol://system@host:port`
func (a *Address) String() string {
	// create a bytes buffer instance
	buf := bytes.NewBuffer(nil)
	// write the protocol field to bytes buffer
	buf.WriteString(a.protocol)
	buf.WriteString("://")
	buf.WriteString(a.system)
	buf.WriteByte('@')

	// write the host and port to the bytes buffer when it is remote
	if a.IsRemote() {
		buf.WriteString(a.host)
		buf.WriteByte(':')
		buf.WriteString(strconv.Itoa(a.port))
	}

	// returns the constructed string value
	return buf.String()
}

// IsLocal helps set the actor address locally
func (a *Address) IsLocal() bool {
	return len(a.host) == 0
}

// IsRemote states whether the actor address is in the remote environment
// This happens when remoting is enabled
func (a *Address) IsRemote() bool {
	return len(a.host) > 0 && a.port > 0
}
