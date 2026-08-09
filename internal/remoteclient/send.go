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

package remoteclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/remote"
)

// maxBatchAskConcurrency bounds concurrent duplex asks issued by one
// RemoteBatchAsk call so a large batch cannot fan out unbounded goroutines.
const maxBatchAskConcurrency = 64

// tellParams holds the wire-ready pieces of one fire-and-forget user
// message before it is encoded as a duplex DATA envelope or wrapped in a
// legacy RemoteTellRequest. Fields mirror [inet.DataEnvelope] so the duplex
// and legacy paths share one construction site.
type tellParams struct {
	// sender is the logical actor path of the message originator.
	sender string
	// receiver is the destination actor path on the remote node.
	receiver string
	// payload is the serialized message body produced by the caller's serializer.
	payload []byte
	// serID is the duplex serializer byte (public proto, JSON, CBOR, custom).
	serID byte
	// typeName is the protobuf full name or frame type name carried for decoding.
	typeName string
	// metadata is marshaled [inet.Metadata] when the context carries headers or deadlines.
	metadata []byte
}

// askParams carries the same wire fields as [userTellParams] for a
// request/response user message. The ask path always sets expectsReply and
// merges ask-specific metadata (including the remaining deadline).
type askParams struct {
	tellParams
	// timeout is the caller-supplied ask timeout mirrored onto legacy
	// RemoteAskRequest.Timeout so fallback peers enforce the same bound.
	timeout time.Duration
}

// sendControl routes an internal control RPC to host:port, choosing duplex or
// legacy SendProto based on the client's protocol pin and per-peer cache.
// When duplex dial or handshake fails with [errPreferLegacy], the call
// transparently retries on the legacy unary path.
func (x *client) sendControl(ctx context.Context, host string, port int, req proto.Message) (proto.Message, error) {
	if x.pinRequiresLegacy() {
		return x.sendControlLegacy(ctx, host, port, req)
	}

	peer := x.peerFor(host, port)
	resp, err := x.sendControlDuplex(ctx, peer, req)
	if err == nil {
		return resp, nil
	}

	if errors.Is(err, errPreferLegacy) {
		return x.sendControlLegacy(ctx, host, port, req)
	}

	return nil, err
}

// sendControlLegacy sends req through the pooled legacy NetClient after
// registering with the peer's legacyInflight drain counter so a concurrent
// switchover to duplex waits for this unary send to finish.
func (x *client) sendControlLegacy(ctx context.Context, host string, port int, req proto.Message) (proto.Message, error) {
	p := x.peerFor(host, port)
	p.beginLegacySend()
	defer p.endLegacySend()

	nc := x.NetClient(host, port)
	return nc.SendProto(ctx, req)
}

// sendControlDuplex encodes req as an expectsReply DATA frame on the control
// lane, waits for the correlated REPLY, and decodes it with [decodeControlReply].
// Any transport failure closes the peer session so the next call re-dials.
func (x *client) sendControlDuplex(ctx context.Context, p *peer, req proto.Message) (proto.Message, error) {
	session, err := p.ensureDuplex(ctx)
	if err != nil {
		return nil, err
	}

	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}

	// Control RPCs stamp the caller's deadline like user asks so the server
	// bounds handler work, matching the legacy path's metadata propagation.
	meta, flags := askMetadataFromContext(ctx)
	env := inet.DataEnvelope{
		TypeName:     string(proto.MessageName(req)),
		SerializerID: inet.SerializerIDInternalProto,
		Metadata:     meta,
		Payload:      payload,
	}

	encoded, err := inet.EncodeDataEnvelope(env)
	if err != nil {
		return nil, err
	}

	frame := inet.Frame{
		Type:    inet.FrameTypeData,
		Flags:   flags,
		Lane:    inet.LaneControl,
		Payload: encoded,
	}

	replyFrame, err := session.Ask(ctx, frame)
	if err != nil {
		if shouldRetireDuplexSession(err, replyFrame) {
			p.retireSession(session)
		}
		return nil, mapDuplexErr(err)
	}

	return decodeControlReply(replyFrame)
}

// sendTell routes one fire-and-forget user message, falling back to legacy
// SendProto when the protocol pin or peer cache requires the unary path.
func (x *client) sendTell(ctx context.Context, host string, port int, params tellParams) error {
	if x.pinRequiresLegacy() {
		return x.sendTellLegacy(ctx, host, port, params)
	}

	p := x.peerFor(host, port)
	err := x.sendTellDuplex(ctx, p, params)
	if err == nil {
		return nil
	}

	if errors.Is(err, errPreferLegacy) {
		return x.sendTellLegacy(ctx, host, port, params)
	}

	return err
}

// sendTellLegacy wraps params in RemoteTellRequest and sends via SendProto.
// beginLegacySend/endLegacySend bracket the call for switchover drain.
func (x *client) sendTellLegacy(ctx context.Context, host string, port int, params tellParams) error {
	p := x.peerFor(host, port)
	p.beginLegacySend()
	defer p.endLegacySend()

	nc := x.NetClient(host, port)
	resp, err := nc.SendProto(ctx, &internalpb.RemoteTellRequest{
		RemoteMessages: []*internalpb.RemoteMessage{{
			Sender:   params.sender,
			Receiver: params.receiver,
			Message:  params.payload,
			Metadata: metadataMapFromBytes(params.metadata),
		}},
	})
	if err != nil {
		return err
	}

	return checkProtoError(resp)
}

// sendTellDuplex enqueues one DATA frame without expecting a reply. Transport
// errors retire the duplex session so later sends re-probe. All Milestone 2
// frames carry the control lane byte, matching the single negotiated
// control-role connection; per-role lane bytes arrive with Milestone 3.
func (x *client) sendTellDuplex(ctx context.Context, p *peer, params tellParams) error {
	session, err := p.ensureDuplex(ctx)
	if err != nil {
		return err
	}

	flags := byte(0)
	if len(params.metadata) > 0 {
		flags |= inet.FrameFlagHasMetadata
	}

	env := inet.DataEnvelope{
		Sender:       params.sender,
		Receiver:     params.receiver,
		TypeName:     params.typeName,
		SerializerID: params.serID,
		Metadata:     params.metadata,
		Payload:      params.payload,
	}

	encoded, err := inet.EncodeDataEnvelope(env)
	if err != nil {
		return err
	}

	frame := inet.Frame{
		Type:    inet.FrameTypeData,
		Flags:   flags,
		Lane:    inet.LaneControl,
		Payload: encoded,
	}

	if err := session.Tell(ctx, frame); err != nil {
		if shouldRetireDuplexSession(err, inet.Frame{}) {
			p.retireSession(session)
		}
		return mapDuplexErr(err)
	}

	return nil
}

// sendAsk routes one user ask and deserializes the response with serializer.
// Falls back to legacy RemoteAskRequest when duplex is unavailable.
func (x *client) sendAsk(ctx context.Context, host string, port int, params askParams, serializer remote.Serializer) (any, error) {
	if x.pinRequiresLegacy() {
		return x.sendAskLegacy(ctx, host, port, params, serializer)
	}

	p := x.peerFor(host, port)
	resp, err := x.sendAskDuplex(ctx, p, params, serializer)
	if err == nil {
		return resp, nil
	}

	if errors.Is(err, errPreferLegacy) {
		return x.sendAskLegacy(ctx, host, port, params, serializer)
	}

	return nil, err
}

// sendAskLegacy sends RemoteAskRequest via SendProto and deserializes the first
// response message. An empty Messages slice yields a nil reply without error.
func (x *client) sendAskLegacy(ctx context.Context, host string, port int, params askParams, serializer remote.Serializer) (any, error) {
	p := x.peerFor(host, port)
	p.beginLegacySend()
	defer p.endLegacySend()

	nc := x.NetClient(host, port)
	req := &internalpb.RemoteAskRequest{
		RemoteMessages: []*internalpb.RemoteMessage{{
			Sender:   params.sender,
			Receiver: params.receiver,
			Message:  params.payload,
			Metadata: metadataMapFromBytes(params.metadata),
		}},
	}
	if params.timeout > 0 {
		req.Timeout = durationpb.New(params.timeout)
	}

	resp, err := nc.SendProto(ctx, req)
	if err != nil {
		return nil, err
	}

	if err := checkProtoError(resp); err != nil {
		return nil, err
	}

	askResp, ok := resp.(*internalpb.RemoteAskResponse)
	if !ok {
		return nil, errors.New("invalid response type")
	}

	if len(askResp.Messages) == 0 {
		return nil, nil
	}

	return serializer.Deserialize(askResp.Messages[0])
}

// sendAskDuplex sends an expectsReply DATA frame on the ordinary lane, decodes
// the REPLY envelope, and maps internalpb.Error payloads to Go errors via
// [checkProtoError]. Terminal transport failures close the peer session;
// caller timeouts and request-scoped ERROR frames do not.
func (x *client) sendAskDuplex(ctx context.Context, p *peer, params askParams, serializer remote.Serializer) (any, error) {
	session, err := p.ensureDuplex(ctx)
	if err != nil {
		return nil, err
	}

	flags := inet.FrameFlagExpectsReply | inet.FrameFlagHasMetadata
	meta, _ := askMetadataFromContext(ctx)

	env := inet.DataEnvelope{
		Sender:       params.sender,
		Receiver:     params.receiver,
		TypeName:     params.typeName,
		SerializerID: params.serID,
		Metadata:     meta,
		Payload:      params.payload,
	}

	encoded, err := inet.EncodeDataEnvelope(env)
	if err != nil {
		return nil, err
	}

	frame := inet.Frame{
		Type:    inet.FrameTypeData,
		Flags:   flags,
		Lane:    inet.LaneControl,
		Payload: encoded,
	}

	replyFrame, err := session.Ask(ctx, frame)
	if err != nil {
		if shouldRetireDuplexSession(err, replyFrame) {
			p.retireSession(session)
		}
		return nil, mapDuplexErr(err)
	}

	replyEnv, err := inet.DecodeReplyEnvelope(replyFrame.Payload, replyFrame.HasMetadata())
	if err != nil {
		return nil, err
	}

	msg, err := deserializeReplyEnvelope(replyEnv, serializer)
	if err != nil {
		return nil, err
	}

	if errResp, ok := msg.(*internalpb.Error); ok {
		return nil, checkProtoError(errResp)
	}

	return msg, nil
}

// sendBatchTellDuplex sends multiple tells on one duplex session when the peer
// supports duplex; otherwise delegates to [sendBatchTellLegacy]. Each message
// is a separate DATA frame on the ordinary lane. The session is closed on the
// first transport error.
func (x *client) sendBatchTellDuplex(ctx context.Context, host string, port int, params []tellParams) error {
	if x.pinRequiresLegacy() {
		return x.sendBatchTellLegacy(ctx, host, port, params)
	}

	p := x.peerFor(host, port)
	session, err := p.ensureDuplex(ctx)
	if err != nil {
		if errors.Is(err, errPreferLegacy) {
			return x.sendBatchTellLegacy(ctx, host, port, params)
		}

		return err
	}

	for _, param := range params {
		flags := byte(0)
		if len(param.metadata) > 0 {
			flags |= inet.FrameFlagHasMetadata
		}

		env := inet.DataEnvelope{
			Sender:       param.sender,
			Receiver:     param.receiver,
			TypeName:     param.typeName,
			SerializerID: param.serID,
			Metadata:     param.metadata,
			Payload:      param.payload,
		}

		encoded, encErr := inet.EncodeDataEnvelope(env)
		if encErr != nil {
			return encErr
		}

		frame := inet.Frame{
			Type:    inet.FrameTypeData,
			Flags:   flags,
			Lane:    inet.LaneControl,
			Payload: encoded,
		}

		if tellErr := session.Tell(ctx, frame); tellErr != nil {
			if shouldRetireDuplexSession(tellErr, inet.Frame{}) {
				p.retireSession(session)
			}
			return mapDuplexErr(tellErr)
		}
	}

	return nil
}

// sendBatchTellLegacy batches all messages into one RemoteTellRequest and
// sends it through the pooled legacy NetClient.
func (x *client) sendBatchTellLegacy(ctx context.Context, host string, port int, params []tellParams) error {
	p := x.peerFor(host, port)
	p.beginLegacySend()
	defer p.endLegacySend()

	msgs := make([]*internalpb.RemoteMessage, 0, len(params))
	for _, param := range params {
		msgs = append(msgs, &internalpb.RemoteMessage{
			Sender:   param.sender,
			Receiver: param.receiver,
			Message:  param.payload,
			Metadata: metadataMapFromBytes(param.metadata),
		})
	}

	nc := x.NetClient(host, port)
	resp, err := nc.SendProto(ctx, &internalpb.RemoteTellRequest{RemoteMessages: msgs})
	if err != nil {
		return err
	}

	return checkProtoError(resp)
}

// batchAskResult holds one goroutine outcome from [sendBatchAskDuplex].
// index preserves request order when merging into the results slice.
type batchAskResult struct {
	// index is the position of this ask in the original params slice.
	index int
	// value is the deserialized reply when err is nil.
	value any
	// err is the first failure encountered while sending or decoding this ask.
	err error
	// reply is the raw Ask frame when err came from session.Ask; used to
	// distinguish request-scoped ERROR from terminal transport loss.
	reply inet.Frame
}

// sendBatchAskDuplex issues N concurrent asks on one duplex session and returns
// responses indexed to match params. Concurrency is capped by
// [maxBatchAskConcurrency]. The first error is returned; the session is
// retired only when that error is a terminal transport failure.
func (x *client) sendBatchAskDuplex(ctx context.Context, host string, port int, params []askParams, serializers []remote.Serializer) ([]any, error) {
	if x.pinRequiresLegacy() {
		return x.sendBatchAskLegacy(ctx, host, port, params, serializers)
	}

	p := x.peerFor(host, port)
	session, err := p.ensureDuplex(ctx)
	if err != nil {
		if errors.Is(err, errPreferLegacy) {
			return x.sendBatchAskLegacy(ctx, host, port, params, serializers)
		}

		return nil, err
	}

	results := make([]any, len(params))
	var wg sync.WaitGroup
	out := make(chan batchAskResult, len(params))
	sem := make(chan struct{}, maxBatchAskConcurrency)

	// One metadata blob and flag set serve every ask in the batch: all asks
	// share ctx, so per-goroutine marshaling would only add allocations.
	meta, _ := askMetadataFromContext(ctx)
	flags := inet.FrameFlagExpectsReply | inet.FrameFlagHasMetadata

	for i, param := range params {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, ask askParams, ser remote.Serializer) {
			defer wg.Done()
			defer func() { <-sem }()

			env := inet.DataEnvelope{
				Sender:       ask.sender,
				Receiver:     ask.receiver,
				TypeName:     ask.typeName,
				SerializerID: ask.serID,
				Metadata:     meta,
				Payload:      ask.payload,
			}

			encoded, encErr := inet.EncodeDataEnvelope(env)
			if encErr != nil {
				out <- batchAskResult{index: idx, err: encErr}
				return
			}

			frame := inet.Frame{
				Type:    inet.FrameTypeData,
				Flags:   flags,
				Lane:    inet.LaneControl,
				Payload: encoded,
			}

			replyFrame, askErr := session.Ask(ctx, frame)
			if askErr != nil {
				out <- batchAskResult{index: idx, err: mapDuplexErr(askErr), reply: replyFrame}
				return
			}

			replyEnv, decErr := inet.DecodeReplyEnvelope(replyFrame.Payload, replyFrame.HasMetadata())
			if decErr != nil {
				out <- batchAskResult{index: idx, err: decErr}
				return
			}

			val, desErr := deserializeReplyEnvelope(replyEnv, ser)
			if desErr == nil {
				if errResp, ok := val.(*internalpb.Error); ok {
					desErr = checkProtoError(errResp)
					val = nil
				}
			}

			out <- batchAskResult{index: idx, value: val, err: desErr}
		}(i, param, serializers[i])
	}

	wg.Wait()
	close(out)

	var firstErr error
	var firstReply inet.Frame
	for res := range out {
		if res.err != nil && firstErr == nil {
			firstErr = res.err
			firstReply = res.reply
		}

		if res.err == nil {
			results[res.index] = res.value
		}
	}

	if firstErr != nil {
		if shouldRetireDuplexSession(firstErr, firstReply) {
			p.retireSession(session)
		}
		return nil, firstErr
	}

	return results, nil
}

// sendBatchAskLegacy sends one RemoteAskRequest containing all messages and
// deserializes each response with the matching serializer entry. Failures on
// any message abort the whole batch.
func (x *client) sendBatchAskLegacy(ctx context.Context, host string, port int, params []askParams, serializers []remote.Serializer) ([]any, error) {
	p := x.peerFor(host, port)
	p.beginLegacySend()
	defer p.endLegacySend()

	msgs := make([]*internalpb.RemoteMessage, 0, len(params))
	for _, param := range params {
		msgs = append(msgs, &internalpb.RemoteMessage{
			Sender:   param.sender,
			Receiver: param.receiver,
			Message:  param.payload,
			Metadata: metadataMapFromBytes(param.metadata),
		})
	}

	nc := x.NetClient(host, port)
	req := &internalpb.RemoteAskRequest{RemoteMessages: msgs}
	if len(params) > 0 && params[0].timeout > 0 {
		req.Timeout = durationpb.New(params[0].timeout)
	}

	resp, err := nc.SendProto(ctx, req)
	if err != nil {
		return nil, err
	}

	if err := checkProtoError(resp); err != nil {
		return nil, err
	}

	askResp, ok := resp.(*internalpb.RemoteAskResponse)
	if !ok {
		return nil, errors.New("invalid response type")
	}

	responses := make([]any, 0, len(askResp.Messages))
	for i, msg := range askResp.Messages {
		des, desErr := serializers[i].Deserialize(msg)
		if desErr != nil {
			return nil, desErr
		}

		responses = append(responses, des)
	}

	return responses, nil
}

// decodeControlReply unmarshals a duplex REPLY or ERROR frame into the
// appropriate internal protobuf type. Control replies must use
// SerializerIDInternalProto; other serializer IDs are rejected.
func decodeControlReply(frame inet.Frame) (proto.Message, error) {
	if frame.Type == inet.FrameTypeError {
		return nil, decodeErrorPayload(frame.Payload)
	}

	replyEnv, err := inet.DecodeReplyEnvelope(frame.Payload, frame.HasMetadata())
	if err != nil {
		return nil, err
	}

	if replyEnv.SerializerID != inet.SerializerIDInternalProto {
		return nil, fmt.Errorf("remote: unexpected control reply serializer 0x%02x", replyEnv.SerializerID)
	}

	msgType, err := inet.FindMessageType(protoreflect.FullName(replyEnv.TypeName))
	if err != nil {
		return nil, err
	}

	msg := msgType.New().Interface()
	if err := proto.Unmarshal(replyEnv.Payload, msg); err != nil {
		return nil, err
	}

	return msg, nil
}

// deserializeReplyEnvelope turns a user REPLY envelope into a Go value using
// serializer for public wire formats. Empty payload with no type name denotes
// a void reply (nil, nil). Internal protobuf types bypass the user serializer.
func deserializeReplyEnvelope(env inet.ReplyEnvelope, serializer remote.Serializer) (any, error) {
	if len(env.Payload) == 0 && env.TypeName == "" {
		return nil, nil
	}

	switch env.SerializerID {
	case inet.SerializerIDPublicProto, inet.SerializerIDJSON, inet.SerializerIDCBOR, inet.SerializerIDCustom:
		return serializer.Deserialize(env.Payload)
	default:
		msgType, err := inet.FindMessageType(protoreflect.FullName(env.TypeName))
		if err != nil {
			return nil, err
		}

		msg := msgType.New().Interface()
		if err := proto.Unmarshal(env.Payload, msg); err != nil {
			return nil, err
		}

		return msg, nil
	}
}

// serializerWireID maps the registered serializer and message to the duplex
// serializer byte and type name used on the wire. Unknown serializers fall
// back to SerializerIDCustom with an empty type name.
func serializerWireID(serializer remote.Serializer, message any, payload []byte) (byte, string) {
	switch serializer.(type) {
	case *remote.ProtoSerializer:
		if msg, ok := message.(proto.Message); ok {
			return inet.SerializerIDPublicProto, string(proto.MessageName(msg))
		}
	case *remote.JSONSerializer:
		if name, ok := frameTypeName(payload); ok {
			return inet.SerializerIDJSON, string(name)
		}
	case *remote.CBORSerializer:
		if name, ok := frameTypeName(payload); ok {
			return inet.SerializerIDCBOR, string(name)
		}
	}

	return inet.SerializerIDCustom, ""
}

// metadataWireFromContext extracts marshaled metadata and sets
// FrameFlagHasMetadata when the context carries non-empty [inet.Metadata].
func metadataWireFromContext(ctx context.Context) ([]byte, byte) {
	md, ok := inet.FromContext(ctx)
	if !ok || md == nil {
		return nil, 0
	}

	wire := md.MarshalBinary()
	if len(wire) == 0 {
		return nil, 0
	}

	return wire, inet.FrameFlagHasMetadata
}

// askMetadataFromContext builds metadata for duplex asks by copying context
// headers and deadlines. The context deadline always wins so the remote side
// enforces the caller's remaining time budget on its own clock.
func askMetadataFromContext(ctx context.Context) ([]byte, byte) {
	md := inet.NewMetadata()
	if existing, ok := inet.FromContext(ctx); ok && existing != nil {
		existing.IterateHeaders(func(k, v string) { md.Set(k, v) })

		if deadline, ok := existing.GetDeadline(); ok {
			md.SetDeadline(deadline)
		}
	}

	if deadline, ok := ctx.Deadline(); ok {
		md.SetDeadline(deadline)
	}

	return md.MarshalBinary(), inet.FrameFlagHasMetadata
}

// metadataMapFromBytes converts marshaled duplex metadata into the string map
// expected by legacy RemoteMessage protobuf fields. Invalid wire bytes yield nil
// so callers treat the message as having no metadata rather than failing the send.
func metadataMapFromBytes(wire []byte) map[string]string {
	if len(wire) == 0 {
		return nil
	}

	md := inet.NewMetadata()
	if err := md.UnmarshalBinary(wire); err != nil {
		return nil
	}

	out := make(map[string]string)
	md.IterateHeaders(func(k, v string) { out[k] = v })

	return out
}

// decodeErrorPayload parses a duplex ERROR frame payload into a Go error.
// Unreadable payloads still return an error wrapping the unmarshal failure.
func decodeErrorPayload(payload []byte) error {
	var e internalpb.Error
	if err := proto.Unmarshal(payload, &e); err != nil {
		return fmt.Errorf("unreadable error payload: %w", err)
	}

	return fmt.Errorf("%s: %s", e.GetCode().String(), e.GetMessage())
}

// buildUserTellParams attaches sender, receiver, wire serializer ID, type
// name, and context metadata to an already serialized payload so tell and ask
// routing can share one parameter builder.
func (x *client) buildUserTellParams(ctx context.Context, sender, receiver string, message any, serializer remote.Serializer, payload []byte) (tellParams, error) {
	serID, typeName := serializerWireID(serializer, message, payload)
	meta, _ := metadataWireFromContext(ctx)

	return tellParams{
		sender:   sender,
		receiver: receiver,
		payload:  payload,
		serID:    serID,
		typeName: typeName,
		metadata: meta,
	}, nil
}
