package cluster

import "github.com/pkg/errors"

var (
	// ErrActorNotFound is return when an actor is not found
	ErrActorNotFound = errors.New("actor not found")
)
