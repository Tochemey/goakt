package raft

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/bufbuild/connect-go"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/internal/goakt/v1/goaktv1connect"
	"github.com/tochemey/goakt/internal/telemetry"
	goaktlog "github.com/tochemey/goakt/log"
	"go.etcd.io/etcd/raft/v3/raftpb"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// StoreService implements the raft api service
type StoreService struct {
	store       *Store
	confChangeC chan<- raftpb.ConfChange
	port        int
	log         *goaktlog.Log
}

// enforce compiler errors
var _ goaktv1connect.StoreServiceHandler = &StoreService{}

// NewStoreService creates an instance of StoreService
func NewStoreService(kv *Store, port int, confChangeC chan<- raftpb.ConfChange, log *goaktlog.Log) *StoreService {
	return &StoreService{
		store:       kv,
		port:        port,
		log:         log,
		confChangeC: confChangeC,
	}
}

// ListenAndServe starts the StoreService
func (s *StoreService) ListenAndServe(errorC <-chan error) {
	// create a http server mux
	mux := http.NewServeMux()
	// create the resource and handler
	path, handler := goaktv1connect.NewStoreServiceHandler(
		s,
		// TODO add interceptors
	)
	mux.Handle(path, handler)
	// create the address
	serverAddr := fmt.Sprintf(":%s", strconv.Itoa(s.port))
	// create a http server instance
	// TODO revisit the timeouts
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
	}

	// listen and serve
	go func() {
		if err := server.ListenAndServe(); err != nil {
			// FIXME fix log
			s.log.Fatal(err)
		}
	}()

	// exit when raft goes down
	if err, ok := <-errorC; ok {
		s.log.Fatal(err)
	}
}

// PutActor persists an actor information in the cluster
func (s *StoreService) PutActor(ctx context.Context, c *connect.Request[goaktpb.PutActorRequest]) (*connect.Response[goaktpb.PutActorResponse], error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "PutActor")
	defer span.End()

	// grab the actor from the request
	actor := c.Msg.GetActor()
	// persist the actor onto the store
	s.store.Propose(actor)
	return &connect.Response[goaktpb.PutActorResponse]{}, nil
}

// GetActor retrieves an actor information in the cluster
func (s *StoreService) GetActor(ctx context.Context, c *connect.Request[goaktpb.GetActorRequest]) (*connect.Response[goaktpb.GetActorResponse], error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "GetActor")
	defer span.End()

	// grab the actor name from the request
	actorName := c.Msg.GetActorName()

	// perform a lookup in the store
	actor, ok := s.store.Lookup(actorName)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("actor=%s not found", actorName))
	}
	// send the response
	return connect.NewResponse(&goaktpb.GetActorResponse{Actor: actor}), nil
}
