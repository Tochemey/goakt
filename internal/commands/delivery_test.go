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

package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
)

// TestReliableDeliveryCommands verifies the immutable command API.
func TestReliableDeliveryCommands(t *testing.T) {
	register, err := NewRegisterConsumer("nonce-1")
	require.NoError(t, err)
	assert.Equal(t, "nonce-1", register.Nonce())

	registrationAck, err := NewRegistrationAck("session-1", 2, "nonce-1")
	require.NoError(t, err)
	assert.Equal(t, "session-1", registrationAck.SessionID())
	assert.EqualValues(t, 2, registrationAck.NextSeq())
	assert.Equal(t, "nonce-1", registrationAck.Nonce())

	request, err := NewRequest("session-1", "nonce-1", 1, 51, true)
	require.NoError(t, err)
	assert.Equal(t, "session-1", request.SessionID())
	assert.Equal(t, "nonce-1", request.RegistrationNonce())
	assert.EqualValues(t, 1, request.ConfirmedSeq())
	assert.EqualValues(t, 51, request.RequestUpToSeq())
	assert.True(t, request.ViaTimeout())

	ack, err := NewAck("session-1", "nonce-1", 1)
	require.NoError(t, err)
	assert.Equal(t, "session-1", ack.SessionID())
	assert.Equal(t, "nonce-1", ack.RegistrationNonce())
	assert.EqualValues(t, 1, ack.ConfirmedSeq())

	source := []byte("payload")
	sequenced, err := NewSequencedMessage("session-1", "message-1", 2, source)
	require.NoError(t, err)

	source[0] = 'P'
	assert.Equal(t, "session-1", sequenced.SessionID())
	assert.Equal(t, "message-1", sequenced.MessageID())
	assert.EqualValues(t, 2, sequenced.Seq())
	assert.Equal(t, []byte("payload"), sequenced.Payload())

	payload := sequenced.Payload()
	payload[0] = 'P'
	assert.Equal(t, []byte("payload"), sequenced.Payload())
	assert.Equal(t, []byte("payload"), sequenced.rawPayload())
}

// TestReliableDeliveryCommandValidation verifies every constructor invariant.
func TestReliableDeliveryCommandValidation(t *testing.T) {
	tests := map[string]func() error{
		"register nonce": func() error {
			_, err := NewRegisterConsumer(" ")
			return err
		},

		"registration session": func() error {
			_, err := NewRegistrationAck(" ", 1, "nonce-1")
			return err
		},

		"registration sequence": func() error {
			_, err := NewRegistrationAck("session-1", 0, "nonce-1")
			return err
		},

		"registration nonce": func() error {
			_, err := NewRegistrationAck("session-1", 1, " ")
			return err
		},

		"request session": func() error {
			_, err := NewRequest(" ", "nonce-1", 0, 1, false)
			return err
		},

		"request nonce": func() error {
			_, err := NewRequest("session-1", " ", 0, 1, false)
			return err
		},

		"request confirmed sequence": func() error {
			_, err := NewRequest("session-1", "nonce-1", -1, 1, false)
			return err
		},

		"request range": func() error {
			_, err := NewRequest("session-1", "nonce-1", 2, 1, false)
			return err
		},

		"ack session": func() error {
			_, err := NewAck(" ", "nonce-1", 0)
			return err
		},

		"ack nonce": func() error {
			_, err := NewAck("session-1", " ", 0)
			return err
		},

		"ack confirmed sequence": func() error {
			_, err := NewAck("session-1", "nonce-1", -1)
			return err
		},

		"sequenced session": func() error {
			_, err := NewSequencedMessage(" ", "message-1", 1, []byte("payload"))
			return err
		},

		"sequenced message ID": func() error {
			_, err := NewSequencedMessage("session-1", " ", 1, []byte("payload"))
			return err
		},

		"sequenced sequence": func() error {
			_, err := NewSequencedMessage("session-1", "message-1", 0, []byte("payload"))
			return err
		},

		"sequenced payload": func() error {
			_, err := NewSequencedMessage("session-1", "message-1", 1, nil)
			return err
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, test(), gerrors.ErrInvalidMessage)
		})
	}
}

// TestReliableDeliveryCommandZeroWatermarks verifies that the initial
// confirmation watermark is valid.
func TestReliableDeliveryCommandZeroWatermarks(t *testing.T) {
	request, err := NewRequest("session-1", "nonce-1", 0, 0, false)
	require.NoError(t, err)
	assert.Zero(t, request.ConfirmedSeq())
	assert.Zero(t, request.RequestUpToSeq())

	ack, err := NewAck("session-1", "nonce-1", 0)
	require.NoError(t, err)
	assert.Zero(t, ack.ConfirmedSeq())
}
