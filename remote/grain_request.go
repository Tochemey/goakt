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

package remote

import (
	"strings"
	"time"

	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/internal/validation"
)

// GrainRequest describes the parameters used to activate (spawn) a Grain on a remote node.
//
// A request identifies the Grain to activate (Name + Kind), optionally provides dependency
// injection values, and controls activation resiliency via timeout and retry settings.
//
// Field expectations:
//
//   - Name and Kind SHOULD be provided. Name is the logical/unique identity of the Grain instance.
//     Kind is the fully-qualified Grain type name (typically derived via reflection).
//   - Dependencies is optional and is passed to the Grain at initialization time.
//   - ActivationTimeout and ActivationRetries are optional controls; their effective behavior
//     (e.g., defaults, backoff strategy, what counts as a retryable failure) is defined by the
//     activation/transport layer that consumes this request.
//
// Note: The given Grain kind must be registered on the remote node actor system where activation is requested using the RegisterGrainKind function.
type GrainRequest struct {
	// Name is the logical, unique identity of the Grain instance to activate.
	Name string

	// Kind is the fully-qualified type name of the Grain to activate (typically derived via reflection).
	Kind string

	// Dependencies is the set of values to inject into the Grain during initialization.
	// This enables supplying services/clients/configuration needed by the Grain.
	Dependencies []extension.Dependency

	// ActivationTimeout is the maximum time allowed for the activation to complete.
	// If exceeded, activation is treated as failed by the caller/activation layer.
	ActivationTimeout time.Duration

	// ActivationRetries is the number of additional activation attempts to perform after a failure.
	// A value of 0 means "do not retry".
	ActivationRetries int
}

var _ validation.Validator = (*GrainRequest)(nil)

// Validate checks that the GrainRequest has valid fields.
func (s *GrainRequest) Validate() error {
	if err := validation.
		New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("Name", s.Name)).
		AddValidator(validation.NewEmptyStringValidator("Kind", s.Kind)).
		Validate(); err != nil {
		return err
	}

	if len(s.Dependencies) > 0 {
		for _, dependency := range s.Dependencies {
			if err := validation.NewIDValidator(dependency.ID()).Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Sanitize sanitizes the request
func (s *GrainRequest) Sanitize() {
	s.Name = strings.TrimSpace(s.Name)
	s.Kind = strings.TrimSpace(s.Kind)
	if s.ActivationTimeout <= 0 {
		s.ActivationTimeout = time.Second
	}
	if s.ActivationRetries <= 0 {
		s.ActivationRetries = 5
	}
}
