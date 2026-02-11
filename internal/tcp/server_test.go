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
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/internal/pause"
)

func TestNewServer(t *testing.T) {
	t.Run("valid address with defaults", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0")
		require.NoError(t, err)
		require.NotNil(t, srv)
		require.NotNil(t, srv.listenAddr)
		require.Equal(t, 8, srv.Loops())
		require.NotNil(t, srv.ListenConfig())
		require.Nil(t, srv.TLSConfig())
	})

	t.Run("invalid address", func(t *testing.T) {
		_, err := NewServer("invalid:::address")
		require.Error(t, err)
	})
}

func TestServer_Listen(t *testing.T) {
	t.Run("successful listen", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0")
		require.NoError(t, err)

		err = srv.Listen()
		require.NoError(t, err)
		require.NotNil(t, srv.listener)

		addr := srv.ListenAddr()
		require.NotNil(t, addr)
		require.Greater(t, addr.Port, 0)

		require.NoError(t, srv.listener.Close())
	})

	t.Run("listen on specific port", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := l.Addr().(*net.TCPAddr).Port
		require.NoError(t, l.Close())

		srv, err := NewServer(l.Addr().String())
		require.NoError(t, err)

		err = srv.Listen()
		require.NoError(t, err)
		require.Equal(t, port, srv.ListenAddr().Port)

		require.NoError(t, srv.listener.Close())
	})
}

func TestServerOptions(t *testing.T) {
	t.Run("WithLoops", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0", WithLoops(4))
		require.NoError(t, err)
		require.Equal(t, 4, srv.Loops())
	})

	t.Run("WithLoops clamped to 1 when zero", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0", WithLoops(0))
		require.NoError(t, err)
		require.Equal(t, 1, srv.Loops())
	})

	t.Run("WithLoops clamped to 1 when negative", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0", WithLoops(-5))
		require.NoError(t, err)
		require.Equal(t, 1, srv.Loops())
	})

	t.Run("WithMaxAcceptConnections", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0", WithMaxAcceptConnections(100))
		require.NoError(t, err)
		require.Equal(t, int32(100), srv.maxAcceptConns.Load())
	})

	t.Run("WithAllowThreadLocking", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0", WithAllowThreadLocking(true))
		require.NoError(t, err)
		require.True(t, srv.allowThreadLock)
	})

	// nolint
	t.Run("WithServerContext", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), "someKey", "someValue")
		srv, err := NewServer("127.0.0.1:0", WithServerContext(ctx))
		require.NoError(t, err)
		require.Equal(t, ctx, srv.Context())
	})

	t.Run("GetContext default", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0")
		require.NoError(t, err)
		ctx := srv.Context()
		require.NotNil(t, ctx)
	})

	t.Run("WithBallast", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0", WithBallast(10))
		require.NoError(t, err)
		require.Len(t, srv.ballast, 10*1024*1024)
	})

	t.Run("WithListenConfig", func(t *testing.T) {
		custom := &ListenConfig{
			SocketReusePort: false,
			SocketFastOpen:  true,
		}
		srv, err := NewServer("127.0.0.1:0", WithListenConfig(custom))
		require.NoError(t, err)
		require.Equal(t, custom, srv.ListenConfig())
	})

	t.Run("WithTLSConfig", func(t *testing.T) {
		tlsCfg := &tls.Config{} //nolint:gosec
		srv, err := NewServer("127.0.0.1:0", WithTLSConfig(tlsCfg))
		require.NoError(t, err)
		require.Equal(t, tlsCfg, srv.TLSConfig())
	})

	t.Run("WithConnWrapper", func(t *testing.T) {
		w := &testWrapper{}
		srv, err := NewServer("127.0.0.1:0", WithConnWrapper(w))
		require.NoError(t, err)
		require.Len(t, srv.connWrappers, 1)
	})
}

//nolint:gosec
func TestServer_TLS(t *testing.T) {
	cert, key := generateTestCert(t)
	tlsCert, err := tls.X509KeyPair(cert, key)
	require.NoError(t, err)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}

	t.Run("EnableTLS without config", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0")
		require.NoError(t, err)

		err = srv.EnableTLS()
		require.ErrorIs(t, err, ErrNoTLSConfig)
	})

	t.Run("EnableTLS with config", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0", WithTLSConfig(tlsConfig))
		require.NoError(t, err)
		require.Equal(t, tlsConfig, srv.TLSConfig())

		err = srv.EnableTLS()
		require.NoError(t, err)
		require.True(t, srv.tlsEnabled)
	})

	t.Run("ListenTLS", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0", WithTLSConfig(tlsConfig))
		require.NoError(t, err)

		err = srv.ListenTLS()
		require.NoError(t, err)
		require.NotNil(t, srv.listener)
		require.True(t, srv.tlsEnabled)

		require.NoError(t, srv.listener.Close())
	})
}

func TestServer_Serve(t *testing.T) {
	t.Run("serve without listener", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0")
		require.NoError(t, err)

		err = srv.Serve()
		require.ErrorIs(t, err, ErrNoListener)
	})

	t.Run("serve and accept connections", func(t *testing.T) {
		var handled atomic.Int32

		srv, err := NewServer("127.0.0.1:0", WithRequestHandler(func(conn Connection) {
			handled.Add(1)
			buf := make([]byte, 1024)
			n, _ := conn.Read(buf) //nolint:errcheck // handler reads until client disconnects
			if n > 0 {
				_, _ = conn.Write(buf[:n]) //nolint:errcheck // best-effort echo
			}
		}))
		require.NoError(t, err)

		err = srv.Listen()
		require.NoError(t, err)

		addr := srv.ListenAddr().String()

		done := make(chan error, 1)
		go func() {
			done <- srv.Serve()
		}()

		pause.For(100 * time.Millisecond)

		conn, err := net.Dial("tcp", addr)
		require.NoError(t, err)

		_, err = conn.Write([]byte("hello"))
		require.NoError(t, err)

		buf := make([]byte, 1024)
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
		n, err := conn.Read(buf)
		require.NoError(t, err)
		require.Equal(t, "hello", string(buf[:n]))

		require.NoError(t, conn.Close())

		err = srv.Shutdown(time.Second)
		require.NoError(t, err)

		<-done

		require.Greater(t, handled.Load(), int32(0))
	})
}

func TestServer_Shutdown(t *testing.T) {
	t.Run("graceful shutdown", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0", WithRequestHandler(func(conn Connection) {
			pause.For(100 * time.Millisecond)
		}))
		require.NoError(t, err)

		err = srv.Listen()
		require.NoError(t, err)

		go func() { _ = srv.Serve() }()
		pause.For(50 * time.Millisecond)

		err = srv.Shutdown(2 * time.Second)
		require.NoError(t, err)

		// Double shutdown should be no-op.
		err = srv.Shutdown(time.Second)
		require.NoError(t, err)
	})

	t.Run("immediate shutdown", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0", WithRequestHandler(func(conn Connection) {
			pause.For(5 * time.Second)
		}))
		require.NoError(t, err)

		err = srv.Listen()
		require.NoError(t, err)

		go func() { _ = srv.Serve() }()
		pause.For(50 * time.Millisecond)

		err = srv.Halt()
		require.NoError(t, err)
	})
}

func TestServer_MaxAcceptConnections(t *testing.T) {
	var accepted atomic.Int32

	srv, err := NewServer("127.0.0.1:0",
		WithRequestHandler(func(conn Connection) {
			accepted.Add(1)
			pause.For(100 * time.Millisecond)
			_, _ = io.Copy(io.Discard, conn)
		}),
		WithMaxAcceptConnections(2),
	)
	require.NoError(t, err)

	err = srv.Listen()
	require.NoError(t, err)

	addr := srv.ListenAddr().String()

	go func() { _ = srv.Serve() }()
	pause.For(50 * time.Millisecond)

	// Try to open 5 connections quickly. Some dials may fail because the
	// server shuts down after reaching the limit — errors are expected.
	conns := make([]net.Conn, 5)
	for i := range conns {
		conns[i], _ = net.Dial("tcp", addr) //nolint:errcheck // dial failures expected past limit
	}

	pause.For(200 * time.Millisecond)

	for _, conn := range conns {
		if conn != nil {
			_ = conn.Close() // Close error intentionally ignored — test cleanup.
		}
	}

	err = srv.Shutdown(time.Second)
	require.NoError(t, err)

	require.LessOrEqual(t, srv.AcceptedConnections(), int32(3))
}

func TestServer_ConnectionTracking(t *testing.T) {
	srv, err := NewServer("127.0.0.1:0", WithRequestHandler(func(conn Connection) {
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf) //nolint:errcheck // handler reads until client disconnects
	}))
	require.NoError(t, err)

	err = srv.Listen()
	require.NoError(t, err)

	addr := srv.ListenAddr().String()

	go func() { _ = srv.Serve() }()
	pause.For(50 * time.Millisecond)

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)

	pause.For(100 * time.Millisecond)

	require.Equal(t, int32(1), srv.ActiveConnections())
	require.Equal(t, int32(1), srv.AcceptedConnections())

	require.NoError(t, conn.Close())
	pause.For(100 * time.Millisecond)

	require.Equal(t, int32(0), srv.ActiveConnections())

	err = srv.Shutdown(time.Second)
	require.NoError(t, err)
}

func TestServer_CustomConnection(t *testing.T) {
	type customConn struct {
		TCPConn
		customField string
	}

	srv, err := NewServer("127.0.0.1:0",
		WithConnectionCreator(func() Connection {
			return &customConn{customField: "test"}
		}),
		WithRequestHandler(func(conn Connection) {
			cc, ok := conn.(*customConn)
			require.True(t, ok)
			require.Equal(t, "test", cc.customField)
			_, _ = io.Copy(io.Discard, conn)
		}),
	)
	require.NoError(t, err)

	err = srv.Listen()
	require.NoError(t, err)

	addr := srv.ListenAddr().String()

	go func() { _ = srv.Serve() }()
	pause.For(50 * time.Millisecond)

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	_, err = conn.Write([]byte("test"))
	require.NoError(t, err)

	pause.For(50 * time.Millisecond)
	require.NoError(t, conn.Close())

	err = srv.Shutdown(time.Second)
	require.NoError(t, err)
}

func TestServer_ConnWrapper(t *testing.T) {
	var wrapCalled atomic.Int32
	wrapper := &testWrapper{onWrap: func() { wrapCalled.Add(1) }}

	srv, err := NewServer("127.0.0.1:0",
		WithConnWrapper(wrapper),
		WithRequestHandler(func(conn Connection) {
			_, _ = io.Copy(io.Discard, conn)
		}),
	)
	require.NoError(t, err)

	err = srv.Listen()
	require.NoError(t, err)

	addr := srv.ListenAddr().String()

	go func() { _ = srv.Serve() }()
	pause.For(50 * time.Millisecond)

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	_, err = conn.Write([]byte("test"))
	require.NoError(t, err)
	pause.For(100 * time.Millisecond)
	require.NoError(t, conn.Close())

	err = srv.Shutdown(time.Second)
	require.NoError(t, err)

	require.Greater(t, wrapCalled.Load(), int32(0), "wrapper should have been called")
}

// nolint
func TestTCPConn_Methods(t *testing.T) {
	srv, err := NewServer("127.0.0.1:0", WithRequestHandler(func(conn Connection) {
		tcpConn := conn.(*TCPConn)

		require.NotNil(t, tcpConn.Server())
		require.NotNil(t, tcpConn.ClientAddr())
		require.NotNil(t, tcpConn.ServerAddr())
		require.NotNil(t, tcpConn.NetConn())
		require.False(t, tcpConn.StartTime().IsZero())

		ctx := context.WithValue(context.Background(), "key", "value")
		tcpConn.SetContext(ctx)
		require.Equal(t, ctx, tcpConn.Context())

		_, _ = io.Copy(io.Discard, conn)
	}))
	require.NoError(t, err)

	err = srv.Listen()
	require.NoError(t, err)

	addr := srv.ListenAddr().String()

	go func() { _ = srv.Serve() }()
	pause.For(50 * time.Millisecond)

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	_, err = conn.Write([]byte("test"))
	require.NoError(t, err)
	pause.For(100 * time.Millisecond)
	require.NoError(t, conn.Close())

	err = srv.Shutdown(time.Second)
	require.NoError(t, err)
}

func TestIsIPv6Addr(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want bool
	}{
		{"IPv4", "127.0.0.1:8080", false},
		{"IPv6", "[::1]:8080", true},
		{"IPv6 full", "[2001:db8::1]:8080", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, err := net.ResolveTCPAddr("tcp", tt.addr)
			require.NoError(t, err)
			require.Equal(t, tt.want, IsIPv6Addr(addr))
		})
	}
}

func TestServer_GetListenAddr_NilListener(t *testing.T) {
	srv, err := NewServer("127.0.0.1:0")
	require.NoError(t, err)

	addr := srv.ListenAddr()
	require.Nil(t, addr)
}

func TestServer_ListenTLS_NoConfig(t *testing.T) {
	srv, err := NewServer("127.0.0.1:0")
	require.NoError(t, err)

	err = srv.ListenTLS()
	require.ErrorIs(t, err, ErrNoTLSConfig)
}

func TestServer_GetNetTCPConn(t *testing.T) {
	t.Run("with real TCP conn", func(t *testing.T) {
		var gotNetTCPConn atomic.Bool
		srv, err := NewServer("127.0.0.1:0", WithRequestHandler(func(conn Connection) {
			tcpConn := conn.(*TCPConn)
			if tcpConn.GetNetTCPConn() != nil {
				gotNetTCPConn.Store(true)
			}
			_, _ = io.Copy(io.Discard, conn)
		}))
		require.NoError(t, err)

		err = srv.Listen()
		require.NoError(t, err)

		go func() { _ = srv.Serve() }()
		pause.For(50 * time.Millisecond)

		conn, err := net.Dial("tcp", srv.ListenAddr().String())
		require.NoError(t, err)
		_, _ = conn.Write([]byte("x")) //nolint:errcheck // best-effort write
		pause.For(50 * time.Millisecond)
		_ = conn.Close() // Close error intentionally ignored — test cleanup.

		pause.For(100 * time.Millisecond)
		require.True(t, gotNetTCPConn.Load())

		require.NoError(t, srv.Shutdown(time.Second))
	})

	t.Run("with non-TCP conn", func(t *testing.T) {
		tc := &TCPConn{Conn: &mockNetConn{}}
		require.Nil(t, tc.GetNetTCPConn())
	})
}

func TestServer_StartTLS(t *testing.T) {
	t.Run("nil config falls back to server config", func(t *testing.T) {
		cert, key := generateTestCert(t)
		tlsCert, err := tls.X509KeyPair(cert, key)
		require.NoError(t, err)

		tlsConfig := &tls.Config{Certificates: []tls.Certificate{tlsCert}} //nolint:gosec
		srv, err := NewServer("127.0.0.1:0", WithTLSConfig(tlsConfig))
		require.NoError(t, err)

		clientRaw, serverRaw := net.Pipe()
		defer func() { _ = clientRaw.Close() }()
		defer func() { _ = serverRaw.Close() }()

		tc := &TCPConn{Conn: serverRaw}
		tc.SetServer(srv)

		err = tc.StartTLS(nil) // should use server TLS config
		require.NoError(t, err)
	})

	t.Run("explicit config", func(t *testing.T) {
		cert, key := generateTestCert(t)
		tlsCert, err := tls.X509KeyPair(cert, key)
		require.NoError(t, err)

		tlsConfig := &tls.Config{Certificates: []tls.Certificate{tlsCert}} //nolint:gosec
		srv, err := NewServer("127.0.0.1:0")
		require.NoError(t, err)

		clientRaw, serverRaw := net.Pipe()
		defer func() { _ = clientRaw.Close() }()
		defer func() { _ = serverRaw.Close() }()

		tc := &TCPConn{Conn: serverRaw}
		tc.SetServer(srv)

		err = tc.StartTLS(tlsConfig)
		require.NoError(t, err)
	})

	t.Run("nil config and no server config", func(t *testing.T) {
		srv, err := NewServer("127.0.0.1:0")
		require.NoError(t, err)

		tc := &TCPConn{Conn: &mockNetConn{}}
		tc.SetServer(srv)

		err = tc.StartTLS(nil)
		require.ErrorIs(t, err, ErrInvalidTLSConfig)
	})
}

func TestServer_ConnWrapperError(t *testing.T) {
	handlerCalled := make(chan struct{}, 1)
	srv, err := NewServer("127.0.0.1:0",
		WithConnWrapper(&errConnWrapper{err: errors.New("wrap failed")}),
		WithRequestHandler(func(conn Connection) {
			handlerCalled <- struct{}{}
		}),
	)
	require.NoError(t, err)

	err = srv.Listen()
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve()
	}()
	pause.For(50 * time.Millisecond)

	conn, err := net.Dial("tcp", srv.ListenAddr().String())
	require.NoError(t, err)
	_ = conn.Close() // Close error intentionally ignored — test cleanup.

	pause.For(100 * time.Millisecond)

	select {
	case <-handlerCalled:
		t.Fatal("handler should not be called when wrapper fails")
	default:
	}

	require.Equal(t, int32(0), srv.ActiveConnections())

	require.NoError(t, srv.Shutdown(time.Second))
	<-done
}

func TestServer_AwaitConnectionsTimeout(t *testing.T) {
	srv, err := NewServer("127.0.0.1:0", WithRequestHandler(func(conn Connection) {
		pause.For(5 * time.Second)
	}))
	require.NoError(t, err)

	err = srv.Listen()
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve()
	}()
	pause.For(50 * time.Millisecond)

	conn, err := net.Dial("tcp", srv.ListenAddr().String())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	pause.For(50 * time.Millisecond)

	// Shutdown with a short timeout — will timeout waiting for the connection.
	err = srv.Shutdown(100 * time.Millisecond)
	require.NoError(t, err)

	err = <-done
	require.NoError(t, err)
}

func TestServer_Serve_WithThreadLocking(t *testing.T) {
	srv, err := NewServer("127.0.0.1:0",
		WithAllowThreadLocking(true),
		WithLoops(4),
		WithRequestHandler(func(conn Connection) {
			_, _ = io.Copy(io.Discard, conn)
		}),
	)
	require.NoError(t, err)

	err = srv.Listen()
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve()
	}()
	pause.For(50 * time.Millisecond)

	conn, err := net.Dial("tcp", srv.ListenAddr().String())
	require.NoError(t, err)
	_, _ = conn.Write([]byte("test")) //nolint:errcheck // best-effort write
	_ = conn.Close()                  // Close error intentionally ignored — test cleanup.

	pause.For(50 * time.Millisecond)
	require.NoError(t, srv.Shutdown(time.Second))
	<-done
}

func TestServer_Serve_WithTLS(t *testing.T) {
	cert, key := generateTestCert(t)
	tlsCert, err := tls.X509KeyPair(cert, key)
	require.NoError(t, err)

	srv, err := NewServer("127.0.0.1:0",
		WithTLSConfig(&tls.Config{Certificates: []tls.Certificate{tlsCert}}), //nolint:gosec
		WithRequestHandler(func(conn Connection) {
			buf := make([]byte, 1024)
			n, _ := conn.Read(buf) //nolint:errcheck // handler reads until client disconnects
			if n > 0 {
				_, _ = conn.Write(buf[:n]) //nolint:errcheck // best-effort echo
			}
		}),
	)
	require.NoError(t, err)

	err = srv.ListenTLS()
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve()
	}()
	pause.For(50 * time.Millisecond)

	tlsClientCfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	conn, err := tls.Dial("tcp", srv.ListenAddr().String(), tlsClientCfg)
	require.NoError(t, err)

	_, err = conn.Write([]byte("hello tls"))
	require.NoError(t, err)

	buf := make([]byte, 1024)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	n, err := conn.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "hello tls", string(buf[:n]))

	require.NoError(t, conn.Close())
	require.NoError(t, srv.Shutdown(time.Second))
	<-done
}

// nolint
func TestTCPConn_Context(t *testing.T) {
	tc := &TCPConn{}

	ctx := tc.Context()
	require.NotNil(t, ctx)

	// nolint
	custom := context.WithValue(context.Background(), "someKey", "someValue")
	tc.SetContext(custom)
	require.Equal(t, custom, tc.Context())
}

func TestTCPConn_StartTime(t *testing.T) {
	tc := &TCPConn{}
	tc.Start()
	st := tc.StartTime()
	require.False(t, st.IsZero())
	require.WithinDuration(t, time.Now(), st, time.Second)
}

func TestTCPConn_Reset(t *testing.T) {
	tc := &TCPConn{}
	tc.SetContext(context.Background())

	raw := &mockNetConn{}
	tc.Reset(raw)

	require.Equal(t, raw, tc.NetConn())
	require.Nil(t, tc.ctx) // Reset clears context.
}

func TestServer_AcceptLoopNonShutdownError(t *testing.T) {
	srv, err := NewServer("127.0.0.1:0", WithRequestHandler(func(conn Connection) {
		_, _ = io.Copy(io.Discard, conn)
	}))
	require.NoError(t, err)

	err = srv.Listen()
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve()
	}()
	pause.For(50 * time.Millisecond)

	// Close the listener directly WITHOUT setting the shutdown flag.
	// This causes AcceptTCP to return a non-shutdown error.
	require.NoError(t, srv.listener.Close())

	err = <-done
	require.Error(t, err)
}

func TestServer_AcceptLoopTimeout(t *testing.T) {
	srv, err := NewServer("127.0.0.1:0",
		WithRequestHandler(func(conn Connection) {
			_, _ = io.Copy(io.Discard, conn)
		}),
		WithLoops(1),
	)
	require.NoError(t, err)

	err = srv.Listen()
	require.NoError(t, err)

	// Set a very short deadline on the listener to trigger timeout errors.
	require.NoError(t, srv.listener.SetDeadline(time.Now().Add(10*time.Millisecond)))

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve()
	}()

	pause.For(100 * time.Millisecond)

	require.NoError(t, srv.Shutdown(time.Second))
	err = <-done
	require.NoError(t, err)
}

func TestServer_ShutdownWaitIndefinitely(t *testing.T) {
	srv, err := NewServer("127.0.0.1:0", WithRequestHandler(func(conn Connection) {
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf) //nolint:errcheck // handler reads until client disconnects
	}))
	require.NoError(t, err)

	err = srv.Listen()
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve()
	}()
	pause.For(50 * time.Millisecond)

	conn, err := net.Dial("tcp", srv.ListenAddr().String())
	require.NoError(t, err)
	pause.For(50 * time.Millisecond)

	// Shutdown(0) means wait indefinitely for connections to drain.
	go func() {
		_ = srv.Shutdown(0) // Error intentionally ignored — runs in background goroutine.
	}()
	pause.For(50 * time.Millisecond)

	// Close client to unblock the handler.
	_ = conn.Close() // Close error intentionally ignored — test cleanup.

	err = <-done
	require.NoError(t, err)
}

func TestServer_IPv6Listen(t *testing.T) {
	srv, err := NewServer("[::1]:0")
	require.NoError(t, err)

	err = srv.Listen()
	if err != nil {
		t.Skip("IPv6 not available:", err)
	}

	addr := srv.ListenAddr()
	require.NotNil(t, addr)
	require.True(t, IsIPv6Addr(addr))

	require.NoError(t, srv.listener.Close())
}

// errConnWrapper is a ConnWrapper that always returns an error.
type errConnWrapper struct {
	err error
}

func (w *errConnWrapper) Wrap(net.Conn) (net.Conn, error) {
	return nil, w.err
}

// testWrapper is a simple ConnWrapper for testing.
type testWrapper struct {
	onWrap func()
}

func (w *testWrapper) Wrap(conn net.Conn) (net.Conn, error) {
	if w.onWrap != nil {
		w.onWrap()
	}
	return conn, nil
}

// generateTestCert generates a self-signed certificate for testing.
func generateTestCert(t *testing.T) ([]byte, []byte) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	return certPEM, keyPEM
}
