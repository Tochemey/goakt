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

package actors

import (
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/tochemey/goakt/v3/internal/collection/syncmap"
)

// Strategy represents the type of supervision strategy used by an actor's supervisor.
// In an actor framework, supervisors manage child actors and define how failures should be handled.
// Different strategy types determine whether to restart, stop, resume, or escalate failures.
type Strategy int

const (
	// OneForOneStrategy is a supervision strategy where if a child actor fails, only that specific child
	// is affected by the supervisor's directive (e.g., restart, stop, resume, or escalate).
	// Other sibling actors continue running unaffected.
	OneForOneStrategy Strategy = iota

	// OneForAllStrategy is a supervision strategy that applies the supervisor’s directive to all
	// sibling child actors if any one of them fails or panics during message processing.
	//
	// When using OneForAllStrategy, a failure in any child actor triggers a collective response:
	// the same directive (e.g., restart, resume, or stop) is applied to every child under the supervisor.
	// This strategy is particularly appropriate when the child actors are tightly coupled or
	// interdependent—where the malfunction of one actor can adversely affect the overall functionality
	// of the ensemble.
	//
	// Use this strategy in scenarios where maintaining a consistent state across all child actors is crucial,
	// such as when they share common resources or work together on a composite task.
	// For more granular control over failures, consider using the OneForOneStrategy instead.
	OneForAllStrategy
)

// Directive defines the supervisor directive
//
// It represents the action that a supervisor can take when a child actor fails or panics
// during message processing. Each directive corresponds to a specific recovery behavior:
//
//   - StopDirective: Instructs the supervisor to stop the failing actor.
//   - ResumeDirective: Instructs the supervisor to resume the failing actor without restarting it,
//     allowing it to continue processing messages (typically used for recoverable errors).
//   - RestartDirective: Instructs the supervisor to restart the failing actor, reinitializing its state.
type Directive int

const (
	// StopDirective indicates that when an actor fails, the supervisor should immediately stop
	// the actor. This directive is typically used when a failure is deemed irrecoverable
	// or when the actor's state cannot be safely resumed.
	StopDirective Directive = iota
	// ResumeDirective indicates that when an actor fails, the supervisor should resume the actor's
	// operation without restarting it. This directive is used when the failure is transient and the
	// actor can continue processing messages without a state reset.
	ResumeDirective
	// RestartDirective indicates that when an actor fails, the supervisor should restart the actor.
	// Restarting involves stopping the current instance and creating a new one, effectively resetting
	// the actor's internal state.
	RestartDirective
)

// SupervisorOption defines the various options to apply to a given Supervisor
type SupervisorOption func(*Supervisor)

// WithStrategy sets the supervisor strategy
func WithStrategy(strategy Strategy) SupervisorOption {
	return func(s *Supervisor) {
		s.Lock()
		s.strategy = strategy
		s.Unlock()
	}
}

// WithDirective sets the mapping between an error and a given directive
func WithDirective(err error, directive Directive) SupervisorOption {
	return func(s *Supervisor) {
		s.Lock()
		s.directives.Set(errorType(err), directive)
		s.Unlock()
	}
}

// WithRetry configures the retry behavior for an actor when using the RestartDirective.
// It sets the maximum number of retry attempts and the timeout period between retries.
//
// Parameters:
//   - maxRetries: The maximum number of times an actor will be restarted after failure.
//     Exceeding this count will trigger escalation according to the supervisor's policy.
//   - timeout: The duration to wait before attempting a retry.
//     This timeout defines the retry window and can help avoid immediate, repeated restarts.
//
// Use WithRetry to provide a controlled recovery mechanism for transient failures,
// ensuring that the actor is not endlessly restarted.
func WithRetry(maxRetries uint32, timeout time.Duration) SupervisorOption {
	return func(s *Supervisor) {
		s.Lock()
		s.maxRetries = maxRetries
		s.timeout = timeout
		s.Unlock()
	}
}

// WithAnyErrorDirective sets the directive to apply to any error
//
// Use WithAnyErrorDirective to apply a given directive to any error that occurs during message processing.
// This option is useful when you want to handle all errors uniformly, regardless of their type.
//
// Parameters:
//   - directive: The directive to apply to any error
//
// Example Usage:
//
//	supervisor := NewSupervisor(
//	    WithAnyErrorDirective(DirectiveRestart),
//	)
func WithAnyErrorDirective(directive Directive) SupervisorOption {
	return func(s *Supervisor) {
		s.Lock()
		s.directives.Set(errorType(new(anyError)), directive)
		s.Unlock()
	}
}

// Supervisor defines the supervisor behavior rules applied to a faulty actor during message processing.
//
// A supervision behavior determines how a supervisor responds when a child actor encounters an error.
// The strategy dictates whether the faulty actor should be resumed, restarted, stopped, or if the error
// should be escalated to the supervisor’s parent.
//
// The default supervision strategy is OneForOneStrategy, meaning that failures are handled individually,
// affecting only the actor that encountered the error.
type Supervisor struct {
	sync.Mutex
	// Specifies the strategy
	strategy Strategy
	// Specifies the maximum number of retries
	// When reaching this number the faulty actor is stopped
	maxRetries uint32
	// Specifies the time range to restart the faulty actor
	timeout time.Duration

	directives *syncmap.Map[string, Directive]
}

// NewSupervisor creates a new instance of supervisor behavior for managing actor supervision.
//
// This function initializes a supervisor behavior with a set of error handlers and optional configuration options.
// The default strategy applied is OneForOneStrategy, meaning that when a child actor fails, only that actor is affected,
// and the supervisor applies the defined directive to it individually.
//
// Once the behavior instance is created, one need to add the various directive/error mappings required to handle
// faulty actor using the WithDirective method of the behavior.
//
// Returns:
//   - A pointer to the initialized *Supervisor instance.
func NewSupervisor(opts ...SupervisorOption) *Supervisor {
	// define an instance of Behavior and sets the default strategy type
	// to OneForOneStrategy
	s := &Supervisor{
		Mutex:      sync.Mutex{},
		strategy:   OneForOneStrategy,
		directives: syncmap.New[string, Directive](),
		maxRetries: 0,
		timeout:    -1,
	}

	// set the default directives
	s.directives.Set(errorType(PanicError{}), StopDirective)
	s.directives.Set(errorType(&runtime.PanicNilError{}), RestartDirective)

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Strategy returns the supervisor strategy
func (s *Supervisor) Strategy() Strategy {
	s.Lock()
	strategy := s.strategy
	s.Unlock()
	return strategy
}

// Directive returns the directive associated to a given error
func (s *Supervisor) Directive(err error) (Directive, bool) {
	s.Lock()
	directive, ok := s.directives.Get(errorType(err))
	s.Unlock()
	return directive, ok
}

// MaxRetries returns the maximum number of times an actor will be restarted after failure.
func (s *Supervisor) MaxRetries() uint32 {
	return s.maxRetries
}

// Timeout returns the timeout
// This is the duration to wait before attempting a retry.
func (s *Supervisor) Timeout() time.Duration {
	return s.timeout
}

// Reset resets the strategy
func (s *Supervisor) Reset() {
	s.Lock()
	s.strategy = OneForAllStrategy
	s.directives = syncmap.New[string, Directive]()
	s.Unlock()
}

// errorType returns the string representation of an error's type using reflection
func errorType(err error) string {
	// Handle nil errors first
	if err == nil {
		return "nil"
	}

	rtype := reflect.TypeOf(err)
	if rtype.Kind() == reflect.Ptr {
		rtype = rtype.Elem()
	}

	return rtype.String()
}
