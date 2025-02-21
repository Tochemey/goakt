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

package remote

import (
	"strings"

	"github.com/tochemey/goakt/v3/internal/validation"
)

// SpawnRequest defines configuration options for spawning an actor on a remote node.
// These options control the actor’s identity, behavior, and lifecycle, especially in scenarios involving node failures or load balancing.
type SpawnRequest struct {
	// Name represents the unique name of the actor.
	// This name is used to identify and reference the actor across different nodes.
	Name string

	// Kind represents the type of the actor.
	// It typically corresponds to the actor’s implementation within the system
	Kind string

	// Singleton specifies whether the actor is a singleton, meaning only one instance of the actor
	// can exist across the entire cluster at any given time.
	// This option is useful for actors responsible for global coordination or shared state.
	// When Singleton is set to true it means that the given actor is automatically relocatable
	Singleton bool

	// Relocatable indicates whether the actor can be automatically relocated to another node
	// if its current host node unexpectedly shuts down.
	// By default, actors are relocatable to ensure system resilience and high availability.
	// Setting this to false ensures that the actor will not be redeployed after a node failure,
	// which may be necessary for actors with node-specific dependencies or state.
	Relocatable bool
}

var _ validation.Validator = (*SpawnRequest)(nil)

// Validate validates the SpawnRequest
func (s *SpawnRequest) Validate() error {
	return validation.
		New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("Name", s.Name)).
		AddValidator(validation.NewEmptyStringValidator("Kind", s.Kind)).
		Validate()
}

// Sanitize sanitizes the request
func (s *SpawnRequest) Sanitize() {
	s.Name = strings.TrimSpace(s.Name)
	s.Kind = strings.TrimSpace(s.Kind)
	if s.Singleton {
		s.Relocatable = true
	}
}
