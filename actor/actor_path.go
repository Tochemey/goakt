// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package actor

import "github.com/tochemey/goakt/v4/internal/address"

// Path represents the logical path of an actor within an actor system.
// It provides a location-transparent view of host, port, name, system, and parent.
type Path interface {
	// Host returns the hostname of the actor system node where the actor resides.
	Host() string
	// HostPort returns the host:port combination as a string.
	HostPort() string
	// Port returns the port number of the actor system node where the actor resides.
	Port() int
	// Name returns the name of the actor.
	Name() string
	// Parent returns the Path of the actor's parent, or nil if there is no parent.
	Parent() Path
	// String returns the full string representation of the actor path.
	String() string
	// System returns the name of the actor system this actor belongs to.
	System() string
	// Equals reports whether this path is equal to the given path.
	Equals(other Path) bool

	incarnationID() string
}

// path is a thin, read-only view over an actor's address. It duplicates none of
// the address components: every accessor reads them back through the shared
// address pointer.
type path struct {
	// addr is the actor's address. Address is immutable once constructed, so the
	// view shares it by pointer and every accessor reads through it.
	addr *address.Address
	// parent is the pre-built view of the parent actor's path, or nil for a root
	// or otherwise parentless actor.
	parent Path
}

// Host returns the host component of the underlying address.
func (x *path) Host() string {
	if x == nil {
		return ""
	}
	return x.addr.Host()
}

// Port returns the port component of the underlying address.
func (x *path) Port() int {
	if x == nil {
		return 0
	}
	return x.addr.Port()
}

// HostPort returns the "host:port" endpoint of the underlying address.
func (x *path) HostPort() string {
	if x == nil {
		return ""
	}
	return x.addr.HostPort()
}

// incarnationID returns the incarnation identifier of the underlying address.
func (x *path) incarnationID() string {
	if x == nil {
		return ""
	}
	return x.addr.IncarnationID()
}

// Name returns the actor name component of the underlying address.
func (x *path) Name() string {
	if x == nil {
		return ""
	}
	return x.addr.Name()
}

// Parent returns the pre-built view of the parent actor's path, or nil when
// there is no parent.
func (x *path) Parent() Path {
	if x == nil {
		return nil
	}
	return x.parent
}

// String returns the canonical string representation of the underlying address.
func (x *path) String() string {
	if x == nil {
		return ""
	}
	return x.addr.String()
}

// System returns the actor system name component of the underlying address.
func (x *path) System() string {
	if x == nil {
		return ""
	}
	return x.addr.System()
}

// Equals reports whether this path and other share the same canonical string
// form. It returns false when either receiver or argument is nil.
func (x *path) Equals(other Path) bool {
	if x == nil || other == nil {
		return false
	}
	return x.String() == other.String()
}

// newPath builds a read-only view over addr, recursively building the parent
// view when addr carries a real parent. It returns nil when addr is nil.
func newPath(addr *address.Address) Path {
	if addr == nil {
		return nil
	}

	var parent Path
	if p := addr.Parent(); p != nil && !p.Equals(address.NoSender()) {
		parent = newPath(p)
	}

	return &path{
		addr:   addr,
		parent: parent,
	}
}

// pathString returns p.String() when p is non-nil, otherwise "".
func pathString(path Path) string {
	if path == nil {
		return ""
	}
	return path.String()
}

// pathToAddress converts a Path to *address.Address for use with APIs that require it
// (e.g., RemoteTell, RemoteAsk). The path's incarnation identifier is restored on
// the address when present. Returns address.NoSender() when p is nil or when
// parsing the path string fails.
func pathToAddress(path Path) *address.Address {
	if path == nil {
		return address.NoSender()
	}

	if incarnationID := path.incarnationID(); incarnationID != "" {
		if addr, err := address.ParseWithIncarnationID(path.String(), incarnationID); err == nil {
			return addr
		}
	}

	addr, err := address.Parse(path.String())
	if err != nil {
		return address.NoSender()
	}
	return addr
}
