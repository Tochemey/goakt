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
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/id"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/remote"
)

// Async envelopes are framed with an 8-byte sentinel followed by the marshaled
// internalpb form.
//
// The leading four 0xFF bytes are load-bearing, not decoration. The remoting
// dispatcher inspects every inbound frame as [totalLen|nameLen|typeName|payload]
// and, when the embedded type name resolves in the protobuf registry, decodes it
// with the proto serializer regardless of registration order. internalpb.AsyncRequest
// and internalpb.AsyncResponse are compiled into the binary, so a frame naming them
// would decode to the generated proto type and silently bypass every envelope
// branch in the runtime. Reading the sentinel as that header yields a totalLen of
// 0xFFFFFFFF, which no frame can satisfy, so the dispatcher rejects the fast path
// and falls through to the registered serializers.
var (
	asyncRequestMagic  = [8]byte{0xFF, 0xFF, 0xFF, 0xFF, 'A', 'R', 'E', 'Q'}
	asyncResponseMagic = [8]byte{0xFF, 0xFF, 0xFF, 0xFF, 'A', 'R', 'S', 'P'}

	// errNotAsyncRequestFrame is returned when a value or frame is not an async request.
	errNotAsyncRequestFrame = errors.New("remote: not an async request frame")
	// errNotAsyncResponseFrame is returned when a value or frame is not an async response.
	errNotAsyncResponseFrame = errors.New("remote: not an async response frame")
)

// AsyncRequestSerializer is the unexported [remote.Serializer] for asyncRequest
// envelopes. It is registered inside [actorSystem.setupRemoting] and is never
// exposed to application code.
type AsyncRequestSerializer struct{}

// AsyncResponseSerializer is the unexported [remote.Serializer] for asyncResponse
// envelopes. It is registered inside [actorSystem.setupRemoting] and is never
// exposed to application code.
type AsyncResponseSerializer struct{}

// enforce the remote.Serializer interface at compile time.
var (
	_ remote.Serializer = (*AsyncRequestSerializer)(nil)
	_ remote.Serializer = (*AsyncResponseSerializer)(nil)
)

// Serialize encodes an *AsyncRequest into a sentinel-framed internalpb.AsyncRequest.
// Returns errNotAsyncRequestFrame when message is not an *AsyncRequest.
func (x *AsyncRequestSerializer) Serialize(message any) ([]byte, error) {
	request, ok := message.(*AsyncRequest)
	if !ok {
		return nil, errNotAsyncRequestFrame
	}

	payload, err := marshalAsyncPayload(request.Message)
	if err != nil {
		return nil, err
	}

	kind, replyTo, err := encodeAsyncReplyTo(request.ReplyTo)
	if err != nil {
		return nil, err
	}

	return frameAsyncEnvelope(asyncRequestMagic, &internalpb.AsyncRequest{
		CorrelationId: request.CorrelationID,
		ReplyTo:       replyTo,
		ReplyKind:     kind,
		Message:       payload,
	})
}

// Deserialize decodes a sentinel-framed internalpb.AsyncRequest back into an
// *AsyncRequest. Returns errNotAsyncRequestFrame for any other frame, and a
// descriptive error when the frame carries an unusable reply target or payload.
func (x *AsyncRequestSerializer) Deserialize(data []byte) (any, error) {
	body, ok := unframeAsyncEnvelope(asyncRequestMagic, data)
	if !ok {
		return nil, errNotAsyncRequestFrame
	}

	wire := new(internalpb.AsyncRequest)
	if err := proto.Unmarshal(body, wire); err != nil {
		return nil, err
	}

	replyTo, err := decodeAsyncReplyTo(wire.GetReplyKind(), wire.GetReplyTo())
	if err != nil {
		return nil, err
	}

	message, err := unmarshalAsyncPayload(wire.GetMessage())
	if err != nil {
		return nil, err
	}

	return &AsyncRequest{
		CorrelationID: wire.GetCorrelationId(),
		ReplyTo:       replyTo,
		Message:       message,
	}, nil
}

// Serialize encodes an *AsyncResponse into a sentinel-framed internalpb.AsyncResponse.
// Returns errNotAsyncResponseFrame when message is not an *AsyncResponse.
func (x *AsyncResponseSerializer) Serialize(message any) ([]byte, error) {
	response, ok := message.(*AsyncResponse)
	if !ok {
		return nil, errNotAsyncResponseFrame
	}

	wire := &internalpb.AsyncResponse{
		CorrelationId: response.CorrelationID,
		Error:         response.Error,
	}

	if response.Message != nil {
		payload, err := marshalAsyncPayload(response.Message)
		if err != nil {
			return nil, err
		}
		wire.Message = payload
	}

	return frameAsyncEnvelope(asyncResponseMagic, wire)
}

// Deserialize decodes a sentinel-framed internalpb.AsyncResponse back into an
// *AsyncResponse. Returns errNotAsyncResponseFrame for any other frame.
func (x *AsyncResponseSerializer) Deserialize(data []byte) (any, error) {
	body, ok := unframeAsyncEnvelope(asyncResponseMagic, data)
	if !ok {
		return nil, errNotAsyncResponseFrame
	}

	wire := new(internalpb.AsyncResponse)
	if err := proto.Unmarshal(body, wire); err != nil {
		return nil, err
	}

	response := &AsyncResponse{
		CorrelationID: wire.GetCorrelationId(),
		Error:         wire.GetError(),
	}

	if payload := wire.GetMessage(); payload != nil {
		message, err := unmarshalAsyncPayload(payload)
		if err != nil {
			return nil, err
		}
		response.Message = message
	}

	return response, nil
}

// frameAsyncEnvelope marshals the wire form directly behind the sentinel.
//
// Appending onto the sentinel builds the frame in a single allocation and never
// derives a buffer size from the marshaled length, so no size arithmetic can
// overflow on a large payload. The sentinel slice is full, so the append always
// moves to a fresh buffer and the caller's array is never written through.
func frameAsyncEnvelope(magic [8]byte, wire proto.Message) ([]byte, error) {
	frame, err := proto.MarshalOptions{}.MarshalAppend(magic[:], wire)
	if err != nil {
		return nil, err
	}

	return frame, nil
}

// unframeAsyncEnvelope strips the sentinel, reporting whether the frame matches.
func unframeAsyncEnvelope(magic [8]byte, data []byte) ([]byte, bool) {
	if len(data) < len(magic) || !bytes.Equal(data[:len(magic)], magic[:]) {
		return nil, false
	}
	return data[len(magic):], true
}

// encodeAsyncReplyTo maps a typed reply target onto its wire representation.
func encodeAsyncReplyTo(replyTo *AsyncReplyTo) (internalpb.ReplyKind, string, error) {
	if replyTo == nil {
		return internalpb.ReplyKind_REPLY_KIND_CLIENT, "", nil
	}

	switch replyTo.Kind {
	case ReplyToActor:
		if replyTo.Actor == nil {
			return 0, "", gerrors.NewErrInvalidMessage(errors.New("async envelope: actor reply target is missing its address"))
		}
		return internalpb.ReplyKind_REPLY_KIND_ACTOR, replyTo.Actor.String(), nil

	case ReplyToGrain:
		if replyTo.Grain == "" {
			return 0, "", gerrors.NewErrInvalidMessage(errors.New("async envelope: grain reply target is missing its identity"))
		}
		return internalpb.ReplyKind_REPLY_KIND_GRAIN, replyTo.Grain, nil

	default:
		return 0, "", gerrors.NewErrInvalidMessage(fmt.Errorf("async envelope: unknown reply target kind %d", replyTo.Kind))
	}
}

// decodeAsyncReplyTo rebuilds the typed reply target from its wire representation.
// Malformed targets fail here, at the node boundary, rather than at reply time.
func decodeAsyncReplyTo(kind internalpb.ReplyKind, replyTo string) (*AsyncReplyTo, error) {
	switch kind {
	case internalpb.ReplyKind_REPLY_KIND_CLIENT:
		return nil, nil

	case internalpb.ReplyKind_REPLY_KIND_ACTOR:
		addr, err := address.Parse(replyTo)
		if err != nil {
			return nil, err
		}
		return &AsyncReplyTo{Kind: ReplyToActor, Actor: addr}, nil

	case internalpb.ReplyKind_REPLY_KIND_GRAIN:
		// Structural check only: a grain identity is "kind<sep>name". The actor
		// package rebuilds the identity from this exact string, so anything that
		// cannot split into two non-empty halves is rejected here rather than
		// failing later on the reply path.
		kind, name, ok := strings.Cut(replyTo, id.GrainIdentitySeparator)
		if !ok || kind == "" || name == "" {
			return nil, gerrors.ErrInvalidGrainIdentity
		}
		return &AsyncReplyTo{Kind: ReplyToGrain, Grain: replyTo}, nil

	default:
		return nil, gerrors.NewErrInvalidMessage(fmt.Errorf("async envelope: unknown reply kind %v", kind))
	}
}

// marshalAsyncPayload packs a user payload for transport.
//
// Remote envelopes carry their payload in an Any, so the payload must be a
// proto.Message; local delivery never reaches this path and keeps accepting
// any value.
func marshalAsyncPayload(message any) (*anypb.Any, error) {
	if message == nil {
		return nil, gerrors.NewErrInvalidMessage(errors.New("async envelope: payload is required"))
	}

	payload, ok := message.(proto.Message)
	if !ok {
		return nil, gerrors.NewErrInvalidMessage(fmt.Errorf("async envelope: payload must be a proto.Message, got %T", message))
	}

	return anypb.New(payload)
}

// unmarshalAsyncPayload restores a transported payload to its concrete type.
func unmarshalAsyncPayload(payload *anypb.Any) (any, error) {
	if payload == nil {
		return nil, gerrors.NewErrInvalidMessage(errors.New("async envelope: payload is required"))
	}
	return payload.UnmarshalNew()
}
