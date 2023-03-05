package grpc

import (
	"context"
	"crypto/tls"
	"log"
	"net"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// GetInProcessClientConn creates an in-process grpc client
func GetInProcessClientConn(ctx context.Context, listener *bufconn.Listener, options []grpc.DialOption) (*grpc.ClientConn, error) {
	dialOptions := append(options, grpc.WithContextDialer(GetBufDialer(listener)))
	dialOptions = append(dialOptions, grpc.WithTransportCredentials(insecure.NewCredentials())) // Required to always set insecure for in-processing
	conn, err := grpc.DialContext(
		ctx,
		"bufconn",
		dialOptions...,
	)
	return conn, err
}

// GetInProcessServer creates an in-process grpc server
func GetInProcessServer(options []grpc.ServerOption) (*grpc.Server, *bufconn.Listener) {
	bufferSize := 1024 * 1024
	listener := bufconn.Listen(bufferSize)
	srv := grpc.NewServer(options...)
	return srv, listener
}

func GetBufDialer(listener *bufconn.Listener) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, url string) (net.Conn, error) {
		return listener.Dial()
	}
}

// InProcessServer server interface
type InProcessServer interface {
	Start() error
	RegisterService(reg func(*grpc.Server))
	Cleanup()
	GetListener() *bufconn.Listener
}

// InProcessServerBuilder in-processing grpc server builder
type InProcessServerBuilder struct {
	options []grpc.ServerOption
}

// NewInProcessServerBuilder creates an instance of InProcessServerBuilder
func NewInProcessServerBuilder() *InProcessServerBuilder {
	return new(InProcessServerBuilder)
}

// WithOption configures how we set up the connection.
func (sb *InProcessServerBuilder) WithOption(o grpc.ServerOption) *InProcessServerBuilder {
	sb.options = append(sb.options, o)
	return sb
}

// WithStreamInterceptors set a list of interceptors to the Grpc server for stream connection
// By default, gRPC doesn't allow one to have more than one interceptor either on the client nor on the server side.
// By using `grpcMiddleware` we are able to provides convenient method to add a list of interceptors
func (sb *InProcessServerBuilder) WithStreamInterceptors(interceptors ...grpc.StreamServerInterceptor) *InProcessServerBuilder {
	chain := grpc.StreamInterceptor(grpcMiddleware.ChainStreamServer(interceptors...))
	sb.WithOption(chain)
	return sb
}

// WithUnaryInterceptors set a list of interceptors to the Grpc server for unary connection
// By default, gRPC doesn't allow one to have more than one interceptor either on the client nor on the server side.
// By using `grpcMiddleware` we are able to provides convenient method to add a list of interceptors
func (sb *InProcessServerBuilder) WithUnaryInterceptors(interceptors ...grpc.UnaryServerInterceptor) *InProcessServerBuilder {
	chain := grpc.UnaryInterceptor(grpcMiddleware.ChainUnaryServer(interceptors...))
	sb.WithOption(chain)
	return sb
}

// WithTLSCert sets credentials for server connections
func (sb *InProcessServerBuilder) WithTLSCert(cert *tls.Certificate) *InProcessServerBuilder {
	sb.WithOption(grpc.Creds(credentials.NewServerTLSFromCert(cert)))
	return sb
}

// Build is responsible for building a Fiji GRPC server
func (sb *InProcessServerBuilder) Build() InProcessServer {
	server, listener := GetInProcessServer(sb.options)
	return &testServer{server, listener}
}

type testServer struct {
	server   *grpc.Server
	listener *bufconn.Listener
}

// GetListener register the services to the server
func (s *testServer) GetListener() *bufconn.Listener {
	return s.listener
}

// RegisterService register the services to the server
func (s *testServer) RegisterService(reg func(*grpc.Server)) {
	reg(s.server)
}

// Start the GRPC server
func (s *testServer) Start() error {
	go s.serv()
	log.Printf("In processing server started")
	return nil
}

// Cleanup stops the server and close the tcp listener
func (s *testServer) Cleanup() {
	s.server.Stop()
	_ = s.listener.Close()
	log.Println("Server stopped")
}

func (s *testServer) serv() {
	if err := s.server.Serve(s.listener); err != nil {
		log.Fatalf("failed to serve: %+v", err)
	}
}
