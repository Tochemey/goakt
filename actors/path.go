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

package actors

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/pkg/errors"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/validation"
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

var _ validation.Validator = (*Path)(nil)

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

// Equals is used to compare two paths
func (p *Path) Equals(x *Path) bool {
	return p.ID().ID() == x.ID().ID() && p.String() == x.String()
}

// RemoteAddress returns the remote from path
func (p *Path) RemoteAddress() *goaktpb.Address {
	// only returns a remote address when we are in a remote scope otherwise return nil
	if !p.address.IsRemote() {
		return nil
	}
	return &goaktpb.Address{
		Host: p.address.Host(),
		Port: int32(p.address.Port()),
		Name: p.Name(),
		Id:   p.ID().String(),
	}
}

// Validate returns an error when the path is not valid
func (p *Path) Validate() error {
	pattern := "^[a-zA-Z0-9][a-zA-Z0-9-_\\.]*$"
	customErr := errors.New("path name must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')")
	return validation.
		New(validation.FailFast()).
		AddValidator(validation.NewPatternValidator(pattern, strings.TrimSpace(p.name), customErr)).
		AddAssertion(p.address != nil, "address is required").
		Validate()
}
