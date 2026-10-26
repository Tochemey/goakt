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
	"encoding/binary"
	"fmt"
	"math"
)

// TellBatchTypeName is the DATA envelope type name of a coalesced tell batch
// in the revision-5 layout. It is not a protobuf message name: the body is
// encoded and decoded by hand.
//
// Body layout:
//
//	count (4B BE) | message*
//	message = senderRef | receiverRef | metaCount | (keyLen key valueLen value)* | payloadLen | payload
//
// Refs use the DATA envelope encoding (a uvarint actor-path table ID, or 0
// followed by an inline literal); counts and lengths are uvarints. The count
// has a fixed width so an encoder can write it after packing the messages.
const TellBatchTypeName = "goakt.duplex.TellBatch"

// TellBatchCountSize is the width of the message count that starts a batch
// body.
const TellBatchCountSize = 4

// maxTellBatchMetadataEntries caps the metadata entries of one batch message
// at the count frame metadata can carry ([Metadata.MarshalBinary]), so a
// hostile count cannot make the decoder build an outsized map.
const maxTellBatchMetadataEntries = math.MaxUint16

// TellBatchMessage is one message of a revision-5 tell batch.
type TellBatchMessage struct {
	// Sender is the wire address of the origin actor; empty for no sender.
	Sender string
	// SenderID is the actor-path table ID the encoder writes for Sender, or 0
	// to write Sender inline. Unused when decoding.
	SenderID uint64
	// SenderHandle is the actor-layer cache filled on a sender table hit
	// when decoding (see [receiverTable.resolveRef]). Never serialized.
	SenderHandle any
	// Receiver is the wire address of the target actor.
	Receiver string
	// ReceiverID is the actor-path table ID the encoder writes for Receiver,
	// or 0 to write Receiver inline. Unused when decoding.
	ReceiverID uint64
	// Metadata is the per-message propagation metadata. Nil when empty.
	Metadata map[string]string
	// Payload is the serialized message. A decoded payload aliases the frame
	// and is valid only until the batch handler returns.
	Payload []byte
}

// TellBatch reads the messages of one inbound revision-5 tell batch in
// order. The body is validated before a TellBatch is handed out, so every
// message it reports is well formed and every table ref resolves.
type TellBatch struct {
	// body holds the encoded messages after the count.
	body []byte
	// count is the number of messages in body.
	count int
	// read is the number of messages Next has returned.
	read int
	// paths resolves actor-path table refs on the batch's connection.
	paths *receiverTable
	// resolve fills SenderHandle on a sender table hit; nil leaves it empty.
	resolve func(string) any
}

// AppendTellBatchMessage appends the encoding of msg to dst.
func AppendTellBatchMessage(dst []byte, msg *TellBatchMessage) []byte {
	dst = appendEncodedRef(dst, msg.SenderID, msg.Sender)
	dst = appendEncodedRef(dst, msg.ReceiverID, msg.Receiver)
	dst = binary.AppendUvarint(dst, uint64(len(msg.Metadata)))

	for key, value := range msg.Metadata {
		dst = binary.AppendUvarint(dst, uint64(len(key)))
		dst = append(dst, key...)
		dst = binary.AppendUvarint(dst, uint64(len(value)))
		dst = append(dst, value...)
	}

	dst = binary.AppendUvarint(dst, uint64(len(msg.Payload)))
	return append(dst, msg.Payload...)
}

// PutTellBatchCount writes count into the first [TellBatchCountSize] bytes
// of dst, the slot an encoder reserved at the start of the body.
func PutTellBatchCount(dst []byte, count uint32) {
	binary.BigEndian.PutUint32(dst, count)
}

// Len returns the number of messages in the batch.
func (x *TellBatch) Len() int {
	return x.count
}

// Next decodes the next message into msg and reports whether there was one.
func (x *TellBatch) Next(msg *TellBatchMessage) bool {
	if x.read == x.count {
		return false
	}

	n, err := readTellBatchMessage(x.body, x.paths, x.resolve, msg)
	if err != nil {
		// Unreachable for a validated body; stop rather than misread.
		x.read = x.count
		return false
	}

	x.body = x.body[n:]
	x.read++
	return true
}

// decodeTellBatch validates a revision-5 batch body and returns a reader
// over it. A ref the connection's path table cannot resolve fails with an
// error wrapping [ErrUnknownTableRef] or [ErrTableRefUnsupported]; any other
// malformation fails with a plain error.
func (x *duplexConn) decodeTellBatch(src []byte) (*TellBatch, error) {
	if len(src) < TellBatchCountSize {
		return nil, fmt.Errorf("tcp: tell batch truncated: missing count")
	}

	count := binary.BigEndian.Uint32(src)
	body := src[TellBatchCountSize:]
	rest := body

	for i := uint32(0); i < count; i++ {
		n, err := skipTellBatchMessage(rest, x.pathReceiver)
		if err != nil {
			return nil, fmt.Errorf("tcp: tell batch message %d: %w", i, err)
		}

		rest = rest[n:]
	}

	if len(rest) != 0 {
		return nil, fmt.Errorf("tcp: tell batch has %d trailing bytes", len(rest))
	}

	return &TellBatch{
		body:    body,
		count:   int(count),
		paths:   x.pathReceiver,
		resolve: x.senderResolver,
	}, nil
}

// appendEncodedRef appends a table ref when id is nonzero, otherwise an
// inline literal ref for literal.
func appendEncodedRef(dst []byte, id uint64, literal string) []byte {
	if id != 0 {
		return binary.AppendUvarint(dst, id)
	}

	dst = binary.AppendUvarint(dst, 0)
	dst = binary.AppendUvarint(dst, uint64(len(literal)))
	return append(dst, literal...)
}

// skipTellBatchMessage checks one batch message at the start of src without
// copying anything out of it and returns the number of bytes it occupies.
// Table refs must resolve in paths.
func skipTellBatchMessage(src []byte, paths *receiverTable) (int, error) {
	pos := 0

	for _, field := range [2]string{"sender", "receiver"} {
		n, err := skipRef(src[pos:], paths)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", field, err)
		}
		pos += n
	}

	entries, n := binary.Uvarint(src[pos:])
	if n <= 0 {
		return 0, fmt.Errorf("truncated metadata count")
	}
	pos += n

	if err := checkMetadataCount(entries, len(src)-pos); err != nil {
		return 0, err
	}

	for i := uint64(0); i < 2*entries; i++ {
		n, err := skipInlineLiteral(src[pos:])
		if err != nil {
			return 0, fmt.Errorf("metadata: %w", err)
		}
		pos += n
	}

	n, err := skipInlineLiteral(src[pos:])
	if err != nil {
		return 0, fmt.Errorf("payload: %w", err)
	}

	return pos + n, nil
}

// checkMetadataCount rejects a metadata entry count above
// [maxTellBatchMetadataEntries] or above what remaining bytes can hold: each
// entry takes at least two bytes, an empty key and an empty value.
func checkMetadataCount(entries uint64, remaining int) error {
	if entries > maxTellBatchMetadataEntries {
		return fmt.Errorf("metadata count %d exceeds the limit %d", entries, maxTellBatchMetadataEntries)
	}

	if entries > uint64(remaining)/2 {
		return fmt.Errorf("metadata count %d exceeds remaining %d bytes", entries, remaining)
	}

	return nil
}

// skipRef checks one envelope ref at the start of src and returns its size.
// A nonzero table ID must resolve in table.
func skipRef(src []byte, table *receiverTable) (int, error) {
	id, n := binary.Uvarint(src)
	if n <= 0 {
		return 0, fmt.Errorf("truncated table id")
	}

	if id != 0 {
		if table == nil {
			return 0, fmt.Errorf("%w: id %d", ErrTableRefUnsupported, id)
		}

		if table.lookup(id) == nil {
			return 0, fmt.Errorf("%w: id %d", ErrUnknownTableRef, id)
		}

		return n, nil
	}

	m, err := skipInlineLiteral(src[n:])
	if err != nil {
		return 0, err
	}

	return n + m, nil
}

// skipInlineLiteral checks a uvarint length followed by that many bytes at
// the start of src and returns the total size.
func skipInlineLiteral(src []byte) (int, error) {
	length, n := binary.Uvarint(src)
	if n <= 0 {
		return 0, fmt.Errorf("truncated length")
	}

	if length > uint64(len(src)-n) {
		return 0, fmt.Errorf("length %d exceeds remaining %d", length, len(src)-n)
	}

	return n + int(length), nil
}

// readTellBatchMessage decodes one batch message from the start of src into
// msg and returns the number of bytes it occupies. resolve, when non-nil,
// fills msg.SenderHandle on a sender table hit.
func readTellBatchMessage(src []byte, paths *receiverTable, resolve func(string) any, msg *TellBatchMessage) (int, error) {
	sender, handle, pos, err := readSenderRef(src, paths, resolve)
	if err != nil {
		return 0, fmt.Errorf("sender: %w", err)
	}

	receiver, _, n, err := readRefResolved(src[pos:], paths)
	if err != nil {
		return 0, fmt.Errorf("receiver: %w", err)
	}
	pos += n

	entries, n := binary.Uvarint(src[pos:])
	if n <= 0 {
		return 0, fmt.Errorf("truncated metadata count")
	}
	pos += n

	if err := checkMetadataCount(entries, len(src)-pos); err != nil {
		return 0, err
	}

	// The map grows with the entries actually read; it is not presized
	// from the wire count.
	var metadata map[string]string
	if entries > 0 {
		metadata = make(map[string]string)
	}

	for range entries {
		key, n, err := readInlineLiteral(src[pos:])
		if err != nil {
			return 0, fmt.Errorf("metadata key: %w", err)
		}
		pos += n

		value, n, err := readInlineLiteral(src[pos:])
		if err != nil {
			return 0, fmt.Errorf("metadata value: %w", err)
		}
		pos += n

		metadata[key] = value
	}

	length, n := binary.Uvarint(src[pos:])
	if n <= 0 {
		return 0, fmt.Errorf("truncated payload length")
	}
	pos += n

	if length > uint64(len(src)-pos) {
		return 0, fmt.Errorf("payload length %d exceeds remaining %d", length, len(src)-pos)
	}

	*msg = TellBatchMessage{
		Sender:       sender,
		SenderHandle: handle,
		Receiver:     receiver,
		Metadata:     metadata,
		Payload:      src[pos : pos+int(length)],
	}

	return pos + int(length), nil
}
