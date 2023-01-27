package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

const (
	// MaxConnectionAge is the duration a connection may exist before shutdown
	MaxConnectionAge = 600 * time.Second
	// MaxConnectionAgeGrace is the maximum duration a
	// connection will be kept alive for outstanding RPCs to complete
	MaxConnectionAgeGrace = 60 * time.Second
	// KeepAliveTime is the period after which a keepalive ping is sent on the
	// transport
	KeepAliveTime = 1200 * time.Second

	// default metrics port
	defaultMetricsPort = 9102
	// default grpc port
	defaultGrpcPort = 50051
)

// ShutdownHook is used to perform some cleaning before stopping
// the long-running grpcServer
type ShutdownHook func()

// serviceRegistry.RegisterService will be implemented by any grpc service
type serviceRegistry interface {
	RegisterService(*grpc.Server)
}

// Server will be implemented by the grpcServer
type Server interface {
	Start(ctx context.Context)
	Stop(ctx context.Context)
	AwaitTermination(ctx context.Context)
	GetListener() net.Listener
	GetServer() *grpc.Server
}

type grpcServer struct {
	addr          string
	server        *grpc.Server
	listener      net.Listener
	logger        log.Logger
	traceProvider *TraceProvider
	shutdownHook  ShutdownHook
}

var _ Server = (*grpcServer)(nil)

// GetServer returns the underlying grpc.Server
// This is useful when one want to use the underlying grpc.Server
// for some registration like metrics, traces and so one
func (s *grpcServer) GetServer() *grpc.Server {
	return s.server
}

// GetListener returns the underlying tcp listener
func (s *grpcServer) GetListener() net.Listener {
	return s.listener
}

// Start the GRPC server and listen to incoming connections.
// It will panic in case of error
func (s *grpcServer) Start(ctx context.Context) {
	// let us register the tracer
	if s.traceProvider != nil {
		err := s.traceProvider.Register(ctx)
		if err != nil {
			s.logger.Fatal(errors.Wrap(err, errMsgTracerRegistrationFailure))
		}
	}

	var err error
	s.listener, err = net.Listen("tcp", s.addr)

	if err != nil {
		s.logger.Fatal(errors.Wrap(err, "failed to listen"))
	}

	go s.serv()

	s.logger.Infof("gRPC Server started on %s ", s.addr)
}

// Stop will shut down gracefully the running service.
// This is very useful when one wants to control the shutdown
// without waiting for an OS signal. For a long-running process, kindly use
// AwaitTermination after Start
func (s *grpcServer) Stop(ctx context.Context) {
	s.cleanup(ctx)
	if s.shutdownHook != nil {
		s.shutdownHook()
	}
}

// AwaitTermination makes the program wait for the signal termination
// Valid signal termination (SIGINT, SIGTERM). This function should succeed Start.
func (s *grpcServer) AwaitTermination(ctx context.Context) {
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal
	s.cleanup(ctx)
	if s.shutdownHook != nil {
		s.shutdownHook()
	}
}

// serv makes the grpc listener ready to accept connections
func (s *grpcServer) serv() {
	if err := s.server.Serve(s.listener); err != nil {
		s.logger.Panic(errors.Wrap(err, errMsgListenerServiceFailure))
	}
}

// cleanup stops the OTLP tracer and the metrics server and gracefully shutdowns the grpc server
// It stops the server from accepting new connections and RPCs and blocks until all the pending RPCs are
// finished and closes the underlying listener.
func (s *grpcServer) cleanup(ctx context.Context) {
	// stop the tracing service
	if s.traceProvider != nil {
		err := s.traceProvider.Deregister(ctx)
		if err != nil {
			s.logger.Error(errors.Wrap(err, errMsgTracerDeregistrationFailure))
		}
	}

	s.logger.Info("Stopping the server")
	s.server.GracefulStop()
	s.logger.Info("Server stopped")
}

// ServerBuilder helps build a grpc grpcServer
type ServerBuilder struct {
	options           []grpc.ServerOption
	services          []serviceRegistry
	enableReflection  bool
	enableHealthCheck bool
	metricsEnabled    bool
	tracingEnabled    bool
	serviceName       string
	grpcPort          int
	grpcHost          string
	traceURL          string

	shutdownHook ShutdownHook
	isBuilt      bool

	rwMutex *sync.RWMutex
}

// NewServerBuilder creates an instance of ServerBuilder
func NewServerBuilder() *ServerBuilder {
	return &ServerBuilder{
		grpcPort: defaultGrpcPort,
		isBuilt:  false,
		rwMutex:  &sync.RWMutex{},
	}
}

// WithShutdownHook sets the shutdown hook
func (sb *ServerBuilder) WithShutdownHook(fn ShutdownHook) *ServerBuilder {
	sb.shutdownHook = fn
	return sb
}

// WithPort sets the grpc service port
func (sb *ServerBuilder) WithPort(port int) *ServerBuilder {
	sb.grpcPort = port
	return sb
}

// WithHost sets the grpc service host
func (sb *ServerBuilder) WithHost(host string) *ServerBuilder {
	sb.grpcHost = host
	return sb
}

// WithMetricsEnabled enable grpc metrics
func (sb *ServerBuilder) WithMetricsEnabled(enabled bool) *ServerBuilder {
	sb.metricsEnabled = enabled
	return sb
}

// WithTracingEnabled enables tracing
func (sb *ServerBuilder) WithTracingEnabled(enabled bool) *ServerBuilder {
	sb.tracingEnabled = enabled
	return sb
}

// WithTraceURL sets the tracing URL
func (sb *ServerBuilder) WithTraceURL(traceURL string) *ServerBuilder {
	sb.traceURL = traceURL
	return sb
}

// WithOption adds a grpc service option
func (sb *ServerBuilder) WithOption(o grpc.ServerOption) *ServerBuilder {
	sb.options = append(sb.options, o)
	return sb
}

// WithService registers service with gRPC grpcServer
func (sb *ServerBuilder) WithService(service serviceRegistry) *ServerBuilder {
	sb.services = append(sb.services, service)
	return sb
}

// WithServiceName sets the service name
func (sb *ServerBuilder) WithServiceName(serviceName string) *ServerBuilder {
	sb.serviceName = serviceName
	return sb
}

// WithReflection enables the reflection
// gRPC RunnableService Reflection provides information about publicly-accessible gRPC services on a grpcServer,
// and assists clients at runtime to construct RPC requests and responses without precompiled service information.
// It is used by gRPC CLI, which can be used to introspect grpcServer protos and send/receive test RPCs.
// Warning! We should not have this enabled in production
func (sb *ServerBuilder) WithReflection(enabled bool) *ServerBuilder {
	sb.enableReflection = enabled
	return sb
}

// WithHealthCheck enables the default health check service
func (sb *ServerBuilder) WithHealthCheck(enabled bool) *ServerBuilder {
	sb.enableHealthCheck = enabled
	return sb
}

// WithKeepAlive is used to set keepalive and max-age parameters on the grpcServer-side.
func (sb *ServerBuilder) WithKeepAlive(serverParams keepalive.ServerParameters) *ServerBuilder {
	keepAlive := grpc.KeepaliveParams(serverParams)
	sb.WithOption(keepAlive)
	return sb
}

// WithDefaultKeepAlive is used to set the default keep alive parameters on the grpcServer-side
func (sb *ServerBuilder) WithDefaultKeepAlive() *ServerBuilder {
	return sb.WithKeepAlive(keepalive.ServerParameters{
		MaxConnectionIdle:     0,
		MaxConnectionAge:      MaxConnectionAge,
		MaxConnectionAgeGrace: MaxConnectionAgeGrace,
		Time:                  KeepAliveTime,
		Timeout:               0,
	})
}

// WithStreamInterceptors set a list of interceptors to the Grpc grpcServer for stream connection
// By default, gRPC doesn't allow one to have more than one interceptor either on the client nor on the grpcServer side.
// By using `grpcMiddleware` we are able to provides convenient method to add a list of interceptors
func (sb *ServerBuilder) WithStreamInterceptors(interceptors ...grpc.StreamServerInterceptor) *ServerBuilder {
	chain := grpc.StreamInterceptor(grpcMiddleware.ChainStreamServer(interceptors...))
	sb.WithOption(chain)
	return sb
}

// WithUnaryInterceptors set a list of interceptors to the Grpc grpcServer for unary connection
// By default, gRPC doesn't allow one to have more than one interceptor either on the client nor on the grpcServer side.
// By using `grpc_middleware` we are able to provides convenient method to add a list of interceptors
func (sb *ServerBuilder) WithUnaryInterceptors(interceptors ...grpc.UnaryServerInterceptor) *ServerBuilder {
	chain := grpc.UnaryInterceptor(grpcMiddleware.ChainUnaryServer(interceptors...))
	sb.WithOption(chain)
	return sb
}

// WithTLSCert sets credentials for grpcServer connections
func (sb *ServerBuilder) WithTLSCert(cert *tls.Certificate) *ServerBuilder {
	sb.WithOption(grpc.Creds(credentials.NewServerTLSFromCert(cert)))
	return sb
}

// WithDefaultUnaryInterceptors sets the default unary interceptors for the grpc grpcServer
func (sb *ServerBuilder) WithDefaultUnaryInterceptors() *ServerBuilder {
	return sb.WithUnaryInterceptors(
		NewRequestIDUnaryServerInterceptor(),
		NewTracingUnaryInterceptor(),
		NewRecoveryUnaryInterceptor(),
	)
}

// WithDefaultStreamInterceptors sets the default stream interceptors for the grpc grpcServer
func (sb *ServerBuilder) WithDefaultStreamInterceptors() *ServerBuilder {
	return sb.WithStreamInterceptors(
		NewRequestIDStreamServerInterceptor(),
		NewTracingStreamInterceptor(),
		NewRecoveryStreamInterceptor(),
	)
}

// Build is responsible for building a GRPC grpcServer
func (sb *ServerBuilder) Build() (Server, error) {
	// check whether the builder has already been used
	sb.rwMutex.Lock()
	defer sb.rwMutex.Unlock()
	if sb.isBuilt {
		return nil, errMsgCannotUseSameBuilder
	}

	// create the grpc server
	srv := grpc.NewServer(sb.options...)

	// create the grpc server
	addr := fmt.Sprintf("%s:%d", sb.grpcHost, sb.grpcPort)
	grpcServer := &grpcServer{
		addr:         addr,
		server:       srv,
		shutdownHook: sb.shutdownHook,
	}

	// register services
	for _, service := range sb.services {
		service.RegisterService(srv)
	}

	// set reflection when enable
	if sb.enableReflection {
		reflection.Register(srv)
	}

	// register health check if enabled
	if sb.enableHealthCheck {
		grpc_health_v1.RegisterHealthServer(srv, health.NewServer())
	}

	// register tracing if enabled
	if sb.tracingEnabled {
		if sb.traceURL == "" {
			return nil, errMissingTraceURL
		}

		if sb.serviceName == "" {
			return nil, errMissingServiceName
		}
		grpcServer.traceProvider = NewTraceProvider(sb.traceURL, sb.serviceName)
	}

	// set isBuild
	sb.isBuilt = true

	return grpcServer, nil
}

// GetServerBuilder returns a grpcserver.ServerBuilder given a grpc config
func GetServerBuilder(cfg *Config) *ServerBuilder {
	// build the grpc server
	return NewServerBuilder().
		WithReflection(cfg.EnableReflection).
		WithDefaultUnaryInterceptors().
		WithDefaultStreamInterceptors().
		WithTracingEnabled(cfg.TraceEnabled).
		WithTraceURL(cfg.TraceURL).
		WithServiceName(cfg.ServiceName).
		WithPort(cfg.GrpcPort).
		WithHost(cfg.GrpcHost)
}
