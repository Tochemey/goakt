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

	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/internal/validation"
)

// GrainKey uniquely identifies a grain (virtual actor) instance within the actor system.
//
// It consists of:
//   - kind: Fully qualified type name of the grain (derived via reflection).
//   - name: Unique identifier for the grain instance.
//
// GrainIDs enable location-transparent routing, lifecycle management, and stable grain identity
// across distributed systems and restarts. They are immutable and safe for concurrent use.
//
// Example:
//
//	user := &UserAccountGrain{}
//	id := NewGrainID(user, "user-12345")
//	system.SendMessage(id, &UpdateUserMessage{Name: "John"})
type GrainKey struct {
	kind string // Fully qualified type name of the grain
	name string // Unique instance identifier within the grain type
}

// ensure GrainKey implements the validation.Validator interface
var _ validation.Validator = (*GrainKey)(nil)

// newGrainKey constructs a GrainKey from a grain instance and a unique name.
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
//	A pointer to a GrainKey instance.
//
// Notes:
//   - Kind is automatically derived and should not be manually set.
//   - Name should be meaningful, unique, and safe for serialization.
func newGrainKey(grain Grain, name string) *GrainKey {
	kind := types.Name(grain)
	return &GrainKey{
		kind: kind,
		name: name,
	}
}

// Kind returns the fully qualified type name of the grain.
//
// Used by the actor system for instantiation, factory lookups, and routing.
//
// Example:
//
//	id := NewGrainID(&UserGrain{}, "user-123")
//	fmt.Println(id.Kind()) // e.g., "main.UserGrain"
func (g *GrainKey) Kind() string {
	return g.kind
}

// Name returns the unique name of the grain instance.
//
// It identifies this instance within its grain type and is used for routing and persistence.
//
// Example:
//
//	id := NewGrainID(&UserGrain{}, "user-123")
//	fmt.Println(id.Name()) // "user-123"
func (g *GrainKey) Name() string {
	return g.name
}

// String returns the formatted string representation of the GrainKey as "kind:name".
//
// Useful for logging, debugging, and human-readable configuration.
//
// Example:
//
//	id := NewGrainID(&UserGrain{}, "user-123")
//	fmt.Println(id) // Output: "main.UserGrain:user-123"
func (g *GrainKey) String() string {
	return fmt.Sprintf("%s%s%s", g.kind, grainKeySeparator, g.name)
}

// Equal checks whether this GrainKey is equal to another.
//
// Two GrainIDs are equal if both kind and name are identical.
// Returns false if the other is nil.
//
// Example:
//
//	id1 := NewGrainID(&UserGrain{}, "user-123")
//	id2 := NewGrainID(&UserGrain{}, "user-123")
//	fmt.Println(id1.Equal(id2)) // true
func (g *GrainKey) Equal(other *GrainKey) bool {
	if other == nil {
		return false
	}
	return g.kind == other.kind && g.name == other.name
}

// FromString reconstructs a given GrainKey from its string representation
func (g *GrainKey) FromString(s string) (*GrainKey, error) {
	parts := strings.SplitN(s, grainKeySeparator, 2)
	if len(parts) != 2 {
		// TODO: abstract this into sentinel error
		return nil, errors.New("invalid grain id format")
	}
	return &GrainKey{kind: parts[0], name: parts[1]}, nil
}

// Validate implements validation.Validator.
func (g *GrainKey) Validate() error {
	pattern := "^[a-zA-Z0-9][a-zA-Z0-9-_\\.]*$"
	customErr := errors.New("must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')")
	return validation.
		New(validation.FailFast()).
		AddAssertion(len(g.Name()) <= 255, "actor name is too long. Maximum length is 255").
		AddValidator(validation.NewPatternValidator(pattern, strings.TrimSpace(g.Name()), customErr)).
		Validate()
}
