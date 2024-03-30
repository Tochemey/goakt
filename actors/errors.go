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
	"io"

	"connectrpc.com/connect"
	"github.com/pkg/errors"
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

	// ErrInvalidInstance is returned when the creation of an actor instance fails
	ErrInvalidInstance = errors.New("failed to create instance. Reason: invalid instance")
	// ErrTypeNotFound is returned when an actor type is not found
	ErrTypeNotFound = func(typeName string) error { return fmt.Errorf("typeName=%s not found", typeName) }
	// ErrActorNotFound is returned when an actor is not found
	ErrActorNotFound = func(actorPath string) error { return fmt.Errorf("actor=%s not found", actorPath) }
	// ErrMethodCallNotAllowed is returned when rpc call is not allowed
	ErrMethodCallNotAllowed = errors.New("method call not allowed")
	// ErrInvalidRemoteMessage is returned when an invalid remote message is sent
	ErrInvalidRemoteMessage = func(err error) error { return errors.Wrap(err, "invalid remote message") }
	// ErrStashBufferNotSet when stashing is not set while requesting for messages to be stashed
	ErrStashBufferNotSet = errors.New("actor is not setup with a stash buffer")
	// ErrInitFailure is returned when the initialization of an actor fails.
	ErrInitFailure = func(err error) error { return errors.Wrap(err, "failed to initialize") }
	// ErrActorSystemNotStarted is returned when the actor is not started while accessing the features of the actor system
	ErrActorSystemNotStarted = errors.New("actor system has not started yet")
	// ErrLocalAddress is returned when a remote address is used instead of a local address
	ErrLocalAddress = errors.New("address is a local address")
	// ErrEmptyMailbox is returned when the mailbox is empty
	ErrEmptyMailbox = errors.New("mailbox is empty")
	// ErrFullMailbox is returned when the mailbox is full
	ErrFullMailbox = errors.New("mailbox is full")
	// ErrInstanceNotAnActor is returned when we failed to create the instance of an actor
	ErrInstanceNotAnActor = errors.New("failed to create instance. Reason: instance does not implement the Actor interface")
)

// IsEOF returns true if the given error is an EOF error
func IsEOF(err error) bool {
	return err != nil && (errors.Is(err, io.EOF) || errors.Unwrap(err) == io.EOF)
}
