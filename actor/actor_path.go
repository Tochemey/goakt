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
	Host() string
	HostPort() string
	Port() int
	Name() string
	Parent() Path
	String() string
	System() string
	Equals(other Path) bool
}

type path struct {
	host           string
	port           int
	name           string
	system         string
	parent         Path
	cachedStr      string
	cachedHostPort string
}

func (x *path) Host() string {
	if x == nil {
		return ""
	}
	return x.host
}

func (x *path) Port() int {
	if x == nil {
		return 0
	}
	return x.port
}

func (x *path) HostPort() string {
	if x == nil {
		return ""
	}
	return x.cachedHostPort
}

func (x *path) Name() string {
	if x == nil {
		return ""
	}
	return x.name
}

func (x *path) Parent() Path {
	if x == nil {
		return nil
	}
	return x.parent
}

func (x *path) String() string {
	if x == nil {
		return ""
	}
	return x.cachedStr
}

func (x *path) System() string {
	if x == nil {
		return ""
	}
	return x.system
}

func (x *path) Equals(other Path) bool {
	if x == nil || other == nil {
		return false
	}
	return x.cachedStr == other.String()
}

func newPath(addr *address.Address) Path {
	if addr == nil {
		return nil
	}
	var parent Path
	if p := addr.Parent(); p != nil && !p.Equals(address.NoSender()) {
		parent = newPath(p)
	}
	p := &path{
		host:           addr.Host(),
		port:           addr.Port(),
		name:           addr.Name(),
		system:         addr.System(),
		parent:         parent,
		cachedStr:      addr.String(),
		cachedHostPort: addr.HostPort(),
	}
	return p
}

// pathString returns p.String() when p is non-nil, otherwise "".
func pathString(path Path) string {
	if path == nil {
		return ""
	}
	return path.String()
}

// pathToAddress converts a Path to *address.Address for use with APIs that require it
// (e.g., RemoteTell, RemoteAsk). Returns address.NoSender() when p is nil or when
// parsing the path string fails.
func pathToAddress(path Path) *address.Address {
	if path == nil {
		return address.NoSender()
	}
	addr, err := address.Parse(path.String())
	if err != nil {
		return address.NoSender()
	}
	return addr
}
