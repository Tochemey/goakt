/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/pkg/errors"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/actors"
	kactors "github.com/tochemey/goakt/v2/examples/actor-cluster/dnssd/actors"
	samplepb "github.com/tochemey/goakt/v2/examples/protos/samplepb"
	"github.com/tochemey/goakt/v2/examples/protos/samplepb/samplepbconnect"
	"github.com/tochemey/goakt/v2/log"
)

type AccountService struct {
	actorSystem actors.ActorSystem
	logger      log.Logger
	port        int
	server      *http.Server
}

var _ samplepbconnect.AccountServiceHandler = &AccountService{}

// NewAccountService creates an instance of AccountService
func NewAccountService(system actors.ActorSystem, logger log.Logger, port int) *AccountService {
	return &AccountService{
		actorSystem: system,
		logger:      logger,
		port:        port,
	}
}

// CreateAccount helps create an account
func (s *AccountService) CreateAccount(ctx context.Context, c *connect.Request[samplepb.CreateAccountRequest]) (*connect.Response[samplepb.CreateAccountResponse], error) {
	// grab the actual request
	req := c.Msg
	// grab the account id
	accountID := req.GetCreateAccount().GetAccountId()
	// create the pid and send the command create account
	accountEntity := kactors.NewAccountEntity(req.GetCreateAccount().GetAccountId())
	// create the given pid
	pid, err := s.actorSystem.Spawn(ctx, accountID, accountEntity)
	if err != nil {
		return nil, err
	}
	// send the create command to the pid
	reply, err := actors.Ask(ctx, pid, &samplepb.CreateAccount{
		AccountId:      accountID,
		AccountBalance: req.GetCreateAccount().GetAccountBalance(),
	}, time.Second)

	// handle the error
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// pattern match on the reply
	switch x := reply.(type) {
	case *samplepb.Account:
		// return the appropriate response
		return connect.NewResponse(&samplepb.CreateAccountResponse{Account: x}), nil
	default:
		// create the error message to send
		err := fmt.Errorf("invalid reply=%s", reply.ProtoReflect().Descriptor().FullName())
		return nil, connect.NewError(connect.CodeInternal, err)
	}
}

// CreditAccount helps credit a given account
func (s *AccountService) CreditAccount(ctx context.Context, c *connect.Request[samplepb.CreditAccountRequest]) (*connect.Response[samplepb.CreditAccountResponse], error) {
	req := c.Msg
	accountID := req.GetCreditAccount().GetAccountId()

	addr, pid, err := s.actorSystem.ActorOf(ctx, accountID)
	if err != nil {
		// check whether it is not found error
		if !errors.Is(err, actors.ErrActorNotFound(accountID)) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}

		// return not found
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	var message proto.Message
	command := &samplepb.CreditAccount{
		AccountId: accountID,
		Balance:   req.GetCreditAccount().GetBalance(),
	}

	if pid != nil {
		s.logger.Info("actor is found locally...")
		message, err = actors.Ask(ctx, pid, command, time.Second)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	if pid == nil {
		s.logger.Info("actor is not found locally...")
		reply, err := actors.RemoteAsk(ctx, addr, command)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		message, _ = reply.UnmarshalNew()
	}

	switch x := message.(type) {
	case *samplepb.Account:
		return connect.NewResponse(&samplepb.CreditAccountResponse{Account: x}), nil
	default:
		err := fmt.Errorf("invalid reply=%s", message.ProtoReflect().Descriptor().FullName())
		return nil, connect.NewError(connect.CodeInternal, err)
	}
}

// GetAccount helps get an account
func (s *AccountService) GetAccount(ctx context.Context, c *connect.Request[samplepb.GetAccountRequest]) (*connect.Response[samplepb.GetAccountResponse], error) {
	// grab the actual request
	req := c.Msg
	// grab the account id
	accountID := req.GetAccountId()

	// locate the given actor
	addr, pid, err := s.actorSystem.ActorOf(ctx, accountID)
	// handle the error
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var message proto.Message
	command := &samplepb.GetAccount{
		AccountId: accountID,
	}

	if pid != nil {
		s.logger.Info("actor is found locally...")
		message, err = actors.Ask(ctx, pid, command, time.Second)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	if pid == nil {
		s.logger.Info("actor is not found locally...")
		reply, err := actors.RemoteAsk(ctx, addr, command)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		message, _ = reply.UnmarshalNew()
	}

	// pattern match on the reply
	switch x := message.(type) {
	case *samplepb.Account:
		return connect.NewResponse(&samplepb.GetAccountResponse{Account: x}), nil
	default:
		err := fmt.Errorf("invalid reply=%s", message.ProtoReflect().Descriptor().FullName())
		return nil, connect.NewError(connect.CodeInternal, err)
	}
}

// Start starts the service
func (s *AccountService) Start() {
	go func() {
		s.listenAndServe()
	}()
}

// Stop stops the service
func (s *AccountService) Stop(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// listenAndServe starts the http server
func (s *AccountService) listenAndServe() {
	// create a http service mux
	mux := http.NewServeMux()
	// create an interceptor
	interceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		s.logger.Panic(err)
	}
	// create the resource and handler
	path, handler := samplepbconnect.NewAccountServiceHandler(s,
		connect.WithInterceptors(interceptor))
	mux.Handle(path, handler)
	// create the address
	serverAddr := fmt.Sprintf(":%d", s.port)
	// create a http server instance
	server := &http.Server{
		Addr:              serverAddr,
		ReadTimeout:       3 * time.Second,
		ReadHeaderTimeout: time.Second,
		WriteTimeout:      time.Second,
		IdleTimeout:       1200 * time.Second,
		Handler: h2c.NewHandler(mux, &http2.Server{
			IdleTimeout: 1200 * time.Second,
		}),
	}

	// set the server
	s.server = server
	// listen and service requests
	if err := s.server.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			return
		}
		s.logger.Panic(errors.Wrap(err, "failed to start remoting service"))
	}
}
