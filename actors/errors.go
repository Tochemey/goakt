/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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
	// ErrLocalAddress is returned when a remote address is used instead of a local address
	ErrLocalAddress = errors.New("address is a local address")
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
	// ErrFullMailbox is returned when the mailbox is full
	ErrFullMailbox = errors.New("mailbox is full")
	// ErrSchedulerNotStarted is returned when the scheduler has not started
	ErrSchedulerNotStarted = errors.New("scheduler has not started")
	// ErrInvalidMessage is returned when an invalid remote message is sent
	ErrInvalidMessage = func(err error) error { return fmt.Errorf("invalid remote message: %w", err) }
	// ErrInvalidTimeout is returned when a given timeout is negative or zero
	ErrInvalidTimeout = errors.New("invalid timeout")
	// ErrPriorityMessageRequired is returned when a non-priority message is used in a priority mailbox
	ErrPriorityMessageRequired = errors.New("priority message type is required")
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
