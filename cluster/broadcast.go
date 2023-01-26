package cluster

import "github.com/hashicorp/memberlist"

// iBroadcast is something that can be broadcast via gossip to
// the memberlist cluster.
type iBroadcast interface {
	// Message returns a byte form of the message
	Message() []byte
	// Done is invoked when the message will no longer
	// be broadcast, either due to invalidation or to the
	// transmit limit being reached
	Done()
	// Invalidates checks if enqueuing the current broadcast
	// invalidates a previous broadcast
	Invalidates(other memberlist.Broadcast) bool
}

// broadcast is used to broadcast state in the cluster
type broadcast struct {
	// specifies the broadcast message
	msg []byte
	// specifies the notification channel
	notificationChan chan struct{}
}

var _ iBroadcast = &broadcast{}

// newBroadcast create a broadcast instance
func newBroadcast(msg []byte, channel chan struct{}) *broadcast {
	return &broadcast{
		msg:              msg,
		notificationChan: channel,
	}
}

// Message returns the broadcast message
func (b *broadcast) Message() []byte {
	return b.msg
}

// Done is invoked when the message will no longer
// be broadcast, either due to invalidation or to the
// transmit limit being reached
func (b *broadcast) Done() {
	if b.notificationChan != nil {
		close(b.notificationChan)
	}
}

// Invalidates checks if enqueuing the current broadcast
// invalidates a previous broadcast
func (b *broadcast) Invalidates(other memberlist.Broadcast) bool {
	// TODO enhance implementation on need
	return false
}
