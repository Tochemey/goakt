/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package address

import (
	"bytes"
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/validation"
	"github.com/tochemey/goakt/v2/secureconn"
)

// protocol defines the Go-Akt addressing protocol
const protocol = "goakt"

// NoSender means that there is no sender
var NoSender = new(goaktpb.Address)

type Address struct {
	*goaktpb.Address
	secureConn *secureconn.SecureConn
}

var _ validation.Validator = (*Address)(nil)

// New creates an instance of Address
func New(name, system string, host string, port int) *Address {
	return &Address{
		Address: &goaktpb.Address{
			Host:   host,
			Port:   int32(port),
			Name:   name,
			Id:     uuid.NewString(),
			System: system,
			Parent: NoSender,
		},
	}
}

// From creates an instance of Address provided the serializable address instance
func From(addr *goaktpb.Address) *Address {
	return &Address{
		Address: addr,
	}
}

// Default creates an instance of Address with default values.
func Default() *Address {
	return &Address{
		Address: NoSender,
	}
}

// WithName sets the name of a given Address and returns the instance of the address
func (a *Address) WithName(name string) *Address {
	a.Address.Name = name
	return a
}

// WithHost sets the hosts of a given Address and returns the instance of the address
func (a *Address) WithHost(host string) *Address {
	a.Address.Host = host
	return a
}

// WithPort sets the port of a given Address and returns the instance of the address
func (a *Address) WithPort(port int) *Address {
	a.Address.Port = int32(port)
	return a
}

// WithSystem sets the actor system of a given Address and returns the instance of the address
func (a *Address) WithSystem(system string) *Address {
	a.Address.System = system
	return a
}

// WithParent sets the parent of a given Address and returns the instance of the address
func (a *Address) WithParent(parent *Address) *Address {
	a.Address.Parent = parent.Address
	return a
}

// WithSecureConn sets the address with some secured connection settings
func (a *Address) WithSecureConn(conn *secureconn.SecureConn) *Address {
	a.secureConn = conn
	return a
}

// Parent returns the parent path
func (a *Address) Parent() *Address {
	return &Address{Address: a.GetParent(), secureConn: a.secureConn}
}

// Name returns the name
func (a *Address) Name() string {
	return a.GetName()
}

// Host returns the host
func (a *Address) Host() string {
	return a.GetHost()
}

// Port returns the port number
func (a *Address) Port() int {
	return int(a.GetPort())
}

// System returns the actor system name
func (a *Address) System() string {
	return a.GetSystem()
}

// ID returns actor ID
func (a *Address) ID() string {
	return a.GetId()
}

// String returns the canonical String representation of this Address formatted as:
// `protocol://system@host:port/name`
func (a *Address) String() string {
	buf := bytes.NewBuffer(nil)
	buf.WriteString(protocol)
	buf.WriteString("://")
	buf.WriteString(a.GetSystem())
	buf.WriteByte('@')

	// write the host and port to the bytes buffer
	buf.WriteString(a.GetHost())
	buf.WriteByte(':')
	buf.WriteString(strconv.Itoa(int(a.GetPort())))
	buf.WriteString("/")
	buf.WriteString(a.GetName())

	// returns the constructed string value
	return buf.String()
}

// HostPort returns the host and port in the following string format
// @host:port
func (a *Address) HostPort() string {
	// create a bytes buffer instance
	buf := bytes.NewBuffer(nil)
	// write the host and port value
	buf.WriteString(a.GetHost())
	buf.WriteByte(':')
	buf.WriteString(strconv.Itoa(int(a.GetPort())))
	// return the constructed string
	return buf.String()
}

// Equals is used to compare two addresses
func (a *Address) Equals(x *Address) bool {
	return a.GetId() == x.GetId() && a.String() == x.String()
}

// MarshalBinary encodes the receiver into a binary form and returns the result.
func (a *Address) MarshalBinary() (data []byte, err error) {
	return proto.Marshal(a)
}

// UnmarshalBinary must be able to decode the form generated by MarshalBinary.
// UnmarshalBinary must copy the data if it wishes to retain the data after returning.
func (a *Address) UnmarshalBinary(data []byte) error {
	pb := new(goaktpb.Address)
	if err := proto.Unmarshal(data, pb); err != nil {
		return err
	}
	a.Address = pb
	return nil
}

// Validate returns an error when the address is not valid
func (a *Address) Validate() error {
	if proto.Equal(a.Address, NoSender) {
		return nil
	}
	pattern := "^[a-zA-Z0-9][a-zA-Z0-9-_\\.]*$"
	customErr := errors.New("must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')")
	return validation.
		New(validation.FailFast()).
		AddValidator(validation.NewTCPAddressValidator(net.JoinHostPort(a.GetHost(), strconv.Itoa(int(a.GetPort()))))).
		AddValidator(validation.NewEmptyStringValidator("system", a.GetSystem())).
		AddValidator(validation.NewEmptyStringValidator("name", a.GetName())).
		AddValidator(validation.NewPatternValidator(pattern, a.GetSystem(), customErr)).
		AddValidator(validation.NewPatternValidator(pattern, strings.TrimSpace(a.GetName()), customErr)).
		AddAssertion(a.Address != nil, "address is required").
		Validate()
}

// IsSecured returns true when the given address is secured or not
func (a *Address) IsSecured() bool {
	return a.secureConn != nil
}

// SecureConn returns the secureConn instance
func (a *Address) SecureConn() *secureconn.SecureConn {
	return a.secureConn
}
