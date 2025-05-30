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
	"io"

	"connectrpc.com/connect"
)

var (
	// ErrInvalidActorSystemName is returned when the actor system name is invalid
	ErrInvalidActorSystemName = errors.New("invalid ActorSystem name, must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')")
	// ErrDead means that the given actor is not alive
	ErrDead = errors.New("actor is not alive")
	// ErrUnhandled is used when an actor can handle a given message
	ErrUnhandled = errors.New("unhandled message")
	// ErrClusterDisabled is returned when cluster mode is not enabled while accessing cluster features
	ErrClusterDisabled = errors.New("cluster is not enabled")
	// ErrUndefinedActor is returned when an actor is defined
	ErrUndefinedActor = errors.New("actor is not defined")
	// ErrRequestTimeout is returned when sending an Ask message times out
	ErrRequestTimeout = errors.New("request timed out")
	// ErrRemotingDisabled is returned when remoting is not enabled
	ErrRemotingDisabled = errors.New("remoting is not enabled")
	// ErrAddressNotFound is returned when an actor address is not found
	ErrAddressNotFound = func(addr string) error {
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("actor=%s not found", addr))
	}
	// ErrRemoteSendFailure is returned when remote message fails
	ErrRemoteSendFailure = func(err error) error { return connect.NewError(connect.CodeInternal, err) }
	// ErrNameRequired is used when the actor system name is required
	ErrNameRequired = errors.New("actor system is required")
	// ErrInvalidInstance is returned when the creation of an actor instance fails
	ErrInvalidInstance = errors.New("failed to create instance. Reason: invalid instance")
	// ErrActorNotFound is returned when an actor is not found
	ErrActorNotFound = func(actorPath string) error { return fmt.Errorf("actor=%s not found", actorPath) }
	// ErrMethodCallNotAllowed is returned when rpc call is not allowed
	ErrMethodCallNotAllowed = errors.New("method call not allowed")
	// ErrInvalidRemoteMessage is returned when an invalid remote message is sent
	ErrInvalidRemoteMessage = func(err error) error { return fmt.Errorf("invalid remote message: %w", err) }
	// ErrStashBufferNotSet when stashing is not set while requesting for messages to be stashed
	ErrStashBufferNotSet = errors.New("actor is not setup with a stash buffer")
	// ErrInitFailure is returned when the initialization of an actor fails.
	ErrInitFailure = func(err error) error { return fmt.Errorf("failed to initialize: %w", err) }
	// ErrActorSystemNotStarted is returned when the actor is not started while accessing the features of the actor system
	ErrActorSystemNotStarted = errors.New("actor system has not started yet")
	// ErrReservedName is returned when the actor name is reserved
	ErrReservedName = errors.New("actor name is a reserved")
	// ErrInstanceNotAnActor is returned when we failed to create the instance of an actor
	ErrInstanceNotAnActor = errors.New("failed to create instance. Reason: instance does not implement the Actor interface")
	// ErrTypeNotRegistered is returned when a given actor is not registered
	ErrTypeNotRegistered = errors.New("actor type is not registered")
	// ErrPeerNotFound is returned when locating a given peer
	ErrPeerNotFound = errors.New("peer is not found")
	// ErrUndefinedTask is returned when piping a long-running task result to an actor
	ErrUndefinedTask = errors.New("task is not defined")
	// ErrInvalidHost is returned when a request is sent to an invalid host
	ErrInvalidHost = errors.New("invalid host")
	// ErrSchedulerNotStarted is returned when the scheduler has not started
	ErrSchedulerNotStarted = errors.New("scheduler has not started")
	// ErrInvalidMessage is returned when an invalid remote message is sent
	ErrInvalidMessage = func(err error) error { return fmt.Errorf("invalid remote message: %w", err) }
	// ErrInvalidTimeout is returned when a given timeout is negative or zero
	ErrInvalidTimeout = errors.New("invalid timeout")
	// ErrActorAlreadyExists is returned when trying to create the same actor more than once
	ErrActorAlreadyExists = func(actorName string) error { return fmt.Errorf("actor=(%s) already exists", actorName) }
	// ErrInvalidTLSConfiguration is returned when the TLS configuration is not properly set
	ErrInvalidTLSConfiguration = errors.New("TLS configuration is invalid")
	// ErrSingletonAlreadyExists is returned when a given singleton actor type already exists
	ErrSingletonAlreadyExists = errors.New("singleton already exists")
	// ErrLeaderNotFound is returned when the cluster oldest node(leader) is not found
	ErrLeaderNotFound = errors.New("leader is not found")
	// ErrDependencyTypeNotRegistered is returned when a given cluster-aware dependency is not registered
	ErrDependencyTypeNotRegistered = errors.New("dependency type is not registered")
	// ErrInstanceNotDependency is returned when we failed to create the instance of a dependency
	ErrInstanceNotDependency = errors.New("failed to create instance. Reason: instance does not implement the Dependency interface")
	// ErrActorSystemAlreadyStarted is returned when the actor system has already started
	ErrActorSystemAlreadyStarted = errors.New("actor system has already started")
	// ErrScheduledReferenceNotFound is returned when a scheduled reference cannot be found.
	ErrScheduledReferenceNotFound = errors.New("scheduled reference not found")
)

// eof returns true if the given error is an EOF error
func eof(err error) bool {
	return err != nil && (errors.Is(err, io.EOF) || errors.Unwrap(err) == io.EOF)
}

// PanicError defines the panic error
// wrapping the underlying error
type PanicError struct {
	err error
}

// enforce compilation error
var _ error = (*PanicError)(nil)

// NewPanicError creates an instance of PanicError
func NewPanicError(err error) PanicError {
	return PanicError{err}
}

// Error implements the standard error interface
func (e PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.err)
}

// InternalError defines an error that is explicit to the application
type InternalError struct {
	err error
}

// enforce compilation error
var _ error = (*InternalError)(nil)

// NewInternalError returns an intance of InternalError
func NewInternalError(err error) InternalError {
	return InternalError{
		err: fmt.Errorf("internal error: %w", err),
	}
}

// Error implements the standard error interface
func (i InternalError) Error() string {
	return i.err.Error()
}

// SpawnError defines an error when re/creating an actor
type SpawnError struct {
	err error
}

var _ error = (*SpawnError)(nil)

// NewSpawnError returns an instance of SpawnError
func NewSpawnError(err error) SpawnError {
	return SpawnError{
		err: fmt.Errorf("spawn error: %w", err),
	}
}

// Error implements the standard error interface
func (s SpawnError) Error() string {
	return s.err.Error()
}

type rebalancingError struct {
	err error
}

var _ error = (*rebalancingError)(nil)

// creates an instance of rebalancingError
func newRebalancingError(err error) rebalancingError {
	return rebalancingError{err}
}

func (e rebalancingError) Error() string {
	return fmt.Errorf("rebalancing: %w", e.err).Error()
}

// anyError defines the any error type
// this is used to represent any error when handling the supervisor directive
type anyError struct{}

// interface guard
var _ error = (*anyError)(nil)

// Error implements error.
func (a *anyError) Error() string {
	return "*"
}
