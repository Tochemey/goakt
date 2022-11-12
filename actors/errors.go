package actors

import "errors"

var (
	ErrTimeout                     = errors.New("request timed out")
	ErrNotReady                    = errors.New("actor is not ready")
	ErrUnhandled                   = errors.New("unhandled message")
	ErrActorSystemNameRequired     = errors.New("actor system is required")
	ErrActorSystemNodeAddrRequired = errors.New("actor system node address is required")
)
