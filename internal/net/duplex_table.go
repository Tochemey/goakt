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

// PrepareRef registers literal in the session sender table when the negotiated
// revision supports tables. It returns a table ID, or 0 when the caller must
// encode an inline ref (revision below 3, empty literal, or table full).
// Fresh assignments admit a TABLE frame via [duplexConn.admitFrame]: the call
// may wait briefly on the connection mutex (lock order is sender-table mutex
// then connection mutex) but never waits on byte capacity or a full writer
// queue; those cases fail with [ErrDuplexBackpressure] or [ErrDuplexClosed]
// and roll the mapping back.
func (x *duplexConn) PrepareRef(kind byte, literal string) (uint64, error) {
	if x.revision < CapabilityRevisionTables || literal == "" {
		return 0, nil
	}

	table := x.senderTableFor(kind)
	if table == nil {
		return 0, nil
	}

	return table.register(literal, func(id uint64) error {
		return x.emitTable(kind, id, literal)
	})
}

// emitTable admits one TABLE frame. It may wait on the connection mutex but
// does not wait on byte capacity or writer-queue backpressure.
func (x *duplexConn) emitTable(kind byte, id uint64, literal string) error {
	payload, err := encodeTablePayload(kind, id, literal)
	if err != nil {
		return err
	}

	return x.admitFrame(Frame{
		Version: ProtocolVersion,
		Type:    FrameTypeTable,
		Lane:    x.lane,
		Length:  uint32(len(payload)),
		Payload: payload,
	})
}

// senderTableFor returns the outbound table for kind, or nil when unknown.
func (x *duplexConn) senderTableFor(kind byte) *senderTable {
	switch kind {
	case TableKindActorPath:
		return x.pathSender
	case TableKindTypeName:
		return x.typeSender
	default:
		return nil
	}
}

// receiverTableFor returns the inbound table for kind, or nil when unknown.
func (x *duplexConn) receiverTableFor(kind byte) *receiverTable {
	switch kind {
	case TableKindActorPath:
		return x.pathReceiver
	case TableKindTypeName:
		return x.typeReceiver
	default:
		return nil
	}
}

// handleInboundTable installs one TABLE frame into the receiver tables.
func (x *duplexConn) handleInboundTable(frame Frame) {
	if x.revision < CapabilityRevisionTables {
		x.rejectProtocol("TABLE requires capability revision 3")
		return
	}

	kind, id, literal, err := parseTablePayload(frame.Payload)
	if err != nil {
		x.rejectProtocol(err.Error())
		return
	}

	if !knownTableKind(kind) {
		x.rejectProtocol("unknown table kind")
		return
	}

	table := x.receiverTableFor(kind)
	if table == nil {
		x.rejectProtocol("TABLE received but receiver table is not configured")
		return
	}

	out := table.install(id, literal)
	if out.HardError != "" {
		x.rejectProtocol(out.HardError)
	}
}

// DecodeDataEnvelope resolves table refs through this connection's receiver
// tables and lazily fills SenderHandle via the configured sender resolver.
func (x *duplexConn) DecodeDataEnvelope(src []byte, hasMetadata bool) (DataEnvelope, error) {
	return decodeDataEnvelopeWithTables(src, hasMetadata, x.pathReceiver, x.typeReceiver, x.senderResolver)
}

// DecodeReplyEnvelope resolves a table type ref through this connection's
// type receiver table.
func (x *duplexConn) DecodeReplyEnvelope(src []byte, hasMetadata bool) (ReplyEnvelope, error) {
	return decodeReplyEnvelopeWithTables(src, hasMetadata, x.typeReceiver)
}

// EncodeReplyEnvelope registers the reply type name when tables are enabled
// and encodes the REPLY payload.
func (x *duplexConn) EncodeReplyEnvelope(envelope ReplyEnvelope) ([]byte, error) {
	typeID, err := x.PrepareRef(TableKindTypeName, envelope.TypeName)
	if err != nil {
		return nil, err
	}

	return encodeReplyEnvelopeWithTables(envelope, typeID)
}
