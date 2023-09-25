package actors

import (
	deadletterpb "github.com/tochemey/goakt/pb/deadletter/v1"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// toDeadletters add the given message to the deadletters queue
func (p *pid) toDeadletters(recvCtx ReceiveContext, err error) {
	// only send to the stream when defined
	if p.deadletterStream == nil {
		return
	}
	// marshal the message
	msg, _ := anypb.New(recvCtx.Message())
	// grab the sender
	sender := recvCtx.Sender().ActorPath().RemoteAddress()
	// define the receiver
	receiver := p.actorPath.RemoteAddress()
	// create the deadletter
	deadletter := &deadletterpb.Deadletter{
		Sender:   sender,
		Receiver: receiver,
		Message:  msg,
		SendTime: timestamppb.Now(),
		Reason:   err.Error(),
	}
	// add to the events stream
	p.deadletterStream.Publish(deadlettersTopic, deadletter)
}
