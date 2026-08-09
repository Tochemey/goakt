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

package net

import (
	"context"
	"encoding/binary"
	"errors"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

// remoteAskRequestName is the [ProtoHandler] key for the legacy unary
// RemoteAsk RPC. When no duplex user ask handler is registered, user DATA
// frames are bridged through this handler so fixture servers that only
// register RemoteAsk keep working under duplex dial.
const remoteAskRequestName protoreflect.FullName = "internalpb.RemoteAskRequest"

// remoteTellRequestName is the [ProtoHandler] key for the legacy unary
// RemoteTell RPC. When no duplex tell handler is registered, fire-and-forget
// DATA frames are bridged through this handler.
const remoteTellRequestName protoreflect.FullName = "internalpb.RemoteTellRequest"

// DuplexTellHandler handles a fire-and-forget DATA envelope on the duplex
// acceptor read loop. Implementations must not block beyond mailbox enqueue:
// the read loop cannot make progress until the handler returns.
type DuplexTellHandler func(ctx context.Context, env DataEnvelope)

// DuplexAskHandler handles a DATA envelope that expects a reply. The returned
// [ReplyEnvelope] is written as a REPLY frame. A non-nil error becomes a
// request-scoped ERROR frame. Application-level failures that should keep
// today's [checkProtoError] semantics should return an [internalpb.Error]
// body inside the reply with a nil error instead.
type DuplexAskHandler func(ctx context.Context, env DataEnvelope) (ReplyEnvelope, error)

// duplexAskTask is work dispatched off the duplex read loop for expectsReply
// DATA frames (control RPCs and user asks). Holding the decoded envelope and
// originating frame lets the worker write REPLY/ERROR without re-reading.
type duplexAskTask struct {
	// server owns handlers, the ask pool, and panic recovery for this task.
	server *ProtoServer
	// conn is the duplex connection that must receive the REPLY or ERROR.
	conn *duplexConn
	// frame is the inbound DATA frame (lane and correlation are echoed).
	frame Frame
	// env is the decoded DATA envelope for this request.
	env DataEnvelope
	// ctx carries request metadata and the server base context.
	ctx context.Context
	// cancel ends the request-scoped deadline derived from envelope metadata.
	// Nil when the frame carried no deadline metadata.
	cancel context.CancelFunc
}

// handleDuplexAskTask runs one expectsReply dispatch on a worker-pool goroutine
// and submits REPLY or ERROR on the originating duplex connection. Panics are
// always recovered at this boundary so a misbehaving handler cannot crash the
// accept worker process.
func handleDuplexAskTask(task duplexAskTask) {
	if task.cancel != nil {
		defer task.cancel()
	}

	x := task.server
	corr := task.frame.Correlation
	ctx := task.ctx

	reply, panicked, err := x.recoverDuplexAsk(ctx, task.env)
	if panicked {
		_ = submitErrorFrame(ctx, task.conn, corr, internalpb.Code_CODE_INTERNAL_ERROR, "handler panic")
		return
	}

	if err != nil {
		_ = submitErrorFrame(ctx, task.conn, corr, internalpb.Code_CODE_INTERNAL_ERROR, err.Error())
		return
	}

	payload, err := encodeReplyEnvelope(reply)
	if err != nil {
		_ = submitErrorFrame(ctx, task.conn, corr, internalpb.Code_CODE_INTERNAL_ERROR, err.Error())
		return
	}

	flags := byte(0)
	if len(reply.Metadata) > 0 {
		flags |= FrameFlagHasMetadata
	}

	_ = task.conn.Submit(ctx, Frame{
		Version:     ProtocolVersion,
		Type:        FrameTypeReply,
		Flags:       flags,
		Lane:        task.frame.Lane,
		Length:      uint32(len(payload)),
		Correlation: corr,
		Payload:     payload,
	})
}

// recoverDuplexAsk invokes [ProtoServer.dispatchDuplexAsk] with panic recovery
// so worker-pool goroutines never crash the process. When a panic handler is
// configured it is notified; panicked is always true on panic so the caller
// can emit a request-scoped ERROR.
func (x *ProtoServer) recoverDuplexAsk(ctx context.Context, env DataEnvelope) (reply ReplyEnvelope, panicked bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			if x.panicHandler != nil {
				x.panicHandler(protoreflect.FullName(env.TypeName), r)
			}
			panicked = true
		}
	}()

	reply, err = x.dispatchDuplexAsk(ctx, env)
	return reply, false, err
}

// dispatchDuplexAsk routes an expectsReply DATA envelope to a registered
// control [ProtoHandler] (typeRef matches a handler key), the configured
// duplex ask handler, or the legacy RemoteAskRequest bridge. Control frames
// use serializer ID 0 and empty sender/receiver refs.
func (x *ProtoServer) dispatchDuplexAsk(ctx context.Context, env DataEnvelope) (ReplyEnvelope, error) {
	name := protoreflect.FullName(env.TypeName)
	if handler, ok := x.handlers[name]; ok {
		return x.dispatchDuplexControl(ctx, handler, env)
	}

	if x.duplexAsk != nil {
		return x.duplexAsk(ctx, env)
	}

	if handler, ok := x.handlers[remoteAskRequestName]; ok {
		return x.dispatchDuplexUserAskViaLegacy(ctx, handler, env)
	}

	return ReplyEnvelope{}, errors.New("tcp: no duplex ask handler configured")
}

// dispatchDuplexUserAskViaLegacy wraps a user DATA envelope as a
// RemoteAskRequest for servers that only registered the legacy ask handler.
// The first response message becomes a public-proto REPLY body; an
// [internalpb.Error] response is returned as an internal-proto REPLY so
// clients can apply checkProtoError.
func (x *ProtoServer) dispatchDuplexUserAskViaLegacy(ctx context.Context, handler ProtoHandler, env DataEnvelope) (ReplyEnvelope, error) {
	req := &internalpb.RemoteAskRequest{
		RemoteMessages: []*internalpb.RemoteMessage{{
			Sender:   env.Sender,
			Receiver: env.Receiver,
			Message:  env.Payload,
			Metadata: metadataMapFromEnvelope(env.Metadata),
		}},
	}

	if len(env.Metadata) > 0 {
		md := NewMetadata()
		if err := md.UnmarshalBinary(env.Metadata); err != nil {
			return ReplyEnvelope{}, err
		}

		ctx = md.ToContext(ctx)
		var cancel context.CancelFunc
		ctx, cancel = md.DeadlineContext(ctx)
		defer cancel()
	}

	resp, err := handler(ctx, nil, req)
	if err != nil {
		return ReplyEnvelope{}, err
	}

	if errMsg, ok := resp.(*internalpb.Error); ok {
		payload, mErr := proto.Marshal(errMsg)
		if mErr != nil {
			return ReplyEnvelope{}, mErr
		}

		return ReplyEnvelope{
			TypeName:     string(proto.MessageName(errMsg)),
			SerializerID: SerializerIDInternalProto,
			Payload:      payload,
		}, nil
	}

	askResp, ok := resp.(*internalpb.RemoteAskResponse)
	if !ok {
		return ReplyEnvelope{}, errors.New("invalid response type")
	}

	if len(askResp.GetMessages()) == 0 {
		// Empty successful ask: custom serializer allows an empty typeRef.
		return ReplyEnvelope{SerializerID: SerializerIDCustom}, nil
	}

	payload := askResp.GetMessages()[0]
	typeName := ""
	if name, ok := frameTypeNameFromPayload(payload); ok {
		typeName = string(name)
	}

	return ReplyEnvelope{
		TypeName:     typeName,
		SerializerID: SerializerIDPublicProto,
		Payload:      payload,
	}, nil
}

// frameTypeNameFromPayload extracts the fully-qualified type name from a
// self-describing public serializer frame
// (totalLen | nameLen | name | bytes). It returns false when the frame is
// too short or the name length is inconsistent.
func frameTypeNameFromPayload(data []byte) (protoreflect.FullName, bool) {
	if len(data) < 8 {
		return "", false
	}

	nameLen := int(binary.BigEndian.Uint32(data[4:8]))
	if nameLen <= 0 || 8+nameLen > len(data) {
		return "", false
	}

	return protoreflect.FullName(data[8 : 8+nameLen]), true
}

// metadataMapFromEnvelope converts marshaled duplex metadata into the map
// form carried by [internalpb.RemoteMessage]. Invalid wire bytes yield nil
// rather than an error so a bad metadata blob does not fail the whole ask
// before the legacy handler can classify it.
func metadataMapFromEnvelope(wire []byte) map[string]string {
	if len(wire) == 0 {
		return nil
	}

	md := NewMetadata()
	if err := md.UnmarshalBinary(wire); err != nil {
		return nil
	}

	out := make(map[string]string)
	md.IterateHeaders(func(k, v string) { out[k] = v })
	return out
}

// dispatchDuplexControl unmarshals a control request from a DATA envelope
// (serializer ID 0, typeRef = protobuf full name) and invokes the registered
// [ProtoHandler]. The response is encoded as an internal-proto REPLY,
// including application-level [internalpb.Error] bodies so checkProtoError
// continues to work on the client.
func (x *ProtoServer) dispatchDuplexControl(ctx context.Context, handler ProtoHandler, env DataEnvelope) (ReplyEnvelope, error) {
	if env.SerializerID != SerializerIDInternalProto {
		return ReplyEnvelope{}, errors.New("tcp: control frames require internal proto serializer")
	}

	msgType, err := FindMessageType(protoreflect.FullName(env.TypeName))
	if err != nil {
		return ReplyEnvelope{}, err
	}

	req := msgType.New().Interface()
	if err := proto.Unmarshal(env.Payload, req); err != nil {
		return ReplyEnvelope{}, err
	}

	if len(env.Metadata) > 0 {
		md := NewMetadata()
		if err := md.UnmarshalBinary(env.Metadata); err != nil {
			return ReplyEnvelope{}, err
		}

		ctx = md.ToContext(ctx)
		var cancel context.CancelFunc
		ctx, cancel = md.DeadlineContext(ctx)
		defer cancel()
	}

	resp, err := handler(ctx, nil, req)
	if err != nil {
		return ReplyEnvelope{}, err
	}

	if resp == nil {
		return ReplyEnvelope{
			TypeName:     string(proto.MessageName(req)),
			SerializerID: SerializerIDInternalProto,
		}, nil
	}

	payload, err := proto.Marshal(resp)
	if err != nil {
		return ReplyEnvelope{}, err
	}

	return ReplyEnvelope{
		TypeName:     string(proto.MessageName(resp)),
		SerializerID: SerializerIDInternalProto,
		Payload:      payload,
	}, nil
}

// submitErrorFrame encodes an [internalpb.Error] and submits it on conn.
// Correlation 0 marks a connection-scoped error; a nonzero value echoes the
// failed request's correlation ID for request-scoped errors.
func submitErrorFrame(ctx context.Context, conn *duplexConn, correlation uint64, code internalpb.Code, message string) error {
	payload, err := proto.Marshal(&internalpb.Error{
		Code:    code,
		Message: message,
	})
	if err != nil {
		return err
	}

	return conn.Submit(ctx, Frame{
		Version:     ProtocolVersion,
		Type:        FrameTypeError,
		Lane:        LaneControl,
		Length:      uint32(len(payload)),
		Correlation: correlation,
		Payload:     payload,
	})
}
