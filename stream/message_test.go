package stream

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMessage(t *testing.T) {
	t.Run("with Accept", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		msg := NewMessage("id", []byte("content"))
		require.True(t, msg.Accept())
		assertAccepted(t, msg)
		assertReject(t, msg)
	})
	t.Run("with Accept No-Op", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		msg := NewMessage("id", []byte("content"))
		require.True(t, msg.Accept())
		require.True(t, msg.Accept())
		assertAccepted(t, msg)
	})
	t.Run("with Accept already Rejected", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		msg := NewMessage("id", []byte("content"))
		require.True(t, msg.Reject())
		assert.False(t, msg.Accept())
	})
	t.Run("with Reject", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		msg := NewMessage("id", []byte("content"))
		require.True(t, msg.Reject())
		assertRejected(t, msg)
		assertAccept(t, msg)
	})
	t.Run("with Reject No-Op", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		msg := NewMessage("id", []byte("content"))
		require.True(t, msg.Reject())
		require.True(t, msg.Reject())
		assertRejected(t, msg)
	})
	t.Run("with Reject already Accepted", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		msg := NewMessage("id", []byte("content"))
		require.True(t, msg.Accept())
		assert.False(t, msg.Reject())
	})
}

func assertAccepted(t *testing.T, msg *Message) {
	select {
	case <-msg.Accepted():
		// ok
	default:
		t.Fatal("no Acknowledgement received")
	}
}

func assertRejected(t *testing.T, msg *Message) {
	select {
	case <-msg.Rejected():
		// ok
	default:
		t.Fatal("no Acknowledgement received")
	}
}

func assertAccept(t *testing.T, msg *Message) {
	select {
	case <-msg.Accepted():
		t.Fatal("rejected should be not sent")
	default:
		// ok
	}
}

func assertReject(t *testing.T, msg *Message) {
	select {
	case <-msg.Rejected():
		t.Fatal("rejected should be not sent")
	default:
		// ok
	}
}
