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

	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestServer(t *testing.T) {
	t.Run("With Start and stop", func(t *testing.T) {
		ports := dynaport.Get(1)
		addr := net.JoinHostPort("", strconv.Itoa(ports[0]))
		service := &MockedService{}
		server, err := NewServer(addr, WithServices(service))
		require.NoError(t, err)
		require.NotNil(t, server)

		require.NoError(t, server.Start())

		require.NotNil(t, server.GetServer())
		require.NotNil(t, server.GetListener())

		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		require.NotNil(t, conn)

		require.NoError(t, server.Stop())
	})
	t.Run("Return error when no service is provided", func(t *testing.T) {
		ports := dynaport.Get(1)
		addr := net.JoinHostPort("", strconv.Itoa(ports[0]))
		server, err := NewServer(addr)
		require.Error(t, err)
		require.Nil(t, server)
	})
	t.Run("Return error when no address not provided", func(t *testing.T) {
		server, err := NewServer("")
		require.Error(t, err)
		require.Nil(t, server)
	})

	t.Run("Return error when already started", func(t *testing.T) {
		ports := dynaport.Get(1)
		addr := net.JoinHostPort("", strconv.Itoa(ports[0]))
		service := &MockedService{}
		server, err := NewServer(addr, WithServices(service))
		require.NoError(t, err)
		require.NotNil(t, server)

		require.NoError(t, server.Start())

		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		require.NotNil(t, conn)

		require.Error(t, server.Start())

		require.NoError(t, server.Stop())
	})
	t.Run("Stop returns error when not started", func(t *testing.T) {
		ports := dynaport.Get(1)
		addr := net.JoinHostPort("", strconv.Itoa(ports[0]))
		service := &MockedService{}
		server, err := NewServer(addr, WithServices(service))
		require.NoError(t, err)
		require.NotNil(t, server)

		require.Error(t, server.Stop())
	})

	t.Run("Stop returns error when already stopped", func(t *testing.T) {
		ports := dynaport.Get(1)
		addr := net.JoinHostPort("", strconv.Itoa(ports[0]))
		service := &MockedService{}
		server, err := NewServer(addr, WithServices(service))
		require.NoError(t, err)
		require.NotNil(t, server)

		require.NoError(t, server.Start())

		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		require.NotNil(t, conn)

		require.NoError(t, server.Stop())
		require.Error(t, server.Stop())
	})
}

// MockedService is only used in grpc unit tests
type MockedService struct{}

// SayHello will handle the HelloRequest and return the appropriate response
func (s *MockedService) SayHello(_ context.Context, in *testpb.HelloRequest) (*testpb.HelloResponse, error) {
	return &testpb.HelloResponse{Message: "This is a mocked service " + in.Name}, nil
}

func (s *MockedService) RegisterService(server *grpc.Server) {
	testpb.RegisterHelloServiceServer(server, s)
}
