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
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/remote"
)

// maxBatchAskConcurrency bounds concurrent duplex asks issued by one
// RemoteBatchAsk call so a large batch cannot fan out unbounded goroutines.
const maxBatchAskConcurrency = 64

// tellBatchTypeName is the wire type name of a coalesced fire-and-forget
// batch delivered over the duplex ordinary lane as one internal-proto DATA
// frame. The remoting server routes it to the registered batch tell handler.
const tellBatchTypeName = "internalpb.RemoteTellRequest"

// tellBatchSplitMargin is the headroom reserved under the session's
// negotiated max message size for the DATA-envelope framing wrapped around a
// marshaled batch payload.
const tellBatchSplitMargin = 4 * 1024

// tellBatchPerMessageOverhead over-approximates the RemoteTellRequest field
// tag and length prefix wrapped around one RemoteMessage entry when packing
// an oversized batch into size-bounded sub-batches.
const tellBatchPerMessageOverhead = 6

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

// sendControlDuplex encodes req as an expectsReply DATA frame, waits for the
// correlated REPLY, and decodes it with [decodeControlReply]. Oversized bulk
// control RPCs ride the large lane (chunked when the peer supports it); other
// control traffic stays on the control lane. Any transport failure retires
// the selected lane so the next call re-dials.
func (x *client) sendControlDuplex(ctx context.Context, peer *peer, req proto.Message) (proto.Message, error) {
	payload, err := inet.MarshalProtoAppend(req)
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

	role := internalpb.LaneRole_LANE_ROLE_CONTROL
	index := uint32(0)
	lane := inet.LaneControl
	if isControlBulk(req) {
		// Conservative inline-ref size: table compression can only shrink the
		// envelope, so oversized bulk still selects the large lane correctly.
		logicalLen := inet.FrameHeaderSize + inet.InlineDataEnvelopeSize(env)
		if uint32(logicalLen) > x.chunkSize {
			role = internalpb.LaneRole_LANE_ROLE_LARGE
			lane = inet.LaneLarge
		}
	}

	session, err := peer.ensureLane(ctx, role, index)
	if err != nil {
		inet.ReleaseMarshalBuffer(payload)
		return nil, err
	}

	encoded, err := encodeControlDataEnvelope(session, env)
	inet.ReleaseMarshalBuffer(payload)
	if err != nil {
		return nil, err
	}

	frame := inet.Frame{
		Type:    inet.FrameTypeData,
		Flags:   flags,
		Lane:    lane,
		Payload: encoded,
	}

	replyFrame, err := session.Ask(ctx, frame)
	if err != nil {
		if shouldRetireDuplexSession(err, replyFrame) {
			peer.retireLane(laneKey{role: role, index: index}, session)
		}

		session.ReleasePayload(replyFrame)
		return nil, mapDuplexErr(err)
	}

	msg, err := decodeControlReply(replyFrame, session.DecodeReplyEnvelope)
	session.ReleasePayload(replyFrame)
	return msg, err
}

// encodeControlDataEnvelope registers a non-empty control type name and encodes
// the DATA envelope. Path refs stay empty/inline for control RPCs.
func encodeControlDataEnvelope(session inet.DuplexSession, env inet.DataEnvelope) ([]byte, error) {
	typeID, err := session.PrepareRef(inet.TableKindTypeName, env.TypeName)
	if err != nil {
		return nil, err
	}

	return inet.EncodeDataEnvelopeWithTables(env, 0, 0, typeID)
}

// isControlBulk reports whether req is a bulk control RPC that may leave the
// control lane when its logical frame exceeds ChunkSize.
func isControlBulk(req proto.Message) bool {
	switch req.(type) {
	case *internalpb.RelocateBatchRequest,
		*internalpb.PersistPeerStateRequest,
		*internalpb.RemoteStateRequest:
		return true
	default:
		return false
	}
}

// sendTell routes one fire-and-forget user message, falling back to legacy
// SendProto when the protocol pin or peer cache requires the unary path.
func (x *client) sendTell(ctx context.Context, host string, port int, params tellParams) error {
	if x.pinRequiresLegacy() {
		return x.sendTellLegacy(ctx, host, port, params)
	}

	peer := x.peerFor(host, port)
	err := x.sendTellDuplex(ctx, peer, params)
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
	peer := x.peerFor(host, port)
	peer.beginLegacySend()
	defer peer.endLegacySend()

	nc := x.NetClient(host, port)
	rm := &internalpb.RemoteMessage{}
	rm.SetSender(params.sender)
	rm.SetReceiver(params.receiver)
	rm.SetMessage(params.payload)
	rm.SetMetadata(metadataMapFromBytes(params.metadata))
	rtr := &internalpb.RemoteTellRequest{}
	rtr.SetRemoteMessages([]*internalpb.RemoteMessage{rm})
	resp, err := nc.SendProto(ctx, rtr)
	if err != nil {
		return err
	}

	return checkProtoError(resp)
}

// sendTellDuplex enqueues one DATA frame without expecting a reply. A live
// session on an unfenced lane is written synchronously; otherwise the tell is
// admitted to the lane pump, which dials, encodes, and writes it. Transport
// failures fan out as dead letters rather than surfacing to this
// fire-and-forget caller; only backpressure and caller cancellation stay
// synchronous. The frame lane byte matches the negotiated role/index.
func (x *client) sendTellDuplex(ctx context.Context, peer *peer, params tellParams) error {
	entry, cached, session, preferLegacy := peer.tellSendPlan(params.receiver)
	if preferLegacy {
		// A peer currently classified legacy keeps the synchronous legacy
		// contract; duplex admission is only for duplex or unproven peers.
		return errPreferLegacy
	}

	key := entry.lane
	if session == nil {
		// No live session (or earlier admitted tells still queued): admit
		// and return. The lane pump dials, encodes, and writes; transport
		// failures fan out as dead letters, never to this caller.
		return peer.admitTellOrFanOut(ctx, key, params)
	}

	encoded, flags, err := encodeTellFrame(peer, session, entry, cached, params)
	if err != nil {
		// Encode failure is permanent for this message; fan out like the
		// pump path so fire-and-forget callers never see a sync error here.
		peer.fanOutTellFailure(params, err)
		return nil
	}

	frame := inet.Frame{
		Type:    inet.FrameTypeData,
		Flags:   flags,
		Lane:    session.Lane(),
		Payload: encoded,
	}

	err = session.Tell(ctx, frame)
	if err == nil {
		return nil
	}

	if shouldRetireDuplexSession(err, inet.Frame{}) {
		// The session died under the write: the frame never entered the
		// writer queue, so re-admit for redial-or-dead-letter instead of
		// surfacing transport loss to a fire-and-forget caller. This re-admit
		// is a fresh admission, so a concurrent tell on the same lane can land
		// ahead of it. That only reorders across sender-target pairs, which the
		// pump never orders: a single sender is sequential per pair, so it is
		// still blocked in this call and cannot be admitting concurrently here.
		peer.retireLane(key, session)
		return peer.admitTellOrFanOut(ctx, key, params)
	}

	mapped := mapDuplexErr(err)
	// Backpressure and caller cancellation stay synchronous so senders can
	// apply their own deadline policy. Permanent rejects (e.g. oversize)
	// fan out as dead letters, matching admitted-tell failure semantics.
	if errors.Is(mapped, gerrors.ErrRemoteSendBackpressure) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return mapped
	}

	peer.fanOutTellFailure(params, mapped)
	return nil
}

// encodeTellFrame encodes one fire-and-forget tell as a DATA envelope on
// session, using table refs where registered. It returns the envelope bytes
// and the frame flags matching the metadata presence.
func encodeTellFrame(peer *peer, session inet.DuplexSession, entry pathEntry, cached bool, params tellParams) ([]byte, byte, error) {
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

	encoded, err := encodeUserDataEnvelope(peer, session, entry, cached, env)
	return encoded, flags, err
}

// encodeUserDataEnvelope registers sender/type (and receiver when cached) on
// the session sender tables and encodes env with those IDs. A cached receiver
// pathID is reused only when entry.session is the live session.
func encodeUserDataEnvelope(peer *peer, session inet.DuplexSession, entry pathEntry, receiverCached bool, env inet.DataEnvelope) ([]byte, error) {
	senderID, err := session.PrepareRef(inet.TableKindActorPath, env.Sender)
	if err != nil {
		return nil, err
	}

	typeID, err := session.PrepareRef(inet.TableKindTypeName, env.TypeName)
	if err != nil {
		return nil, err
	}

	receiverID := uint64(0)
	if receiverCached {
		if entry.pathID != 0 && entry.session == session {
			receiverID = entry.pathID
		} else if session.Revision() >= inet.CapabilityRevisionTables {
			receiverID, err = session.PrepareRef(inet.TableKindActorPath, env.Receiver)
			if err != nil {
				return nil, err
			}

			// pathID 0 means inline (table full); do not rewrite the route
			// cache or every later send pays a peer.mu write for no benefit.
			if receiverID != 0 {
				peer.rememberPathRef(env.Receiver, receiverID, session)
			}
		}
	}

	return inet.EncodeDataEnvelopeWithTables(env, senderID, receiverID, typeID)
}

// sendAsk routes one user ask and deserializes the response with serializer.
// Falls back to legacy RemoteAskRequest when duplex is unavailable.
func (x *client) sendAsk(ctx context.Context, host string, port int, params askParams, serializer remote.Serializer) (any, error) {
	if x.pinRequiresLegacy() {
		return x.sendAskLegacy(ctx, host, port, params, serializer)
	}

	peer := x.peerFor(host, port)
	resp, err := x.sendAskDuplex(ctx, peer, params, serializer)
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
	peer := x.peerFor(host, port)
	peer.beginLegacySend()
	defer peer.endLegacySend()

	nc := x.NetClient(host, port)
	rm := &internalpb.RemoteMessage{}
	rm.SetSender(params.sender)
	rm.SetReceiver(params.receiver)
	rm.SetMessage(params.payload)
	rm.SetMetadata(metadataMapFromBytes(params.metadata))
	req := &internalpb.RemoteAskRequest{}
	req.SetRemoteMessages([]*internalpb.RemoteMessage{rm})
	if params.timeout > 0 {
		req.SetTimeout(durationpb.New(params.timeout))
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

	if len(askResp.GetMessages()) == 0 {
		return nil, nil
	}

	return serializer.Deserialize(askResp.GetMessages()[0])
}

// sendAskDuplex sends an expectsReply DATA frame on the ordinary lane, decodes
// the REPLY envelope, and maps internalpb.Error payloads to Go errors via
// [checkProtoError]. Terminal transport failures close the peer session;
// caller timeouts and request-scoped ERROR frames do not.
func (x *client) sendAskDuplex(ctx context.Context, peer *peer, params askParams, serializer remote.Serializer) (any, error) {
	entry, cached := peer.route(params.receiver)
	session, err := peer.ensureLane(ctx, entry.lane.role, entry.lane.index)
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

	encoded, err := encodeUserDataEnvelope(peer, session, entry, cached, env)
	if err != nil {
		return nil, err
	}

	frame := inet.Frame{
		Type:    inet.FrameTypeData,
		Flags:   flags,
		Lane:    session.Lane(),
		Payload: encoded,
	}

	replyFrame, err := session.Ask(ctx, frame)
	if err != nil {
		if shouldRetireDuplexSession(err, replyFrame) {
			peer.retireLane(entry.lane, session)
		}

		session.ReleasePayload(replyFrame)
		return nil, mapDuplexErr(err)
	}

	defer session.ReleasePayload(replyFrame)

	replyEnv, err := session.DecodeReplyEnvelope(replyFrame.Payload, replyFrame.HasMetadata())
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

// sendBatchTellDuplex delivers a batch of tells bound for one receiver. When
// coalescing is enabled and the receiver is not a configured large-message
// destination, the batch is enqueued in order onto the receiver's coalescer
// shard so batch tells and coalesced singles share one FIFO fence. Otherwise
// each message follows the routed per-message send (large lane included),
// with the legacy unary batch as fallback for legacy peers.
func (x *client) sendBatchTellDuplex(ctx context.Context, host string, port int, params []tellParams) error {
	if len(params) == 0 {
		return nil
	}

	if x.coalescing.enabled() && !x.isLargeReceiver(params[0].receiver) {
		return x.submitBatchTellCoalesced(ctx, host, port, params)
	}

	if x.pinRequiresLegacy() {
		_, err := x.flushTellBatchLegacy(ctx, host, port, batchTellRequest(params))
		return err
	}

	peer := x.peerFor(host, port)

	for i, param := range params {
		if err := x.sendTellDuplex(ctx, peer, param); err != nil {
			if errors.Is(err, errPreferLegacy) {
				// Send only the remainder through the legacy path. Messages
				// before i were already delivered or admitted on duplex
				// lanes; re-sending the whole batch would duplicate them,
				// breaking at-most-once.
				_, ferr := x.flushTellBatchLegacy(ctx, host, port, batchTellRequest(params[i:]))
				return ferr
			}

			return err
		}
	}

	return nil
}

// submitBatchTellCoalesced enqueues the batch in submission order onto the
// receiver's coalescer shard, the same shard coalesced singles use, so
// per-receiver FIFO holds across RemoteTell and RemoteBatchTell. Backpressure
// and caller cancellation surface synchronously (a prefix of the batch may
// already be enqueued); transport failures after admission dead-letter
// through the client's TellFailureHandler.
func (x *client) submitBatchTellCoalesced(ctx context.Context, host string, port int, params []tellParams) error {
	c := x.getCoalescer(host, port, x.ordinaryLaneForReceiver(params[0].receiver))
	if c == nil {
		return gerrors.NewErrRemoteSendFailure(errors.New("no coalescer available for destination"))
	}

	for _, param := range params {
		msg := &internalpb.RemoteMessage{}
		msg.SetSender(param.sender)
		msg.SetReceiver(param.receiver)
		msg.SetMessage(param.payload)
		msg.SetMetadata(metadataMapFromBytes(param.metadata))

		switch err := c.submit(ctx, msg); {
		case err == nil:
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			return errors.Join(gerrors.ErrRemoteSendBackpressure, err)
		default:
			return gerrors.NewErrRemoteSendFailure(err)
		}
	}

	return nil
}

// batchTellRequest packs params into one RemoteTellRequest for the legacy
// unary batch path.
func batchTellRequest(params []tellParams) *internalpb.RemoteTellRequest {
	msgs := make([]*internalpb.RemoteMessage, 0, len(params))

	for _, param := range params {
		rm := &internalpb.RemoteMessage{}
		rm.SetSender(param.sender)
		rm.SetReceiver(param.receiver)
		rm.SetMessage(param.payload)
		rm.SetMetadata(metadataMapFromBytes(param.metadata))
		msgs = append(msgs, rm)
	}

	rtr := &internalpb.RemoteTellRequest{}
	rtr.SetRemoteMessages(msgs)
	return rtr
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
	// lane identifies the lane used by this request.
	lane laneKey
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
	results := make([]any, len(params))
	var wg sync.WaitGroup
	out := make(chan batchAskResult, len(params))
	sem := make(chan struct{}, maxBatchAskConcurrency)

	// submitted counts asks handed to a duplex session. The whole-batch legacy
	// fallback is safe only while it is zero: once any ask may have reached the
	// peer, re-sending the batch would duplicate it, breaking at-most-once.
	var submitted atomic.Int32

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

			entry, cached := p.route(ask.receiver)
			session, sessionErr := p.ensureLane(ctx, entry.lane.role, entry.lane.index)
			if sessionErr != nil {
				out <- batchAskResult{index: idx, err: sessionErr, lane: entry.lane}
				return
			}

			env := inet.DataEnvelope{
				Sender:       ask.sender,
				Receiver:     ask.receiver,
				TypeName:     ask.typeName,
				SerializerID: ask.serID,
				Metadata:     meta,
				Payload:      ask.payload,
			}

			encoded, encErr := encodeUserDataEnvelope(p, session, entry, cached, env)
			if encErr != nil {
				out <- batchAskResult{index: idx, err: encErr, lane: entry.lane}
				return
			}

			frame := inet.Frame{
				Type:    inet.FrameTypeData,
				Flags:   flags,
				Lane:    session.Lane(),
				Payload: encoded,
			}

			submitted.Add(1)
			replyFrame, askErr := session.Ask(ctx, frame)
			if askErr != nil {
				if shouldRetireDuplexSession(askErr, replyFrame) {
					p.retireLane(entry.lane, session)
				}

				session.ReleasePayload(replyFrame)
				// The payload was just released; the result must not carry a
				// dangling slice into the pool's next reuse.
				replyFrame.Payload = nil
				out <- batchAskResult{index: idx, err: mapDuplexErr(askErr), reply: replyFrame, lane: entry.lane}
				return
			}

			replyEnv, decErr := session.DecodeReplyEnvelope(replyFrame.Payload, replyFrame.HasMetadata())
			if decErr != nil {
				session.ReleasePayload(replyFrame)
				out <- batchAskResult{index: idx, err: decErr, lane: entry.lane}
				return
			}

			val, desErr := deserializeReplyEnvelope(replyEnv, ser)
			if desErr == nil {
				if errResp, ok := val.(*internalpb.Error); ok {
					desErr = checkProtoError(errResp)
					val = nil
				}
			}

			session.ReleasePayload(replyFrame)
			out <- batchAskResult{index: idx, value: val, err: desErr, lane: entry.lane}
		}(i, param, serializers[i])
	}

	wg.Wait()
	close(out)

	var firstErr error
	for res := range out {
		if res.err != nil && firstErr == nil {
			firstErr = res.err
		}

		if res.err == nil {
			results[res.index] = res.value
		}
	}

	if firstErr != nil {
		if errors.Is(firstErr, errPreferLegacy) {
			if submitted.Load() == 0 {
				return x.sendBatchAskLegacy(ctx, host, port, params, serializers)
			}

			// Some asks already reached duplex lanes; re-sending the batch on
			// the legacy path would execute them twice on the target actors.
			return nil, fmt.Errorf("remote batch ask aborted: peer switched to the legacy protocol after %d of %d asks were submitted on duplex lanes", submitted.Load(), len(params))
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
		rm := &internalpb.RemoteMessage{}
		rm.SetSender(param.sender)
		rm.SetReceiver(param.receiver)
		rm.SetMessage(param.payload)
		rm.SetMetadata(metadataMapFromBytes(param.metadata))
		msgs = append(msgs, rm)
	}

	nc := x.NetClient(host, port)
	req := &internalpb.RemoteAskRequest{}
	req.SetRemoteMessages(msgs)
	if len(params) > 0 && params[0].timeout > 0 {
		req.SetTimeout(durationpb.New(params[0].timeout))
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

	responses := make([]any, 0, len(askResp.GetMessages()))
	for i, msg := range askResp.GetMessages() {
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
// SerializerIDInternalProto; other serializer IDs are rejected. decodeReply
// is typically [inet.DuplexSession.DecodeReplyEnvelope] so table type refs
// resolve on the owning session.
func decodeControlReply(frame inet.Frame, decodeReply func([]byte, bool) (inet.ReplyEnvelope, error)) (proto.Message, error) {
	if frame.Type == inet.FrameTypeError {
		return nil, decodeErrorPayload(frame.Payload)
	}

	replyEnv, err := decodeReply(frame.Payload, frame.HasMetadata())
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
	case inet.SerializerIDPublicProto, inet.SerializerIDJSON, inet.SerializerIDCBOR:
		return serializer.Deserialize(env.Payload)
	case inet.SerializerIDCustom:
		// Custom serializers may retain the input slice; copy out of the
		// pooled frame body before Deserialize so ReleasePayload is safe.
		return serializer.Deserialize(copyReplyPayload(env.Payload))
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

// copyReplyPayload returns a heap copy of payload for serializers that may
// retain their input. Empty payloads are returned unchanged.
func copyReplyPayload(payload []byte) []byte {
	if len(payload) == 0 {
		return payload
	}

	out := make([]byte, len(payload))
	copy(out, payload)
	return out
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

// flushTellBatch delivers one coalesced batch to host:port, choosing the
// duplex ordinary lane for duplex peers and the legacy unary path otherwise.
// On error it returns the messages that were not delivered, so the caller's
// failure fan-out covers each message exactly once even when the duplex
// flush split the batch and delivered a prefix.
func (x *client) flushTellBatch(ctx context.Context, host string, port int, laneIndex uint32, req *internalpb.RemoteTellRequest) ([]*internalpb.RemoteMessage, error) {
	if x.pinRequiresLegacy() {
		return x.flushTellBatchLegacy(ctx, host, port, req)
	}

	peer := x.peerFor(host, port)
	undelivered, err := x.flushTellBatchDuplex(ctx, peer, laneIndex, req)
	if errors.Is(err, errPreferLegacy) {
		return x.flushTellBatchLegacy(ctx, host, port, req)
	}

	return undelivered, err
}

// flushTellBatchDuplex encodes req as internal-proto DATA envelopes on the
// given ordinary lane. A batch whose marshaled size would exceed the
// session's negotiated max message size is split into size-bounded
// sub-batches sent in order, so individually valid tells are never
// dead-lettered for sharing a batch with others. Terminal transport failures
// retire the lane so the next flush redials; the undelivered suffix is
// returned for the caller's fan-out.
func (x *client) flushTellBatchDuplex(ctx context.Context, peer *peer, laneIndex uint32, req *internalpb.RemoteTellRequest) ([]*internalpb.RemoteMessage, error) {
	// ensureLane reports errPreferLegacy for pinned or cached-legacy peers,
	// which routes the batch to the legacy unary flush.
	key := laneKey{role: internalpb.LaneRole_LANE_ROLE_ORDINARY, index: laneIndex}
	session, err := peer.ensureLane(ctx, key.role, key.index)
	if err != nil {
		return req.GetRemoteMessages(), err
	}

	// Pooled marshal (same pattern as sendControlDuplex): the envelope encode
	// downstream copies the payload, so the buffer is released as soon as
	// encoding is done instead of feeding one allocation per batch to the GC.
	payload, err := inet.MarshalProtoAppend(req)
	if err != nil {
		return req.GetRemoteMessages(), err
	}

	msgs := req.GetRemoteMessages()
	limit := tellBatchSplitLimit(session)

	// Fast path: the whole batch fits one logical frame. This is the
	// overwhelmingly common case; the split below re-marshals only oversized
	// aggregates.
	if len(msgs) <= 1 || limit == 0 || uint64(len(payload)) <= limit {
		if err := x.sendMarshaledTellBatch(ctx, peer, key, session, payload); err != nil {
			return msgs, err
		}

		return nil, nil
	}

	inet.ReleaseMarshalBuffer(payload)

	// Greedy packing: each sub-batch takes messages in order until the next
	// one would push it over the limit. A single message above the limit
	// still ships alone and fails on its own merits, exactly as it would as
	// a direct tell.
	start := 0

	for start < len(msgs) {
		end := start
		groupSize := uint64(0)

		for end < len(msgs) {
			size := uint64(proto.Size(msgs[end])) + tellBatchPerMessageOverhead

			if end > start && groupSize+size > limit {
				break
			}

			groupSize += size
			end++
		}

		sub := &internalpb.RemoteTellRequest{}
		sub.SetRemoteMessages(msgs[start:end])
		payload, err := inet.MarshalProtoAppend(sub)
		if err != nil {
			return msgs[start:], err
		}

		if err := x.sendMarshaledTellBatch(ctx, peer, key, session, payload); err != nil {
			return msgs[start:], err
		}

		start = end
	}

	return nil, nil
}

// tellBatchSplitLimit returns the largest marshaled batch payload flushed as
// one logical frame on session, leaving envelope headroom under the
// negotiated max message size. Zero means the session advertises no usable
// ceiling; the batch is then sent unsplit and the transport enforces its own
// cap.
func tellBatchSplitLimit(session inet.DuplexSession) uint64 {
	maxMessage := session.MaxMessageSize()
	if maxMessage <= tellBatchSplitMargin {
		return 0
	}

	return maxMessage - tellBatchSplitMargin
}

// sendMarshaledTellBatch wraps an already-marshaled RemoteTellRequest payload
// in a DATA envelope and submits it on session, releasing the pooled marshal
// buffer as soon as the envelope encode has copied it. Terminal transport
// failures retire the lane so the next flush redials.
func (x *client) sendMarshaledTellBatch(ctx context.Context, peer *peer, key laneKey, session inet.DuplexSession, payload []byte) error {
	env := inet.DataEnvelope{
		TypeName:     tellBatchTypeName,
		SerializerID: inet.SerializerIDInternalProto,
		Payload:      payload,
	}

	encoded, err := encodeControlDataEnvelope(session, env)
	inet.ReleaseMarshalBuffer(payload)
	if err != nil {
		return err
	}

	err = session.Tell(ctx, inet.Frame{
		Type:    inet.FrameTypeData,
		Lane:    session.Lane(),
		Payload: encoded,
	})
	if err != nil && shouldRetireDuplexSession(err, inet.Frame{}) {
		peer.retireLane(key, session)
	}

	return mapDuplexErr(err)
}

// flushTellBatchLegacy sends req through the pooled legacy unary client,
// preserving the pre-duplex coalesced flush behavior. The legacy request is
// all-or-nothing: on error the whole batch is undelivered.
func (x *client) flushTellBatchLegacy(ctx context.Context, host string, port int, req *internalpb.RemoteTellRequest) ([]*internalpb.RemoteMessage, error) {
	p := x.peerFor(host, port)
	p.beginLegacySend()
	defer p.endLegacySend()

	nc := x.NetClient(host, port)
	resp, err := nc.SendProto(ctx, req)
	if err != nil {
		return req.GetRemoteMessages(), err
	}

	if err := checkProtoError(resp); err != nil {
		return req.GetRemoteMessages(), err
	}

	return nil, nil
}
