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

	"google.golang.org/protobuf/proto"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/remote"
)

var (
	// deliveryFrameMagic prevents reliable-delivery envelopes from being
	// mistaken for their generated protobuf wire types by remoting.
	deliveryFrameMagic = [8]byte{0xFF, 0xFF, 0xFF, 0xFF, 'R', 'D', 'E', 'L'}
	// errNotDeliveryFrame tells remoting to try another registered serializer.
	errNotDeliveryFrame = errors.New("remote: not a reliable delivery frame")
)

// DeliverySerializer encodes the reliable-delivery commands exchanged by the
// producer and consumer controllers.
type DeliverySerializer struct{}

// DeliverySerializer implements remote.Serializer.
var _ remote.Serializer = (*DeliverySerializer)(nil)

// EncodeReliablePayload serializes and snapshots an application message before
// reliable delivery begins asynchronous processing. The serializer must come
// from the existing remoting Serializer(message) lookup.
func EncodeReliablePayload(message any, serializer remote.Serializer) ([]byte, error) {
	if types.IsNil(serializer) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("serializer is required"))
	}

	if types.IsNil(message) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("reliable payload is required"))
	}

	data, err := serializer.Serialize(message)
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return nil, gerrors.NewErrInvalidMessage(errors.New("serializer returned an empty payload"))
	}

	return bytes.Clone(data), nil
}

// DecodeReliablePayload restores a fresh application message from its
// serialized snapshot. The serializer should be the composite serializer
// returned by the existing remoting Serializer(nil) lookup. As on the normal
// remoting path, the serializer must not retain or modify data.
func DecodeReliablePayload(data []byte, serializer remote.Serializer) (any, error) {
	if types.IsNil(serializer) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("serializer is required"))
	}

	if len(data) == 0 {
		return nil, gerrors.NewErrInvalidMessage(errors.New("serialized payload is required"))
	}

	message, err := serializer.Deserialize(data)
	if err != nil {
		return nil, err
	}

	if types.IsNil(message) {
		return nil, errors.New("reliable delivery: serializer returned a nil payload")
	}

	return message, nil
}

// Serialize converts a reliable-delivery command into a sentinel-framed
// DeliveryEnvelope.
func (x *DeliverySerializer) Serialize(message any) ([]byte, error) {
	if types.IsNil(message) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("reliable delivery command is required"))
	}

	envelope := new(internalpb.DeliveryEnvelope)

	switch command := message.(type) {
	case *RegisterConsumer:
		if err := command.validate(); err != nil {
			return nil, err
		}

		rc := &internalpb.RegisterConsumer{}
		rc.SetNonce(command.nonce)
		envelope.SetRegisterConsumer(proto.ValueOrDefault(rc))

	case *RegistrationAck:
		if err := command.validate(); err != nil {
			return nil, err
		}

		ra := &internalpb.RegistrationAck{}
		ra.SetSessionId(command.sessionID)
		ra.SetNextSeq(command.nextSeq)
		ra.SetNonce(command.nonce)
		envelope.SetRegistrationAck(proto.ValueOrDefault(ra))

	case *Request:
		if err := command.validate(); err != nil {
			return nil, err
		}

		request := &internalpb.Request{}
		request.SetSessionId(command.sessionID)
		request.SetRegistrationNonce(command.registrationNonce)
		request.SetConfirmedSeq(command.confirmedSeq)
		request.SetRequestUpToSeq(command.requestUpToSeq)
		request.SetViaTimeout(command.viaTimeout)
		envelope.SetRequest(proto.ValueOrDefault(request))

	case *Ack:
		if err := command.validate(); err != nil {
			return nil, err
		}

		ack := &internalpb.Ack{}
		ack.SetSessionId(command.sessionID)
		ack.SetRegistrationNonce(command.registrationNonce)
		ack.SetConfirmedSeq(command.confirmedSeq)
		envelope.SetAck(proto.ValueOrDefault(ack))

	case *SequencedMessage:
		if err := command.validate(); err != nil {
			return nil, err
		}

		rp := &internalpb.ReliablePayload{}
		rp.SetData(command.rawPayload())
		sequenced := &internalpb.SequencedMessage{}
		sequenced.SetSessionId(command.sessionID)
		sequenced.SetMessageId(command.messageID)
		sequenced.SetSeq(command.seq)
		sequenced.SetPayload(rp)

		if command.chunked {
			chunkInfo := &internalpb.ChunkInfo{}
			chunkInfo.SetFirst(command.firstChunk)
			chunkInfo.SetLast(command.lastChunk)
			sequenced.SetChunkInfo(chunkInfo)
		}

		envelope.SetSequencedMessage(proto.ValueOrDefault(sequenced))

	default:
		return nil, errNotDeliveryFrame
	}

	return proto.MarshalOptions{}.MarshalAppend(deliveryFrameMagic[:], envelope)
}

// Deserialize converts a sentinel-framed DeliveryEnvelope into an immutable
// reliable-delivery command.
func (x *DeliverySerializer) Deserialize(data []byte) (any, error) {
	if len(data) < len(deliveryFrameMagic) || !bytes.Equal(data[:len(deliveryFrameMagic)], deliveryFrameMagic[:]) {
		return nil, errNotDeliveryFrame
	}

	envelope := new(internalpb.DeliveryEnvelope)
	if err := proto.Unmarshal(data[len(deliveryFrameMagic):], envelope); err != nil {
		return nil, err
	}

	switch envelope.WhichCommand() {
	case internalpb.DeliveryEnvelope_RegisterConsumer_case:
		message := envelope.GetRegisterConsumer()
		return NewRegisterConsumer(message.GetNonce())

	case internalpb.DeliveryEnvelope_RegistrationAck_case:
		message := envelope.GetRegistrationAck()
		return NewRegistrationAck(message.GetSessionId(), message.GetNextSeq(), message.GetNonce())

	case internalpb.DeliveryEnvelope_Request_case:
		message := envelope.GetRequest()
		return NewRequest(message.GetSessionId(), message.GetRegistrationNonce(), message.GetConfirmedSeq(), message.GetRequestUpToSeq(), message.GetViaTimeout())

	case internalpb.DeliveryEnvelope_Ack_case:
		message := envelope.GetAck()
		return NewAck(message.GetSessionId(), message.GetRegistrationNonce(), message.GetConfirmedSeq())

	case internalpb.DeliveryEnvelope_SequencedMessage_case:
		message := envelope.GetSequencedMessage()
		payload := message.GetPayload()

		if payload == nil {
			return nil, gerrors.NewErrInvalidMessage(errors.New("reliable payload is required"))
		}

		if chunk := message.GetChunkInfo(); chunk != nil {
			return NewChunkedSequencedMessage(message.GetSessionId(), message.GetMessageId(), message.GetSeq(), payload.GetData(), chunk.GetFirst(), chunk.GetLast())
		}

		return NewSequencedMessage(message.GetSessionId(), message.GetMessageId(), message.GetSeq(), payload.GetData())

	default:
		return nil, gerrors.NewErrInvalidMessage(errors.New("reliable delivery command is required"))
	}
}
