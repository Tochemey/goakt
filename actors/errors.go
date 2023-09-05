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
	ErrClusterNotEnabled      = errors.New("cluster is not enabled")
	ErrUndefinedActor         = errors.New("actor is not defined")
	ErrRequestTimeout         = errors.New("request timed out")
	ErrRemotingNotEnabled     = errors.New("remoting is not enabled")
	ErrInvalidActorSystemNode = connect.NewError(connect.CodeFailedPrecondition, errors.New("invalid actor system node"))
	ErrAddressNotFound        = func(addr string) error {
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("actor=%s not found", addr))
	}
	ErrRemoteSendFailure    = func(err error) error { return connect.NewError(connect.CodeInternal, err) }
	ErrInstanceNotAnActor   = errors.New("failed to create instance. Reason: instance does not implement the Actor interface")
	ErrInvalidInstance      = errors.New("failed to create instance. Reason: invalid instance")
	ErrTypeNotFound         = func(typeName string) error { return fmt.Errorf("typeName=%s not found", typeName) }
	ErrActorNotFound        = errors.New("actor not found")
	ErrMethodCallNotAllowed = errors.New("method call not allowed")
	ErrInvalidRemoteMessage = func(err error) error { return errors.Wrap(err, "imvalid remote message") }
)
