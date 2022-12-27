package stream

import "context"

// Topic is a type alias
type Topic struct {
	Name     string
	Messages []*Message
}

// Producer helps implement the producer
type Producer interface {
	// Produce produces messages to given topic and wait for all consumers to either Accept/Reject the message
	// after a pre-defined timeout.
	Produce(cxt context.Context, topic string, messages ...*Message) error
	// Stop should flush unsent messages, if producer is async.
	Stop(ctx context.Context) error
}

// Consumer is the consuming part of the Producer/Consumer.
type Consumer interface {
	// Consume returns output channel with messages from provided topic.
	// Channel is closed, when Stop() was called on the subscriber.
	//
	// To receive the next message, `Accept()` must be called on the received message.
	// If message processing failed and message should be redelivered `Reject()` should be called.
	//
	// When provided ctx is cancelled, consumer will close subscribe and close processing channel.
	// Provided ctx is set to all produced messages.
	// When Reject or Accept is called on the message, context of the message is canceled.
	Consume(ctx context.Context, topic string) (<-chan *Message, error)
	// Stop closes all subscriptions with their output channels and flush offsets etc. when needed.
	Stop(ctx context.Context) error
}

// RetentionLog defines the interface that helps streamed
// messages to be persisted.
type RetentionLog interface {
	// Connect helps connect to the retention log
	Connect(ctx context.Context) error
	// Persist persists topics onto t a durable store
	Persist(ctx context.Context, topic *Topic) error
	// Disconnect shuts down the persistence storage
	Disconnect(ctx context.Context) error
	// GetMessages returns the list of messages in a topic
	GetMessages(ctx context.Context, topic string) ([]*Message, error)
}
