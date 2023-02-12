package actors

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrNotReady                     = errors.New("actor is not ready")
	ErrUnhandled                    = errors.New("unhandled message")
	ErrMissingConfig                = errors.New("config is missing")
	ErrUndefinedActor               = errors.New("actor is not defined")
	ErrRequestTimeout               = errors.New("request timed out")
	ErrEmptyBehavior                = errors.New("no behavior defined")
	ErrRemoteSendInvalidActorSystem = status.Error(codes.FailedPrecondition, "invalid actor system")
	ErrRemoteSendInvalidNode        = status.Error(codes.FailedPrecondition, "invalid actor system node")
	ErrRemoteActorNotFound          = func(addr string) error { return status.Errorf(codes.NotFound, "remote actor=%s not found", addr) }
	ErrRemoteSendFailure            = func(err error) error { return status.Error(codes.Internal, err.Error()) }
)
