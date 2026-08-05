// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package actor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
)

// TestDurableQueueValues verifies queue value accessors and defensive copies.
func TestDurableQueueValues(t *testing.T) {
	source := []byte("message-1")
	payload, err := NewReliablePayload(source)
	require.NoError(t, err)

	message, err := NewUnconfirmedMessage("message-1", 1, payload)
	require.NoError(t, err)
	request, err := NewStoreRequest("message-1", 1, payload)
	require.NoError(t, err)
	result, err := NewStoreResult(1, true, payload)
	require.NoError(t, err)
	state, err := NewDurableQueueState(1, 0, []UnconfirmedMessage{message})
	require.NoError(t, err)

	source[0] = 'M'

	assert.Equal(t, "message-1", message.MessageID())
	assert.EqualValues(t, 1, message.Seq())
	assert.Equal(t, []byte("message-1"), message.Payload().Bytes())
	assert.Equal(t, "message-1", request.MessageID())
	assert.EqualValues(t, 1, request.ProposedSeq())
	assert.Equal(t, []byte("message-1"), request.Payload().Bytes())
	assert.EqualValues(t, 1, result.Seq())
	assert.True(t, result.AlreadyStored())
	assert.Equal(t, []byte("message-1"), result.Payload().Bytes())
	assert.EqualValues(t, 1, state.CurrentSeq())
	assert.Zero(t, state.ConfirmedSeq())

	unconfirmed := state.Unconfirmed()
	require.Len(t, unconfirmed, 1)
	unconfirmed[0] = UnconfirmedMessage{}
	unconfirmed = append(unconfirmed, UnconfirmedMessage{})
	assert.Len(t, unconfirmed, 2)

	actual := state.Unconfirmed()
	require.Len(t, actual, 1)
	assert.Equal(t, []byte("message-1"), actual[0].Payload().Bytes())
}

// TestDurableQueueValueValidation verifies malformed queue values are rejected.
func TestDurableQueueValueValidation(t *testing.T) {
	payload := durableQueuePayload(t, "payload")
	first := durableQueueMessage(t, "message-1", 1, "first")
	second := durableQueueMessage(t, "message-2", 2, "second")
	duplicateID := durableQueueMessage(t, "message-1", 2, "second")
	gap := durableQueueMessage(t, "message-3", 3, "third")

	tests := map[string]func() error{
		"unconfirmed message ID": func() error {
			_, err := NewUnconfirmedMessage(" ", 1, payload)
			return err
		},
		"unconfirmed sequence": func() error {
			_, err := NewUnconfirmedMessage("message-1", 0, payload)
			return err
		},
		"unconfirmed payload": func() error {
			_, err := NewUnconfirmedMessage("message-1", 1, ReliablePayload{})
			return err
		},
		"store message ID": func() error {
			_, err := NewStoreRequest(" ", 1, payload)
			return err
		},
		"store sequence": func() error {
			_, err := NewStoreRequest("message-1", 0, payload)
			return err
		},
		"store payload": func() error {
			_, err := NewStoreRequest("message-1", 1, ReliablePayload{})
			return err
		},
		"result sequence": func() error {
			_, err := NewStoreResult(0, false, payload)
			return err
		},
		"result payload": func() error {
			_, err := NewStoreResult(1, false, ReliablePayload{})
			return err
		},
		"negative current sequence": func() error {
			_, err := NewDurableQueueState(-1, 0, nil)
			return err
		},
		"negative confirmed sequence": func() error {
			_, err := NewDurableQueueState(0, -1, nil)
			return err
		},
		"confirmed exceeds current": func() error {
			_, err := NewDurableQueueState(1, 2, nil)
			return err
		},
		"missing unconfirmed message": func() error {
			_, err := NewDurableQueueState(2, 0, []UnconfirmedMessage{first})
			return err
		},
		"sequence gap": func() error {
			_, err := NewDurableQueueState(2, 0, []UnconfirmedMessage{first, gap})
			return err
		},
		"blank stored message ID": func() error {
			invalid := second
			invalid.messageID = " "
			_, err := NewDurableQueueState(2, 0, []UnconfirmedMessage{first, invalid})
			return err
		},
		"empty stored payload": func() error {
			invalid := second
			invalid.payload = ReliablePayload{}
			_, err := NewDurableQueueState(2, 0, []UnconfirmedMessage{first, invalid})
			return err
		},
		"duplicate message ID": func() error {
			_, err := NewDurableQueueState(2, 0, []UnconfirmedMessage{first, duplicateID})
			return err
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, test(), gerrors.ErrInvalidMessage)
		})
	}
}

// durableQueuePayload creates a serialized payload for queue tests.
func durableQueuePayload(t *testing.T, data string) ReliablePayload {
	t.Helper()

	payload, err := NewReliablePayload([]byte(data))
	require.NoError(t, err)
	return payload
}

// durableQueueMessage creates an unconfirmed message for queue tests.
func durableQueueMessage(t *testing.T, messageID string, seq int64, data string) UnconfirmedMessage {
	t.Helper()

	message, err := NewUnconfirmedMessage(messageID, seq, durableQueuePayload(t, data))
	require.NoError(t, err)
	return message
}
