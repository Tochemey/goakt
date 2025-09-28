/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

// Package address provides the canonical representation and utilities for
// addressing actors in a Go-Akt actor system.
//
// An address identifies a single actor and is made of the following parts:
//
//   - System: logical name of the actor system
//   - Host: network host or IP where the actor system is reachable
//   - Port: TCP port where the actor system is reachable
//   - Name: local, hierarchical name of the actor within the system
//   - ID: unique, opaque identifier of the actor instance (UUIDv4)
//   - Parent: the parent actor's address (if any)
//
// The canonical textual representation of an Address is:
//
//	goakt://<system>@<host>:<port>/<name>
//
// Unless stated otherwise, methods on Address mutate the receiver and are not
// safe for concurrent use without external synchronization.
package address

import (
	"bytes"
	"encoding"
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/validation"
)

// protocol defines the Go-Akt addressing protocol
const protocol = "goakt"

// zeroAddress means that there is no sender
var zeroAddress = new(goaktpb.Address)

// Address represents the address of an actor in a Go-Akt actor system.
//
// Address is a thin wrapper around the protobuf type goaktpb.Address that adds
// convenience methods, validation, binary marshaling, and a canonical string
// representation.
//
// Fields:
//   - System: logical actor system name (non-empty, pattern-validated)
//   - Host: hostname or IP where the system can be reached (validated as TCP address)
//   - Port: TCP port where the system can be reached (validated as TCP address)
//   - Name: actor name within the system (non-empty, <= 255 chars, pattern-validated)
//   - ID: a unique identifier for the actor instance (UUID string)
//   - Parent: the parent actor's Address, or a zero address if none
//
// Zero Value and No Sender:
// A special "no sender" value can be produced via NoSender, which wraps a zero
// protobuf Address. Validation treats the zero address as valid to allow it to be
// used as a sentinel in message envelopes and internal signaling.
type Address struct {
	*goaktpb.Address
}

var _ validation.Validator = (*Address)(nil)
var _ encoding.BinaryMarshaler = (*Address)(nil)
var _ encoding.BinaryUnmarshaler = (*Address)(nil)

// New creates a new Address with the given attributes.
//
// New generates a new unique ID for the address and sets the Parent to NoSender
// (i.e., a zero/empty parent). New does not validate the inputs; call Validate
// to verify the resulting address.
//
// Parameters:
//   - name: actor name within the system
//   - system: logical actor system name
//   - host: hostname or IP of the node
//   - port: TCP port of the node
//
// Example canonical form of the returned address:
//
//	goakt://system@127.0.0.1:9000/actorName
func New(name, system string, host string, port int) *Address {
	return &Address{
		&goaktpb.Address{
			Host:   host,
			Port:   int32(port),
			Name:   name,
			Id:     uuid.NewString(),
			System: system,
			Parent: zeroAddress,
		},
	}
}

// NewWithParent creates a new Address and optionally sets its parent.
//
// It behaves like New(name, system, host, port) and then, if parent is non-nil,
// assigns the parent's underlying protobuf message to the Parent field. When
// parent is nil, the Parent is set to NoSender. A fresh opaque ID is generated
// for the new Address.
//
// This constructor does not validate inputs; call Validate on the returned
// Address. In particular, when a parent is set, Validate ensures that:
//   - the parent belongs to the same actor system (case-insensitive),
//   - the parent has the same host and port,
//   - the parent name differs from the child name,
//
// and that all other invariants (system/name syntax, TCP address) hold.
//
// Note that the Parent field is not deep-copied. The underlying protobuf
// message is shared with the provided parent; mutating one will reflect in the
// other where they reference the same message.
//
// Example:
//
//	parent := New("parent", "orders", "127.0.0.1", 9000)
//	child := NewWithParent("child", "orders", "127.0.0.1", 9000, parent)
//	if err := child.Validate(); err != nil {
//	  // handle invalid address
//	}
func NewWithParent(name, system, host string, port int, parent *Address) *Address {
	addr := New(name, system, host, port)
	if parent != nil {
		addr.Address.Parent = parent.Address
	}
	return addr
}

// From wraps an existing protobuf Address.
//
// From does not copy the provided protobuf message. Modifying the returned
// Address will mutate the provided protobuf instance as well. Callers should
// ensure addr is non-nil when using methods that dereference it.
//
// Use Validate to verify that the wrapped address is well-formed.
func From(addr *goaktpb.Address) *Address {
	return &Address{
		addr,
	}
}

// NoSender returns a sentinel Address that represents the absence of a sender.
//
// This is commonly used in message envelopes to indicate that a message has no
// originating actor. Validation treats this value as valid.
func NoSender() *Address {
	return &Address{
		zeroAddress,
	}
}

// Parent returns the parent Address.
//
// If no parent was set, this returns an Address that wraps the zero protobuf
// Address (equivalent to NoSender). The returned Address shares the underlying
// protobuf message with the receiver's Parent field.
func (a *Address) Parent() *Address {
	return &Address{a.GetParent()}
}

// Name returns the actor name component of the Address.
func (a *Address) Name() string {
	return a.GetName()
}

// Host returns the host component of the Address.
func (a *Address) Host() string {
	return a.GetHost()
}

// Port returns the port component of the Address.
func (a *Address) Port() int {
	return int(a.GetPort())
}

// System returns the actor system name component of the Address.
func (a *Address) System() string {
	return a.GetSystem()
}

// ID returns the opaque unique identifier of the actor instance.
//
// IDs are generated by New as UUIDv4 strings. Equality checks (Equals) compare
// the entire underlying protobuf message, including this ID.
func (a *Address) ID() string {
	return a.GetId()
}

// String returns the canonical string representation of the Address.
//
// The format is:
//
//	goakt://<system>@<host>:<port>/<kind>::<name>
//
// Example:
//
//	goakt://orders@10.0.0.12:9000/orderActor::checkout
//
// Note: The output does not include the Parent or ID fields.
func (a *Address) String() string {
	buf := bytes.NewBuffer(nil)
	_, _ = buf.WriteString(protocol)
	_, _ = buf.WriteString("://")
	_, _ = buf.WriteString(a.GetSystem())
	_ = buf.WriteByte('@')

	// write the host and port to the bytes buffer
	_, _ = buf.WriteString(a.GetHost())
	_ = buf.WriteByte(':')
	_, _ = buf.WriteString(strconv.Itoa(int(a.GetPort())))
	_, _ = buf.WriteString("/")
	_, _ = buf.WriteString(a.GetName())

	// returns the constructed string value
	return buf.String()
}

// HostPort returns the "host:port" portion of the Address.
//
// This is suitable for dialing or logging the network endpoint. It does not
// include the protocol, system, or name components.
func (a *Address) HostPort() string {
	// create a bytes buffer instance
	buf := bytes.NewBuffer(nil)
	// write the host and port value
	_, _ = buf.WriteString(a.GetHost())
	_ = buf.WriteByte(':')
	_, _ = buf.WriteString(strconv.Itoa(int(a.GetPort())))
	// return the constructed string
	return buf.String()
}

// Equals reports whether x and a represent the same address.
//
// Equals performs a deep, field-by-field comparison of the underlying protobuf
// messages using proto.Equal. It returns false if either receiver or argument
// is nil.
//
// This comparison includes System, Host, Port, Name, ID, and Parent.
func (a *Address) Equals(x *Address) bool {
	if a == nil || x == nil {
		return false
	}
	return proto.Equal(a.Address, x.Address)
}

// MarshalBinary encodes the Address to a binary form.
//
// The encoding is the protobuf wire format of the underlying goaktpb.Address.
// MarshalBinary implements encoding.BinaryMarshaler. The returned bytes are
// suitable for persistence or transmission.
//
// Note: Wire compatibility follows the protobuf schema guarantees; additions of
// new fields are backward compatible, but field renames/removals are not.
func (a *Address) MarshalBinary() (data []byte, err error) {
	return proto.Marshal(a)
}

// UnmarshalBinary decodes the Address from its binary form.
//
// The input must be the protobuf wire format produced by MarshalBinary.
// UnmarshalBinary replaces the receiver's underlying protobuf message with the
// decoded value. It copies from the provided data and does not retain the byte
// slice after returning.
//
// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (a *Address) UnmarshalBinary(data []byte) error {
	pb := new(goaktpb.Address)
	if err := proto.Unmarshal(data, pb); err != nil {
		return err
	}
	a.Address = pb
	return nil
}

// Validate checks whether the Address is well-formed.
//
// Validation rules:
//   - The zero address (NoSender) is considered valid.
//   - Host:Port must form a valid TCP address (via net.JoinHostPort).
//   - System must be non-empty and match pattern: ^[a-zA-Z0-9][a-zA-Z0-9-_.]*$
//     (starts with alphanumeric; may contain alphanumerics, '-', '_' or '.')
//   - Name must be non-empty, <= 255 characters, and match the same pattern.
//   - The underlying protobuf message must be non-nil.
//
// Validate returns an error on the first failure (fail-fast). The exact error
// type and message come from the internal validation package.
func (a *Address) Validate() error {
	if proto.Equal(a.Address, zeroAddress) {
		return nil
	}
	pattern := "^[a-zA-Z0-9][a-zA-Z0-9-_\\.]*$"
	customErr := errors.New("must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')")
	verr := validation.
		New(validation.FailFast()).
		AddValidator(validation.NewTCPAddressValidator(net.JoinHostPort(a.GetHost(), strconv.Itoa(int(a.GetPort()))))).
		AddValidator(validation.NewEmptyStringValidator("system", a.GetSystem())).
		AddValidator(validation.NewEmptyStringValidator("name", a.GetName())).
		AddAssertion(len(a.GetName()) <= 255, "actor name is too long. Maximum length is 255").
		AddValidator(validation.NewPatternValidator(pattern, a.GetSystem(), customErr)).
		AddValidator(validation.NewPatternValidator(pattern, strings.TrimSpace(a.GetName()), customErr)).
		AddAssertion(a.Address != nil, "address is required").
		Validate()

	if a.Parent() != nil && !a.Parent().Equals(NoSender()) {
		if err := a.Parent().Validate(); err != nil {
			return errors.Join(verr, ErrInvalidParent, err)
		}

		if !strings.EqualFold(a.Parent().System(), a.System()) {
			return errors.Join(verr, ErrInvalidActorSystem)
		}

		if a.Parent().Host() != a.Host() || a.Parent().Port() != a.Port() {
			return errors.Join(verr, ErrInvalidHostAddress)
		}

		if a.Parent().Name() == a.Name() {
			return errors.Join(verr, ErrInvalidName)
		}
	}

	return verr
}

// Parse parses a canonical address string into an Address.
//
// The expected format is:
//
//	goakt://<system>@<host>:<port>/<name>
//
// Parse validates only the syntactic structure of the input and converts the
// port to an integer; it does not perform network resolution. The returned
// Address is constructed via New and will have a freshly generated ID.
//
// Limitations:
//   - IPv6 host literals are not supported by this parser because it splits on
//     ':' (e.g., [::1]:9000 will not parse). Use an IPv4 address or hostname.
//
// Errors are returned for empty inputs, unsupported protocol, or malformed
// segments.
func Parse(addr string) (*Address, error) {
	if addr == "" {
		return nil, errors.New("address is required")
	}
	parts := strings.Split(addr, "://")
	if len(parts) != 2 {
		return nil, errors.New("address format is invalid")
	}

	if parts[0] != protocol {
		return nil, errors.New("address protocol is not supported")
	}

	addr = parts[1]
	addrParts := strings.Split(addr, "@")
	if len(addrParts) != 2 {
		return nil, errors.New("address format is invalid")
	}
	system := addrParts[0]
	addr = addrParts[1]
	hostPortParts := strings.Split(addr, "/")
	if len(hostPortParts) != 2 {
		return nil, errors.New("address format is invalid")
	}
	hostPort := hostPortParts[0]
	name := hostPortParts[1]
	hostPortParts = strings.Split(hostPort, ":")
	if len(hostPortParts) != 2 {
		return nil, errors.New("address format is invalid")
	}
	host := hostPortParts[0]
	portStr := hostPortParts[1]
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, err
	}
	return New(name, system, host, port), nil
}
