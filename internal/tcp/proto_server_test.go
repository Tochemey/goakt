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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestNewProtoServer(t *testing.T) {
	t.Run("valid address with defaults", func(t *testing.T) {
		ps, err := NewProtoServer("127.0.0.1:0")
		require.NoError(t, err)
		require.NotNil(t, ps)
		require.NotNil(t, ps.server)
		require.NotNil(t, ps.handlers)
		require.NotNil(t, ps.serializer)
		require.NotNil(t, ps.framePool)
		require.Nil(t, ps.fallback)
		require.Equal(t, time.Duration(0), ps.idleTimeout)
	})

	t.Run("invalid address", func(t *testing.T) {
		_, err := NewProtoServer("invalid:::address")
		require.Error(t, err)
	})

	t.Run("with handler", func(t *testing.T) {
		h := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
			return nil, nil
		}
		ps, err := NewProtoServer("127.0.0.1:0",
			WithProtoHandler("testpb.Reply", h),
		)
		require.NoError(t, err)
		require.Len(t, ps.handlers, 1)
	})

	t.Run("with multiple handlers", func(t *testing.T) {
		h := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
			return nil, nil
		}
		ps, err := NewProtoServer("127.0.0.1:0",
			WithProtoHandler("testpb.Reply", h),
			WithProtoHandler("testpb.TestSend", h),
			WithProtoHandler("testpb.TestPing", h),
		)
		require.NoError(t, err)
		require.Len(t, ps.handlers, 3)
	})
}

func TestProtoServerOptions(t *testing.T) {
	t.Run("WithProtoIdleTimeout", func(t *testing.T) {
		ps, err := NewProtoServer("127.0.0.1:0",
			WithProtoServerIdleTimeout(10*time.Second),
		)
		require.NoError(t, err)
		require.Equal(t, 10*time.Second, ps.idleTimeout)
	})

	t.Run("WithProtoFallbackHandler", func(t *testing.T) {
		h := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
			return nil, nil
		}
		ps, err := NewProtoServer("127.0.0.1:0",
			WithFallbackProtoHandler(h),
		)
		require.NoError(t, err)
		require.NotNil(t, ps.fallback)
	})

	t.Run("WithProtoLoops", func(t *testing.T) {
		ps, err := NewProtoServer("127.0.0.1:0",
			WithProtoServerLoops(4),
		)
		require.NoError(t, err)
		require.Equal(t, 4, ps.server.Loops())
	})

	// nolint
	t.Run("WithProtoServerContext", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), "someKey", "someValue")
		ps, err := NewProtoServer("127.0.0.1:0",
			WithProtoServerContext(ctx),
		)
		require.NoError(t, err)
		require.Equal(t, ctx, ps.server.Context())
	})

	t.Run("WithProtoBallast", func(t *testing.T) {
		ps, err := NewProtoServer("127.0.0.1:0",
			WithProtoServerBallast(10),
		)
		require.NoError(t, err)
		require.Len(t, ps.server.ballast, 10*1024*1024)
	})

	t.Run("WithProtoTLSConfig", func(t *testing.T) {
		tlsCfg := &tls.Config{} //nolint:gosec
		ps, err := NewProtoServer("127.0.0.1:0",
			WithProtoServerTLSConfig(tlsCfg),
		)
		require.NoError(t, err)
		require.Equal(t, tlsCfg, ps.server.TLSConfig())
	})

	t.Run("WithProtoListenConfig", func(t *testing.T) {
		custom := &ListenConfig{SocketReusePort: false, SocketFastOpen: true}
		ps, err := NewProtoServer("127.0.0.1:0",
			WithProtoServerListenConfig(custom),
		)
		require.NoError(t, err)
		require.Equal(t, custom, ps.server.ListenConfig())
	})

	t.Run("WithProtoAllowThreadLocking", func(t *testing.T) {
		ps, err := NewProtoServer("127.0.0.1:0",
			WithProtoServerAllowThreadLocking(true),
		)
		require.NoError(t, err)
		require.True(t, ps.server.allowThreadLock)
	})

	t.Run("WithProtoConnWrapper", func(t *testing.T) {
		w := &testWrapper{}
		ps, err := NewProtoServer("127.0.0.1:0",
			WithProtoServerConnWrapper(w),
		)
		require.NoError(t, err)
		require.Len(t, ps.server.connWrappers, 1)
	})

	t.Run("WithProtoMaxAcceptConnections", func(t *testing.T) {
		ps, err := NewProtoServer("127.0.0.1:0",
			WithProtoServerMaxAcceptConnections(100),
		)
		require.NoError(t, err)
		require.Equal(t, int32(100), ps.server.maxAcceptConns.Load())
	})

	t.Run("WithProtoConnectionCreator", func(t *testing.T) {
		ps, err := NewProtoServer("127.0.0.1:0",
			WithProtoServerConnectionCreator(func() Connection { return &TCPConn{} }),
		)
		require.NoError(t, err)
		require.NotNil(t, ps)
	})
}

func TestProtoServer_RequestResponse(t *testing.T) {
	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", echoHandler),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	t.Run("single request-response", func(t *testing.T) {
		req := &testpb.Reply{Content: "hello proto server"}
		resp, err := client.SendProto(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		reply, ok := resp.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, "hello proto server", reply.Content)
	})

	t.Run("multiple sequential requests on same connection", func(t *testing.T) {
		for i := range 5 {
			req := &testpb.Reply{Content: time.Now().String()}
			resp, err := client.SendProto(context.Background(), req)
			require.NoError(t, err, "request %d failed", i)
			require.NotNil(t, resp)

			reply, ok := resp.(*testpb.Reply)
			require.True(t, ok)
			require.Equal(t, req.Content, reply.Content)
		}
	})

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestProtoServer_BatchRequestResponse(t *testing.T) {
	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", echoHandler),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	reqs := []proto.Message{
		&testpb.Reply{Content: "msg1"},
		&testpb.Reply{Content: "msg2"},
		&testpb.Reply{Content: "msg3"},
	}

	resps, err := client.SendProtoMany(context.Background(), reqs)
	require.NoError(t, err)
	require.Len(t, resps, 3)

	for i, resp := range resps {
		reply, ok := resp.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, reqs[i].(*testpb.Reply).Content, reply.Content)
	}

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestProtoServer_FireAndForget(t *testing.T) {
	var received atomic.Int32

	sinkHandler := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
		received.Add(1)
		return nil, nil // fire-and-forget: no response
	}

	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", sinkHandler),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	err = client.SendProtoNoReply(context.Background(), &testpb.Reply{Content: "fire"})
	require.NoError(t, err)

	pause.For(100 * time.Millisecond)
	require.Equal(t, int32(1), received.Load())

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestProtoServer_FireAndForgetBatch(t *testing.T) {
	var received atomic.Int32

	sinkHandler := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
		received.Add(1)
		return nil, nil
	}

	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", sinkHandler),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	reqs := []proto.Message{
		&testpb.Reply{Content: "a"},
		&testpb.Reply{Content: "b"},
		&testpb.Reply{Content: "c"},
	}

	err = client.SendProtoManyNoReply(context.Background(), reqs)
	require.NoError(t, err)

	pause.For(200 * time.Millisecond)
	require.Equal(t, int32(3), received.Load())

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestProtoServer_MultipleMessageTypes(t *testing.T) {
	replyHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		r := req.(*testpb.Reply)
		return &testpb.Reply{Content: "reply:" + r.Content}, nil
	}

	pingHandler := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
		return &testpb.TestPong{}, nil
	}

	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", replyHandler),
		WithProtoHandler("testpb.TestPing", pingHandler),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	// Send a Reply message.
	resp, err := client.SendProto(context.Background(), &testpb.Reply{Content: "hello"})
	require.NoError(t, err)
	reply, ok := resp.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "reply:hello", reply.Content)

	// Send a TestPing message.
	resp, err = client.SendProto(context.Background(), &testpb.TestPing{})
	require.NoError(t, err)
	_, ok = resp.(*testpb.TestPong)
	require.True(t, ok)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestProtoServer_FallbackHandler(t *testing.T) {
	var fallbackCalled atomic.Int32

	fallback := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		fallbackCalled.Add(1)
		return req, nil // echo back
	}

	ps, err := NewProtoServer("127.0.0.1:0",
		WithFallbackProtoHandler(fallback),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	// No handler registered for Reply — fallback should be invoked.
	resp, err := client.SendProto(context.Background(), &testpb.Reply{Content: "fallback"})
	require.NoError(t, err)
	require.NotNil(t, resp)

	reply, ok := resp.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "fallback", reply.Content)

	require.Equal(t, int32(1), fallbackCalled.Load())

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestProtoServer_UnregisteredMessageSkipped(t *testing.T) {
	// No handlers registered and no fallback — unregistered messages are skipped.
	ps, err := NewProtoServer("127.0.0.1:0")
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	// Fire-and-forget with no handler — should not hang or error.
	err = client.SendProtoNoReply(context.Background(), &testpb.Reply{Content: "ignored"})
	require.NoError(t, err)

	pause.For(100 * time.Millisecond)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestProtoServer_HandlerError(t *testing.T) {
	errHandler := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
		return nil, errors.New("handler failed")
	}

	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", errHandler),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	// The handler returns an error — the server closes the connection.
	// The client's readProtoFrame will get an EOF.
	_, err = client.SendProto(context.Background(), &testpb.Reply{Content: "fail"})
	require.Error(t, err)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestProtoServer_ConcurrentClients(t *testing.T) {
	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", echoHandler),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	addr := ps.ListenAddr().String()

	const numClients = 20
	const numReqs = 10

	var wg sync.WaitGroup
	var errCount atomic.Int32

	for range numClients {
		wg.Go(func() {
			client := NewClient(addr)
			defer func() { _ = client.Close() }()

			for j := range numReqs {
				req := &testpb.Reply{Content: time.Now().String()}
				resp, err := client.SendProto(context.Background(), req)
				if err != nil {
					errCount.Add(1)
					return
				}

				reply, ok := resp.(*testpb.Reply)
				if !ok || reply.Content != req.Content {
					errCount.Add(1)
					return
				}
				_ = j
			}
		})
	}

	wg.Wait()
	require.Equal(t, int32(0), errCount.Load(), "all concurrent requests should succeed")

	require.NoError(t, ps.Shutdown(2*time.Second))
	<-done
}

func TestProtoServer_IdleTimeout(t *testing.T) {
	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", echoHandler),
		WithProtoServerIdleTimeout(200*time.Millisecond),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	// First request should work.
	resp, err := client.SendProto(context.Background(), &testpb.Reply{Content: "alive"})
	require.NoError(t, err)
	reply, ok := resp.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "alive", reply.Content)

	// Wait for the idle timeout to expire.
	pause.For(400 * time.Millisecond)

	// The server should have closed the connection. The client's pooled
	// connection is stale — next request may fail or dial a new connection.
	// This is expected behaviour: the idle timeout reclaimed the connection.
	// We just verify no panic or hang occurs.
	_, _ = client.SendProto(context.Background(), &testpb.Reply{Content: "after timeout"}) //nolint:errcheck

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestProtoServer_GracefulShutdown(t *testing.T) {
	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", echoHandler),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	require.NoError(t, ps.Shutdown(2*time.Second))

	err = <-done
	require.NoError(t, err)

	// Double shutdown should be a no-op.
	require.NoError(t, ps.Shutdown(time.Second))
}

func TestProtoServer_Halt(t *testing.T) {
	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
			return req, nil
		}),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	require.NoError(t, ps.Halt())
	<-done
}

func TestProtoServer_ListenAddr(t *testing.T) {
	t.Run("before listen", func(t *testing.T) {
		ps, err := NewProtoServer("127.0.0.1:0")
		require.NoError(t, err)
		require.Nil(t, ps.ListenAddr())
	})

	t.Run("after listen", func(t *testing.T) {
		ps, err := NewProtoServer("127.0.0.1:0")
		require.NoError(t, err)

		require.NoError(t, ps.Listen())
		addr := ps.ListenAddr()
		require.NotNil(t, addr)
		require.Greater(t, addr.Port, 0)

		require.NoError(t, ps.Halt())
	})
}

func TestProtoServer_ActiveConnections(t *testing.T) {
	blockCh := make(chan struct{})

	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
			<-blockCh
			return req, nil
		}),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	// Start a request that blocks in the handler.
	go func() {
		_, _ = client.SendProto(context.Background(), &testpb.Reply{Content: "block"}) //nolint:errcheck
	}()

	pause.For(100 * time.Millisecond)
	require.Equal(t, int32(1), ps.ActiveConnections())
	require.Equal(t, int32(1), ps.AcceptedConnections())

	close(blockCh)
	pause.For(100 * time.Millisecond)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestProtoServer_WithTLS(t *testing.T) {
	cert, key := generateTestCert(t)
	tlsCert, err := tls.X509KeyPair(cert, key)
	require.NoError(t, err)

	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoServerTLSConfig(&tls.Config{Certificates: []tls.Certificate{tlsCert}}), //nolint:gosec
		WithProtoHandler("testpb.Reply", echoHandler),
	)
	require.NoError(t, err)

	require.NoError(t, ps.ListenTLS())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(
		ps.ListenAddr().String(),
		WithTLS(&tls.Config{InsecureSkipVerify: true}), //nolint:gosec
	)
	defer func() { _ = client.Close() }()

	resp, err := client.SendProto(context.Background(), &testpb.Reply{Content: "tls"})
	require.NoError(t, err)
	reply, ok := resp.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "tls", reply.Content)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestProtoServer_HandlerOverwrite(t *testing.T) {
	h1 := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
		return &testpb.Reply{Content: "h1"}, nil
	}
	h2 := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
		return &testpb.Reply{Content: "h2"}, nil
	}

	// Register h1 first, then overwrite with h2.
	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", h1),
		WithProtoHandler("testpb.Reply", h2),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	resp, err := client.SendProto(context.Background(), &testpb.Reply{Content: "x"})
	require.NoError(t, err)

	reply, ok := resp.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "h2", reply.Content, "second handler should have overwritten the first")

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestProtoServer_ListenTLS_NoConfig(t *testing.T) {
	ps, err := NewProtoServer("127.0.0.1:0")
	require.NoError(t, err)

	err = ps.ListenTLS()
	require.ErrorIs(t, err, ErrNoTLSConfig)
}

// ---------------------------------------------------------------------------
// Frame pool unit tests
// ---------------------------------------------------------------------------

func TestFramePool_GetPut(t *testing.T) {
	fp := newFramePool()

	t.Run("small buffer", func(t *testing.T) {
		buf := fp.Get(100)
		require.Len(t, buf, 100)
		require.Equal(t, 256, cap(buf), "should be rounded up to 256 bucket")
		fp.Put(buf)
	})

	t.Run("exact bucket boundary", func(t *testing.T) {
		buf := fp.Get(256)
		require.Len(t, buf, 256)
		require.Equal(t, 256, cap(buf))
		fp.Put(buf)
	})

	t.Run("just over bucket boundary", func(t *testing.T) {
		buf := fp.Get(257)
		require.Len(t, buf, 257)
		require.Equal(t, 512, cap(buf), "should be rounded up to 512 bucket")
		fp.Put(buf)
	})

	t.Run("large buffer within max bucket", func(t *testing.T) {
		buf := fp.Get(1 << 22) // 4 MiB
		require.Len(t, buf, 1<<22)
		fp.Put(buf)
	})

	t.Run("oversized buffer", func(t *testing.T) {
		buf := fp.Get((1 << 22) + 1) // 4 MiB + 1
		require.Len(t, buf, (1<<22)+1)
		// Put should not panic on oversized buffers.
		fp.Put(buf)
	})
}

func TestBucketIndex(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want int
	}{
		{"zero", 0, 0},
		{"one", 1, 0},
		{"min bucket", 256, 0},
		{"min+1", 257, 1},
		{"512", 512, 1},
		{"513", 513, 2},
		{"1024", 1024, 2},
		{"4MiB", 1 << 22, numBuckets - 1},
		{"over max", (1 << 22) + 1, numBuckets},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, bucketIndex(tt.n))
		})
	}
}

func TestBucketIndexExact(t *testing.T) {
	tests := []struct {
		name string
		c    int
		want int
	}{
		{"zero", 0, -1},
		{"non-power", 300, -1},
		{"256", 256, 0},
		{"512", 512, 1},
		{"1024", 1024, 2},
		{"4MiB", 1 << 22, numBuckets - 1},
		{"too small", 128, -1},
		{"too large", 1 << 23, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, bucketIndexExact(tt.c))
		})
	}
}
