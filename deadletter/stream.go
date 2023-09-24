package deadletter

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	deadletterpb "github.com/tochemey/goakt/pb/deadletter/v1"
	"github.com/tochemey/goakt/pb/deadletter/v1/deadletterpbconnect"
	"github.com/tochemey/goakt/telemetry"
	"go.uber.org/atomic"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const (
	DefaultDeadletterQueueCapacity = 1_000
)

// Stream implements the DeadletterSubscriptionService
type Stream struct {
	// the underlying queue
	queue *queue
	// Specifies the streaming service port
	port int32
	// Specifies the streaming service host
	host string
	// Specifies the streaming service server
	server *http.Server
	// stream engine logger
	logger log.Logger
	// Specifies the telemetry config
	telemetry   *telemetry.Telemetry
	started     atomic.Bool
	stopTimeout time.Duration
	capacity    int
	// help protect some the fields to set
	sem sync.Mutex
}

// NewStream creates an instance of deadletter Stream Service
func NewStream(host string, port int32, opts ...Option) *Stream {
	// create an instance of the queue
	q := newQueue(DefaultDeadletterQueueCapacity)
	// create the stream instance and return it
	stream := &Stream{
		queue:       q,
		port:        port,
		host:        host,
		logger:      log.DefaultLogger,
		telemetry:   telemetry.New(),
		stopTimeout: 30 * time.Second,
		sem:         sync.Mutex{},
	}
	// set the started to false
	stream.started.Store(false)
	// apply the various options
	for _, opt := range opts {
		opt.Apply(stream)
	}

	return stream
}

// Start starts the Go-Akt deadletter streaming engine
func (s *Stream) Start(ctx context.Context) {
	// add some logging information
	s.logger.Info("starting Go-Akt deadletter streaming engine...")
	// create a http service mux
	mux := http.NewServeMux()
	// create the interceptors
	interceptor := otelconnect.NewInterceptor(
		otelconnect.WithTracerProvider(s.telemetry.TracerProvider),
		otelconnect.WithMeterProvider(s.telemetry.MeterProvider),
	)
	// create the resource and handler
	path, handler := deadletterpbconnect.NewDeadletterSubscriptionServiceHandler(
		s,
		connect.WithInterceptors(interceptor),
	)
	mux.Handle(path, handler)
	// create the address
	serverAddr := fmt.Sprintf("%s:%d", s.host, s.port)

	// create a http service instance
	// reference: https://adam-p.ca/blog/2022/01/golang-http-server-timeouts/
	server := &http.Server{
		Addr: serverAddr,
		// The maximum duration for reading the entire request, including the body.
		// It’s implemented in net/http by calling SetReadDeadline immediately after Accept
		// ReadTimeout := handler_timeout + ReadHeaderTimeout + wiggle_room
		ReadTimeout: 3 * time.Second,
		// ReadHeaderTimeout is the amount of time allowed to read request headers
		ReadHeaderTimeout: time.Second,
		// WriteTimeout is the maximum duration before timing out writes of the response.
		// It is reset whenever a new request’s header is read.
		// This effectively covers the lifetime of the ServeHTTP handler stack
		WriteTimeout: time.Second,
		// IdleTimeout is the maximum amount of time to wait for the next request when keep-alive are enabled.
		// If IdleTimeout is zero, the value of ReadTimeout is used. Not relevant to request timeouts
		IdleTimeout: 1200 * time.Second,
		// For gRPC clients, it's convenient to support HTTP/2 without TLS. You can
		// avoid x/net/http2 by using http.ListenAndServeTLS.
		Handler: h2c.NewHandler(mux, &http2.Server{
			IdleTimeout: 1200 * time.Second,
		}),
		// Set the base context to incoming context of the system
		BaseContext: func(listener net.Listener) context.Context {
			return ctx
		},
	}
	// listen and service requests
	go func() {
		if err := server.ListenAndServe(); err != nil {
			// check the error type
			if !errors.Is(err, http.ErrServerClosed) {
				s.logger.Fatal(errors.Wrap(err, "failed to start Go-Akt deadletter streaming engine"))
			}
		}
	}()

	// acquire the lock
	s.sem.Lock()
	// set the server
	s.server = server
	// set started to True
	s.started.Store(true)
	// release the lock
	s.sem.Unlock()

	// add some logging information
	s.logger.Info("Go-Akt deadletter streaming engine started...:)")
}

// Stop stops the Go-Akt deadletter streaming engine
func (s *Stream) Stop(ctx context.Context) {
	// check whether the stream engine has started or not
	if !s.started.Load() {
		return
	}

	// set started to false
	defer func() {
		s.started.Store(false)
	}()

	// create a cancellation context to gracefully shutdown
	ctx, cancel := context.WithTimeout(ctx, s.stopTimeout)
	defer cancel()
	// stop the server
	if err := s.server.Shutdown(ctx); err != nil {
		s.logger.Fatal(errors.Wrap(err, "failed stop Go-Akt deadletter streaming engine"))
		return
	}

	// reset the queue
	s.sem.Lock()
	s.queue.Shutdown()
	s.sem.Unlock()
}

// Subscribe helps any service to subscribe to the deadletter stream
func (s *Stream) Subscribe(ctx context.Context, request *connect.Request[deadletterpb.SubscribeRequest], serverStream *connect.ServerStream[deadletterpb.SubscribeResponse]) error {
	// set the actor path with the remoting is enabled
	if !s.started.Load() {
		return errors.New("Go-Akt deadletter streaming engine has not started")
	}

	// get a context log
	logger := s.logger
	// add some debug logger here
	logger.Debugf("received a deadletter subscription from=(%s)", request.Peer().Addr)
	// subscribe the given client
	subscription := s.queue.Subscribe()
	// start consuming the dead letters
	for {
		select {
		case <-ctx.Done():
			// add some logging information
			s.logger.Infof("client=(%s) connection is closed", serverStream.Conn().Peer().Addr)
			// unsubscribe the client
			s.queue.Unsubscribe(subscription)
			// return
			return nil
		case deadletter := <-subscription:
			// send the deadletter to the subscriber
			if err := serverStream.Send(&deadletterpb.SubscribeResponse{Deadletter: deadletter}); err != nil {
				// add some error log
				s.logger.Error(errors.Wrapf(err, "failed to send deadletter to=(%s)", serverStream.Conn().Peer().Addr))
			}
		}
	}
}
