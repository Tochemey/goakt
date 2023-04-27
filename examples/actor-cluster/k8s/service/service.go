package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/bufbuild/connect-go"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/actors"
	"github.com/tochemey/goakt/examples/actor-cluster/k8s/entities"
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
	accountEntity := entities.NewAccountEntity(req.GetCreateAccount().GetAccountId())
	// create the given pid
	pid := s.actorSystem.StartActor(ctx, accountID, accountEntity)
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
	// create a variable to continue the search
	// TODO provide to the actor system a Lookup method that can find an actor both locally or in the cluster when cluster is enabled
	islocal := true

	// locate the given actor
	pid, err := s.actorSystem.GetLocalActor(ctx, accountID)
	// handle the error
	if err != nil {
		// check whether it is not found error
		if errors.Is(err, actors.ErrActorNotFound) {
			islocal = false
		} else {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	// in case we find the actor locally we proceed
	if islocal {
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

	// here we could not find the actor locally then we need to lookup in the cluster
	// NOTE: yeah we could have looked it up directly in the cluster. Just that this will be a bit expensive if the actor can be locally found
	// TODO: check the traces and see the performance
	addr, err := s.actorSystem.GetRemoteActor(ctx, accountID)
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

// GetAccount helps get an account
func (s *AccountService) GetAccount(ctx context.Context, c *connect.Request[samplepb.GetAccountRequest]) (*connect.Response[samplepb.GetAccountResponse], error) {
	//TODO implement me
	panic("implement me")
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

// listenAndServe starts the http2 server
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
