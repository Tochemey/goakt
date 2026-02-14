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
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
)

// defaultMaxFrameSize is the default maximum allowed size for a single proto frame (16 MiB).
// This can be overridden per-client using [WithMaxFrameSize] or per-server using [WithProtoServerMaxFrameSize].
const defaultMaxFrameSize uint32 = 16 << 20

// Client is a thread-safe, connection-pooling TCP client. It dials the
// target address, optionally wraps connections with TLS and [ConnWrapper]
// layers (e.g. compression), and maintains a LIFO pool of idle connections
// for reuse. A single Client should be created and shared across goroutines.
//
// Stale connections are evicted lazily on [Client.Get] — no background
// goroutines are created.
//
// There are two usage patterns:
//
// Fire-and-forget writes using [Client.Send]:
//
//	compressor, err := tcp.NewZstdConnWrapper()
//	if err != nil { ... }
//
//	client := tcp.NewClient("server:9000",
//		tcp.WithMaxIdleConns(16),
//		tcp.WithClientConnWrapper(compressor),
//	)
//	defer client.Close()
//
//	err := client.Send(ctx, payload)
//
// Manual read/write using [Client.Get] and [Client.Put]:
//
//	conn, err := client.Get(ctx)
//	if err != nil { ... }
//
//	_, err = conn.Write(request)
//	if err != nil {
//		client.Discard(conn)
//		return err
//	}
//
//	_, err = io.ReadFull(conn, response)
//	if err != nil {
//		client.Discard(conn)
//		return err
//	}
//
//	client.Put(conn) // return to pool for reuse
//
// A connection obtained via [Client.Get] is owned by the caller until
// returned with [Client.Put] or [Client.Discard]. It must not be used
// from multiple goroutines concurrently.
type Client struct {
	addr         string
	dialer       net.Dialer
	tlsConfig    *tls.Config
	connWrappers []ConnWrapper
	maxIdle      int
	idleTimeout  time.Duration
	maxFrameSize uint32
	serializer   *ProtoSerializer
	framePool    *framePool

	mu     sync.Mutex
	idle   []idleConn
	closed atomic.Bool
}

type idleConn struct {
	conn  net.Conn
	since int64 // UnixNano
}

// ClientOption configures a [Client].
type ClientOption func(*Client)

// NewClient creates a Client that connects to addr (host:port).
//
// Defaults: 8 max idle connections, 30 s idle timeout, 5 s dial timeout,
// 15 s TCP keep-alive, 16 MiB max frame size.
func NewClient(addr string, opts ...ClientOption) *Client {
	c := &Client{
		addr:         addr,
		maxIdle:      8,
		idleTimeout:  30 * time.Second,
		maxFrameSize: defaultMaxFrameSize,
		serializer:   NewProtoSerializer(),
		framePool:    newFramePool(),
		dialer: net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 15 * time.Second,
		},
	}
	for _, o := range opts {
		o(c)
	}
	c.idle = make([]idleConn, 0, c.maxIdle)
	return c
}

// WithTLS configures the client to wrap every connection with TLS.
func WithTLS(config *tls.Config) ClientOption {
	return func(c *Client) { c.tlsConfig = config }
}

// WithClientConnWrapper appends a [ConnWrapper] (e.g. compression) to the
// client's wrapping pipeline, applied after TLS.
func WithClientConnWrapper(w ConnWrapper) ClientOption {
	return func(c *Client) { c.connWrappers = append(c.connWrappers, w) }
}

// WithMaxIdleConns sets the maximum number of idle connections kept in
// the pool. Zero disables pooling.
func WithMaxIdleConns(n int) ClientOption {
	return func(c *Client) {
		if n < 0 {
			n = 0
		}
		c.maxIdle = n
	}
}

// WithIdleTimeout sets how long an idle connection stays in the pool
// before being evicted on the next [Client.Get].
func WithIdleTimeout(d time.Duration) ClientOption {
	return func(c *Client) { c.idleTimeout = d }
}

// WithKeepAlive sets the TCP keep-alive interval for new connections.
func WithKeepAlive(duration time.Duration) ClientOption {
	return func(c *Client) { c.dialer.KeepAlive = duration }
}

// WithDialTimeout sets the timeout for establishing new TCP connections.
func WithDialTimeout(d time.Duration) ClientOption {
	return func(c *Client) { c.dialer.Timeout = d }
}

// WithMaxFrameSize sets the maximum allowed size for a single proto frame.
// Frames larger than this limit will be rejected with ErrFrameTooLarge.
// The default is 16 MiB. Set to 0 to use a very large limit (4 GiB - 1).
func WithMaxFrameSize(size uint32) ClientOption {
	return func(c *Client) {
		if size == 0 {
			size = 1<<32 - 1 // Max uint32
		}
		c.maxFrameSize = size
	}
}

// Get returns a pooled connection or dials a new one. Stale idle
// connections are closed transparently. The caller must call [Client.Put]
// after a successful exchange or [Client.Discard] on error.
func (c *Client) Get(ctx context.Context) (net.Conn, error) {
	if c.closed.Load() {
		return nil, ErrClientClosed
	}

	now := time.Now().UnixNano()
	cutoff := now - c.idleTimeout.Nanoseconds()

	c.mu.Lock()
	for len(c.idle) > 0 {
		n := len(c.idle)
		ic := c.idle[n-1]
		c.idle[n-1] = idleConn{}
		c.idle = c.idle[:n-1]

		if ic.since < cutoff {
			c.mu.Unlock()
			// Close error intentionally ignored — stale connection is being evicted.
			_ = ic.conn.Close()
			c.mu.Lock()
			continue
		}

		c.mu.Unlock()
		return ic.conn, nil
	}
	c.mu.Unlock()

	return c.dial(ctx)
}

// Put returns a healthy connection to the idle pool. If the pool is full
// the connection is closed. Any previously set deadlines are cleared.
func (c *Client) Put(conn net.Conn) {
	if c.closed.Load() {
		// Close error intentionally ignored — client is shut down, connection is being discarded.
		_ = conn.Close()
		return
	}

	if err := conn.SetDeadline(time.Time{}); err != nil {
		// Close error intentionally ignored — deadline reset failed, connection is unusable.
		_ = conn.Close()
		return
	}

	c.mu.Lock()
	if len(c.idle) < c.maxIdle {
		c.idle = append(c.idle, idleConn{
			conn:  conn,
			since: time.Now().UnixNano(),
		})
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	// Close error intentionally ignored — pool is full, excess connection is released.
	_ = conn.Close()
}

// Discard closes a connection without returning it to the pool. Use this
// after a failed read or write.
func (c *Client) Discard(conn net.Conn) {
	// Close error intentionally ignored — caller already encountered an error on this connection.
	_ = conn.Close()
}

// SendBytes is a convenience method that gets a connection, writes data, and
// returns the connection to the pool. On write failure the connection is
// discarded. If ctx carries a deadline it is applied as the write timeout.
func (c *Client) SendBytes(ctx context.Context, data []byte) error {
	conn, err := c.Get(ctx)
	if err != nil {
		return err
	}

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetWriteDeadline(deadline); err != nil {
			c.Discard(conn)
			return err
		}
	}

	_, err = conn.Write(data)
	if err != nil {
		c.Discard(conn)
		return err
	}

	c.Put(conn)
	return nil
}

// SendProto marshals a protobuf request using [ProtoSerializer], sends it,
// reads the response, and unmarshals it. Returns the unmarshaled response message.
// The wire format includes message type information for dynamic deserialization.
//
// If the context contains metadata (via [ContextWithMetadata]), it is automatically
// included in the request frame. The response is intelligently unmarshaled, handling
// both metadata and non-metadata formats transparently.
//
// If ctx carries a deadline, it is applied to both read and write operations.
//
// Use [Client.SendProtoWithMetadata] if you need to inspect metadata from the response.
func (c *Client) SendProto(ctx context.Context, req proto.Message) (proto.Message, error) {
	resp, _, err := c.SendProtoWithMetadata(ctx, req)
	return resp, err
}

// SendProtoWithMetadata is like [Client.SendProto] but also returns any metadata
// received in the response. This is useful for extracting tracing information,
// error details, or other context propagated by the server.
//
// The returned metadata will be nil if the server response did not include metadata.
func (c *Client) SendProtoWithMetadata(ctx context.Context, req proto.Message) (proto.Message, *Metadata, error) {
	conn, err := c.Get(ctx)
	if err != nil {
		return nil, nil, err
	}

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			c.Discard(conn)
			return nil, nil, err
		}
	}

	// Marshal request with context-aware metadata handling.
	reqData, err := c.marshalProtoWithContext(ctx, req)
	if err != nil {
		c.Discard(conn)
		return nil, nil, err
	}

	// Write serialized message.
	if _, err := conn.Write(reqData); err != nil {
		c.Discard(conn)
		return nil, nil, err
	}

	// Read the complete response frame from a pooled buffer.
	respData, err := readProtoFrame(conn, c.framePool, c.maxFrameSize)
	if err != nil {
		c.Discard(conn)
		return nil, nil, err
	}

	// Unmarshal response with automatic format detection.
	resp, md, err := c.unmarshalProtoResponse(respData)
	c.framePool.Put(respData)
	if err != nil {
		c.Discard(conn)
		return nil, nil, err
	}

	c.Put(conn)
	return resp, md, nil
}

// SendProtoNoReply marshals a protobuf message using [ProtoSerializer] and
// sends it without waiting for a response. The wire format includes message
// type information for dynamic deserialization on the server.
//
// If the context contains metadata (via [ContextWithMetadata]), it is automatically
// included in the request frame for context propagation (e.g., tracing, deadlines).
//
// If ctx carries a deadline, it is applied as the write timeout.
func (c *Client) SendProtoNoReply(ctx context.Context, req proto.Message) error {
	conn, err := c.Get(ctx)
	if err != nil {
		return err
	}

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetWriteDeadline(deadline); err != nil {
			c.Discard(conn)
			return err
		}
	}

	// Marshal request with context-aware metadata handling.
	reqData, err := c.marshalProtoWithContext(ctx, req)
	if err != nil {
		c.Discard(conn)
		return err
	}

	// Write serialized message.
	if _, err := conn.Write(reqData); err != nil {
		c.Discard(conn)
		return err
	}

	c.Put(conn)
	return nil
}

// SendBatchProto sends multiple protobuf requests in sequence using
// [ProtoSerializer] and reads the corresponding responses in the same order.
// Returns a slice of unmarshaled response messages. All requests are sent
// before reading any responses.
//
// If the context contains metadata (via [ContextWithMetadata]), it is automatically
// included in each request frame. Responses are intelligently unmarshaled, handling
// both metadata and non-metadata formats transparently.
//
// If ctx carries a deadline, it is applied to the entire operation.
func (c *Client) SendBatchProto(ctx context.Context, reqs []proto.Message) ([]proto.Message, error) {
	conn, err := c.Get(ctx)
	if err != nil {
		return nil, err
	}

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			c.Discard(conn)
			return nil, err
		}
	}

	// Send all requests with context-aware metadata handling.
	for i, req := range reqs {
		reqData, err := c.marshalProtoWithContext(ctx, req)
		if err != nil {
			c.Discard(conn)
			return nil, err
		}

		if _, err := conn.Write(reqData); err != nil {
			c.Discard(conn)
			return nil, err
		}

		// Allow context cancellation between sends.
		if i < len(reqs)-1 {
			select {
			case <-ctx.Done():
				c.Discard(conn)
				return nil, ctx.Err()
			default:
			}
		}
	}

	// Read all responses in order with automatic format detection.
	resps := make([]proto.Message, len(reqs))

	for i := range resps {
		// Read the complete response frame from a pooled buffer.
		respData, err := readProtoFrame(conn, c.framePool, c.maxFrameSize)
		if err != nil {
			c.Discard(conn)
			return nil, err
		}

		// Unmarshal response with automatic format detection.
		resp, _, err := c.unmarshalProtoResponse(respData)
		c.framePool.Put(respData)
		if err != nil {
			c.Discard(conn)
			return nil, err
		}
		resps[i] = resp

		// Allow context cancellation between reads.
		if i < len(resps)-1 {
			select {
			case <-ctx.Done():
				c.Discard(conn)
				return nil, ctx.Err()
			default:
			}
		}
	}

	c.Put(conn)
	return resps, nil
}

// SendProtoManyNoReply sends multiple protobuf messages in sequence using
// [ProtoSerializer] without waiting for responses. The wire format includes
// message type information for dynamic deserialization on the server.
//
// If the context contains metadata (via [ContextWithMetadata]), it is automatically
// included in each request frame for context propagation (e.g., tracing, deadlines).
//
// If ctx carries a deadline, it is applied as the write timeout.
func (c *Client) SendProtoManyNoReply(ctx context.Context, reqs []proto.Message) error {
	conn, err := c.Get(ctx)
	if err != nil {
		return err
	}

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetWriteDeadline(deadline); err != nil {
			c.Discard(conn)
			return err
		}
	}

	for i, req := range reqs {
		// Marshal request with context-aware metadata handling.
		reqData, err := c.marshalProtoWithContext(ctx, req)
		if err != nil {
			c.Discard(conn)
			return err
		}

		if _, err := conn.Write(reqData); err != nil {
			c.Discard(conn)
			return err
		}

		// Allow context cancellation between sends.
		if i < len(reqs)-1 {
			select {
			case <-ctx.Done():
				c.Discard(conn)
				return ctx.Err()
			default:
			}
		}
	}

	c.Put(conn)
	return nil
}

// Close shuts down the client and closes all pooled connections. It is
// idempotent — subsequent calls are no-ops.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}

	c.mu.Lock()
	idle := c.idle
	c.idle = nil
	c.mu.Unlock()

	var firstErr error
	for i := range idle {
		if err := idle[i].conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	raw, err := c.dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return nil, err
	}

	conn := raw

	if c.tlsConfig != nil {
		conn = tls.Client(conn, c.tlsConfig)
	}

	for _, w := range c.connWrappers {
		wrapped, err := w.Wrap(conn)
		if err != nil {
			// Close error intentionally ignored — wrapper setup already failed.
			_ = conn.Close()
			return nil, err
		}
		conn = wrapped
	}

	return conn, nil
}

// readProtoFrame reads a single [ProtoSerializer] frame from r.
// It reads the 4-byte length prefix, validates it, then reads the remaining
// bytes and returns the complete frame (including the prefix).
//
// The maxFrameSize parameter specifies the maximum allowed frame size in bytes.
// Frames larger than this limit will be rejected with ErrFrameTooLarge.
//
// When fp is non-nil the frame buffer is drawn from the pool. The caller
// must return it via fp.Put after the frame contents have been consumed
// (typically right after [ProtoSerializer.UnmarshalBinary]).
// When fp is nil a fresh []byte is allocated for each frame.
func readProtoFrame(r io.Reader, fp *framePool, maxFrameSize uint32) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}

	totalLen := binary.BigEndian.Uint32(hdr[:])
	if totalLen < 8 {
		// Minimum valid frame: 4 (total len) + 4 (name len) + 0 + 0.
		return nil, ErrInvalidMessageLength
	}
	if totalLen > maxFrameSize {
		return nil, ErrFrameTooLarge
	}

	var frame []byte
	if fp != nil {
		frame = fp.Get(int(totalLen))
	} else {
		frame = make([]byte, totalLen)
	}

	copy(frame[:4], hdr[:])
	if _, err := io.ReadFull(r, frame[4:]); err != nil {
		if fp != nil {
			fp.Put(frame)
		}
		return nil, err
	}

	return frame, nil
}

// marshalProtoWithContext marshals a protobuf message, automatically including
// metadata from the context if present. This is a zero-allocation decision path
// that checks the context once and delegates to the appropriate serializer method.
//
// Returns the serialized frame bytes ready for transmission.
func (c *Client) marshalProtoWithContext(ctx context.Context, msg proto.Message) ([]byte, error) {
	// Check once if metadata exists in the context.
	md, hasMD := FromContext(ctx)
	if hasMD && md != nil {
		return c.serializer.MarshalBinaryWithMetadata(msg, md)
	}
	return c.serializer.MarshalBinary(msg)
}

// unmarshalProtoResponse unmarshals a protobuf response frame, automatically
// detecting whether it includes metadata. The detection is performed by examining
// the frame structure (checking if bytes 8:12 represent a valid metaLen field).
//
// This enables the client to handle both legacy responses (no metadata) and
// modern responses (with metadata) transparently. Returns the unmarshaled message
// and metadata (nil if not present in the frame).
//
// The frame buffer should be returned to the pool by the caller after this call.
func (c *Client) unmarshalProtoResponse(frame []byte) (proto.Message, *Metadata, error) {
	// Fast path: check frame length to determine format.
	// Metadata format has minimum 12 bytes (totalLen + nameLen + metaLen).
	// Non-metadata format has minimum 8 bytes (totalLen + nameLen).
	if len(frame) < 12 {
		// Must be non-metadata format (or invalid).
		msg, _, err := c.serializer.UnmarshalBinary(frame)
		return msg, nil, err
	}

	// Read the header fields to detect the format.
	totalLen := int(binary.BigEndian.Uint32(frame[0:4]))
	nameLen := int(binary.BigEndian.Uint32(frame[4:8]))
	potentialMetaLen := int(binary.BigEndian.Uint32(frame[8:12]))

	// Heuristic: if the frame structure is consistent with the metadata format,
	// use UnmarshalBinaryWithMetadata. The metadata format has:
	//   totalLen >= 12 + nameLen + metaLen
	// The non-metadata format would have the proto bytes starting at position 8+nameLen,
	// so the third uint32 (bytes 8:12) would be part of either the type name or proto data.
	//
	// If 12 + nameLen + potentialMetaLen <= totalLen, it's likely the metadata format.
	// Also check that nameLen is reasonable (type names are typically < 256 bytes).
	if nameLen > 0 && nameLen < 256 && potentialMetaLen >= 0 && (12+nameLen+potentialMetaLen) <= totalLen {
		// Try metadata format first.
		msg, md, _, err := c.serializer.UnmarshalBinaryWithMetadata(frame)
		if err == nil {
			return msg, md, nil
		}
		// If it fails, fall through to try non-metadata format.
	}

	// Fall back to non-metadata format.
	msg, _, err := c.serializer.UnmarshalBinary(frame)
	return msg, nil, err
}
