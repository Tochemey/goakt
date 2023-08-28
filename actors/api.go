package actors

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/internal/goakt/v1/goaktv1connect"
	pb "github.com/tochemey/goakt/pb/v1"
	"github.com/tochemey/goakt/pkg/http"
	"github.com/tochemey/goakt/pkg/telemetry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func Ask(ctx context.Context, to PID, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	// make sure the actor is live
	if !to.IsRunning() {
		return nil, ErrNotReady
	}

	// create a mutex
	mu := sync.Mutex{}

	// acquire a lock to set the message context
	mu.Lock()

	// create a receiver context
	context := new(receiveContext)
	// set the needed properties of the message context
	context.ctx = ctx
	context.recipient = to

	// set the actual message
	switch msg := message.(type) {
	case *goaktpb.RemoteMessage:
		// define the actual message variable
		var actual proto.Message
		// unmarshal the message and handle the error
		if actual, err = msg.GetMessage().UnmarshalNew(); err != nil {
			return nil, err
		}
		// set the context message and the sender
		context.message = actual
		context.remoteSender = msg.GetSender()
		context.sender = NoSender
	default:
		// set the context message and the sender
		context.message = message
		context.sender = NoSender
		context.remoteSender = RemoteNoSender
	}

	context.isAsyncMessage = false
	context.mu = sync.Mutex{}
	context.response = make(chan proto.Message, 1)

	// release the lock after config the message context
	mu.Unlock()

	// put the message context in the mailbox of the recipient actor
	to.doReceive(context)

	// await patiently to receive the response from the actor
	for await := time.After(timeout); ; {
		select {
		case response = <-context.response:
			return
		case <-await:
			err = ErrRequestTimeout
			return
		}
	}
}

// Tell sends an asynchronous message to an actor
func Tell(ctx context.Context, to PID, message proto.Message) error {
	// make sure the recipient actor is live
	if !to.IsRunning() {
		return ErrNotReady
	}

	// create a mutex
	mu := sync.Mutex{}

	// acquire a lock to set the message context
	mu.Lock()
	// create a message context
	context := new(receiveContext)

	// set the needed properties of the message context
	context.ctx = ctx
	context.recipient = to

	// set the actual message
	switch msg := message.(type) {
	case *goaktpb.RemoteMessage:
		var (
			// define the actual message variable
			actual proto.Message
			// define the error variable
			err error
		)
		// unmarshal the message and handle the error
		if actual, err = msg.GetMessage().UnmarshalNew(); err != nil {
			return err
		}

		// set the context message and sender
		context.message = actual
		context.remoteSender = msg.GetSender()
		context.sender = NoSender
	default:
		context.message = message
		context.sender = NoSender
		context.remoteSender = RemoteNoSender
	}

	context.isAsyncMessage = true
	context.mu = sync.Mutex{}

	// release the lock after config the message context
	mu.Unlock()

	// put the message context in the mailbox of the recipient actor
	to.doReceive(context)

	return nil
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func RemoteTell(ctx context.Context, to *pb.Address, message proto.Message) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteTell")
	defer span.End()

	// marshal the message
	marshaled, err := anypb.New(message)
	if err != nil {
		return err
	}

	// create an instance of remote client service
	remoteClient := goaktv1connect.NewRemoteMessagingServiceClient(
		http.Client(),
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(otelconnect.NewInterceptor()),
		connect.WithGRPC(),
	)
	// prepare the rpcRequest to send
	request := connect.NewRequest(&goaktpb.RemoteTellRequest{
		RemoteMessage: &goaktpb.RemoteMessage{
			Sender:   RemoteNoSender,
			Receiver: to,
			Message:  marshaled,
		},
	})
	// send the message and handle the error in case there is any
	if _, err := remoteClient.RemoteTell(ctx, request); err != nil {
		return err
	}
	return nil
}

// RemoteAsk sends a synchronous message to another actor remotely and expect a response.
func RemoteAsk(ctx context.Context, to *pb.Address, message proto.Message) (response *anypb.Any, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteAsk")
	defer span.End()

	// marshal the message
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, err
	}

	// create an instance of remote client service
	remoteClient := goaktv1connect.NewRemoteMessagingServiceClient(
		http.Client(),
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(otelconnect.NewInterceptor()),
		connect.WithGRPC(),
	)
	// prepare the rpcRequest to send
	rpcRequest := connect.NewRequest(&goaktpb.RemoteAskRequest{
		RemoteMessage: &goaktpb.RemoteMessage{
			Sender:   RemoteNoSender,
			Receiver: to,
			Message:  marshaled,
		},
	})
	// send the request
	rpcResponse, rpcErr := remoteClient.RemoteAsk(ctx, rpcRequest)
	// handle the error
	if rpcErr != nil {
		return nil, rpcErr
	}

	return rpcResponse.Msg.GetMessage(), nil
}

// RemoteLookup look for an actor address on a remote node.
func RemoteLookup(ctx context.Context, host string, port int, name string) (addr *pb.Address, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteLookup")
	defer span.End()

	// create an instance of remote client service
	remoteClient := goaktv1connect.NewRemoteMessagingServiceClient(
		http.Client(),
		http.URL(host, port),
		connect.WithInterceptors(otelconnect.NewInterceptor()),
		connect.WithGRPC(),
	)

	// prepare the request to send
	request := connect.NewRequest(&goaktpb.RemoteLookupRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})
	// send the message and handle the error in case there is any
	response, err := remoteClient.RemoteLookup(ctx, request)
	// we know the error will always be a grpc error
	if err != nil {
		// get the status error
		s := status.Convert(err)
		if s.Code() == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}

	// return the response
	return response.Msg.GetAddress(), nil
}
