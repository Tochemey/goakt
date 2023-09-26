package eventstream

// Message defines the stream message
type Message struct {
	topic   string
	payload any
}

// Topic returns the message topic
func (m Message) Topic() string {
	return m.topic
}

// Payload returns the message payload
func (m Message) Payload() any {
	return m.payload
}

// NewMessage creates an instance of Stream Message
func NewMessage(topic string, payload any) *Message {
	return &Message{
		topic:   topic,
		payload: payload,
	}
}
