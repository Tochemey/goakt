// Code generated by protoc-gen-connect-go. DO NOT EDIT.
//
// Source: pb/v1/service.proto

package samplepbconnect

import (
	connect "connectrpc.com/connect"
	context "context"
	errors "errors"
	v1 "github.com/tochemey/goakt/examples/protos/pb/v1"
	http "net/http"
	strings "strings"
)

// This is a compile-time assertion to ensure that this generated file and the connect package are
// compatible. If you get a compiler error that this constant is not defined, this code was
// generated with a version of connect newer than the one compiled into your binary. You can fix the
// problem by either regenerating this code with an older version of connect or updating the connect
// version compiled into your binary.
const _ = connect.IsAtLeastVersion1_13_0

const (
	// AccountServiceName is the fully-qualified name of the AccountService service.
	AccountServiceName = "sample.v1.AccountService"
)

// These constants are the fully-qualified names of the RPCs defined in this package. They're
// exposed at runtime as Spec.Procedure and as the final two segments of the HTTP route.
//
// Note that these are different from the fully-qualified method names used by
// google.golang.org/protobuf/reflect/protoreflect. To convert from these constants to
// reflection-formatted method names, remove the leading slash and convert the remaining slash to a
// period.
const (
	// AccountServiceCreateAccountProcedure is the fully-qualified name of the AccountService's
	// CreateAccount RPC.
	AccountServiceCreateAccountProcedure = "/sample.v1.AccountService/CreateAccount"
	// AccountServiceCreditAccountProcedure is the fully-qualified name of the AccountService's
	// CreditAccount RPC.
	AccountServiceCreditAccountProcedure = "/sample.v1.AccountService/CreditAccount"
	// AccountServiceGetAccountProcedure is the fully-qualified name of the AccountService's GetAccount
	// RPC.
	AccountServiceGetAccountProcedure = "/sample.v1.AccountService/GetAccount"
)

// These variables are the protoreflect.Descriptor objects for the RPCs defined in this package.
var (
	accountServiceServiceDescriptor             = v1.File_pb_v1_service_proto.Services().ByName("AccountService")
	accountServiceCreateAccountMethodDescriptor = accountServiceServiceDescriptor.Methods().ByName("CreateAccount")
	accountServiceCreditAccountMethodDescriptor = accountServiceServiceDescriptor.Methods().ByName("CreditAccount")
	accountServiceGetAccountMethodDescriptor    = accountServiceServiceDescriptor.Methods().ByName("GetAccount")
)

// AccountServiceClient is a client for the sample.v1.AccountService service.
type AccountServiceClient interface {
	CreateAccount(context.Context, *connect.Request[v1.CreateAccountRequest]) (*connect.Response[v1.CreateAccountResponse], error)
	CreditAccount(context.Context, *connect.Request[v1.CreditAccountRequest]) (*connect.Response[v1.CreditAccountResponse], error)
	GetAccount(context.Context, *connect.Request[v1.GetAccountRequest]) (*connect.Response[v1.GetAccountResponse], error)
}

// NewAccountServiceClient constructs a client for the sample.v1.AccountService service. By default,
// it uses the Connect protocol with the binary Protobuf Codec, asks for gzipped responses, and
// sends uncompressed requests. To use the gRPC or gRPC-Web protocols, supply the connect.WithGRPC()
// or connect.WithGRPCWeb() options.
//
// The URL supplied here should be the base URL for the Connect or gRPC server (for example,
// http://api.acme.com or https://acme.com/grpc).
func NewAccountServiceClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) AccountServiceClient {
	baseURL = strings.TrimRight(baseURL, "/")
	return &accountServiceClient{
		createAccount: connect.NewClient[v1.CreateAccountRequest, v1.CreateAccountResponse](
			httpClient,
			baseURL+AccountServiceCreateAccountProcedure,
			connect.WithSchema(accountServiceCreateAccountMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
		creditAccount: connect.NewClient[v1.CreditAccountRequest, v1.CreditAccountResponse](
			httpClient,
			baseURL+AccountServiceCreditAccountProcedure,
			connect.WithSchema(accountServiceCreditAccountMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
		getAccount: connect.NewClient[v1.GetAccountRequest, v1.GetAccountResponse](
			httpClient,
			baseURL+AccountServiceGetAccountProcedure,
			connect.WithSchema(accountServiceGetAccountMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
	}
}

// accountServiceClient implements AccountServiceClient.
type accountServiceClient struct {
	createAccount *connect.Client[v1.CreateAccountRequest, v1.CreateAccountResponse]
	creditAccount *connect.Client[v1.CreditAccountRequest, v1.CreditAccountResponse]
	getAccount    *connect.Client[v1.GetAccountRequest, v1.GetAccountResponse]
}

// CreateAccount calls sample.v1.AccountService.CreateAccount.
func (c *accountServiceClient) CreateAccount(ctx context.Context, req *connect.Request[v1.CreateAccountRequest]) (*connect.Response[v1.CreateAccountResponse], error) {
	return c.createAccount.CallUnary(ctx, req)
}

// CreditAccount calls sample.v1.AccountService.CreditAccount.
func (c *accountServiceClient) CreditAccount(ctx context.Context, req *connect.Request[v1.CreditAccountRequest]) (*connect.Response[v1.CreditAccountResponse], error) {
	return c.creditAccount.CallUnary(ctx, req)
}

// GetAccount calls sample.v1.AccountService.GetAccount.
func (c *accountServiceClient) GetAccount(ctx context.Context, req *connect.Request[v1.GetAccountRequest]) (*connect.Response[v1.GetAccountResponse], error) {
	return c.getAccount.CallUnary(ctx, req)
}

// AccountServiceHandler is an implementation of the sample.v1.AccountService service.
type AccountServiceHandler interface {
	CreateAccount(context.Context, *connect.Request[v1.CreateAccountRequest]) (*connect.Response[v1.CreateAccountResponse], error)
	CreditAccount(context.Context, *connect.Request[v1.CreditAccountRequest]) (*connect.Response[v1.CreditAccountResponse], error)
	GetAccount(context.Context, *connect.Request[v1.GetAccountRequest]) (*connect.Response[v1.GetAccountResponse], error)
}

// NewAccountServiceHandler builds an HTTP handler from the service implementation. It returns the
// path on which to mount the handler and the handler itself.
//
// By default, handlers support the Connect, gRPC, and gRPC-Web protocols with the binary Protobuf
// and JSON codecs. They also support gzip compression.
func NewAccountServiceHandler(svc AccountServiceHandler, opts ...connect.HandlerOption) (string, http.Handler) {
	accountServiceCreateAccountHandler := connect.NewUnaryHandler(
		AccountServiceCreateAccountProcedure,
		svc.CreateAccount,
		connect.WithSchema(accountServiceCreateAccountMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	accountServiceCreditAccountHandler := connect.NewUnaryHandler(
		AccountServiceCreditAccountProcedure,
		svc.CreditAccount,
		connect.WithSchema(accountServiceCreditAccountMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	accountServiceGetAccountHandler := connect.NewUnaryHandler(
		AccountServiceGetAccountProcedure,
		svc.GetAccount,
		connect.WithSchema(accountServiceGetAccountMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	return "/sample.v1.AccountService/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case AccountServiceCreateAccountProcedure:
			accountServiceCreateAccountHandler.ServeHTTP(w, r)
		case AccountServiceCreditAccountProcedure:
			accountServiceCreditAccountHandler.ServeHTTP(w, r)
		case AccountServiceGetAccountProcedure:
			accountServiceGetAccountHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// UnimplementedAccountServiceHandler returns CodeUnimplemented from all methods.
type UnimplementedAccountServiceHandler struct{}

func (UnimplementedAccountServiceHandler) CreateAccount(context.Context, *connect.Request[v1.CreateAccountRequest]) (*connect.Response[v1.CreateAccountResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("sample.v1.AccountService.CreateAccount is not implemented"))
}

func (UnimplementedAccountServiceHandler) CreditAccount(context.Context, *connect.Request[v1.CreditAccountRequest]) (*connect.Response[v1.CreditAccountResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("sample.v1.AccountService.CreditAccount is not implemented"))
}

func (UnimplementedAccountServiceHandler) GetAccount(context.Context, *connect.Request[v1.GetAccountRequest]) (*connect.Response[v1.GetAccountResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("sample.v1.AccountService.GetAccount is not implemented"))
}
