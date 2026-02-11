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
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/test/data/testpb"
)

const (
	// benchConcurrency is the number of concurrent clients (goroutines) used
	// in every benchmark. Each goroutine either owns a dedicated TCP
	// connection (keep-alive ON) or dials a fresh one per request (keep-alive
	// OFF).
	benchConcurrency = 1000

	// benchPayloadSize is the fixed message size for raw TCP echo benchmarks.
	// 64 bytes is representative of a small RPC payload and gives clean
	// framing without a length prefix (both sides read exactly this many
	// bytes).
	benchPayloadSize = 64
)

// benchPayload is the fixed 64-byte payload written by every raw TCP client.
var benchPayload [benchPayloadSize]byte

// benchProtoReq is a small protobuf message reused across all proto
// benchmark iterations. It is read-only and safe for concurrent use.
var benchProtoReq = &testpb.Reply{Content: "bench"}

func init() {
	for i := range benchPayload {
		benchPayload[i] = byte(i)
	}
}

// startBenchTCPServer creates, listens, and starts serving a raw TCP echo
// server. The handler function determines whether connections are
// single-shot (keep-alive OFF) or persistent (keep-alive ON). It returns
// the server, the listening address, and a channel that receives the Serve
// error on shutdown.
func startBenchTCPServer(b *testing.B, handler RequestHandlerFunc) (*Server, string, <-chan error) {
	b.Helper()

	srv, err := NewServer("127.0.0.1:0", WithRequestHandler(handler))
	if err != nil {
		b.Fatal(err)
	}
	if err := srv.Listen(); err != nil {
		b.Fatal(err)
	}

	addr := srv.ListenAddr().String()
	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	time.Sleep(100 * time.Millisecond)

	return srv, addr, done
}

// startBenchProtoServer creates, listens, and starts serving a ProtoServer
// with a single echo handler for testpb.Reply. It returns the server, the
// listening address, and a channel that receives the Serve error on
// shutdown.
func startBenchProtoServer(b *testing.B) (*ProtoServer, string, <-chan error) {
	b.Helper()

	echoHandler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
		return req, nil
	}

	ps, err := NewProtoServer("127.0.0.1:0",
		WithProtoHandler("testpb.Reply", echoHandler),
	)
	if err != nil {
		b.Fatal(err)
	}
	if err := ps.Listen(); err != nil {
		b.Fatal(err)
	}

	addr := ps.ListenAddr().String()
	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	time.Sleep(100 * time.Millisecond)

	return ps, addr, done
}

// BenchmarkTCPServer_NoKeepAlive measures raw TCP echo throughput with 1000
// concurrent clients and no connection reuse. Each benchmark iteration is
// one complete round-trip: dial → write 64 B → read 64 B → close. The TCP
// connection is established and torn down for every single request.
//
// Run:
//
//	go test ./internal/tcp/ -run ^$ -bench BenchmarkTCPServer_NoKeepAlive -benchtime 10s -count 1
func BenchmarkTCPServer_NoKeepAlive(b *testing.B) {
	// Handler: read one fixed-size request, echo it, return.
	// The server closes the connection when the handler returns.
	srv, addr, done := startBenchTCPServer(b, func(conn Connection) {
		var buf [benchPayloadSize]byte
		if _, err := io.ReadFull(conn, buf[:]); err != nil {
			return
		}
		_, _ = conn.Write(buf[:]) //nolint:errcheck // best-effort echo
	})

	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()

	// Distribute b.N iterations across benchConcurrency goroutines.
	var wg sync.WaitGroup
	perWorker := b.N / benchConcurrency
	remainder := b.N % benchConcurrency

	for i := range benchConcurrency {
		n := perWorker
		if i < remainder {
			n++
		}
		if n == 0 {
			continue
		}
		wg.Add(1)
		go func(count int) {
			defer wg.Done()
			var buf [benchPayloadSize]byte
			for range count {
				conn, err := net.Dial("tcp", addr)
				if err != nil {
					continue // ephemeral port exhaustion — skip iteration
				}
				_, _ = conn.Write(benchPayload[:])
				_, _ = io.ReadFull(conn, buf[:])
				_ = conn.Close()
			}
		}(n)
	}
	wg.Wait()

	elapsed := time.Since(start)
	b.StopTimer()
	b.ReportMetric(float64(b.N)/elapsed.Seconds(), "req/s")

	_ = srv.Shutdown(5 * time.Second)
	<-done
}

// BenchmarkTCPServer_KeepAlive measures raw TCP echo throughput with 1000
// persistent connections (keep-alive ON). Exactly 1000 TCP connections are
// established before the timer starts. Each benchmark iteration is one
// round-trip: write 64 B → read 64 B on an already-open connection.
//
// Run:
//
//	go test ./internal/tcp/ -run ^$ -bench BenchmarkTCPServer_KeepAlive -benchtime 10s -count 1
func BenchmarkTCPServer_KeepAlive(b *testing.B) {
	// Handler: echo in a loop until the client disconnects (EOF).
	srv, addr, done := startBenchTCPServer(b, func(conn Connection) {
		var buf [benchPayloadSize]byte
		for {
			if _, err := io.ReadFull(conn, buf[:]); err != nil {
				return
			}
			if _, err := conn.Write(buf[:]); err != nil {
				return
			}
		}
	})

	// Pre-establish exactly 1000 persistent TCP connections.
	conns := make([]net.Conn, benchConcurrency)
	for i := range conns {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			b.Fatalf("failed to establish connection %d: %v", i, err)
		}
		conns[i] = conn
	}

	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()

	// Distribute b.N iterations across the pre-established connections.
	var wg sync.WaitGroup
	perWorker := b.N / benchConcurrency
	remainder := b.N % benchConcurrency

	for i := range benchConcurrency {
		n := perWorker
		if i < remainder {
			n++
		}
		if n == 0 {
			continue
		}
		wg.Add(1)
		go func(conn net.Conn, count int) {
			defer wg.Done()
			var buf [benchPayloadSize]byte
			for range count {
				_, _ = conn.Write(benchPayload[:])
				_, _ = io.ReadFull(conn, buf[:])
			}
		}(conns[i], n)
	}
	wg.Wait()

	elapsed := time.Since(start)
	b.StopTimer()
	b.ReportMetric(float64(b.N)/elapsed.Seconds(), "req/s")

	for _, conn := range conns {
		_ = conn.Close()
	}
	_ = srv.Shutdown(5 * time.Second)
	<-done
}

// BenchmarkProtoServer_NoKeepAlive measures ProtoServer echo throughput with
// 1000 concurrent clients and no connection reuse. Each benchmark iteration
// is one complete protobuf round-trip: dial → marshal → write frame → read
// frame → unmarshal → close. A fresh TCP connection is established and torn
// down for every single request.
//
// Run:
//
//	go test ./internal/tcp/ -run ^$ -bench BenchmarkProtoServer_NoKeepAlive -benchtime 10s -count 1
func BenchmarkProtoServer_NoKeepAlive(b *testing.B) {
	ps, addr, done := startBenchProtoServer(b)

	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()

	// Each goroutine gets its own Client with pooling disabled
	// (MaxIdleConns=0). This ensures every SendProto call dials a fresh TCP
	// connection and closes it after reading the response.
	var wg sync.WaitGroup
	perWorker := b.N / benchConcurrency
	remainder := b.N % benchConcurrency

	for i := range benchConcurrency {
		n := perWorker
		if i < remainder {
			n++
		}
		if n == 0 {
			continue
		}
		wg.Add(1)
		go func(count int) {
			defer wg.Done()
			client := NewClient(addr, WithMaxIdleConns(0))
			defer func() { _ = client.Close() }()

			ctx := context.Background()
			for range count {
				_, _ = client.SendProto(ctx, benchProtoReq) //nolint:errcheck // benchmark hot path
			}
		}(n)
	}
	wg.Wait()

	elapsed := time.Since(start)
	b.StopTimer()
	b.ReportMetric(float64(b.N)/elapsed.Seconds(), "req/s")

	_ = ps.Shutdown(5 * time.Second)
	<-done
}

// BenchmarkProtoServer_KeepAlive measures ProtoServer echo throughput with
// 1000 persistent connections (keep-alive ON). A single [Client] with a
// pool capacity of 1000 is shared across all goroutines. The first b.N
// iterations establish connections (one per goroutine), after which all
// subsequent iterations reuse pooled connections — yielding exactly 1000
// persistent TCP connections under sustained load.
//
// Run:
//
//	go test ./internal/tcp/ -run ^$ -bench BenchmarkProtoServer_KeepAlive -benchtime 10s -count 1
func BenchmarkProtoServer_KeepAlive(b *testing.B) {
	ps, addr, done := startBenchProtoServer(b)

	// Single Client with pool capacity = benchConcurrency. Under full load
	// each goroutine holds one connection, establishing exactly 1000
	// persistent TCP connections that serve requests for the entire run.
	client := NewClient(addr, WithMaxIdleConns(benchConcurrency))

	// Pre-warm: establish benchConcurrency connections by issuing one
	// request per goroutine and returning connections to the pool.
	var warmWg sync.WaitGroup
	for range benchConcurrency {
		warmWg.Add(1)
		go func() {
			defer warmWg.Done()
			_, _ = client.SendProto(context.Background(), benchProtoReq) //nolint:errcheck
		}()
	}
	warmWg.Wait()

	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()

	// Distribute b.N iterations across benchConcurrency goroutines.
	// Every goroutine calls Get (pops a pooled connection), does a full
	// proto round-trip, then Put (returns the connection to the pool).
	var wg sync.WaitGroup
	perWorker := b.N / benchConcurrency
	remainder := b.N % benchConcurrency

	for i := range benchConcurrency {
		n := perWorker
		if i < remainder {
			n++
		}
		if n == 0 {
			continue
		}
		wg.Add(1)
		go func(count int) {
			defer wg.Done()
			ctx := context.Background()
			for range count {
				_, _ = client.SendProto(ctx, benchProtoReq) //nolint:errcheck // benchmark hot path
			}
		}(n)
	}
	wg.Wait()

	elapsed := time.Since(start)
	b.StopTimer()
	b.ReportMetric(float64(b.N)/elapsed.Seconds(), "req/s")

	_ = client.Close()
	_ = ps.Shutdown(5 * time.Second)
	<-done
}
