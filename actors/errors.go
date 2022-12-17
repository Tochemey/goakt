package actors

import "errors"

var (
	ErrNotReady       = errors.New("actor is not ready")
	ErrUnhandled      = errors.New("unhandled message")
	ErrMissingConfig  = errors.New("config is missing")
	ErrUndefinedActor = errors.New("actor is not defined")
	ErrRequestTimeout = errors.New("request timed out")
)
