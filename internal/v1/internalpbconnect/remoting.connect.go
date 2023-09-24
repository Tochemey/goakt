// Code generated by protoc-gen-connect-go. DO NOT EDIT.
//
// Source: internal/v1/remoting.proto

package internalpbconnect

import (
	connect "connectrpc.com/connect"
	context "context"
	errors "errors"
	v1 "github.com/tochemey/goakt/internal/v1"
	http "net/http"
	strings "strings"
)

// This is a compile-time assertion to ensure that this generated file and the connect package are
// compatible. If you get a compiler error that this constant is not defined, this code was
// generated with a version of connect newer than the one compiled into your binary. You can fix the
// problem by either regenerating this code with an older version of connect or updating the connect
// version compiled into your binary.
const _ = connect.IsAtLeastVersion0_1_0

const (
	// RemotingServiceName is the fully-qualified name of the RemotingService service.
	RemotingServiceName = "internal.v1.RemotingService"
)

// These constants are the fully-qualified names of the RPCs defined in this package. They're
// exposed at runtime as Spec.Procedure and as the final two segments of the HTTP route.
//
// Note that these are different from the fully-qualified method names used by
// google.golang.org/protobuf/reflect/protoreflect. To convert from these constants to
// reflection-formatted method names, remove the leading slash and convert the remaining slash to a
// period.
const (
	// RemotingServiceRemoteAskProcedure is the fully-qualified name of the RemotingService's RemoteAsk
	// RPC.
	RemotingServiceRemoteAskProcedure = "/internal.v1.RemotingService/RemoteAsk"
	// RemotingServiceRemoteTellProcedure is the fully-qualified name of the RemotingService's
	// RemoteTell RPC.
	RemotingServiceRemoteTellProcedure = "/internal.v1.RemotingService/RemoteTell"
	// RemotingServiceRemoteLookupProcedure is the fully-qualified name of the RemotingService's
	// RemoteLookup RPC.
	RemotingServiceRemoteLookupProcedure = "/internal.v1.RemotingService/RemoteLookup"
)

// RemotingServiceClient is a client for the internal.v1.RemotingService service.
type RemotingServiceClient interface {
	// RemoteAsk is used to send a message to an actor remotely and expect a response
	// immediately. With this type of message the receiver cannot communicate back to Sender
	// except reply the message with a response. This one-way communication
	RemoteAsk(context.Context, *connect.Request[v1.RemoteAskRequest]) (*connect.Response[v1.RemoteAskResponse], error)
	// RemoteTell is used to send a message to an actor remotely by another actor
	// This is the only way remote actors can interact with each other. The actor on the
	// other line can reply to the sender by using the Sender in the message
	RemoteTell(context.Context, *connect.Request[v1.RemoteTellRequest]) (*connect.Response[v1.RemoteTellResponse], error)
	// Lookup for an actor on a remote host.
	RemoteLookup(context.Context, *connect.Request[v1.RemoteLookupRequest]) (*connect.Response[v1.RemoteLookupResponse], error)
}

// NewRemotingServiceClient constructs a client for the internal.v1.RemotingService service. By
// default, it uses the Connect protocol with the binary Protobuf Codec, asks for gzipped responses,
// and sends uncompressed requests. To use the gRPC or gRPC-Web protocols, supply the
// connect.WithGRPC() or connect.WithGRPCWeb() options.
//
// The URL supplied here should be the base URL for the Connect or gRPC server (for example,
// http://api.acme.com or https://acme.com/grpc).
func NewRemotingServiceClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) RemotingServiceClient {
	baseURL = strings.TrimRight(baseURL, "/")
	return &remotingServiceClient{
		remoteAsk: connect.NewClient[v1.RemoteAskRequest, v1.RemoteAskResponse](
			httpClient,
			baseURL+RemotingServiceRemoteAskProcedure,
			opts...,
		),
		remoteTell: connect.NewClient[v1.RemoteTellRequest, v1.RemoteTellResponse](
			httpClient,
			baseURL+RemotingServiceRemoteTellProcedure,
			opts...,
		),
		remoteLookup: connect.NewClient[v1.RemoteLookupRequest, v1.RemoteLookupResponse](
			httpClient,
			baseURL+RemotingServiceRemoteLookupProcedure,
			opts...,
		),
	}
}

// remotingServiceClient implements RemotingServiceClient.
type remotingServiceClient struct {
	remoteAsk    *connect.Client[v1.RemoteAskRequest, v1.RemoteAskResponse]
	remoteTell   *connect.Client[v1.RemoteTellRequest, v1.RemoteTellResponse]
	remoteLookup *connect.Client[v1.RemoteLookupRequest, v1.RemoteLookupResponse]
}

// RemoteAsk calls internal.v1.RemotingService.RemoteAsk.
func (c *remotingServiceClient) RemoteAsk(ctx context.Context, req *connect.Request[v1.RemoteAskRequest]) (*connect.Response[v1.RemoteAskResponse], error) {
	return c.remoteAsk.CallUnary(ctx, req)
}

// RemoteTell calls internal.v1.RemotingService.RemoteTell.
func (c *remotingServiceClient) RemoteTell(ctx context.Context, req *connect.Request[v1.RemoteTellRequest]) (*connect.Response[v1.RemoteTellResponse], error) {
	return c.remoteTell.CallUnary(ctx, req)
}

// RemoteLookup calls internal.v1.RemotingService.RemoteLookup.
func (c *remotingServiceClient) RemoteLookup(ctx context.Context, req *connect.Request[v1.RemoteLookupRequest]) (*connect.Response[v1.RemoteLookupResponse], error) {
	return c.remoteLookup.CallUnary(ctx, req)
}

// RemotingServiceHandler is an implementation of the internal.v1.RemotingService service.
type RemotingServiceHandler interface {
	// RemoteAsk is used to send a message to an actor remotely and expect a response
	// immediately. With this type of message the receiver cannot communicate back to Sender
	// except reply the message with a response. This one-way communication
	RemoteAsk(context.Context, *connect.Request[v1.RemoteAskRequest]) (*connect.Response[v1.RemoteAskResponse], error)
	// RemoteTell is used to send a message to an actor remotely by another actor
	// This is the only way remote actors can interact with each other. The actor on the
	// other line can reply to the sender by using the Sender in the message
	RemoteTell(context.Context, *connect.Request[v1.RemoteTellRequest]) (*connect.Response[v1.RemoteTellResponse], error)
	// Lookup for an actor on a remote host.
	RemoteLookup(context.Context, *connect.Request[v1.RemoteLookupRequest]) (*connect.Response[v1.RemoteLookupResponse], error)
}

// NewRemotingServiceHandler builds an HTTP handler from the service implementation. It returns the
// path on which to mount the handler and the handler itself.
//
// By default, handlers support the Connect, gRPC, and gRPC-Web protocols with the binary Protobuf
// and JSON codecs. They also support gzip compression.
func NewRemotingServiceHandler(svc RemotingServiceHandler, opts ...connect.HandlerOption) (string, http.Handler) {
	remotingServiceRemoteAskHandler := connect.NewUnaryHandler(
		RemotingServiceRemoteAskProcedure,
		svc.RemoteAsk,
		opts...,
	)
	remotingServiceRemoteTellHandler := connect.NewUnaryHandler(
		RemotingServiceRemoteTellProcedure,
		svc.RemoteTell,
		opts...,
	)
	remotingServiceRemoteLookupHandler := connect.NewUnaryHandler(
		RemotingServiceRemoteLookupProcedure,
		svc.RemoteLookup,
		opts...,
	)
	return "/internal.v1.RemotingService/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case RemotingServiceRemoteAskProcedure:
			remotingServiceRemoteAskHandler.ServeHTTP(w, r)
		case RemotingServiceRemoteTellProcedure:
			remotingServiceRemoteTellHandler.ServeHTTP(w, r)
		case RemotingServiceRemoteLookupProcedure:
			remotingServiceRemoteLookupHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// UnimplementedRemotingServiceHandler returns CodeUnimplemented from all methods.
type UnimplementedRemotingServiceHandler struct{}

func (UnimplementedRemotingServiceHandler) RemoteAsk(context.Context, *connect.Request[v1.RemoteAskRequest]) (*connect.Response[v1.RemoteAskResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("internal.v1.RemotingService.RemoteAsk is not implemented"))
}

func (UnimplementedRemotingServiceHandler) RemoteTell(context.Context, *connect.Request[v1.RemoteTellRequest]) (*connect.Response[v1.RemoteTellResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("internal.v1.RemotingService.RemoteTell is not implemented"))
}

func (UnimplementedRemotingServiceHandler) RemoteLookup(context.Context, *connect.Request[v1.RemoteLookupRequest]) (*connect.Response[v1.RemoteLookupResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("internal.v1.RemotingService.RemoteLookup is not implemented"))
}
