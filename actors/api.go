package actors

import (
	"context"
	"sync"
	"time"

	"github.com/bufbuild/connect-go"
	otelconnect "github.com/bufbuild/connect-opentelemetry-go"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/internal/goakt/v1/goaktv1connect"
	"github.com/tochemey/goakt/internal/http2"
	"github.com/tochemey/goakt/internal/telemetry"
	pb "github.com/tochemey/goakt/messages/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// SendSync sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func SendSync(ctx context.Context, to PID, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	// make sure the actor is live
	if !to.IsOnline() {
		return nil, ErrNotReady
	}

	// create a mutex
	mu := sync.Mutex{}

	// acquire a lock to set the message context
	mu.Lock()
	// check whether we do have at least one behavior
	behaviors := to.behaviors()
	// check whether the recipient does have some behavior
	if behaviors.IsEmpty() {
		// release the lock after config the message context
		mu.Unlock()
		return nil, ErrEmptyBehavior
	}

	// create a receiver context
	context := new(receiveContext)

	// set the needed properties of the message context
	context.ctx = ctx
	context.sender = NoSender
	context.recipient = to
	context.message = message
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

// SendAsync sends an asynchronous message to an actor
func SendAsync(ctx context.Context, to PID, message proto.Message) error {
	// make sure the recipient actor is live
	if !to.IsOnline() {
		return ErrNotReady
	}

	// create a mutex
	mu := sync.Mutex{}

	// acquire a lock to set the message context
	mu.Lock()
	// check whether we do have at least one behavior
	behaviors := to.behaviors()
	// check whether the recipient does have some behavior
	if behaviors.IsEmpty() {
		// release the lock after config the message context
		mu.Unlock()
		return ErrEmptyBehavior
	}
	// create a message context
	context := new(receiveContext)

	// set the needed properties of the message context
	context.ctx = ctx
	context.sender = NoSender
	context.recipient = to
	context.message = message
	context.isAsyncMessage = true
	context.mu = sync.Mutex{}

	// release the lock after config the message context
	mu.Unlock()

	// put the message context in the mailbox of the recipient actor
	to.doReceive(context)

	return nil
}

// RemoteSendAsync sends a message to an actor remotely without expecting any reply
func RemoteSendAsync(ctx context.Context, to *pb.Address, message proto.Message) error {
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
		http2.GetClient(),
		http2.GetURL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(otelconnect.NewInterceptor()),
		connect.WithGRPC(),
	)
	// prepare the rpcRequest to send
	request := connect.NewRequest(&goaktpb.RemoteTellRequest{
		RemoteMessage: &pb.RemoteMessage{
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

// RemoteSendSync sends a synchronous message to another actor remotely and expect a response.
func RemoteSendSync(ctx context.Context, to *pb.Address, message proto.Message) (response proto.Message, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteTell")
	defer span.End()

	// marshal the message
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, err
	}

	// create an instance of remote client service
	remoteClient := goaktv1connect.NewRemoteMessagingServiceClient(
		http2.GetClient(),
		http2.GetURL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(otelconnect.NewInterceptor()),
		connect.WithGRPC(),
	)
	// prepare the rpcRequest to send
	rpcRequest := connect.NewRequest(&goaktpb.RemoteAskRequest{
		Receiver: to,
		Message:  marshaled,
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
		http2.GetClient(),
		http2.GetURL(host, port),
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
