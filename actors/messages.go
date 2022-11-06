package actors

import (
	"context"

	"google.golang.org/protobuf/proto"
)

// send is a type of message that does not expect a reply
type send struct {
	// ctx represents the go context
	ctx context.Context
	// message is the actual message sent to the actor
	message proto.Message
	// channel containing potential processing error
	errChan chan error
}

// sendRecv is a type of message that expects a reply
type sendRecv struct {
	// ctx represents the go context
	ctx context.Context
	// message is the actual message sent to the actor
	message proto.Message
	// reply is the response to the message sent
	reply chan proto.Message
	// channel containing potential processing error
	errChan chan error
}
