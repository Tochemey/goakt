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

package supervisor

import (
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/ds"
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

// String returns the string representation of the strategy
func (s Strategy) String() string {
	switch s {
	case OneForOneStrategy:
		return "OneForOne"
	case OneForAllStrategy:
		return "OneForAll"
	default:
		return ""
	}
}

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

	// EscalateDirective indicates that when an actor fails, the supervisor should escalate the failure
	// to its parent supervisor. This directive is used when the failure is severe and requires
	// intervention at a higher level in the actor hierarchy.
	EscalateDirective
)

// String returns the string representation of the directive
func (d Directive) String() string {
	switch d {
	case StopDirective:
		return "Stop"
	case ResumeDirective:
		return "Resume"
	case RestartDirective:
		return "Restart"
	case EscalateDirective:
		return "Escalate"
	default:
		return ""
	}
}

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
// Parameters:
//   - directive: The directive to apply to any error
//
// Use WithAnyErrorDirective to apply a given directive to any error that occurs during message processing.
// This option is useful when you want to handle all errors uniformly, regardless of their type.
func WithAnyErrorDirective(directive Directive) SupervisorOption {
	return func(s *Supervisor) {
		s.Lock()
		s.directives.Set(errorType(new(errors.AnyError)), directive)
		s.Unlock()
	}
}

// DirectiveRule describes a directive rule keyed by error type.
type DirectiveRule struct {
	// ErrorType should be the fully-qualified Go error type name (reflect.Type.String()).
	ErrorType string
	// Directive is the directive to apply for ErrorType.
	Directive Directive
}

// Supervisor defines how a parent reacts when a child actor fails.
//
// It combines:
//   - a strategy (one-for-one vs one-for-all), and
//   - directive rules that map error types to actions (stop, resume, restart, escalate).
//
// Defaults:
//   - Strategy: OneForOneStrategy.
//   - Directives: PanicError -> Stop, runtime.PanicNilError -> Restart.
//   - Retries: 0 (no retry window unless configured with WithRetry).
//
// Rules are keyed by the error's concrete type name (reflect.Type.String()) as
// provided by WithDirective. If you set an "any error" directive via
// WithAnyErrorDirective, it becomes the sole rule and overrides any
// error-specific directives.
//
// The Restart directive uses MaxRetries and Timeout to bound restarts within a
// window; use WithRetry to configure them.
//
// Supervisor methods are safe for concurrent use.
type Supervisor struct {
	sync.Mutex
	// Specifies the strategy
	strategy Strategy
	// Specifies the maximum number of retries
	// When reaching this number the faulty actor is stopped
	maxRetries uint32
	// Specifies the time range to restart the faulty actor
	timeout time.Duration

	directives *ds.Map[string, Directive]
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
		directives: ds.NewMap[string, Directive](),
		maxRetries: 0,
		timeout:    -1,
	}

	// set the default directives
	s.directives.Set(errorType(&errors.PanicError{}), StopDirective)
	s.directives.Set(errorType(&runtime.PanicNilError{}), RestartDirective)

	for _, opt := range opts {
		opt(s)
	}

	// any error overrides all error types
	if directive, ok := s.directives.Get(errorType(new(errors.AnyError))); ok {
		s.directives.Reset()
		s.directives.Set(errorType(new(errors.AnyError)), directive)
	}

	return s
}

// Strategy returns the configured supervision strategy.
func (s *Supervisor) Strategy() Strategy {
	s.Lock()
	strategy := s.strategy
	s.Unlock()
	return strategy
}

// Directive returns the directive configured for the concrete type of err.
// It does not fall back to the "any error" directive; use AnyErrorDirective
// when you need the catch-all behavior.
func (s *Supervisor) Directive(err error) (Directive, bool) {
	s.Lock()
	if s.directives == nil {
		s.Unlock()
		return 0, false
	}
	directive, ok := s.directives.Get(errorType(err))
	s.Unlock()
	return directive, ok
}

// MaxRetries returns the restart retry budget used with RestartDirective.
func (s *Supervisor) MaxRetries() uint32 {
	return s.maxRetries
}

// Timeout returns the retry window used with RestartDirective.
func (s *Supervisor) Timeout() time.Duration {
	return s.timeout
}

// Reset clears all directive rules and sets the strategy to OneForAllStrategy.
// It does not modify retry settings.
func (s *Supervisor) Reset() {
	s.Lock()
	s.strategy = OneForAllStrategy
	s.directives = ds.NewMap[string, Directive]()
	s.Unlock()
}

// Rules returns a snapshot of the directive rules currently configured.
// The returned slice is a copy; ordering is not guaranteed.
func (s *Supervisor) Rules() []DirectiveRule {
	s.Lock()
	defer s.Unlock()
	if s.directives == nil || s.directives.Len() == 0 {
		return nil
	}
	rules := make([]DirectiveRule, 0, s.directives.Len())
	s.directives.Range(func(errorType string, directive Directive) {
		rules = append(rules, DirectiveRule{
			ErrorType: errorType,
			Directive: directive,
		})
	})
	return rules
}

// AnyErrorDirective returns the directive for the catch-all error type, if configured.
func (s *Supervisor) AnyErrorDirective() (Directive, bool) {
	s.Lock()
	if s.directives == nil {
		s.Unlock()
		return 0, false
	}
	directive, ok := s.directives.Get(errorType(new(errors.AnyError)))
	s.Unlock()
	return directive, ok
}

// SetDirectiveByType associates a supervision directive with an error type name.
//
// The key must be the concrete (non-pointer) error type string as returned by
// reflect.Type.String() (for example: "net.OpError" or "github.com/acme/pkg.MyError").
// Callers typically obtain this value via the internal helper errorType(err).
//
// This is a low-level helper intended for cases where the error type is only known
// by name (e.g., configuration-driven rules). Prefer WithDirective / WithAnyErrorDirective
// when you have an error value or want the catch-all behavior.
//
// Notes:
//   - Unlike WithAnyErrorDirective, this does not clear or override existing rules.
//   - If a catch-all rule (errors.AnyError) is configured, it is NOT automatically applied here;
//     resolution behavior depends on how Directive(err) is called.
//   - Empty errorType is ignored.
//   - Safe for concurrent use.
func (s *Supervisor) SetDirectiveByType(errorType string, directive Directive) {
	if errorType == "" {
		return
	}
	s.Lock()
	if s.directives == nil {
		s.directives = ds.NewMap[string, Directive]()
	}
	s.directives.Set(errorType, directive)
	s.Unlock()
}

// errorType returns the string representation of an error's type using reflection
func errorType(err error) string {
	// Handle nil errors first
	if err == nil {
		return "nil"
	}

	rtype := reflect.TypeOf(err)
	if rtype.Kind() == reflect.Pointer {
		rtype = rtype.Elem()
	}

	return rtype.String()
}
