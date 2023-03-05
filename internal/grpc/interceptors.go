package grpc

import (
	"context"

	"github.com/google/uuid"
	grpcRecovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// NewTracingUnaryInterceptor helps gather traces and metrics from any grpc unary server
// request. Make sure to start the TracerProvider to connect to an OLTP connector
func NewTracingUnaryInterceptor(opts ...otelgrpc.Option) grpc.UnaryServerInterceptor {
	return otelgrpc.UnaryServerInterceptor(opts...)
}

// NewTracingStreamInterceptor helps gather traces and metrics from any grpc stream server
// request. Make sure to start the TracerProvider to connect to an OLTP connector
func NewTracingStreamInterceptor(opts ...otelgrpc.Option) grpc.StreamServerInterceptor {
	return otelgrpc.StreamServerInterceptor(opts...)
}

// NewTracingClientUnaryInterceptor helps gather traces and metrics from any grpc unary client
// request. Make sure to start the TracerProvider to connect to an OLTP connector
func NewTracingClientUnaryInterceptor(opts ...otelgrpc.Option) grpc.UnaryClientInterceptor {
	return otelgrpc.UnaryClientInterceptor(opts...)
}

// NewTracingClientStreamInterceptor helps gather traces and metrics from any grpc stream client
// request. Make sure to start the TracerProvider to connect to an OLTP connector
func NewTracingClientStreamInterceptor(opts ...otelgrpc.Option) grpc.StreamClientInterceptor {
	return otelgrpc.StreamClientInterceptor(opts...)
}

// NewRecoveryUnaryInterceptor recovers from an unexpected panic
// Recovery handlers should typically be last in the chain so that other middleware
// (e.g. logging) can operate on the recovered state instead of being directly affected by any panic
func NewRecoveryUnaryInterceptor() grpc.UnaryServerInterceptor {
	// Define custom func to handle panic
	customFunc := func(p interface{}) (err error) {
		return status.Errorf(codes.Unknown, "panic triggered: %v", p)
	}

	opts := []grpcRecovery.Option{
		grpcRecovery.WithRecoveryHandler(customFunc),
	}

	return grpcRecovery.UnaryServerInterceptor(opts...)
}

// NewRecoveryStreamInterceptor recovers from an unexpected panic
// Recovery handlers should typically be last in the chain so that other middleware
// (e.g. logging) can operate on the recovered state instead of being directly affected by any panic
func NewRecoveryStreamInterceptor() grpc.StreamServerInterceptor {
	// Define custom func to handle panic
	customFunc := func(p interface{}) (err error) {
		return status.Errorf(codes.Unknown, "panic triggered: %v", p)
	}

	opts := []grpcRecovery.Option{
		grpcRecovery.WithRecoveryHandler(customFunc),
	}

	return grpcRecovery.StreamServerInterceptor(opts...)
}

// NewRequestIDUnaryServerInterceptor creates a new request ID interceptor.
// This interceptor adds a request ID to each grpc request
func NewRequestIDUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// create the request ID
		requestID := getServerRequestID(ctx)
		// set the context with the newly created request ID
		ctx = context.WithValue(ctx, XRequestIDKey{}, requestID)
		return handler(ctx, req)
	}
}

// NewRequestIDStreamServerInterceptor creates a new request ID interceptor.
// This interceptor adds a request ID to each grpc request
func NewRequestIDStreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		ctx := ss.Context()
		// create the request ID
		requestID := getServerRequestID(ctx)
		// set the context with the newly created request ID
		ctx = context.WithValue(ctx, XRequestIDKey{}, requestID)
		stream := newServerStreamWithContext(ctx, ss)
		return handler(srv, stream)
	}
}

// NewRequestIDUnaryClientInterceptor creates a new request ID unary client interceptor.
// This interceptor adds a request ID to each outgoing context
func NewRequestIDUnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// make a copy of the metadata
		requestMetadata, _ := metadata.FromOutgoingContext(ctx)
		metadataCopy := requestMetadata.Copy()
		// create the request ID
		requestID := getClientRequestID(ctx)
		// set the context with the newly created request ID
		ctx = context.WithValue(ctx, XRequestIDKey{}, requestID)
		// put back the metadata that originally came in
		newCtx := metadata.NewOutgoingContext(ctx, metadataCopy)
		return invoker(newCtx, method, req, reply, cc, opts...)
	}
}

// NewRequestIDStreamClientInterceptor  creates a new request ID stream client interceptor.
// This interceptor adds a request ID to each outgoing context
func NewRequestIDStreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		// make a copy of the metadata
		requestMetadata, _ := metadata.FromOutgoingContext(ctx)
		metadataCopy := requestMetadata.Copy()
		// create the request ID
		requestID := getClientRequestID(ctx)
		// set the context with the newly created request ID
		ctx = context.WithValue(ctx, XRequestIDKey{}, requestID)
		// put back the metadata that originally came in
		newCtx := metadata.NewOutgoingContext(ctx, metadataCopy)
		return streamer(newCtx, desc, cc, method, opts...)
	}
}

// getServerRequestID returns a request ID from gRPC metadata if available in the incoming ctx.
// If the request ID is not available then it is set
func getServerRequestID(ctx context.Context) string {
	// let us check whether the request id is set in the incoming context or not
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return uuid.NewString()
	}
	// the request is set in the incoming context
	// however the request id is empty then we create a new one
	header, ok := md[XRequestIDMetadataKey]
	if !ok || len(header) == 0 {
		return uuid.NewString()
	}
	// return the found request ID
	requestID := header[0]
	if requestID == "" {
		requestID = uuid.NewString()
	}
	return requestID
}

// getClientRequestID returns a request ID from gRPC metadata if available in outgoing ctx.
// If the request ID is not available then it is set
func getClientRequestID(ctx context.Context) string {
	// let us check whether the request id is set in the incoming context or not
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return uuid.NewString()
	}
	// the request is set in the incoming context
	// however the request id is empty then we create a new one
	header, ok := md[XRequestIDMetadataKey]
	if !ok || len(header) == 0 {
		return uuid.NewString()
	}
	// return the found request ID
	requestID := header[0]
	if requestID == "" {
		requestID = uuid.NewString()
	}
	return requestID
}

// create a serverStreamWithContext wrapper around the server stream
// to be able to pass in a context
type serverStreamWithContext struct {
	grpc.ServerStream
	ctx context.Context
}

// Context return the server steam context
func (ss serverStreamWithContext) Context() context.Context {
	return ss.ctx
}

// newServerStreamWithContext returns a grpc server stream with a given context
func newServerStreamWithContext(ctx context.Context, stream grpc.ServerStream) grpc.ServerStream {
	return serverStreamWithContext{
		ServerStream: stream,
		ctx:          ctx,
	}
}
