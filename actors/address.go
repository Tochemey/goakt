package actors

import (
	"bytes"
	"net/url"
	"strconv"

	"github.com/pkg/errors"
)

const (
	protocol = "goakt"
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
func NewAddress(protocol string, system string, host string, port int) *Address {
	return &Address{
		host:     host,
		port:     port,
		system:   system,
		protocol: protocol,
	}
}

// WithHost sets the hosts of a given Address and returns a new instance of the address
func (a *Address) WithHost(host string) *Address {
	return NewAddress(a.Protocol(), a.System(), host, a.Port())
}

// WithPort sets the port of a given Address and returns a new instance of the address
func (a *Address) WithPort(port int) *Address {
	return NewAddress(a.Protocol(), a.System(), a.System(), port)
}

// WithSystem sets the actor system of a given Address and returns a new instance of the address
func (a *Address) WithSystem(system string) *Address {
	return NewAddress(a.Protocol(), system, a.Host(), a.Port())
}

// WithProtocol sets the protocol of a given Address and returns a new instance of the address
func (a *Address) WithProtocol(protocol string) *Address {
	return NewAddress(protocol, a.System(), a.Host(), a.Port())
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

// IsRemote true if this Address is usable globally.
// Unlike locally defined addresses of global scope are safe to sent to other hosts, as they globally and uniquely identify an addressable entity.
func (a *Address) IsRemote() bool {
	return len(a.host) > 0
}

// IsLocal returns true if this Address is only defined locally.
// It is not safe to send locally scoped addresses to remote
func (a *Address) IsLocal() bool {
	return len(a.host) == 0
}

// HostPort returns the host and port in the following string format
// @host:port
func (a *Address) HostPort() string {
	// create a bytes buffer instance
	buf := bytes.NewBuffer(nil)
	// write the host value when it is set
	if len(a.host) > 0 {
		buf.WriteByte('@')
		buf.WriteString(a.host)
	}

	// write the port when the port is set
	if a.port > 0 {
		buf.WriteByte(':')
		buf.WriteString(strconv.Itoa(a.port))
	}

	// return the constructed string
	return buf.String()
}

// String returns the canonical String representation of this Address formatted as:
// `protocol://system@host:port`
func (a *Address) String() string {
	// if the protocol is not goakt
	// then panic
	if a.protocol != protocol {
		panic("invalid protocol")
	}
	// create a bytes buffer instance
	buf := bytes.NewBuffer(nil)
	// write the protocol field to bytes buffer
	buf.WriteString(a.protocol)
	buf.WriteString("://")
	buf.WriteString(a.system)

	// write the host to the bytes buffer when it is defined
	if len(a.host) > 0 {
		buf.WriteByte('@')
		buf.WriteString(a.host)
	}

	// write the port to the bytes buffer when it is set
	if a.port > 0 {
		buf.WriteByte(':')
		buf.WriteString(strconv.Itoa(a.port))
	}

	// returns the constructed string value
	return buf.String()
}

// Parse parses a new Address from a given string
func (a *Address) Parse(address string) *Address {
	// let us parse the address and panic in case of error
	uri, err := url.Parse(address)
	// panic when there is an error
	if err != nil {
		panic(errors.Wrapf(err, "failed to parse the address [%s]", address))
	}

	// check whether the user info is empty or not
	// parse the port
	port, err := strconv.Atoi(uri.Port())
	// handle the error
	if err != nil {
		panic(errors.Wrapf(err, "failed to parse the address [%s]", address))
	}

	if uri.User == nil {
		return NewAddress(uri.Scheme, uri.Hostname(), "", port)
	}

	return NewAddress(uri.Scheme, uri.User.String(), uri.Hostname(), port)
}
