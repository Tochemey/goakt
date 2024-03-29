// Code generated by protoc-gen-connect-go. DO NOT EDIT.
//
// Source: internal/remoting.proto

package internalpbconnect

import (
	connect "connectrpc.com/connect"
	context "context"
	errors "errors"
	internalpb "github.com/tochemey/goakt/internal/internalpb"
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
	// RemotingServiceName is the fully-qualified name of the RemotingService service.
	RemotingServiceName = "internalpb.RemotingService"
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
	RemotingServiceRemoteAskProcedure = "/internalpb.RemotingService/RemoteAsk"
	// RemotingServiceRemoteTellProcedure is the fully-qualified name of the RemotingService's
	// RemoteTell RPC.
	RemotingServiceRemoteTellProcedure = "/internalpb.RemotingService/RemoteTell"
	// RemotingServiceRemoteLookupProcedure is the fully-qualified name of the RemotingService's
	// RemoteLookup RPC.
	RemotingServiceRemoteLookupProcedure = "/internalpb.RemotingService/RemoteLookup"
	// RemotingServiceRemoteBatchTellProcedure is the fully-qualified name of the RemotingService's
	// RemoteBatchTell RPC.
	RemotingServiceRemoteBatchTellProcedure = "/internalpb.RemotingService/RemoteBatchTell"
	// RemotingServiceRemoteBatchAskProcedure is the fully-qualified name of the RemotingService's
	// RemoteBatchAsk RPC.
	RemotingServiceRemoteBatchAskProcedure = "/internalpb.RemotingService/RemoteBatchAsk"
	// RemotingServiceRemoteReSpawnProcedure is the fully-qualified name of the RemotingService's
	// RemoteReSpawn RPC.
	RemotingServiceRemoteReSpawnProcedure = "/internalpb.RemotingService/RemoteReSpawn"
	// RemotingServiceRemoteStopProcedure is the fully-qualified name of the RemotingService's
	// RemoteStop RPC.
	RemotingServiceRemoteStopProcedure = "/internalpb.RemotingService/RemoteStop"
)

// These variables are the protoreflect.Descriptor objects for the RPCs defined in this package.
var (
	remotingServiceServiceDescriptor               = internalpb.File_internal_remoting_proto.Services().ByName("RemotingService")
	remotingServiceRemoteAskMethodDescriptor       = remotingServiceServiceDescriptor.Methods().ByName("RemoteAsk")
	remotingServiceRemoteTellMethodDescriptor      = remotingServiceServiceDescriptor.Methods().ByName("RemoteTell")
	remotingServiceRemoteLookupMethodDescriptor    = remotingServiceServiceDescriptor.Methods().ByName("RemoteLookup")
	remotingServiceRemoteBatchTellMethodDescriptor = remotingServiceServiceDescriptor.Methods().ByName("RemoteBatchTell")
	remotingServiceRemoteBatchAskMethodDescriptor  = remotingServiceServiceDescriptor.Methods().ByName("RemoteBatchAsk")
	remotingServiceRemoteReSpawnMethodDescriptor   = remotingServiceServiceDescriptor.Methods().ByName("RemoteReSpawn")
	remotingServiceRemoteStopMethodDescriptor      = remotingServiceServiceDescriptor.Methods().ByName("RemoteStop")
)

// RemotingServiceClient is a client for the internalpb.RemotingService service.
type RemotingServiceClient interface {
	// RemoteAsk is used to send a message to an actor remotely and expect a response immediately.
	RemoteAsk(context.Context, *connect.Request[internalpb.RemoteAskRequest]) (*connect.Response[internalpb.RemoteAskResponse], error)
	// RemoteTell is used to send a message to a remote actor
	// The actor on the other line can reply to the sender by using the Sender in the message
	RemoteTell(context.Context, *connect.Request[internalpb.RemoteTellRequest]) (*connect.Response[internalpb.RemoteTellResponse], error)
	// Lookup for an actor on a remote host.
	RemoteLookup(context.Context, *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error)
	// RemoteBatchTell is used to send a bulk of messages to a remote actor
	RemoteBatchTell(context.Context, *connect.Request[internalpb.RemoteBatchTellRequest]) (*connect.Response[internalpb.RemoteBatchTellResponse], error)
	// RemoteBatchAsk is used to send a bulk messages to a remote actor with replies.
	// The replies are sent in the same order as the messages
	RemoteBatchAsk(context.Context, *connect.Request[internalpb.RemoteBatchAskRequest]) (*connect.Response[internalpb.RemoteBatchAskResponse], error)
	// RemoteReSpawn restarts an actor on a remote machine
	RemoteReSpawn(context.Context, *connect.Request[internalpb.RemoteReSpawnRequest]) (*connect.Response[internalpb.RemoteReSpawnResponse], error)
	// RemoteStop stops an actor on a remote machine
	RemoteStop(context.Context, *connect.Request[internalpb.RemoteStopRequest]) (*connect.Response[internalpb.RemoteStopResponse], error)
}

// NewRemotingServiceClient constructs a client for the internalpb.RemotingService service. By
// default, it uses the Connect protocol with the binary Protobuf Codec, asks for gzipped responses,
// and sends uncompressed requests. To use the gRPC or gRPC-Web protocols, supply the
// connect.WithGRPC() or connect.WithGRPCWeb() options.
//
// The URL supplied here should be the base URL for the Connect or gRPC server (for example,
// http://api.acme.com or https://acme.com/grpc).
func NewRemotingServiceClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) RemotingServiceClient {
	baseURL = strings.TrimRight(baseURL, "/")
	return &remotingServiceClient{
		remoteAsk: connect.NewClient[internalpb.RemoteAskRequest, internalpb.RemoteAskResponse](
			httpClient,
			baseURL+RemotingServiceRemoteAskProcedure,
			connect.WithSchema(remotingServiceRemoteAskMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
		remoteTell: connect.NewClient[internalpb.RemoteTellRequest, internalpb.RemoteTellResponse](
			httpClient,
			baseURL+RemotingServiceRemoteTellProcedure,
			connect.WithSchema(remotingServiceRemoteTellMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
		remoteLookup: connect.NewClient[internalpb.RemoteLookupRequest, internalpb.RemoteLookupResponse](
			httpClient,
			baseURL+RemotingServiceRemoteLookupProcedure,
			connect.WithSchema(remotingServiceRemoteLookupMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
		remoteBatchTell: connect.NewClient[internalpb.RemoteBatchTellRequest, internalpb.RemoteBatchTellResponse](
			httpClient,
			baseURL+RemotingServiceRemoteBatchTellProcedure,
			connect.WithSchema(remotingServiceRemoteBatchTellMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
		remoteBatchAsk: connect.NewClient[internalpb.RemoteBatchAskRequest, internalpb.RemoteBatchAskResponse](
			httpClient,
			baseURL+RemotingServiceRemoteBatchAskProcedure,
			connect.WithSchema(remotingServiceRemoteBatchAskMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
		remoteReSpawn: connect.NewClient[internalpb.RemoteReSpawnRequest, internalpb.RemoteReSpawnResponse](
			httpClient,
			baseURL+RemotingServiceRemoteReSpawnProcedure,
			connect.WithSchema(remotingServiceRemoteReSpawnMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
		remoteStop: connect.NewClient[internalpb.RemoteStopRequest, internalpb.RemoteStopResponse](
			httpClient,
			baseURL+RemotingServiceRemoteStopProcedure,
			connect.WithSchema(remotingServiceRemoteStopMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
	}
}

// remotingServiceClient implements RemotingServiceClient.
type remotingServiceClient struct {
	remoteAsk       *connect.Client[internalpb.RemoteAskRequest, internalpb.RemoteAskResponse]
	remoteTell      *connect.Client[internalpb.RemoteTellRequest, internalpb.RemoteTellResponse]
	remoteLookup    *connect.Client[internalpb.RemoteLookupRequest, internalpb.RemoteLookupResponse]
	remoteBatchTell *connect.Client[internalpb.RemoteBatchTellRequest, internalpb.RemoteBatchTellResponse]
	remoteBatchAsk  *connect.Client[internalpb.RemoteBatchAskRequest, internalpb.RemoteBatchAskResponse]
	remoteReSpawn   *connect.Client[internalpb.RemoteReSpawnRequest, internalpb.RemoteReSpawnResponse]
	remoteStop      *connect.Client[internalpb.RemoteStopRequest, internalpb.RemoteStopResponse]
}

// RemoteAsk calls internalpb.RemotingService.RemoteAsk.
func (c *remotingServiceClient) RemoteAsk(ctx context.Context, req *connect.Request[internalpb.RemoteAskRequest]) (*connect.Response[internalpb.RemoteAskResponse], error) {
	return c.remoteAsk.CallUnary(ctx, req)
}

// RemoteTell calls internalpb.RemotingService.RemoteTell.
func (c *remotingServiceClient) RemoteTell(ctx context.Context, req *connect.Request[internalpb.RemoteTellRequest]) (*connect.Response[internalpb.RemoteTellResponse], error) {
	return c.remoteTell.CallUnary(ctx, req)
}

// RemoteLookup calls internalpb.RemotingService.RemoteLookup.
func (c *remotingServiceClient) RemoteLookup(ctx context.Context, req *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	return c.remoteLookup.CallUnary(ctx, req)
}

// RemoteBatchTell calls internalpb.RemotingService.RemoteBatchTell.
func (c *remotingServiceClient) RemoteBatchTell(ctx context.Context, req *connect.Request[internalpb.RemoteBatchTellRequest]) (*connect.Response[internalpb.RemoteBatchTellResponse], error) {
	return c.remoteBatchTell.CallUnary(ctx, req)
}

// RemoteBatchAsk calls internalpb.RemotingService.RemoteBatchAsk.
func (c *remotingServiceClient) RemoteBatchAsk(ctx context.Context, req *connect.Request[internalpb.RemoteBatchAskRequest]) (*connect.Response[internalpb.RemoteBatchAskResponse], error) {
	return c.remoteBatchAsk.CallUnary(ctx, req)
}

// RemoteReSpawn calls internalpb.RemotingService.RemoteReSpawn.
func (c *remotingServiceClient) RemoteReSpawn(ctx context.Context, req *connect.Request[internalpb.RemoteReSpawnRequest]) (*connect.Response[internalpb.RemoteReSpawnResponse], error) {
	return c.remoteReSpawn.CallUnary(ctx, req)
}

// RemoteStop calls internalpb.RemotingService.RemoteStop.
func (c *remotingServiceClient) RemoteStop(ctx context.Context, req *connect.Request[internalpb.RemoteStopRequest]) (*connect.Response[internalpb.RemoteStopResponse], error) {
	return c.remoteStop.CallUnary(ctx, req)
}

// RemotingServiceHandler is an implementation of the internalpb.RemotingService service.
type RemotingServiceHandler interface {
	// RemoteAsk is used to send a message to an actor remotely and expect a response immediately.
	RemoteAsk(context.Context, *connect.Request[internalpb.RemoteAskRequest]) (*connect.Response[internalpb.RemoteAskResponse], error)
	// RemoteTell is used to send a message to a remote actor
	// The actor on the other line can reply to the sender by using the Sender in the message
	RemoteTell(context.Context, *connect.Request[internalpb.RemoteTellRequest]) (*connect.Response[internalpb.RemoteTellResponse], error)
	// Lookup for an actor on a remote host.
	RemoteLookup(context.Context, *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error)
	// RemoteBatchTell is used to send a bulk of messages to a remote actor
	RemoteBatchTell(context.Context, *connect.Request[internalpb.RemoteBatchTellRequest]) (*connect.Response[internalpb.RemoteBatchTellResponse], error)
	// RemoteBatchAsk is used to send a bulk messages to a remote actor with replies.
	// The replies are sent in the same order as the messages
	RemoteBatchAsk(context.Context, *connect.Request[internalpb.RemoteBatchAskRequest]) (*connect.Response[internalpb.RemoteBatchAskResponse], error)
	// RemoteReSpawn restarts an actor on a remote machine
	RemoteReSpawn(context.Context, *connect.Request[internalpb.RemoteReSpawnRequest]) (*connect.Response[internalpb.RemoteReSpawnResponse], error)
	// RemoteStop stops an actor on a remote machine
	RemoteStop(context.Context, *connect.Request[internalpb.RemoteStopRequest]) (*connect.Response[internalpb.RemoteStopResponse], error)
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
		connect.WithSchema(remotingServiceRemoteAskMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	remotingServiceRemoteTellHandler := connect.NewUnaryHandler(
		RemotingServiceRemoteTellProcedure,
		svc.RemoteTell,
		connect.WithSchema(remotingServiceRemoteTellMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	remotingServiceRemoteLookupHandler := connect.NewUnaryHandler(
		RemotingServiceRemoteLookupProcedure,
		svc.RemoteLookup,
		connect.WithSchema(remotingServiceRemoteLookupMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	remotingServiceRemoteBatchTellHandler := connect.NewUnaryHandler(
		RemotingServiceRemoteBatchTellProcedure,
		svc.RemoteBatchTell,
		connect.WithSchema(remotingServiceRemoteBatchTellMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	remotingServiceRemoteBatchAskHandler := connect.NewUnaryHandler(
		RemotingServiceRemoteBatchAskProcedure,
		svc.RemoteBatchAsk,
		connect.WithSchema(remotingServiceRemoteBatchAskMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	remotingServiceRemoteReSpawnHandler := connect.NewUnaryHandler(
		RemotingServiceRemoteReSpawnProcedure,
		svc.RemoteReSpawn,
		connect.WithSchema(remotingServiceRemoteReSpawnMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	remotingServiceRemoteStopHandler := connect.NewUnaryHandler(
		RemotingServiceRemoteStopProcedure,
		svc.RemoteStop,
		connect.WithSchema(remotingServiceRemoteStopMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	return "/internalpb.RemotingService/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case RemotingServiceRemoteAskProcedure:
			remotingServiceRemoteAskHandler.ServeHTTP(w, r)
		case RemotingServiceRemoteTellProcedure:
			remotingServiceRemoteTellHandler.ServeHTTP(w, r)
		case RemotingServiceRemoteLookupProcedure:
			remotingServiceRemoteLookupHandler.ServeHTTP(w, r)
		case RemotingServiceRemoteBatchTellProcedure:
			remotingServiceRemoteBatchTellHandler.ServeHTTP(w, r)
		case RemotingServiceRemoteBatchAskProcedure:
			remotingServiceRemoteBatchAskHandler.ServeHTTP(w, r)
		case RemotingServiceRemoteReSpawnProcedure:
			remotingServiceRemoteReSpawnHandler.ServeHTTP(w, r)
		case RemotingServiceRemoteStopProcedure:
			remotingServiceRemoteStopHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// UnimplementedRemotingServiceHandler returns CodeUnimplemented from all methods.
type UnimplementedRemotingServiceHandler struct{}

func (UnimplementedRemotingServiceHandler) RemoteAsk(context.Context, *connect.Request[internalpb.RemoteAskRequest]) (*connect.Response[internalpb.RemoteAskResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("internalpb.RemotingService.RemoteAsk is not implemented"))
}

func (UnimplementedRemotingServiceHandler) RemoteTell(context.Context, *connect.Request[internalpb.RemoteTellRequest]) (*connect.Response[internalpb.RemoteTellResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("internalpb.RemotingService.RemoteTell is not implemented"))
}

func (UnimplementedRemotingServiceHandler) RemoteLookup(context.Context, *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("internalpb.RemotingService.RemoteLookup is not implemented"))
}

func (UnimplementedRemotingServiceHandler) RemoteBatchTell(context.Context, *connect.Request[internalpb.RemoteBatchTellRequest]) (*connect.Response[internalpb.RemoteBatchTellResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("internalpb.RemotingService.RemoteBatchTell is not implemented"))
}

func (UnimplementedRemotingServiceHandler) RemoteBatchAsk(context.Context, *connect.Request[internalpb.RemoteBatchAskRequest]) (*connect.Response[internalpb.RemoteBatchAskResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("internalpb.RemotingService.RemoteBatchAsk is not implemented"))
}

func (UnimplementedRemotingServiceHandler) RemoteReSpawn(context.Context, *connect.Request[internalpb.RemoteReSpawnRequest]) (*connect.Response[internalpb.RemoteReSpawnResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("internalpb.RemotingService.RemoteReSpawn is not implemented"))
}

func (UnimplementedRemotingServiceHandler) RemoteStop(context.Context, *connect.Request[internalpb.RemoteStopRequest]) (*connect.Response[internalpb.RemoteStopResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("internalpb.RemotingService.RemoteStop is not implemented"))
}
