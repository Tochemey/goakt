package actors

import (
	"fmt"

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
	// ErrInvalidNode is returned when the given actor system node is not the one requested
	ErrInvalidNode = connect.NewError(connect.CodeFailedPrecondition, errors.New("invalid actor system node"))
	// ErrAddressNotFound is returned when an actor address is not found
	ErrAddressNotFound = func(addr string) error {
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("actor=%s not found", addr))
	}
	// ErrRemoteSendFailure is returned when remote message fails
	ErrRemoteSendFailure = func(err error) error { return connect.NewError(connect.CodeInternal, err) }
	// ErrInvalidActorInterface is returned when an actor does not implement the Actor interface
	ErrInvalidActorInterface = errors.New("failed to create instance. Reason: instance does not implement the Actor interface")
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
)
