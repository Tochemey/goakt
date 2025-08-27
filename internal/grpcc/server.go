/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package grpcc

import (
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"

	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/log"
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
)

// Server will be implemented by the server
type Server interface {
	Start() error
	Stop() error
	GetListener() net.Listener
	GetServer() *grpc.Server
}

// serviceRegistry.RegisterService will be implemented by any grpc service
type serviceRegistry interface {
	RegisterService(*grpc.Server)
}

type server struct {
	addr             string
	server           *grpc.Server
	listener         net.Listener
	logger           log.Logger
	options          []grpc.ServerOption
	services         []serviceRegistry
	maxReceivMsgSize int
	maxSendMsgSize   int
	mu               *sync.RWMutex
	started          bool
	tlsConfig        *tls.Config
}

var _ Server = (*server)(nil)

func NewServer(addr string, opts ...ServerOption) (Server, error) {
	if addr == "" {
		return nil, ErrEmptyAddress
	}

	s := newDefaultServer(addr)

	// apply functional options
	for _, opt := range opts {
		opt(s)
	}

	// ensure at least one service registered by options
	if len(s.services) == 0 {
		s.logger.Warn("no service has been registered with the grpc server")
		return nil, errors.New("no service has been registered with the grpc server")
	}

	// build final grpc options without mutating the defaults slice
	finalOpts := make([]grpc.ServerOption, 0, len(s.options)+3)
	finalOpts = append(finalOpts, s.options...)
	finalOpts = append(finalOpts, grpc.MaxRecvMsgSize(s.maxReceivMsgSize))
	finalOpts = append(finalOpts, grpc.MaxSendMsgSize(s.maxSendMsgSize))
	if s.tlsConfig != nil {
		finalOpts = append(finalOpts, grpc.Creds(credentials.NewTLS(s.tlsConfig)))
	}

	// create the grpc server
	s.server = grpc.NewServer(finalOpts...)

	// register health check service
	grpc_health_v1.RegisterHealthServer(s.server, health.NewServer())

	// register services
	for _, service := range s.services {
		service.RegisterService(s.server)
	}

	return s, nil
}

// GetServer returns the underlying grpc.Server
// This is useful when one want to use the underlying grpc.Server
// for some registration like metrics, traces and so one
func (s *server) GetServer() *grpc.Server {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.server
}

// GetListener returns the underlying tcp listener
func (s *server) GetListener() net.Listener {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listener
}

// Start the GRPC server and listen to incoming connections.
func (s *server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return errors.New("grpc server already started")
	}

	var err error
	s.listener, err = net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	go s.serv()
	s.started = true
	return nil
}

// Stop will shut down gracefully the running service.
// This is very useful when one wants to control the shutdown
// without waiting for an OS signal. For a long-running process, kindly use
// AwaitTermination after Start
func (s *server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return errors.New("grpc server not started")
	}

	s.started = false
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			return err
		}
	}

	s.server.GracefulStop()
	return nil
}

// serv makes the grpc listener ready to accept connections
func (s *server) serv() {
	if err := s.server.Serve(s.listener); err != nil && !errors.Is(err, net.ErrClosed) {
		s.logger.Fatal(err)
	}
}

func newDefaultServer(addr string) *server {
	return &server{
		addr:             addr,
		mu:               &sync.RWMutex{},
		started:          false,
		logger:           log.DefaultLogger,
		maxSendMsgSize:   10 * size.MB,
		maxReceivMsgSize: 10 * size.MB,
		options: []grpc.ServerOption{
			grpc.KeepaliveParams(keepalive.ServerParameters{
				MaxConnectionIdle:     0,
				MaxConnectionAge:      MaxConnectionAge,
				MaxConnectionAgeGrace: MaxConnectionAgeGrace,
				Time:                  KeepAliveTime,
				Timeout:               0,
			}),
		},
		services: []serviceRegistry{},
	}
}
