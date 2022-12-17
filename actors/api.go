package actors

import (
	"context"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
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

	// release the lock after setting the message context
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

	// create a message context
	context := new(receiveContext)

	// set the needed properties of the message context
	context.ctx = ctx
	context.sender = NoSender
	context.recipient = to
	context.message = message
	context.isAsyncMessage = true
	context.mu = sync.Mutex{}

	// release the lock after setting the message context
	mu.Unlock()

	// put the message context in the mailbox of the recipient actor
	to.doReceive(context)

	return nil
}
