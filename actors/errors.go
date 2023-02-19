package actors

import (
	"fmt"

	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrInvalidActorSystemName       = errors.New("invalid ActorSystem name, must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')")
	ErrNotReady                     = errors.New("actor is not ready")
	ErrUnhandled                    = errors.New("unhandled message")
	ErrMissingConfig                = errors.New("config is missing")
	ErrUndefinedActor               = errors.New("actor is not defined")
	ErrRequestTimeout               = errors.New("request timed out")
	ErrEmptyBehavior                = errors.New("no behavior defined")
	ErrRemoteSendInvalidActorSystem = status.Error(codes.FailedPrecondition, "invalid actor system") // nolint
	ErrRemoteSendInvalidNode        = status.Error(codes.FailedPrecondition, "invalid actor system node")
	ErrRemoteActorNotFound          = func(addr string) error { return status.Errorf(codes.NotFound, "remote actor=%s not found", addr) }
	ErrRemoteSendFailure            = func(err error) error { return status.Error(codes.Internal, err.Error()) }
	ErrInstanceNotAnActor           = errors.New("failed to create instance. Reason: instance does not implement the Actor interface")
	ErrInvalidInstance              = errors.New("failed to create instance. Reason: invalid instance")
	ErrTypeNotFound                 = func(typeName string) error { return fmt.Errorf("typeName=%s not found", typeName) }
)
