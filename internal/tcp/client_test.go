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

package tcp

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestNewClient(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		c := NewClient("localhost:9000")
		require.NotNil(t, c)
		require.Equal(t, "localhost:9000", c.addr)
		require.Equal(t, 8, c.maxIdle)
		require.Equal(t, 30*time.Second, c.idleTimeout)
		require.Equal(t, 5*time.Second, c.dialer.Timeout)
		require.Equal(t, 15*time.Second, c.dialer.KeepAlive)
		require.NotNil(t, c.serializer)
		require.NotNil(t, c.idle)
	})

	t.Run("with custom options", func(t *testing.T) {
		tlsCfg := &tls.Config{} //nolint:gosec
		c := NewClient("localhost:9000",
			WithMaxIdleConns(16),
			WithIdleTimeout(60*time.Second),
			WithDialTimeout(10*time.Second),
			WithKeepAlive(30*time.Second),
			WithTLS(tlsCfg),
		)
		require.NotNil(t, c)
		require.Equal(t, 16, c.maxIdle)
		require.Equal(t, 60*time.Second, c.idleTimeout)
		require.Equal(t, 10*time.Second, c.dialer.Timeout)
		require.Equal(t, 30*time.Second, c.dialer.KeepAlive)
		require.Equal(t, tlsCfg, c.tlsConfig)
	})

	t.Run("with negative maxIdle clamped to zero", func(t *testing.T) {
		c := NewClient("localhost:9000", WithMaxIdleConns(-10))
		require.Equal(t, 0, c.maxIdle)
	})
}

func TestClient_GetPut(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c) // echo; errors expected on client disconnect
			}(conn)
		}
	}()

	client := NewClient(listener.Addr().String())
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	t.Run("get and put connection", func(t *testing.T) {
		conn, err := client.Get(ctx)
		require.NoError(t, err)
		require.NotNil(t, conn)

		_, err = conn.Write([]byte("test"))
		require.NoError(t, err)

		client.Put(conn)

		client.mu.Lock()
		count := len(client.idle)
		client.mu.Unlock()
		require.Equal(t, 1, count)
	})

	t.Run("get reuses pooled connection", func(t *testing.T) {
		conn1, err := client.Get(ctx)
		require.NoError(t, err)
		client.Put(conn1)

		conn2, err := client.Get(ctx)
		require.NoError(t, err)
		require.Equal(t, conn1, conn2, "should reuse pooled connection")
		client.Put(conn2)
	})

	t.Run("discard connection", func(t *testing.T) {
		conn, err := client.Get(ctx)
		require.NoError(t, err)

		client.Discard(conn)

		client.mu.Lock()
		count := len(client.idle)
		client.mu.Unlock()
		require.Equal(t, 0, count)
	})
}

func TestClient_PoolEviction(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close() // accept and close immediately
		}
	}()

	client := NewClient(listener.Addr().String(), WithIdleTimeout(100*time.Millisecond))
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	conn, err := client.Get(ctx)
	require.NoError(t, err)
	client.Put(conn)

	// Wait for the connection to become stale.
	pause.For(200 * time.Millisecond)

	// Next Get should evict stale connection and dial a new one.
	conn2, err := client.Get(ctx)
	require.NoError(t, err)
	require.NotEqual(t, conn, conn2, "stale connection should be evicted")
	client.Put(conn2)
}

func TestClient_PoolCapacity(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()

	client := NewClient(listener.Addr().String(), WithMaxIdleConns(2))
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	conns := make([]net.Conn, 3)
	for i := range conns {
		conns[i], err = client.Get(ctx)
		require.NoError(t, err)
	}

	for _, conn := range conns {
		client.Put(conn)
	}

	client.mu.Lock()
	count := len(client.idle)
	client.mu.Unlock()
	require.Equal(t, 2, count, "pool should respect maxIdle limit")
}

func TestClient_Send(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	client := NewClient(listener.Addr().String())
	defer func() { _ = client.Close() }()

	t.Run("successful send", func(t *testing.T) {
		ctx := context.Background()
		err := client.Send(ctx, []byte("hello"))
		require.NoError(t, err)

		client.mu.Lock()
		count := len(client.idle)
		client.mu.Unlock()
		require.Equal(t, 1, count)
	})

	t.Run("send with deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := client.Send(ctx, []byte("hello"))
		require.NoError(t, err)
	})
}

func TestClient_Close(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()

	client := NewClient(listener.Addr().String())

	ctx := context.Background()

	for range 3 {
		conn, err := client.Get(ctx)
		require.NoError(t, err)
		client.Put(conn)
	}

	err = client.Close()
	require.NoError(t, err)

	client.mu.Lock()
	count := len(client.idle)
	client.mu.Unlock()
	require.Equal(t, 0, count)

	_, err = client.Get(ctx)
	require.ErrorIs(t, err, ErrClientClosed)

	// Double close should be no-op.
	err = client.Close()
	require.NoError(t, err)
}

func TestClient_ConcurrentAccess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	client := NewClient(listener.Addr().String(), WithMaxIdleConns(10))
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			conn, err := client.Get(ctx)
			if err != nil {
				return
			}
			pause.For(time.Millisecond)
			client.Put(conn)
		})
	}

	wg.Wait()
}

func TestClient_DialFailure(t *testing.T) {
	client := NewClient("127.0.0.1:1", WithDialTimeout(100*time.Millisecond))
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	_, err := client.Get(ctx)
	require.Error(t, err, "should fail to dial non-existent server")
}

func TestClient_ContextCancellation(t *testing.T) {
	// 192.0.2.0/24 is TEST-NET-1 (RFC 5737), guaranteed non-routable.
	client := NewClient("192.0.2.1:9999")
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.Get(ctx)
	require.Error(t, err, "should fail due to context timeout during dial")
}

func TestReadProtoFrame(t *testing.T) {
	t.Run("valid frame", func(t *testing.T) {
		totalLen := uint32(8 + 3 + 2) // header + "abc" + "xx"
		data := make([]byte, totalLen)
		data[0] = byte(totalLen >> 24)
		data[1] = byte(totalLen >> 16)
		data[2] = byte(totalLen >> 8)
		data[3] = byte(totalLen)
		data[4] = 0
		data[5] = 0
		data[6] = 0
		data[7] = 3
		copy(data[8:], "abc")
		copy(data[11:], "xx")

		r := &mockReader{data: data}
		frame, err := readProtoFrame(r, nil)
		require.NoError(t, err)
		require.Equal(t, data, frame)
	})

	t.Run("frame too small", func(t *testing.T) {
		data := []byte{0, 0, 0, 4}
		r := &mockReader{data: data}
		_, err := readProtoFrame(r, nil)
		require.ErrorIs(t, err, ErrInvalidMessageLength)
	})

	t.Run("frame too large", func(t *testing.T) {
		data := []byte{0xFF, 0xFF, 0xFF, 0xFF}
		r := &mockReader{data: data}
		_, err := readProtoFrame(r, nil)
		require.ErrorIs(t, err, ErrFrameTooLarge)
	})

	t.Run("incomplete read", func(t *testing.T) {
		// Header says 20 bytes but only 10 available.
		data := make([]byte, 10)
		data[0] = 0
		data[1] = 0
		data[2] = 0
		data[3] = 20
		r := &mockReader{data: data}
		_, err := readProtoFrame(r, nil)
		require.Error(t, err)
	})
}

func TestClient_WithClientConnWrapper(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	var wrapCalled int
	wrapper := &testWrapper{onWrap: func() { wrapCalled++ }}

	client := NewClient(listener.Addr().String(), WithClientConnWrapper(wrapper))
	defer func() { _ = client.Close() }()

	require.Len(t, client.connWrappers, 1)

	conn, err := client.Get(context.Background())
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, 1, wrapCalled)

	client.Put(conn)
}

func TestClient_PutClosedConn(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			_ = c.Close()
		}
	}()

	client := NewClient(listener.Addr().String())
	defer func() { _ = client.Close() }()

	conn, err := client.Get(context.Background())
	require.NoError(t, err)

	// Manually close the underlying connection.
	require.NoError(t, conn.Close())

	// Put should detect SetDeadline failure and discard the connection.
	client.Put(conn)

	client.mu.Lock()
	count := len(client.idle)
	client.mu.Unlock()
	require.Equal(t, 0, count, "closed conn should not be pooled")
}

func TestClient_PutWhenClosed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			_ = c.Close()
		}
	}()

	client := NewClient(listener.Addr().String())
	conn, err := client.Get(context.Background())
	require.NoError(t, err)

	require.NoError(t, client.Close())

	// Put on a closed client should close the connection.
	client.Put(conn)

	client.mu.Lock()
	count := len(client.idle)
	client.mu.Unlock()
	require.Equal(t, 0, count)
}

func TestClient_SendToClosedServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			_ = c.Close()
		}
	}()

	client := NewClient(listener.Addr().String())
	defer func() { _ = client.Close() }()

	conn, err := client.Get(context.Background())
	require.NoError(t, err)
	client.Put(conn)

	// Close the server so the pooled connection becomes dead.
	require.NoError(t, listener.Close())
	pause.For(50 * time.Millisecond)

	// The pooled connection should be dead; Send may fail on write depending
	// on timing — we only verify no panic.
	_ = client.Send(context.Background(), []byte("test")) //nolint:errcheck // outcome is timing-dependent
}

func TestClient_DialWithConnWrapperError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			_ = c.Close()
		}
	}()

	wrapper := &failingWrapper{}
	client := NewClient(listener.Addr().String(), WithClientConnWrapper(wrapper))
	defer func() { _ = client.Close() }()

	_, err = client.Get(context.Background())
	require.Error(t, err, "should fail when conn wrapper returns error")
}

func TestClient_DialWithTLS(t *testing.T) {
	tlsCfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	client := NewClient("127.0.0.1:1", WithTLS(tlsCfg), WithDialTimeout(50*time.Millisecond))
	defer func() { _ = client.Close() }()

	require.Equal(t, tlsCfg, client.tlsConfig)

	// Dial will fail (no server), but verifies the code path is reached.
	_, err := client.Get(context.Background())
	require.Error(t, err)
}

func TestClient_MaxIdleZero(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			go func(cn net.Conn) {
				defer func() { _ = cn.Close() }()
				_, _ = io.Copy(io.Discard, cn)
			}(c)
		}
	}()

	client := NewClient(listener.Addr().String(), WithMaxIdleConns(0))
	defer func() { _ = client.Close() }()

	conn, err := client.Get(context.Background())
	require.NoError(t, err)

	client.Put(conn)

	client.mu.Lock()
	count := len(client.idle)
	client.mu.Unlock()
	require.Equal(t, 0, count, "maxIdle=0 should never pool connections")
}

func TestClient_SendWriteError_NoDeadline(t *testing.T) {
	client := NewClient("dummy:0")
	c1, c2 := net.Pipe()
	_ = c2.Close() // Only c1 is needed.
	_ = c1.Close()
	client.mu.Lock()
	client.idle = append(client.idle, idleConn{conn: c1, since: time.Now().UnixNano()})
	client.mu.Unlock()

	// No deadline context — skips SetWriteDeadline, fails on Write.
	err := client.Send(context.Background(), []byte("test"))
	require.Error(t, err)
}

func TestClient_SendWriteError_WithDeadline(t *testing.T) {
	client := NewClient("dummy:0")
	c1, c2 := net.Pipe()
	_ = c2.Close() // Only c1 is needed.
	_ = c1.Close()
	client.mu.Lock()
	client.idle = append(client.idle, idleConn{conn: c1, since: time.Now().UnixNano()})
	client.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := client.Send(ctx, []byte("test"))
	require.Error(t, err)
}

func TestClient_CloseWithBrokenConn(t *testing.T) {
	client := NewClient("dummy:0")
	mc := &mockNetConn{closeFunc: func() error { return errors.New("close failed") }}
	client.mu.Lock()
	client.idle = append(client.idle, idleConn{conn: mc, since: time.Now().UnixNano()})
	client.mu.Unlock()

	err := client.Close()
	require.Error(t, err)
	require.Contains(t, err.Error(), "close failed")
}

func TestClient_DialWithTLSSuccess(t *testing.T) {
	cert, key := generateTestCert(t)
	tlsCert, err := tls.X509KeyPair(cert, key)
	require.NoError(t, err)

	tlsConfig := &tls.Config{Certificates: []tls.Certificate{tlsCert}} //nolint:gosec
	listener, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}()

	clientTLSConfig := &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	client := NewClient(listener.Addr().String(), WithTLS(clientTLSConfig))
	defer func() { _ = client.Close() }()

	conn, err := client.Get(context.Background())
	require.NoError(t, err)
	client.Put(conn)
}

// startProtoEchoServer starts a TCP server that reads proto frames, deserializes
// them, and echoes them back. Returns the listen address and a cleanup function.
func startProtoEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	serializer := NewProtoSerializer()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				for {
					frame, err := readProtoFrame(c, nil)
					if err != nil {
						return // client disconnected or read error
					}
					msg, _, err := serializer.UnmarshalBinary(frame)
					if err != nil {
						return
					}
					resp, err := serializer.MarshalBinary(msg)
					if err != nil {
						return
					}
					if _, err := c.Write(resp); err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	return listener.Addr().String(), func() { _ = listener.Close() }
}

// startProtoSinkServer starts a TCP server that reads and discards all data.
func startProtoSinkServer(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()

	return listener.Addr().String(), func() { _ = listener.Close() }
}

func TestClient_SendProto(t *testing.T) {
	addr, cleanup := startProtoEchoServer(t)
	defer cleanup()
	pause.For(50 * time.Millisecond)

	client := NewClient(addr)
	defer func() { _ = client.Close() }()

	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		req := &testpb.Reply{Content: "hello proto"}

		resp, err := client.SendProto(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		reply, ok := resp.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, "hello proto", reply.Content)
	})

	t.Run("with deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req := &testpb.Reply{Content: "deadline test"}
		resp, err := client.SendProto(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("closed client", func(t *testing.T) {
		c := NewClient(addr)
		require.NoError(t, c.Close())

		_, err := c.SendProto(context.Background(), &testpb.Reply{Content: "x"})
		require.ErrorIs(t, err, ErrClientClosed)
	})
}

func TestClient_SendProtoNoReply(t *testing.T) {
	addr, cleanup := startProtoSinkServer(t)
	defer cleanup()
	pause.For(50 * time.Millisecond)

	client := NewClient(addr)
	defer func() { _ = client.Close() }()

	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		err := client.SendProtoNoReply(ctx, &testpb.Reply{Content: "fire and forget"})
		require.NoError(t, err)
	})

	t.Run("with deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := client.SendProtoNoReply(ctx, &testpb.Reply{Content: "deadline noreply"})
		require.NoError(t, err)
	})

	t.Run("closed client", func(t *testing.T) {
		c := NewClient(addr)
		require.NoError(t, c.Close())

		err := c.SendProtoNoReply(context.Background(), &testpb.Reply{Content: "x"})
		require.ErrorIs(t, err, ErrClientClosed)
	})
}

func TestClient_SendProtoMany(t *testing.T) {
	addr, cleanup := startProtoEchoServer(t)
	defer cleanup()
	pause.For(50 * time.Millisecond)

	client := NewClient(addr)
	defer func() { _ = client.Close() }()

	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		reqs := []proto.Message{
			&testpb.Reply{Content: "msg1"},
			&testpb.Reply{Content: "msg2"},
			&testpb.Reply{Content: "msg3"},
		}

		resps, err := client.SendProtoMany(ctx, reqs)
		require.NoError(t, err)
		require.Len(t, resps, 3)

		for i, resp := range resps {
			reply, ok := resp.(*testpb.Reply)
			require.True(t, ok)
			require.Equal(t, reqs[i].(*testpb.Reply).Content, reply.Content)
		}
	})

	t.Run("with deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		reqs := []proto.Message{
			&testpb.Reply{Content: "d1"},
			&testpb.Reply{Content: "d2"},
		}

		resps, err := client.SendProtoMany(ctx, reqs)
		require.NoError(t, err)
		require.Len(t, resps, 2)
	})

	t.Run("closed client", func(t *testing.T) {
		c := NewClient(addr)
		require.NoError(t, c.Close())

		_, err := c.SendProtoMany(context.Background(), []proto.Message{&testpb.Reply{Content: "x"}})
		require.ErrorIs(t, err, ErrClientClosed)
	})
}

func TestClient_SendProtoManyNoReply(t *testing.T) {
	addr, cleanup := startProtoSinkServer(t)
	defer cleanup()
	pause.For(50 * time.Millisecond)

	client := NewClient(addr)
	defer func() { _ = client.Close() }()

	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		reqs := []proto.Message{
			&testpb.Reply{Content: "nr1"},
			&testpb.Reply{Content: "nr2"},
			&testpb.Reply{Content: "nr3"},
		}

		err := client.SendProtoManyNoReply(ctx, reqs)
		require.NoError(t, err)
	})

	t.Run("with deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		reqs := []proto.Message{&testpb.Reply{Content: "dl"}}
		err := client.SendProtoManyNoReply(ctx, reqs)
		require.NoError(t, err)
	})

	t.Run("closed client", func(t *testing.T) {
		c := NewClient(addr)
		require.NoError(t, c.Close())

		err := c.SendProtoManyNoReply(context.Background(), []proto.Message{&testpb.Reply{Content: "x"}})
		require.ErrorIs(t, err, ErrClientClosed)
	})
}

func TestClient_SendProto_MarshalError(t *testing.T) {
	addr, cleanup := startProtoSinkServer(t)
	defer cleanup()
	pause.For(50 * time.Millisecond)

	client := NewClient(addr)
	defer func() { _ = client.Close() }()

	// nil message triggers ErrUnknownMessageType from MarshalBinary.
	_, err := client.SendProto(context.Background(), nil)
	require.Error(t, err)
}

func TestClient_SendProto_ClosedConnWithDeadline(t *testing.T) {
	client := NewClient("dummy:0")
	c1, c2 := net.Pipe()
	_ = c2.Close() // Only c1 is needed.
	_ = c1.Close()
	client.mu.Lock()
	client.idle = append(client.idle, idleConn{conn: c1, since: time.Now().UnixNano()})
	client.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := client.SendProto(ctx, &testpb.Reply{Content: "x"})
	require.Error(t, err)
}

func TestClient_SendProto_ClosedConnNoDeadline(t *testing.T) {
	client := NewClient("dummy:0")
	c1, c2 := net.Pipe()
	_ = c2.Close() // Only c1 is needed.
	_ = c1.Close()
	client.mu.Lock()
	client.idle = append(client.idle, idleConn{conn: c1, since: time.Now().UnixNano()})
	client.mu.Unlock()

	_, err := client.SendProto(context.Background(), &testpb.Reply{Content: "x"})
	require.Error(t, err)
}

func TestClient_SendProto_ReadError(t *testing.T) {
	// Server that accepts, reads, but doesn't respond (closes connection).
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				// Read the request frame then close without responding.
				buf := make([]byte, 4096)
				_, _ = c.Read(buf) // read error expected when client disconnects
				_ = c.Close()
			}(conn)
		}
	}()
	pause.For(50 * time.Millisecond)

	client := NewClient(listener.Addr().String())
	defer func() { _ = client.Close() }()

	_, err = client.SendProto(context.Background(), &testpb.Reply{Content: "x"})
	require.Error(t, err)
}

func TestClient_SendProtoNoReply_MarshalError(t *testing.T) {
	addr, cleanup := startProtoSinkServer(t)
	defer cleanup()
	pause.For(50 * time.Millisecond)

	client := NewClient(addr)
	defer func() { _ = client.Close() }()

	err := client.SendProtoNoReply(context.Background(), nil)
	require.Error(t, err)
}

func TestClient_SendProtoNoReply_ClosedConnWithDeadline(t *testing.T) {
	client := NewClient("dummy:0")
	c1, c2 := net.Pipe()
	_ = c2.Close() // Only c1 is needed.
	_ = c1.Close()
	client.mu.Lock()
	client.idle = append(client.idle, idleConn{conn: c1, since: time.Now().UnixNano()})
	client.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := client.SendProtoNoReply(ctx, &testpb.Reply{Content: "x"})
	require.Error(t, err)
}

func TestClient_SendProtoNoReply_ClosedConnNoDeadline(t *testing.T) {
	client := NewClient("dummy:0")
	c1, c2 := net.Pipe()
	_ = c2.Close() // Only c1 is needed.
	_ = c1.Close()
	client.mu.Lock()
	client.idle = append(client.idle, idleConn{conn: c1, since: time.Now().UnixNano()})
	client.mu.Unlock()

	err := client.SendProtoNoReply(context.Background(), &testpb.Reply{Content: "x"})
	require.Error(t, err)
}

func TestClient_SendProtoMany_MarshalError(t *testing.T) {
	addr, cleanup := startProtoSinkServer(t)
	defer cleanup()
	pause.For(50 * time.Millisecond)

	client := NewClient(addr)
	defer func() { _ = client.Close() }()

	_, err := client.SendProtoMany(context.Background(), []proto.Message{nil})
	require.Error(t, err)
}

func TestClient_SendProtoMany_ClosedConnWithDeadline(t *testing.T) {
	client := NewClient("dummy:0")
	c1, c2 := net.Pipe()
	_ = c2.Close() // Only c1 is needed.
	_ = c1.Close()
	client.mu.Lock()
	client.idle = append(client.idle, idleConn{conn: c1, since: time.Now().UnixNano()})
	client.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := client.SendProtoMany(ctx, []proto.Message{&testpb.Reply{Content: "x"}})
	require.Error(t, err)
}

func TestClient_SendProtoMany_ContextCancellation(t *testing.T) {
	addr, cleanup := startProtoEchoServer(t)
	defer cleanup()
	pause.For(50 * time.Millisecond)

	client := NewClient(addr)
	defer func() { _ = client.Close() }()

	// Pre-pool a connection so Get succeeds even with cancelled context.
	conn, err := client.Get(context.Background())
	require.NoError(t, err)
	client.Put(conn)

	// Cancel context before call.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// With 2+ messages, after the first send the ctx.Done() check triggers.
	reqs := []proto.Message{
		&testpb.Reply{Content: "a"},
		&testpb.Reply{Content: "b"},
	}
	// Might succeed (server fast enough) or fail (context cancelled).
	// Either outcome is valid — we only verify no panic.
	_, _ = client.SendProtoMany(ctx, reqs) //nolint:errcheck // outcome is timing-dependent
}

func TestClient_SendProtoManyNoReply_MarshalError(t *testing.T) {
	addr, cleanup := startProtoSinkServer(t)
	defer cleanup()
	pause.For(50 * time.Millisecond)

	client := NewClient(addr)
	defer func() { _ = client.Close() }()

	err := client.SendProtoManyNoReply(context.Background(), []proto.Message{nil})
	require.Error(t, err)
}

func TestClient_SendProtoManyNoReply_ClosedConnWithDeadline(t *testing.T) {
	client := NewClient("dummy:0")
	c1, c2 := net.Pipe()
	_ = c2.Close() // Only c1 is needed.
	_ = c1.Close()
	client.mu.Lock()
	client.idle = append(client.idle, idleConn{conn: c1, since: time.Now().UnixNano()})
	client.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := client.SendProtoManyNoReply(ctx, []proto.Message{&testpb.Reply{Content: "x"}})
	require.Error(t, err)
}

func TestClient_SendProtoManyNoReply_ContextCancellation(t *testing.T) {
	addr, cleanup := startProtoSinkServer(t)
	defer cleanup()
	pause.For(50 * time.Millisecond)

	client := NewClient(addr)
	defer func() { _ = client.Close() }()

	conn, err := client.Get(context.Background())
	require.NoError(t, err)
	client.Put(conn)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	reqs := []proto.Message{
		&testpb.Reply{Content: "a"},
		&testpb.Reply{Content: "b"},
	}
	// Might succeed or fail depending on timing — we only verify no panic.
	_ = client.SendProtoManyNoReply(ctx, reqs) //nolint:errcheck // outcome is timing-dependent
}

func TestClient_SendProtoMany_ReadError(t *testing.T) {
	// Server that reads requests then closes without sending responses.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 64*1024)
				_, _ = c.Read(buf) // read error expected when client disconnects
				_ = c.Close()
			}(conn)
		}
	}()
	pause.For(50 * time.Millisecond)

	client := NewClient(listener.Addr().String())
	defer func() { _ = client.Close() }()

	reqs := []proto.Message{
		&testpb.Reply{Content: "a"},
		&testpb.Reply{Content: "b"},
	}
	_, err = client.SendProtoMany(context.Background(), reqs)
	require.Error(t, err)
}

func TestClient_SendProtoMany_WriteError(t *testing.T) {
	client := NewClient("dummy:0")
	c1, c2 := net.Pipe()
	_ = c2.Close() // Only c1 is needed.
	_ = c1.Close()
	client.mu.Lock()
	client.idle = append(client.idle, idleConn{conn: c1, since: time.Now().UnixNano()})
	client.mu.Unlock()

	_, err := client.SendProtoMany(context.Background(), []proto.Message{&testpb.Reply{Content: "x"}})
	require.Error(t, err)
}

func TestClient_SendProtoManyNoReply_WriteError(t *testing.T) {
	client := NewClient("dummy:0")
	c1, c2 := net.Pipe()
	_ = c2.Close() // Only c1 is needed.
	_ = c1.Close()
	client.mu.Lock()
	client.idle = append(client.idle, idleConn{conn: c1, since: time.Now().UnixNano()})
	client.mu.Unlock()

	err := client.SendProtoManyNoReply(context.Background(), []proto.Message{&testpb.Reply{Content: "x"}})
	require.Error(t, err)
}

func TestClient_SendProto_UnmarshalError(t *testing.T) {
	// Server that sends back invalid proto frame data.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				// Read the request frame.
				_, _ = readProtoFrame(c, nil) // error expected when client disconnects
				// Send back a valid-length frame with garbage proto content.
				// Frame: totalLen=16, nameLen=4, name="test", payload=garbage
				frame := make([]byte, 16)
				frame[0], frame[1], frame[2], frame[3] = 0, 0, 0, 16 // totalLen=16
				frame[4], frame[5], frame[6], frame[7] = 0, 0, 0, 4  // nameLen=4
				copy(frame[8:12], "test")
				frame[12], frame[13], frame[14], frame[15] = 0xFF, 0xFF, 0xFF, 0xFF
				_, _ = c.Write(frame) // write error ignored — client may have disconnected
			}(conn)
		}
	}()
	pause.For(50 * time.Millisecond)

	client := NewClient(listener.Addr().String())
	defer func() { _ = client.Close() }()

	_, err = client.SendProto(context.Background(), &testpb.Reply{Content: "x"})
	require.Error(t, err)
}

// mockReader is a simple in-memory reader for testing readProtoFrame.
type mockReader struct {
	data []byte
	pos  int
}

func (m *mockReader) Read(p []byte) (int, error) {
	if m.pos >= len(m.data) {
		return 0, io.EOF
	}
	n := copy(p, m.data[m.pos:])
	m.pos += n
	return n, nil
}

// failingWrapper is a ConnWrapper that always returns an error.
type failingWrapper struct{}

func (w *failingWrapper) Wrap(net.Conn) (net.Conn, error) {
	return nil, errors.New("wrapper failed")
}
