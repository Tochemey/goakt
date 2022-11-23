package actors

import (
	"context"

	"google.golang.org/protobuf/proto"
)

// sendCommand is a type of message that does not expect a reply
type sendCommand struct {
	// ctx represents the go context
	ctx context.Context
	// message is the actual message sent to the actor
	message proto.Message
	// channel containing potential processing error
	errChan chan error
}

// sendReceive is a type of message that expects a response
type sendReceive struct {
	// ctx represents the go context
	ctx context.Context
	// message is the actual message sent to the actor
	message proto.Message
	// response is the response to the message sent
	response chan *response
}

type response struct {
	reply proto.Message
	err   error
}
