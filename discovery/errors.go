package discovery

import "github.com/pkg/errors"

var (
	// ErrAlreadyInitialized is used when attempting to re-initialize the discovery provider
	ErrAlreadyInitialized = errors.New("provider already initialized")
	// ErrNotInitialized is used when the provider is not initialized
	ErrNotInitialized = errors.New("provider not initialized")
	// ErrAlreadyRegistered is used when attempting to re-register the provider
	ErrAlreadyRegistered = errors.New("provider already registered")
)
