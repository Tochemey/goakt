/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/actors"
	kactors "github.com/tochemey/goakt/examples/actor-cluster/k8s/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/pb/v1"
	"github.com/tochemey/goakt/examples/protos/pb/v1/samplepbconnect"
	"github.com/tochemey/goakt/log"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
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
	// grab the actual request
	req := c.Msg
	// grab the account id
	accountID := req.GetCreditAccount().GetAccountId()

	// check whether the actor system is running in a cluster
	if !s.actorSystem.InCluster() {
		s.logger.Info("cluster mode not is on....")
		// locate the given actor
		pid, err := s.actorSystem.LocalActor(accountID)
		// handle the error
		if err != nil {
			// check whether it is not found error
			if !errors.Is(err, actors.ErrActorNotFound(accountID)) {
				return nil, connect.NewError(connect.CodeInternal, err)
			}

			// return not found
			return nil, connect.NewError(connect.CodeNotFound, err)
		}

		// defensive programming. making sure pid is defined
		if pid == nil {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}

		// send the create command to the pid
		reply, err := actors.Ask(ctx, pid, &samplepb.CreditAccount{
			AccountId: accountID,
			Balance:   req.GetCreditAccount().GetBalance(),
		}, time.Second)

		// handle the error
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		// pattern match on the reply
		switch x := reply.(type) {
		case *samplepb.Account:
			// return the appropriate response
			return connect.NewResponse(&samplepb.CreditAccountResponse{Account: x}), nil
		default:
			// create the error message to send
			err := fmt.Errorf("invalid reply=%s", reply.ProtoReflect().Descriptor().FullName())
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	// here the actor system is running in a cluster
	addr, err := s.actorSystem.RemoteActor(ctx, accountID)
	// handle the error
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// create the command to send to the remote actor
	command := &samplepb.CreditAccount{
		AccountId: accountID,
		Balance:   req.GetCreditAccount().GetBalance(),
	}
	// send a remote message to the actor
	reply, err := actors.RemoteAsk(ctx, addr, command)
	// handle the error
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	message, _ := reply.UnmarshalNew()
	// pattern match on the reply
	switch x := message.(type) {
	case *samplepb.Account:
		// return the appropriate response
		return connect.NewResponse(&samplepb.CreditAccountResponse{Account: x}), nil
	default:
		// create the error message to send
		err := fmt.Errorf("invalid reply=%s", reply.ProtoReflect().Descriptor().FullName())
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
	addr, _, err := s.actorSystem.ActorOf(ctx, accountID)
	// handle the error
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// send GetAccount message to the actor
	command := &samplepb.GetAccount{
		AccountId: accountID,
	}
	// send a remote message to the actor
	reply, err := actors.RemoteAsk(ctx, addr, command)
	// handle the error
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	message, _ := reply.UnmarshalNew()
	// pattern match on the reply
	switch x := message.(type) {
	case *samplepb.Account:
		// return the appropriate response
		return connect.NewResponse(&samplepb.GetAccountResponse{Account: x}), nil
	default:
		// create the error message to send
		err := fmt.Errorf("invalid reply=%s", reply.ProtoReflect().Descriptor().FullName())
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
	// create the resource and handler
	path, handler := samplepbconnect.NewAccountServiceHandler(s)
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
		s.logger.Panic(errors.Wrap(err, "failed to start remoting service"))
	}
}
