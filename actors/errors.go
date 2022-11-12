package actors

import "errors"

var (
	ErrTimeout       = errors.New("request timed out")
	ErrNotReady      = errors.New("actor is not ready")
	ErrUnhandled     = errors.New("unhandled message")
	ErrMissingConfig = errors.New("config is missing")
)
