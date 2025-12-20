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
// When a parent is defined, the representation becomes:
//
//	goakt://<system>@<host>:<port>/<parent>/<name>
//
// Unless stated otherwise, methods on Address mutate the receiver and are not
// safe for concurrent use without external synchronization.
package address

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/tochemey/goakt/v3/internal/validation"
)

// scheme defines the Go-Akt addressing scheme
const scheme = "goakt"

// zeroAddress means that there is no sender
var zeroAddress = &Address{}

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
	host   string
	port   int
	name   string
	system string
	parent *Address
}

var _ validation.Validator = (*Address)(nil)

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
		host:   host,
		port:   port,
		name:   name,
		system: system,
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
		addr.parent = parent
	}
	return addr
}

// NoSender returns a sentinel Address that represents the absence of a sender.
//
// This is commonly used in message envelopes to indicate that a message has no
// originating actor. Validation treats this value as valid.
func NoSender() *Address {
	return zeroAddress
}

// Parent returns the parent Address.
//
// If no parent was set, this returns an Address that wraps the zero protobuf
// Address (equivalent to NoSender). The returned Address shares the underlying
// protobuf message with the receiver's Parent field.
func (x *Address) Parent() *Address {
	if x == nil {
		return nil
	}
	return x.parent
}

// Name returns the actor name component of the Address.
func (x *Address) Name() string {
	if x == nil {
		return ""
	}
	return x.name
}

// Host returns the host component of the Address.
func (x *Address) Host() string {
	if x == nil {
		return ""
	}
	return x.host
}

// Port returns the port component of the Address.
func (x *Address) Port() int {
	if x == nil {
		return 0
	}
	return x.port
}

// System returns the actor system name component of the Address.
func (x *Address) System() string {
	if x == nil {
		return ""
	}
	return x.system
}

// String returns the canonical, deterministic textual form of the Address.
//
// Format:
//
//	goakt://<system>@<host>:<port>/<name>
//
// If a parent is set and is not NoSender:
//
//	goakt://<system>@<host>:<port>/<parent>/<name>
//
// Behavior:
//   - No validation or escaping is performed; call Validate first.
//   - It is safe to call on a nil receiver; empty components render as: goakt://@:0/
//   - Host and name are emitted as provided; normalization is not applied.
//
// Examples:
//
//	addr := New("checkoutActor", "orders", "10.0.0.12", 9000)
//	addr.String() // "goakt://orders@10.0.0.12:9000/checkoutActor"
//
//	parent := New("root", "orders", "10.0.0.12", 9000)
//	child := NewWithParent("checkoutActor", "orders", "10.0.0.12", 9000, parent)
//	child.String() // "goakt://orders@10.0.0.12:9000/root/checkoutActor"
func (x *Address) String() string {
	if x == nil {
		return ""
	}

	system := x.System()
	host := x.Host()
	name := x.Name()
	parentName := ""
	if parent := x.Parent(); parent != nil && !parent.Equals(NoSender()) {
		parentName = parent.Name()
	}

	var portBuf [6]byte
	portBytes := strconv.AppendInt(portBuf[:0], int64(x.Port()), 10)

	pathLen := 1 + len(name)
	if parentName != "" {
		pathLen += len(parentName) + 1
	}
	totalLen := len(scheme) + len("://") + len(system) + 1 + len(host) + 1 + len(portBytes) + pathLen
	var builder strings.Builder
	builder.Grow(totalLen)

	_, _ = builder.WriteString(scheme)
	_, _ = builder.WriteString("://")
	_, _ = builder.WriteString(system)
	_ = builder.WriteByte('@')
	_, _ = builder.WriteString(host)
	_ = builder.WriteByte(':')
	_, _ = builder.Write(portBytes)
	_ = builder.WriteByte('/')
	if parentName != "" {
		_, _ = builder.WriteString(parentName)
		_ = builder.WriteByte('/')
	}
	_, _ = builder.WriteString(name)

	return builder.String()
}

// HostPort returns the "host:port" portion of the Address.
//
// This is suitable for dialing or logging the network endpoint. It does not
// include the protocol, system, or name components.
func (x *Address) HostPort() string {
	host := x.Host()
	var portBuf [6]byte
	portBytes := strconv.AppendInt(portBuf[:0], int64(x.Port()), 10)

	var builder strings.Builder
	builder.Grow(len(host) + 1 + len(portBytes))

	_, _ = builder.WriteString(host)
	_ = builder.WriteByte(':')
	_, _ = builder.Write(portBytes)

	return builder.String()
}

// Equals reports whether x and a represent the same address.
//
// Equals performs a deep, field-by-field comparison of the underlying protobuf
// messages using proto.Equal. It returns false if either receiver or argument
// is nil.
//
// This comparison includes System, Host, Port, Name, ID, and Parent.
func (x *Address) Equals(y *Address) bool {
	if x == nil || y == nil {
		return false
	}
	return x.Name() == y.Name() &&
		x.System() == y.System() &&
		x.Host() == y.Host() &&
		x.Port() == y.Port()
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
func (x *Address) Validate() error {
	if x == nil || x.Equals(NoSender()) {
		return nil
	}
	pattern := "^[a-zA-Z0-9][a-zA-Z0-9-_\\.]*$"
	customErr := errors.New("must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')")
	verr := validation.
		New(validation.FailFast()).
		AddValidator(validation.NewTCPAddressValidator(net.JoinHostPort(x.Host(), strconv.Itoa(x.Port())))).
		AddValidator(validation.NewEmptyStringValidator("system", x.System())).
		AddValidator(validation.NewEmptyStringValidator("name", x.Name())).
		AddAssertion(len(x.Name()) <= 255, "actor name is too long. Maximum length is 255").
		AddValidator(validation.NewPatternValidator(pattern, x.System(), customErr)).
		AddValidator(validation.NewPatternValidator(pattern, strings.TrimSpace(x.Name()), customErr)).
		Validate()

	if x.Parent() != nil && !x.Parent().Equals(NoSender()) {
		if err := x.Parent().Validate(); err != nil {
			return errors.Join(verr, ErrInvalidParent, err)
		}

		if !strings.EqualFold(x.Parent().System(), x.System()) {
			return errors.Join(verr, ErrInvalidActorSystem)
		}

		if x.Parent().Host() != x.Host() || x.Parent().Port() != x.Port() {
			return errors.Join(verr, ErrInvalidHostAddress)
		}

		if x.Parent().Name() == x.Name() {
			return errors.Join(verr, ErrInvalidName)
		}
	}

	return verr
}

// Parse parses a canonical address string into an Address.
//
// Accepted formats:
//
//	goakt://<system>@<host>:<port>/<name>
//	goakt://<system>@<host>:<port>/<parent>/<name>
//
// Notes:
//   - Only the "goakt" scheme is accepted (case-sensitive).
//   - Port must be a base-10 integer.
//   - Path may contain at most one '/' (to separate <parent>/<name>).
//   - Raw IPv6 literals are not supported by this parser (use a hostname).
//   - No semantic validation is performed; call Validate on the result.
//   - If no parent component is present, the returned Address has no parent (nil).
//
// Errors:
//   - "address is required" when input is empty.
//   - "address protocol is not supported" when scheme != goakt.
//   - "address format is invalid" for malformed delimiters or extra separators.
//   - Atoi error when the port is not an integer.
//
// Examples:
//
//	addr, _ := Parse("goakt://orders@127.0.0.1:9000/checkout")
//	// system=orders, host=127.0.0.1, port=9000, name=checkout, parent=nil
//
//	addr, _ := Parse("goakt://orders@127.0.0.1:9000/root/checkout")
//	// parent.Name() == "root"
//
//	_, err := Parse("goakt://orders@127.0.0.1:abc/checkout")
//	// err != nil
func Parse(addr string) (*Address, error) {
	if addr == "" {
		return nil, errors.New("address is required")
	}

	schemePart, rest, ok := strings.Cut(addr, "://")
	if !ok || strings.Contains(rest, "://") {
		return nil, errors.New("address format is invalid")
	}

	if schemePart != scheme {
		return nil, errors.New("address protocol is not supported")
	}

	system, rest, ok := strings.Cut(rest, "@")
	if !ok || strings.Contains(rest, "@") {
		return nil, errors.New("address format is invalid")
	}

	hostPort, path, ok := strings.Cut(rest, "/")
	if !ok {
		return nil, errors.New("address format is invalid")
	}

	if strings.HasPrefix(path, "/") {
		return nil, errors.New("address format is invalid")
	}

	host, portStr, ok := strings.Cut(hostPort, ":")
	if !ok || strings.Contains(portStr, ":") {
		return nil, errors.New("address format is invalid")
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, err
	}

	parentName := ""
	name := path
	if parentPart, childPart, ok := strings.Cut(path, "/"); ok {
		if strings.Contains(childPart, "/") {
			return nil, errors.New("address format is invalid")
		}
		parentName = parentPart
		name = childPart
	}

	if parentName == "" {
		return New(name, system, host, port), nil
	}

	parent := New(parentName, system, host, port)
	return NewWithParent(name, system, host, port, parent), nil
}
