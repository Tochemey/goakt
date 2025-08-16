/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actor

import (
	"errors"
	"fmt"
	"strings"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/internal/validation"
)

const (
	identitySeparator = "/"
)

// GrainIdentity uniquely identifies a grain (virtual actor) instance within the actor system.
//
// It consists of:
//   - kind: Fully qualified type name of the grain (derived via reflection).
//   - name: Unique identifier for the grain instance.
//
// GrainIdentity enables location-transparent routing, lifecycle management, and stable grain identity
// across distributed systems and restarts. They are immutable and safe for concurrent use.
type GrainIdentity struct {
	kind string // Fully qualified type name of the grain
	name string // Unique instance identifier within the grain type
}

// ensure GrainIdentity implements the validation.Validator interface
var _ validation.Validator = (*GrainIdentity)(nil)

// newGrainIdentity constructs a GrainIdentity from a grain instance and a unique name.
//
// It derives the grain kind via reflection and combines it with the provided name.
// The resulting ID can be used for routing, activation, and identity management.
//
// Parameters:
//   - grain: Any struct implementing the Grain interface (used for type derivation).
//   - name: Unique identifier within the grain type.
//
// Returns:
//
//	A pointer to a GrainIdentity instance.
//
// Notes:
//   - Kind is automatically derived and should not be manually set.
//   - Name should be meaningful, unique, and safe for serialization.
func newGrainIdentity(grain Grain, name string) *GrainIdentity {
	kind := registry.Name(grain)
	return &GrainIdentity{
		kind: kind,
		name: name,
	}
}

// Kind returns the fully qualified type name of the grain.
//
// Used by the actor system for instantiation, factory lookups, and routing.
func (g *GrainIdentity) Kind() string {
	return g.kind
}

// Name returns the unique name of the grain instance.
//
// It identifies this instance within its grain type and is used for routing and persistence.
func (g *GrainIdentity) Name() string {
	return g.name
}

// String returns the formatted string representation of the GrainIdentity as "kind:name".
//
// Useful for logging, debugging, and human-readable configuration.
func (g *GrainIdentity) String() string {
	if g == nil {
		return ""
	}
	return fmt.Sprintf("%s%s%s", g.kind, identitySeparator, g.name)
}

// Equal checks whether this GrainIdentity is equal to another.
//
// Two GrainIDs are equal if both kind and name are identical.
// Returns false if the other is nil.
func (g *GrainIdentity) Equal(other *GrainIdentity) bool {
	if other == nil {
		return false
	}
	return g.kind == other.kind && g.name == other.name
}

// Validate implements validation.Validator.
func (g *GrainIdentity) Validate() error {
	pattern := "^[a-zA-Z0-9][a-zA-Z0-9-_\\.]*$"
	customErr := errors.New("must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')")
	return validation.
		New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("name", g.Name())).
		AddAssertion(len(g.Name()) <= 255, "grain name is too long. Maximum length is 255").
		AddValidator(validation.NewPatternValidator(pattern, strings.TrimSpace(g.Name()), customErr)).
		Validate()
}

// toIdentity reconstructs a given GrainIdentity from its string representation
func toIdentity(s string) (*GrainIdentity, error) {
	parts := strings.SplitN(s, identitySeparator, 2)
	if len(parts) != 2 {
		return nil, gerrors.ErrInvalidGrainIdentity
	}
	identity := &GrainIdentity{kind: parts[0], name: parts[1]}
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	return identity, nil
}
