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
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestNewRemotingServer(t *testing.T) {
	t.Run("valid address with defaults", func(t *testing.T) {
		ps, err := NewRemotingServer("127.0.0.1:0")
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
		_, err := NewRemotingServer("invalid:::address")
		require.Error(t, err)
	})

	t.Run("with handler", func(t *testing.T) {
		h := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
			return nil, nil
		}
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithProtoHandler("testpb.Reply", h),
		)
		require.NoError(t, err)
		require.Len(t, ps.handlers, 1)
	})

	t.Run("with multiple handlers", func(t *testing.T) {
		h := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
			return nil, nil
		}
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithProtoHandler("testpb.Reply", h),
			WithProtoHandler("testpb.TestSend", h),
			WithProtoHandler("testpb.TestPing", h),
		)
		require.NoError(t, err)
		require.Len(t, ps.handlers, 3)
	})
}

func TestRemotingServerOptions(t *testing.T) {
	t.Run("WithProtoIdleTimeout", func(t *testing.T) {
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithRemotingServerIdleTimeout(10*time.Second),
		)
		require.NoError(t, err)
		require.Equal(t, 10*time.Second, ps.idleTimeout)
	})

	t.Run("WithProtoFallbackHandler", func(t *testing.T) {
		h := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
			return nil, nil
		}
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithFallbackProtoHandler(h),
		)
		require.NoError(t, err)
		require.NotNil(t, ps.fallback)
	})

	t.Run("WithProtoLoops", func(t *testing.T) {
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithRemotingServerLoops(4),
		)
		require.NoError(t, err)
		require.Equal(t, 4, ps.server.Loops())
	})

	// nolint
	t.Run("WithRemotingServerContext", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), "someKey", "someValue")
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithRemotingServerContext(ctx),
		)
		require.NoError(t, err)
		require.Equal(t, ctx, ps.server.Context())
	})

	t.Run("WithProtoBallast", func(t *testing.T) {
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithRemotingServerBallast(10),
		)
		require.NoError(t, err)
		require.Len(t, ps.server.ballast, 10*1024*1024)
	})

	t.Run("WithProtoTLSConfig", func(t *testing.T) {
		tlsCfg := &tls.Config{} //nolint:gosec
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithRemotingServerTLSConfig(tlsCfg),
		)
		require.NoError(t, err)
		require.Equal(t, tlsCfg, ps.server.TLSConfig())
	})

	t.Run("WithProtoListenConfig", func(t *testing.T) {
		custom := &ListenConfig{SocketReusePort: false, SocketFastOpen: true}
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithRemotingServerListenConfig(custom),
		)
		require.NoError(t, err)
		require.Equal(t, custom, ps.server.ListenConfig())
	})

	t.Run("WithProtoAllowThreadLocking", func(t *testing.T) {
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithRemotingServerAllowThreadLocking(true),
		)
		require.NoError(t, err)
		require.True(t, ps.server.allowThreadLock)
	})

	t.Run("WithProtoConnWrapper", func(t *testing.T) {
		w := &testWrapper{}
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithRemotingServerConnWrapper(w),
		)
		require.NoError(t, err)
		require.Len(t, ps.server.connWrappers, 1)
	})

	t.Run("WithProtoMaxAcceptConnections", func(t *testing.T) {
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithRemotingServerMaxAcceptConnections(100),
		)
		require.NoError(t, err)
		require.Equal(t, int32(100), ps.server.maxAcceptConns.Load())
	})

	t.Run("WithProtoConnectionCreator", func(t *testing.T) {
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithRemotingServerConnectionCreator(func() Connection { return &TCPConn{} }),
		)
		require.NoError(t, err)
		require.NotNil(t, ps)
	})

	t.Run("WithRemotingServerSenderResolver", func(t *testing.T) {
		resolve := func(path string) any { return path }
		ps, err := NewRemotingServer("127.0.0.1:0",
			WithRemotingServerSenderResolver(resolve),
		)
		require.NoError(t, err)
		require.NotNil(t, ps.senderResolver)
		assert.Equal(t, "a", ps.senderResolver("a"))
	})
}

func TestRemotingServer_RequestResponse(t *testing.T) {
	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewRemotingServer("127.0.0.1:0",
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
		req := testpb.Reply_builder{Content: "hello proto server"}.Build()
		resp, err := client.SendProto(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		reply, ok := resp.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, "hello proto server", reply.GetContent())
	})

	t.Run("multiple sequential requests on same connection", func(t *testing.T) {
		for i := range 5 {
			req := testpb.Reply_builder{Content: time.Now().String()}.Build()
			resp, err := client.SendProto(context.Background(), req)
			require.NoError(t, err, "request %d failed", i)
			require.NotNil(t, resp)

			reply, ok := resp.(*testpb.Reply)
			require.True(t, ok)
			require.Equal(t, req.GetContent(), reply.GetContent())
		}
	})

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotingServer_BatchRequestResponse(t *testing.T) {
	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewRemotingServer("127.0.0.1:0",
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
		testpb.Reply_builder{Content: "msg1"}.Build(),
		testpb.Reply_builder{Content: "msg2"}.Build(),
		testpb.Reply_builder{Content: "msg3"}.Build(),
	}

	resps, err := client.SendBatchProto(context.Background(), reqs)
	require.NoError(t, err)
	require.Len(t, resps, 3)

	for i, resp := range resps {
		reply, ok := resp.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, reqs[i].(*testpb.Reply).GetContent(), reply.GetContent())
	}

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotingServer_FireAndForget(t *testing.T) {
	var received atomic.Int32

	sinkHandler := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
		received.Add(1)
		return nil, nil // fire-and-forget: no response
	}

	ps, err := NewRemotingServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", sinkHandler),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	err = client.SendProtoNoReply(context.Background(), testpb.Reply_builder{Content: "fire"}.Build())
	require.NoError(t, err)

	pause.For(100 * time.Millisecond)
	require.Equal(t, int32(1), received.Load())

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotingServer_FireAndForgetBatch(t *testing.T) {
	var received atomic.Int32

	sinkHandler := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
		received.Add(1)
		return nil, nil
	}

	ps, err := NewRemotingServer("127.0.0.1:0",
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
		testpb.Reply_builder{Content: "a"}.Build(),
		testpb.Reply_builder{Content: "b"}.Build(),
		testpb.Reply_builder{Content: "c"}.Build(),
	}

	err = client.SendProtoManyNoReply(context.Background(), reqs)
	require.NoError(t, err)

	pause.For(200 * time.Millisecond)
	require.Equal(t, int32(3), received.Load())

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotingServer_MultipleMessageTypes(t *testing.T) {
	replyHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		r := req.(*testpb.Reply)
		return testpb.Reply_builder{Content: "reply:" + r.GetContent()}.Build(), nil
	}

	pingHandler := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
		return &testpb.TestPong{}, nil
	}

	ps, err := NewRemotingServer("127.0.0.1:0",
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
	resp, err := client.SendProto(context.Background(), testpb.Reply_builder{Content: "hello"}.Build())
	require.NoError(t, err)
	reply, ok := resp.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "reply:hello", reply.GetContent())

	// Send a TestPing message.
	resp, err = client.SendProto(context.Background(), &testpb.TestPing{})
	require.NoError(t, err)
	_, ok = resp.(*testpb.TestPong)
	require.True(t, ok)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotingServer_FallbackHandler(t *testing.T) {
	var fallbackCalled atomic.Int32

	fallback := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		fallbackCalled.Add(1)
		return req, nil // echo back
	}

	ps, err := NewRemotingServer("127.0.0.1:0",
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
	resp, err := client.SendProto(context.Background(), testpb.Reply_builder{Content: "fallback"}.Build())
	require.NoError(t, err)
	require.NotNil(t, resp)

	reply, ok := resp.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "fallback", reply.GetContent())

	require.Equal(t, int32(1), fallbackCalled.Load())

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotingServer_UnregisteredMessageClosesConn(t *testing.T) {
	// No handlers registered and no fallback — an unroutable frame must close
	// the connection so a request/response peer observes an immediate EOF
	// instead of blocking forever on a response that will never arrive.
	ps, err := NewRemotingServer("127.0.0.1:0")
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	t.Run("request/response caller fails fast with EOF", func(t *testing.T) {
		_, err := client.SendProto(context.Background(), testpb.Reply_builder{Content: "ignored"}.Build())
		require.Error(t, err)
		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("fire-and-forget caller is unaffected", func(t *testing.T) {
		// The write completes before the server processes the frame; the
		// connection closing afterwards does not fail the no-reply call.
		err := client.SendProtoNoReply(context.Background(), testpb.Reply_builder{Content: "ignored"}.Build())
		require.NoError(t, err)
	})

	pause.For(100 * time.Millisecond)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotingServer_HandlerError(t *testing.T) {
	errHandler := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
		return nil, errors.New("handler failed")
	}

	ps, err := NewRemotingServer("127.0.0.1:0",
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
	_, err = client.SendProto(context.Background(), testpb.Reply_builder{Content: "fail"}.Build())
	require.Error(t, err)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotingServer_ConcurrentClients(t *testing.T) {
	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewRemotingServer("127.0.0.1:0",
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
				req := testpb.Reply_builder{Content: time.Now().String()}.Build()
				resp, err := client.SendProto(context.Background(), req)
				if err != nil {
					errCount.Add(1)
					return
				}

				reply, ok := resp.(*testpb.Reply)
				if !ok || reply.GetContent() != req.GetContent() {
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

func TestRemotingServer_IdleTimeout(t *testing.T) {
	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewRemotingServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", echoHandler),
		WithRemotingServerIdleTimeout(200*time.Millisecond),
	)
	require.NoError(t, err)

	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	client := NewClient(ps.ListenAddr().String())
	defer func() { _ = client.Close() }()

	// First request should work.
	resp, err := client.SendProto(context.Background(), testpb.Reply_builder{Content: "alive"}.Build())
	require.NoError(t, err)
	reply, ok := resp.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "alive", reply.GetContent())

	// Wait for the idle timeout to expire.
	pause.For(400 * time.Millisecond)

	// The server should have closed the connection. The client's pooled
	// connection is stale — next request may fail or dial a new connection.
	// This is expected behaviour: the idle timeout reclaimed the connection.
	// We just verify no panic or hang occurs.
	_, _ = client.SendProto(context.Background(), testpb.Reply_builder{Content: "after timeout"}.Build()) //nolint:errcheck

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotingServer_GracefulShutdown(t *testing.T) {
	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewRemotingServer("127.0.0.1:0",
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

func TestRemotingServer_Halt(t *testing.T) {
	ps, err := NewRemotingServer("127.0.0.1:0",
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

func TestRemotingServer_ListenAddr(t *testing.T) {
	t.Run("before listen", func(t *testing.T) {
		ps, err := NewRemotingServer("127.0.0.1:0")
		require.NoError(t, err)
		require.Nil(t, ps.ListenAddr())
	})

	t.Run("after listen", func(t *testing.T) {
		ps, err := NewRemotingServer("127.0.0.1:0")
		require.NoError(t, err)

		require.NoError(t, ps.Listen())
		addr := ps.ListenAddr()
		require.NotNil(t, addr)
		require.Greater(t, addr.Port, 0)

		require.NoError(t, ps.Halt())
	})
}

func TestRemotingServer_ActiveConnections(t *testing.T) {
	blockCh := make(chan struct{})

	ps, err := NewRemotingServer("127.0.0.1:0",
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
		_, _ = client.SendProto(context.Background(), testpb.Reply_builder{Content: "block"}.Build()) //nolint:errcheck
	}()

	pause.For(100 * time.Millisecond)
	require.Equal(t, int32(1), ps.ActiveConnections())
	require.Equal(t, int32(1), ps.AcceptedConnections())

	close(blockCh)
	pause.For(100 * time.Millisecond)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotingServer_WithTLS(t *testing.T) {
	cert, key := generateTestCert(t)
	tlsCert, err := tls.X509KeyPair(cert, key)
	require.NoError(t, err)

	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewRemotingServer("127.0.0.1:0",
		WithRemotingServerTLSConfig(&tls.Config{Certificates: []tls.Certificate{tlsCert}}), //nolint:gosec
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

	resp, err := client.SendProto(context.Background(), testpb.Reply_builder{Content: "tls"}.Build())
	require.NoError(t, err)
	reply, ok := resp.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "tls", reply.GetContent())

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotingServer_HandlerOverwrite(t *testing.T) {
	h1 := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
		return testpb.Reply_builder{Content: "h1"}.Build(), nil
	}
	h2 := func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
		return testpb.Reply_builder{Content: "h2"}.Build(), nil
	}

	// Register h1 first, then overwrite with h2.
	ps, err := NewRemotingServer("127.0.0.1:0",
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

	resp, err := client.SendProto(context.Background(), testpb.Reply_builder{Content: "x"}.Build())
	require.NoError(t, err)

	reply, ok := resp.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "h2", reply.GetContent(), "second handler should have overwritten the first")

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotingServer_ListenTLS_NoConfig(t *testing.T) {
	ps, err := NewRemotingServer("127.0.0.1:0")
	require.NoError(t, err)

	err = ps.ListenTLS()
	require.ErrorIs(t, err, ErrNoTLSConfig)
}

// ---------------------------------------------------------------------------
// Frame pool unit tests
// ---------------------------------------------------------------------------

func TestRemotingServer_MetadataExtraction(t *testing.T) {
	t.Run("extract metadata from request", func(t *testing.T) {
		var receivedHeaders map[string]string
		var receivedDeadline time.Time
		var hasDeadline bool

		handler := func(ctx context.Context, _ Connection, req proto.Message) (proto.Message, error) {
			// Extract metadata from the context.
			md, ok := FromContext(ctx)
			require.True(t, ok, "metadata should be present in context")
			require.NotNil(t, md, "metadata should not be nil")

			// Capture headers for verification.
			receivedHeaders = make(map[string]string)
			md.IterateHeaders(func(key, value string) {
				receivedHeaders[key] = value
			})

			// Capture deadline if present.
			receivedDeadline, hasDeadline = md.GetDeadline()

			return req, nil
		}

		ps, err := NewRemotingServer("127.0.0.1:0",
			WithProtoHandler("testpb.Reply", handler),
		)
		require.NoError(t, err)
		require.NoError(t, ps.Listen())

		done := make(chan error, 1)
		go func() { done <- ps.Serve() }()
		pause.For(100 * time.Millisecond)

		client := NewClient(ps.ListenAddr().String())
		defer func() { _ = client.Close() }()

		// Create context with metadata.
		md := NewMetadata()
		md.Set("trace-id", "abc123")
		md.Set("span-id", "xyz789")
		md.Set("auth-token", "secret")
		expectedDeadline := time.Now().Add(5 * time.Second).Truncate(time.Microsecond)
		md.SetDeadline(expectedDeadline)

		ctx := ContextWithMetadata(context.Background(), md)

		// Send request with metadata.
		req := testpb.Reply_builder{Content: "with metadata"}.Build()
		resp, _, err := client.SendProtoWithMetadata(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Verify the handler received all headers.
		require.Equal(t, "abc123", receivedHeaders["trace-id"])
		require.Equal(t, "xyz789", receivedHeaders["span-id"])
		require.Equal(t, "secret", receivedHeaders["auth-token"])

		// Verify the handler received the deadline (rebased on the receiver's
		// clock, so equal only within tolerance).
		require.True(t, hasDeadline, "deadline should be present")
		require.WithinDuration(t, expectedDeadline, receivedDeadline, time.Second, "deadline should match")

		require.NoError(t, ps.Shutdown(time.Second))
		<-done
	})

	t.Run("backward compatibility - no metadata", func(t *testing.T) {
		var metadataPresent bool

		handler := func(ctx context.Context, _ Connection, req proto.Message) (proto.Message, error) {
			// Check if metadata is present.
			_, ok := FromContext(ctx)
			metadataPresent = ok
			return req, nil
		}

		ps, err := NewRemotingServer("127.0.0.1:0",
			WithProtoHandler("testpb.Reply", handler),
		)
		require.NoError(t, err)
		require.NoError(t, ps.Listen())

		done := make(chan error, 1)
		go func() { done <- ps.Serve() }()
		pause.For(100 * time.Millisecond)

		client := NewClient(ps.ListenAddr().String())
		defer func() { _ = client.Close() }()

		// Send request WITHOUT metadata (legacy format).
		req := testpb.Reply_builder{Content: "no metadata"}.Build()
		resp, err := client.SendProto(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		reply, ok := resp.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, "no metadata", reply.GetContent())

		// Verify no metadata was present (backward compatibility).
		require.False(t, metadataPresent, "metadata should not be present for legacy frames")

		require.NoError(t, ps.Shutdown(time.Second))
		<-done
	})

	t.Run("empty metadata", func(t *testing.T) {
		var receivedHeaders map[string]string

		handler := func(ctx context.Context, _ Connection, req proto.Message) (proto.Message, error) {
			md, ok := FromContext(ctx)
			require.True(t, ok)
			require.NotNil(t, md)

			receivedHeaders = make(map[string]string)
			md.IterateHeaders(func(key, value string) {
				receivedHeaders[key] = value
			})

			return req, nil
		}

		ps, err := NewRemotingServer("127.0.0.1:0",
			WithProtoHandler("testpb.Reply", handler),
		)
		require.NoError(t, err)
		require.NoError(t, ps.Listen())

		done := make(chan error, 1)
		go func() { done <- ps.Serve() }()
		pause.For(100 * time.Millisecond)

		client := NewClient(ps.ListenAddr().String())
		defer func() { _ = client.Close() }()

		// Create context with empty metadata (no headers, no deadline).
		md := NewMetadata()
		ctx := ContextWithMetadata(context.Background(), md)

		req := testpb.Reply_builder{Content: "empty metadata"}.Build()
		resp, _, err := client.SendProtoWithMetadata(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Verify handler received empty headers.
		require.Empty(t, receivedHeaders)

		require.NoError(t, ps.Shutdown(time.Second))
		<-done
	})

	t.Run("metadata deadline propagation", func(t *testing.T) {
		var ctxHadDeadline bool
		var mdDeadline time.Time
		var mdHadDeadline bool
		var derivedDeadline time.Time
		var derivedHadDeadline bool

		handler := func(ctx context.Context, _ Connection, req proto.Message) (proto.Message, error) {
			// The transport must not impose a deadline context itself (that
			// would leak a timer per message); the deadline travels in the
			// metadata and handlers opt in via DeadlineContext.
			_, ctxHadDeadline = ctx.Deadline()

			md, ok := FromContext(ctx)
			if ok && md != nil {
				mdDeadline, mdHadDeadline = md.GetDeadline()

				bounded, cancel := md.DeadlineContext(ctx)
				defer cancel()
				derivedDeadline, derivedHadDeadline = bounded.Deadline()
			}
			return req, nil
		}

		ps, err := NewRemotingServer("127.0.0.1:0",
			WithProtoHandler("testpb.Reply", handler),
		)
		require.NoError(t, err)
		require.NoError(t, ps.Listen())

		done := make(chan error, 1)
		go func() { done <- ps.Serve() }()
		pause.For(100 * time.Millisecond)

		client := NewClient(ps.ListenAddr().String())
		defer func() { _ = client.Close() }()

		// Create metadata with a deadline.
		md := NewMetadata()
		expectedDeadline := time.Now().Add(10 * time.Second).Truncate(time.Microsecond)
		md.SetDeadline(expectedDeadline)

		ctx := ContextWithMetadata(context.Background(), md)

		req := testpb.Reply_builder{Content: "deadline test"}.Build()
		resp, _, err := client.SendProtoWithMetadata(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// The transport context itself carries no deadline; the metadata does,
		// and DeadlineContext derives an enforced deadline from it.
		require.False(t, ctxHadDeadline, "transport context must not carry a deadline")
		require.True(t, mdHadDeadline, "metadata should carry the deadline")
		require.WithinDuration(t, expectedDeadline, mdDeadline, time.Second, "metadata deadline should match")
		require.True(t, derivedHadDeadline, "DeadlineContext should enforce the deadline")
		require.WithinDuration(t, expectedDeadline, derivedDeadline, time.Second, "derived deadline should match")

		require.NoError(t, ps.Shutdown(time.Second))
		<-done
	})

	t.Run("multiple headers extraction", func(t *testing.T) {
		var receivedHeaders map[string]string

		handler := func(ctx context.Context, _ Connection, req proto.Message) (proto.Message, error) {
			md, ok := FromContext(ctx)
			require.True(t, ok)

			receivedHeaders = make(map[string]string)
			md.IterateHeaders(func(key, value string) {
				receivedHeaders[key] = value
			})

			return req, nil
		}

		ps, err := NewRemotingServer("127.0.0.1:0",
			WithProtoHandler("testpb.Reply", handler),
		)
		require.NoError(t, err)
		require.NoError(t, ps.Listen())

		done := make(chan error, 1)
		go func() { done <- ps.Serve() }()
		pause.For(100 * time.Millisecond)

		client := NewClient(ps.ListenAddr().String())
		defer func() { _ = client.Close() }()

		// Create metadata with many headers.
		md := NewMetadata()
		expectedHeaders := map[string]string{
			"x-trace-id":      "trace-123",
			"x-span-id":       "span-456",
			"x-user-id":       "user-789",
			"x-request-id":    "req-abc",
			"x-correlation":   "corr-def",
			"x-auth-token":    "token-xyz",
			"x-custom-header": "custom-value",
		}
		for k, v := range expectedHeaders {
			md.Set(k, v)
		}

		ctx := ContextWithMetadata(context.Background(), md)

		req := testpb.Reply_builder{Content: "many headers"}.Build()
		resp, _, err := client.SendProtoWithMetadata(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Verify all headers were received.
		require.Equal(t, len(expectedHeaders), len(receivedHeaders))
		for k, expectedVal := range expectedHeaders {
			actualVal, ok := receivedHeaders[k]
			require.True(t, ok, "header %s should be present", k)
			require.Equal(t, expectedVal, actualVal, "header %s value should match", k)
		}

		require.NoError(t, ps.Shutdown(time.Second))
		<-done
	})

	t.Run("concurrent requests with different metadata", func(t *testing.T) {
		type capturedMD struct {
			traceID string
			spanID  string
		}
		var mu sync.Mutex
		captured := make(map[string]capturedMD)

		handler := func(ctx context.Context, _ Connection, req proto.Message) (proto.Message, error) {
			md, ok := FromContext(ctx)
			if !ok {
				return req, nil
			}

			traceID, _ := md.Get("trace-id")
			spanID, _ := md.Get("span-id")
			content := req.(*testpb.Reply).GetContent()

			mu.Lock()
			captured[content] = capturedMD{traceID: traceID, spanID: spanID}
			mu.Unlock()

			return req, nil
		}

		ps, err := NewRemotingServer("127.0.0.1:0",
			WithProtoHandler("testpb.Reply", handler),
		)
		require.NoError(t, err)
		require.NoError(t, ps.Listen())

		done := make(chan error, 1)
		go func() { done <- ps.Serve() }()
		pause.For(100 * time.Millisecond)

		client := NewClient(ps.ListenAddr().String())
		defer func() { _ = client.Close() }()

		// Send concurrent requests with different metadata.
		const numReqs = 50
		var wg sync.WaitGroup

		for i := range numReqs {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()

				md := NewMetadata()
				md.Set("trace-id", time.Now().String()+"-"+strconv.Itoa(idx))
				md.Set("span-id", time.Now().String()+"-span-"+strconv.Itoa(idx))

				ctx := ContextWithMetadata(context.Background(), md)
				content := time.Now().String() + "-" + strconv.Itoa(idx)
				req := testpb.Reply_builder{Content: content}.Build()

				_, _, err := client.SendProtoWithMetadata(ctx, req)
				require.NoError(t, err)
			}(i)
		}

		wg.Wait()

		// Verify each request's metadata was correctly extracted and isolated.
		mu.Lock()
		require.Equal(t, numReqs, len(captured), "all requests should have been captured")
		mu.Unlock()

		require.NoError(t, ps.Shutdown(time.Second))
		<-done
	})
}

func TestRemotingServer_MetadataBackwardCompatibility(t *testing.T) {
	t.Run("mixed metadata and non-metadata requests", func(t *testing.T) {
		var withMetadataCount atomic.Int32
		var withoutMetadataCount atomic.Int32

		handler := func(ctx context.Context, _ Connection, req proto.Message) (proto.Message, error) {
			if _, ok := FromContext(ctx); ok {
				withMetadataCount.Add(1)
			} else {
				withoutMetadataCount.Add(1)
			}
			return req, nil
		}

		ps, err := NewRemotingServer("127.0.0.1:0",
			WithProtoHandler("testpb.Reply", handler),
		)
		require.NoError(t, err)
		require.NoError(t, ps.Listen())

		done := make(chan error, 1)
		go func() { done <- ps.Serve() }()
		pause.For(100 * time.Millisecond)

		client := NewClient(ps.ListenAddr().String())
		defer func() { _ = client.Close() }()

		// Send some requests with metadata.
		for i := range 5 {
			md := NewMetadata()
			md.Set("request-id", strconv.Itoa(i))
			ctx := ContextWithMetadata(context.Background(), md)

			req := testpb.Reply_builder{Content: "with-md"}.Build()
			_, _, err := client.SendProtoWithMetadata(ctx, req)
			require.NoError(t, err)
		}

		// Send some requests without metadata (legacy client).
		for range 3 {
			req := testpb.Reply_builder{Content: "no-md"}.Build()
			_, err := client.SendProto(context.Background(), req)
			require.NoError(t, err)
		}

		pause.For(100 * time.Millisecond)

		// Verify counts.
		require.Equal(t, int32(5), withMetadataCount.Load())
		require.Equal(t, int32(3), withoutMetadataCount.Load())

		require.NoError(t, ps.Shutdown(time.Second))
		<-done
	})
}

func TestRemotingServerHandleDuplexConnHelloAndPing(t *testing.T) {
	ps, err := NewRemotingServer("127.0.0.1:0", WithRemotingServerLoops(1))
	require.NoError(t, err)
	require.NoError(t, ps.Listen())
	t.Cleanup(func() { _ = ps.Shutdown(time.Second) })

	go func() { _ = ps.Serve() }()
	pause.For(50 * time.Millisecond)

	transport := NewTCPTransport()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := transport.Dial(ctx, ps.ListenAddr().String(), LaneSpec{
		Role: internalpb.LaneRole_LANE_ROLE_CONTROL,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	result, err := performHello(conn, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
	require.NoError(t, err)
	require.NotNil(t, result.Effective)
	assert.Equal(t, CapabilityRevisionBaseline, result.Effective.GetRevision())

	wrapped, err := wrapCompression(conn.NetConn(), result.Effective.GetCompression())
	require.NoError(t, err)
	if tcp, ok := conn.(*tcpFramedConn); ok {
		tcp.ReplaceNetConn(wrapped)
	}

	require.NoError(t, conn.WriteFrames(Frame{
		Version:     ProtocolVersion,
		Type:        FrameTypePing,
		Lane:        LaneControl,
		Correlation: 42,
	}))

	pong, err := conn.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypePong, pong.Type)
	assert.Equal(t, uint64(42), pong.Correlation)
}

func TestRemotingServerHandleDuplexConnClosesOnUnsupportedFrame(t *testing.T) {
	ps, err := NewRemotingServer("127.0.0.1:0", WithRemotingServerLoops(1))
	require.NoError(t, err)
	require.NoError(t, ps.Listen())
	t.Cleanup(func() { _ = ps.Shutdown(time.Second) })

	go func() { _ = ps.Serve() }()
	pause.For(50 * time.Millisecond)

	transport := NewTCPTransport()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := transport.Dial(ctx, ps.ListenAddr().String(), LaneSpec{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = performHello(conn, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
	require.NoError(t, err)

	// Post-handshake HELLO is a known wire type but unsupported by the
	// remoting duplex handler (CREDIT/PING/etc. are consumed by the read loop).
	require.NoError(t, conn.WriteFrames(Frame{
		Version:     ProtocolVersion,
		Type:        FrameTypeHello,
		Lane:        LaneControl,
		Correlation: 1,
	}))

	frame, err := conn.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)
	assert.Equal(t, uint64(0), frame.Correlation)

	_, err = conn.ReadFrame()
	require.Error(t, err)
}

func TestRemotingServerHandleDuplexConnRejectsMismatchedLane(t *testing.T) {
	ps, err := NewRemotingServer("127.0.0.1:0", WithRemotingServerLoops(1))
	require.NoError(t, err)
	require.NoError(t, ps.Listen())
	t.Cleanup(func() { _ = ps.Shutdown(time.Second) })

	go func() { _ = ps.Serve() }()
	pause.For(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := NewTCPTransport().Dial(ctx, ps.ListenAddr().String(), LaneSpec{
		Role: internalpb.LaneRole_LANE_ROLE_CONTROL,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = performHello(conn, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
	require.NoError(t, err)

	require.NoError(t, conn.WriteFrames(Frame{
		Version: ProtocolVersion,
		Type:    FrameTypeData,
		Lane:    LaneOrdinary,
	}))

	frame, err := conn.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)
	assert.Equal(t, LaneControl, frame.Lane)
	assert.Equal(t, uint64(0), frame.Correlation)

	_, err = conn.ReadFrame()
	require.Error(t, err)
}

func TestRemotingServerHandleDuplexConnRejectsMismatchedLanePing(t *testing.T) {
	ps, err := NewRemotingServer("127.0.0.1:0", WithRemotingServerLoops(1))
	require.NoError(t, err)
	require.NoError(t, ps.Listen())
	t.Cleanup(func() { _ = ps.Shutdown(time.Second) })

	go func() { _ = ps.Serve() }()
	pause.For(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := NewTCPTransport().Dial(ctx, ps.ListenAddr().String(), LaneSpec{
		Role: internalpb.LaneRole_LANE_ROLE_CONTROL,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = performHello(conn, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
	require.NoError(t, err)

	require.NoError(t, conn.WriteFrames(Frame{
		Version:     ProtocolVersion,
		Type:        FrameTypePing,
		Lane:        LaneOrdinary,
		Correlation: 11,
	}))

	frame, err := conn.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)
	assert.Equal(t, LaneControl, frame.Lane)
	assert.Zero(t, frame.Correlation)

	_, err = conn.ReadFrame()
	require.Error(t, err)
}

func TestRemotingServerHandleDuplexConnNegotiatesCompression(t *testing.T) {
	ps, err := NewRemotingServer("127.0.0.1:0",
		WithRemotingServerLoops(1),
		WithRemotingServerMaxFrameSize(1<<20),
	)
	require.NoError(t, err)
	require.NoError(t, ps.Listen())
	t.Cleanup(func() { _ = ps.Shutdown(time.Second) })

	go func() { _ = ps.Serve() }()
	pause.For(50 * time.Millisecond)

	transport := NewTCPTransport()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := transport.Dial(ctx, ps.ListenAddr().String(), LaneSpec{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	hello := testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_GZIP, 1<<20)
	result, err := performHello(conn, hello)
	require.NoError(t, err)
	assert.Equal(t, internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, result.Effective.GetCompression())

	require.NoError(t, conn.WriteFrames(Frame{
		Version:     ProtocolVersion,
		Type:        FrameTypePing,
		Lane:        LaneControl,
		Correlation: 7,
	}))

	pong, err := conn.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypePong, pong.Type)
}

// TestInvokeDuplexTellReportsPanic verifies the dispatch surface learns about
// a recovered tell-handler panic: that signal drives the repayment of credit
// the handler claimed from its lease but never released.
func TestInvokeDuplexTellReportsPanic(t *testing.T) {
	var recovered atomic.Bool

	panicky, err := NewRemotingServer("127.0.0.1:0",
		WithRemotingServerDuplexTellHandler(func(context.Context, DataEnvelope) { panic("boom") }),
		WithRemotingServerPanicHandler(func(_ protoreflect.FullName, _ any) { recovered.Store(true) }),
	)
	require.NoError(t, err)

	env := DataEnvelope{SerializerID: SerializerIDPublicProto}
	assert.True(t, panicky.invokeDuplexTell(context.Background(), env), "a recovered panic must be reported")
	assert.True(t, recovered.Load(), "the panic handler must run")

	calm, err := NewRemotingServer("127.0.0.1:0",
		WithRemotingServerDuplexTellHandler(func(context.Context, DataEnvelope) {}),
		WithRemotingServerPanicHandler(func(protoreflect.FullName, any) {}),
	)
	require.NoError(t, err)
	assert.False(t, calm.invokeDuplexTell(context.Background(), env), "a clean handler must not report a panic")
}

// TestAcceptHelloHandshakeTimeout pins the slow-loris floor: a peer that opens
// a connection and never sends HELLO must be dropped by the acceptor within
// the handshake deadline, not pin an accept worker until an idle timeout that
// may be unset.
func TestAcceptHelloHandshakeTimeout(t *testing.T) {
	restore := acceptHandshakeTimeout
	acceptHandshakeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { acceptHandshakeTimeout = restore })

	ps, err := NewRemotingServer("127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	t.Cleanup(func() {
		require.NoError(t, ps.Shutdown(time.Second))
		<-done
	})

	conn, err := net.Dial("tcp", ps.ListenAddr().String())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	// Never send HELLO. A generous client read deadline is only a backstop: if
	// the acceptor drops the silent peer (correct), the read returns EOF well
	// inside the handshake window; if it does not, the read would block until
	// this deadline instead.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	_, readErr := conn.Read(make([]byte, 1))
	elapsed := time.Since(start)

	require.Error(t, readErr, "acceptor must drop a peer that never sends HELLO")
	require.Less(t, elapsed, time.Second, "the drop must come from the handshake deadline, not the client backstop")
}
