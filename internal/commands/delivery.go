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
	"bytes"
	"errors"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/types"
)

// RegisterConsumer asks a producer controller to establish a consumer
// registration. A new nonce supersedes every earlier registration attempt.
type RegisterConsumer struct {
	// The nonce identifies this registration attempt.
	nonce string
}

// NewRegisterConsumer creates a consumer registration attempt.
func NewRegisterConsumer(nonce string) (*RegisterConsumer, error) {
	command := &RegisterConsumer{nonce: nonce}
	if err := command.validate(); err != nil {
		return nil, err
	}

	return command, nil
}

// validate checks whether x contains a registration nonce.
func (x RegisterConsumer) validate() error {
	if types.IsBlank(x.nonce) {
		return gerrors.NewErrInvalidMessage(errors.New("registration nonce is required"))
	}

	return nil
}

// Nonce returns the registration attempt identifier.
func (x RegisterConsumer) Nonce() string {
	return x.nonce
}

// RegistrationAck accepts the consumer's latest registration attempt and
// establishes the producer session it must use.
type RegistrationAck struct {
	// The session ID identifies the producer controller incarnation.
	sessionID string
	// The next sequence is the first sequence the consumer should expect.
	nextSeq int64
	// The nonce identifies the registration attempt being accepted.
	nonce string
}

// NewRegistrationAck creates a registration response for nonce.
func NewRegistrationAck(sessionID string, nextSeq int64, nonce string) (*RegistrationAck, error) {
	command := &RegistrationAck{
		sessionID: sessionID,
		nextSeq:   nextSeq,
		nonce:     nonce,
	}

	if err := command.validate(); err != nil {
		return nil, err
	}

	return command, nil
}

// validate checks whether x identifies a valid registration response.
func (x RegistrationAck) validate() error {
	if types.IsBlank(x.sessionID) {
		return gerrors.NewErrInvalidMessage(errors.New("session ID is required"))
	}

	if x.nextSeq <= 0 {
		return gerrors.NewErrInvalidMessage(errors.New("next sequence must be greater than zero"))
	}

	if types.IsBlank(x.nonce) {
		return gerrors.NewErrInvalidMessage(errors.New("registration nonce is required"))
	}

	return nil
}

// SessionID returns the producer controller session.
func (x RegistrationAck) SessionID() string {
	return x.sessionID
}

// NextSeq returns the first sequence the consumer should expect.
func (x RegistrationAck) NextSeq() int64 {
	return x.nextSeq
}

// Nonce returns the accepted registration attempt identifier.
func (x RegistrationAck) Nonce() string {
	return x.nonce
}

// Request grants demand through RequestUpToSeq and reports the consumer's
// latest confirmed sequence.
type Request struct {
	// The session ID identifies the producer controller incarnation.
	sessionID string
	// The registration nonce identifies the accepted consumer registration.
	registrationNonce string
	// The confirmed sequence is the highest sequence processed by the consumer.
	confirmedSeq int64
	// The request-up-to sequence is the highest sequence the producer may send.
	requestUpToSeq int64
	// Via timeout asks the producer to resend unconfirmed messages in range.
	viaTimeout bool
}

// NewRequest creates a demand update for an established registration.
func NewRequest(sessionID, registrationNonce string, confirmedSeq, requestUpToSeq int64, viaTimeout bool) (*Request, error) {
	command := &Request{
		sessionID:         sessionID,
		registrationNonce: registrationNonce,
		confirmedSeq:      confirmedSeq,
		requestUpToSeq:    requestUpToSeq,
		viaTimeout:        viaTimeout,
	}

	if err := command.validate(); err != nil {
		return nil, err
	}

	return command, nil
}

// validate checks whether x contains a valid demand range.
func (x Request) validate() error {
	if types.IsBlank(x.sessionID) {
		return gerrors.NewErrInvalidMessage(errors.New("session ID is required"))
	}

	if types.IsBlank(x.registrationNonce) {
		return gerrors.NewErrInvalidMessage(errors.New("registration nonce is required"))
	}

	if x.confirmedSeq < 0 {
		return gerrors.NewErrInvalidMessage(errors.New("confirmed sequence must not be negative"))
	}

	if x.requestUpToSeq < x.confirmedSeq {
		return gerrors.NewErrInvalidMessage(errors.New("request-up-to sequence must not precede the confirmed sequence"))
	}

	return nil
}

// SessionID returns the producer controller session.
func (x Request) SessionID() string {
	return x.sessionID
}

// RegistrationNonce returns the accepted consumer registration identifier.
func (x Request) RegistrationNonce() string {
	return x.registrationNonce
}

// ConfirmedSeq returns the highest sequence processed by the consumer.
func (x Request) ConfirmedSeq() int64 {
	return x.confirmedSeq
}

// RequestUpToSeq returns the highest sequence the producer may send.
func (x Request) RequestUpToSeq() int64 {
	return x.requestUpToSeq
}

// ViaTimeout reports whether the producer should resend unconfirmed messages
// covered by this request.
func (x Request) ViaTimeout() bool {
	return x.viaTimeout
}

// Ack advances the producer's confirmed sequence without granting additional
// demand.
type Ack struct {
	// The session ID identifies the producer controller incarnation.
	sessionID string
	// The registration nonce identifies the accepted consumer registration.
	registrationNonce string
	// The confirmed sequence is the highest sequence processed by the consumer.
	confirmedSeq int64
}

// NewAck creates a confirmation update for an established registration.
func NewAck(sessionID, registrationNonce string, confirmedSeq int64) (*Ack, error) {
	command := &Ack{
		sessionID:         sessionID,
		registrationNonce: registrationNonce,
		confirmedSeq:      confirmedSeq,
	}

	if err := command.validate(); err != nil {
		return nil, err
	}

	return command, nil
}

// validate checks whether x contains a valid confirmation watermark.
func (x Ack) validate() error {
	if types.IsBlank(x.sessionID) {
		return gerrors.NewErrInvalidMessage(errors.New("session ID is required"))
	}

	if types.IsBlank(x.registrationNonce) {
		return gerrors.NewErrInvalidMessage(errors.New("registration nonce is required"))
	}

	if x.confirmedSeq < 0 {
		return gerrors.NewErrInvalidMessage(errors.New("confirmed sequence must not be negative"))
	}

	return nil
}

// SessionID returns the producer controller session.
func (x Ack) SessionID() string {
	return x.sessionID
}

// RegistrationNonce returns the accepted consumer registration identifier.
func (x Ack) RegistrationNonce() string {
	return x.registrationNonce
}

// ConfirmedSeq returns the highest sequence processed by the consumer.
func (x Ack) ConfirmedSeq() int64 {
	return x.confirmedSeq
}

// SequencedMessage carries one serialized application message at its assigned
// position in the producer stream.
type SequencedMessage struct {
	// The session ID identifies the producer controller incarnation.
	sessionID string
	// The message ID identifies the application message across retries.
	messageID string
	// The sequence number is the message's ordered position in the stream.
	seq int64
	// The payload contains the serializer's self-describing message frame.
	payload []byte
}

// NewSequencedMessage creates an immutable sequenced-message snapshot.
func NewSequencedMessage(sessionID, messageID string, seq int64, payload []byte) (*SequencedMessage, error) {
	command := &SequencedMessage{
		sessionID: sessionID,
		messageID: messageID,
		seq:       seq,
		payload:   payload,
	}

	if err := command.validate(); err != nil {
		return nil, err
	}

	command.payload = bytes.Clone(payload)
	return command, nil
}

// validate checks whether x contains an ordered serialized message.
func (x SequencedMessage) validate() error {
	if types.IsBlank(x.sessionID) {
		return gerrors.NewErrInvalidMessage(errors.New("session ID is required"))
	}

	if types.IsBlank(x.messageID) {
		return gerrors.NewErrInvalidMessage(errors.New("message ID is required"))
	}

	if x.seq <= 0 {
		return gerrors.NewErrInvalidMessage(errors.New("sequence must be greater than zero"))
	}

	if len(x.payload) == 0 {
		return gerrors.NewErrInvalidMessage(errors.New("serialized payload is required"))
	}

	return nil
}

// SessionID returns the producer controller session.
func (x SequencedMessage) SessionID() string {
	return x.sessionID
}

// MessageID returns the stable application message identifier.
func (x SequencedMessage) MessageID() string {
	return x.messageID
}

// Seq returns the message's ordered position in the producer stream.
func (x SequencedMessage) Seq() int64 {
	return x.seq
}

// Payload returns a copy of the serialized application message.
func (x SequencedMessage) Payload() []byte {
	return bytes.Clone(x.payload)
}

// rawPayload returns the serialized application message without copying it.
// Callers must not retain or modify the returned slice.
func (x SequencedMessage) rawPayload() []byte {
	return x.payload
}
