/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package grpcc

import (
	"context"
	"net"
	"strconv"
	"testing"

	"github.com/kapetan-io/tackle/autotls"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v3/test/data/testpb"
)

// nolint
func TestConn(t *testing.T) {
	t.Run("Happy Path", func(t *testing.T) {
		ctx := context.Background()
		ports := dynaport.Get(1)
		addr := net.JoinHostPort("", strconv.Itoa(ports[0]))
		service := &MockedService{}
		server, err := NewServer(addr, WithServices(service))
		require.NoError(t, server.Start())

		conn := NewConn(addr)
		require.NotNil(t, conn)
		grpcClient, err := conn.Dial()
		require.NoError(t, err)
		require.NotNil(t, grpcClient)

		client := testpb.NewHelloServiceClient(grpcClient)
		request := &testpb.HelloRequest{Name: "test"}
		resp, err := client.SayHello(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
	t.Run("Dial returns error when address not set", func(t *testing.T) {
		conn := NewConn("")
		require.NotNil(t, conn)
		_, err := conn.Dial()
		require.Error(t, err)
	})
	t.Run("With TLS", func(t *testing.T) {
		// AutoGenerate TLS certs
		conf := autotls.Config{AutoTLS: true}
		require.NoError(t, autotls.Setup(&conf))

		ctx := context.Background()
		ports := dynaport.Get(1)
		addr := net.JoinHostPort("", strconv.Itoa(ports[0]))
		service := &MockedService{}
		server, err := NewServer(addr, WithServices(service), WithServerTLS(conf.ServerTLS))
		require.NoError(t, server.Start())

		conn := NewConn(addr, WithConnTLS(conf.ClientTLS))
		require.NotNil(t, conn)
		grpcClient, err := conn.Dial()
		require.NoError(t, err)
		require.NotNil(t, grpcClient)

		client := testpb.NewHelloServiceClient(grpcClient)
		request := &testpb.HelloRequest{Name: "test"}
		resp, err := client.SayHello(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}
