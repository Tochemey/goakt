package actors

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/pkg/errors"
)

var (
	ErrInvalidActorSystemName = errors.New("invalid ActorSystem name, must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')")
	ErrNotReady               = errors.New("actor is not ready")
	ErrUnhandled              = errors.New("unhandled message")
	ErrNotRunning             = errors.New("actor is not running")
	ErrUndefinedActor         = errors.New("actor is not defined")
	ErrRequestTimeout         = errors.New("request timed out")
	ErrEmptyBehavior          = errors.New("no behavior defined")
	ErrRemotingNotEnabled     = errors.New("remoting is not enabled")

	ErrRemoteSendInvalidNode = connect.NewError(connect.CodeFailedPrecondition, errors.New("invalid actor system node"))
	ErrRemoteActorNotFound   = func(addr string) error {
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("remote actor=%s not found", addr))
	}
	ErrRemoteSendFailure = func(err error) error {
		return connect.NewError(connect.CodeInternal, err)
	}
	ErrInstanceNotAnActor = errors.New("failed to create instance. Reason: instance does not implement the Actor interface")
	ErrInvalidInstance    = errors.New("failed to create instance. Reason: invalid instance")
	ErrTypeNotFound       = func(typeName string) error { return fmt.Errorf("typeName=%s not found", typeName) }
	ErrActorNotFound      = errors.New("actor not found")
)
