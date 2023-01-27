package grpc

import (
	"context"

	"github.com/google/uuid"
)

// XRequestIDKey is used to store the x-request-id
// into the grpc context
type XRequestIDKey struct{}

const (
	XRequestIDMetadataKey = "x-request-id"
)

// FromContext return the request ID set in context
func FromContext(ctx context.Context) string {
	id, ok := ctx.Value(XRequestIDKey{}).(string)
	if !ok {
		return ""
	}
	return id
}

// Context sets a requestID into the parent context and return the new
// context that can be used in case there is no request ID. In case the parent context contains a requestID then it is returned
// as the newly created context, otherwise a new context is created with a requestID
func Context(ctx context.Context) context.Context {
	// here the given context contains a request ID no need to set one
	// just return the context
	if requestID := FromContext(ctx); requestID != "" {
		return ctx
	}

	// create a requestID
	requestID := uuid.NewString()
	// set the requestID and return the new context
	return context.WithValue(ctx, XRequestIDKey{}, requestID)
}
