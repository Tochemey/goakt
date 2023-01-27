package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// ConnectionBuilder is a builder to create GRPC connection to the GRPC Server
type ConnectionBuilder interface {
	WithOptions(opts ...grpc.DialOption)
	WithInsecure()
	WithUnaryInterceptors(interceptors []grpc.UnaryClientInterceptor)
	WithStreamInterceptors(interceptors []grpc.StreamClientInterceptor)
	WithKeepAliveParams(params keepalive.ClientParameters)
	GetConn(ctx context.Context, addr string) (*grpc.ClientConn, error)
	GetTLSConn(ctx context.Context, addr string) (*grpc.ClientConn, error)
}

// ClientBuilder is grpc client builder
type ClientBuilder struct {
	options              []grpc.DialOption
	transportCredentials credentials.TransportCredentials
}

// NewClientBuilder creates an instance of ClientBuilder
func NewClientBuilder() *ClientBuilder {
	return &ClientBuilder{}
}

// WithOptions set dial options
func (b *ClientBuilder) WithOptions(opts ...grpc.DialOption) *ClientBuilder {
	b.options = append(b.options, opts...)
	return b
}

// WithInsecure set the connection as insecure
func (b *ClientBuilder) WithInsecure() *ClientBuilder {
	b.options = append(b.options, grpc.WithTransportCredentials(insecure.NewCredentials()))
	return b
}

// WithBlock the dialing blocks until the  underlying connection is up.
// Without this, Dial returns immediately and connecting the server happens in background.
func (b *ClientBuilder) WithBlock() *ClientBuilder {
	b.options = append(b.options, grpc.WithBlock())
	return b
}

// WithKeepAliveParams set the keep alive params
// ClientParameters is used to set keepalive parameters on the client-side.
// These configure how the client will actively probe to notice when a
// connection is broken and send pings so intermediaries will be aware of the
// liveness of the connection. Make sure these parameters are set in
// coordination with the keepalive policy on the server, as incompatible
// settings can result in closing of connection.
func (b *ClientBuilder) WithKeepAliveParams(params keepalive.ClientParameters) *ClientBuilder {
	keepAlive := grpc.WithKeepaliveParams(params)
	b.options = append(b.options, keepAlive)
	return b
}

// WithUnaryInterceptors set a list of interceptors to the Grpc client for unary connection
// By default, gRPC doesn't allow one to have more than one interceptor either on the client nor on the server side.
// By using `grpc_middleware` we are able to provides convenient method to add a list of interceptors
func (b *ClientBuilder) WithUnaryInterceptors(interceptors ...grpc.UnaryClientInterceptor) *ClientBuilder {
	b.options = append(b.options, grpc.WithUnaryInterceptor(grpcMiddleware.ChainUnaryClient(interceptors...)))
	return b
}

// WithStreamInterceptors set a list of interceptors to the Grpc client for stream connection
// By default, gRPC doesn't allow one to have more than one interceptor either on the client nor on the server side.
// By using `grpc_middleware` we are able to provides convenient method to add a list of interceptors
func (b *ClientBuilder) WithStreamInterceptors(interceptors ...grpc.StreamClientInterceptor) *ClientBuilder {
	b.options = append(b.options, grpc.WithStreamInterceptor(grpcMiddleware.ChainStreamClient(interceptors...)))
	return b
}

// WithClientTransportCredentials builds transport credentials for a gRPC client using the given properties.
func (b *ClientBuilder) WithClientTransportCredentials(insecureSkipVerify bool, certPool *x509.CertPool) *ClientBuilder {
	var tlsConf tls.Config

	if insecureSkipVerify {
		tlsConf.InsecureSkipVerify = true
		b.transportCredentials = credentials.NewTLS(&tlsConf)
		return b
	}

	tlsConf.RootCAs = certPool
	b.transportCredentials = credentials.NewTLS(&tlsConf)
	return b
}

// WithDefaultUnaryInterceptors sets the default unary interceptors for the grpc server
func (b *ClientBuilder) WithDefaultUnaryInterceptors() *ClientBuilder {
	return b.WithUnaryInterceptors(
		NewRequestIDUnaryClientInterceptor(),
		NewTracingClientUnaryInterceptor(),
	)
}

// WithDefaultStreamInterceptors sets the default stream interceptors for the grpc server
func (b *ClientBuilder) WithDefaultStreamInterceptors() *ClientBuilder {
	return b.WithStreamInterceptors(
		NewRequestIDStreamClientInterceptor(),
		NewTracingClientStreamInterceptor(),
	)
}

// GetConn returns the client connection to the server
func (b *ClientBuilder) GetConn(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	if addr == "" {
		return nil, fmt.Errorf("target connection parameter missing. address = %s", addr)
	}
	cc, err := grpc.DialContext(ctx, addr, b.options...)

	if err != nil {
		return nil, fmt.Errorf("unable to connect to client. address = %s. error = %+v", addr, err)
	}
	return cc, nil
}

// GetTLSConn returns client connection to the server
func (b *ClientBuilder) GetTLSConn(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	b.options = append(b.options, grpc.WithTransportCredentials(b.transportCredentials))
	cc, err := grpc.DialContext(
		ctx,
		addr,
		b.options...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get tls conn. Unable to connect to client. address = %s: %w", addr, err)
	}
	return cc, nil
}

// GetClientConn return a grpc client connection
func GetClientConn(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	// create the client builder
	clientBuilder := NewClientBuilder().
		WithDefaultUnaryInterceptors().
		WithDefaultStreamInterceptors().
		WithInsecure().
		WithKeepAliveParams(keepalive.ClientParameters{
			Time:                1200 * time.Second,
			PermitWithoutStream: true,
		})
	// get the gRPC client connection
	conn, err := clientBuilder.GetConn(ctx, addr)
	// handle the connection error
	if err != nil {
		return nil, errors.Wrap(err, "failed to create grpc service client")
	}
	// return the client connection created
	return conn, nil
}
