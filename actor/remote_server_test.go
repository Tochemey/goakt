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

package actor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

// newRemoteServerTestSystem builds the minimal actorSystem needed to unit-test
// the proto TCP handler methods in remote_server.go.
// It does NOT start the actor system; handlers are called directly.
func newRemoteServerTestSystem(host string, port int) *actorSystem {
	sys := &actorSystem{
		actors:       newTree(),
		logger:       log.DiscardLogger,
		remoteConfig: remote.NewConfig(host, port),
		name:         "testSys",
		grains:       xsync.NewMap[string, *grainPID](),
		askTimeout:   DefaultAskTimeout,
	}
	sys.remotingEnabled.Store(true)
	return sys
}

// requireProtoError asserts that the returned proto message is an *internalpb.Error
// with the expected code.
func requireProtoError(t *testing.T, msg interface{}, code internalpb.Code) {
	t.Helper()
	protoErr, ok := msg.(*internalpb.Error)
	require.True(t, ok, "expected *internalpb.Error, got %T", msg)
	assert.Equal(t, code, protoErr.GetCode())
}

// nullConn is a no-op Connection implementation used where the handler ignores conn.
var nullConn inet.Connection

func TestToProtoError(t *testing.T) {
	err := gerrors.ErrRemotingDisabled
	resp := toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, err)
	require.NotNil(t, resp)
	assert.Equal(t, internalpb.Code_CODE_FAILED_PRECONDITION, resp.GetCode())
	assert.Equal(t, err.Error(), resp.GetMessage())
}

func TestValidateRemoteHost(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9000
	sys := newRemoteServerTestSystem(host, port)

	t.Run("matching host and port returns nil", func(t *testing.T) {
		err := sys.validateRemoteHost(host, int32(port))
		require.NoError(t, err)
	})

	t.Run("different host returns ErrInvalidHost", func(t *testing.T) {
		err := sys.validateRemoteHost("10.0.0.1", int32(port))
		require.ErrorIs(t, err, gerrors.ErrInvalidHost)
	})

	t.Run("different port returns ErrInvalidHost", func(t *testing.T) {
		err := sys.validateRemoteHost(host, 9999)
		require.ErrorIs(t, err, gerrors.ErrInvalidHost)
	})
}

func TestExtractContextWithPropagator(t *testing.T) {
	ctx := context.Background()

	t.Run("nil propagator returns original context unchanged", func(t *testing.T) {
		sys := newRemoteServerTestSystem("127.0.0.1", 9000)
		// remoteConfig created with no ContextPropagator option → propagator is nil
		got, err := sys.extractContextWithPropagator(ctx)
		require.NoError(t, err)
		assert.Equal(t, ctx, got)
	})

	t.Run("propagator set but no metadata in context returns original context", func(t *testing.T) {
		// headerPropagator only adds a value when the header key is present in
		// the metadata. With no metadata attached to the context, Extract is
		// never called, so the returned context must equal the original.
		propagator := &headerPropagator{headerKey: "x-trace-id", ctxKey: "trace"}
		sys := newRemoteServerTestSystem("127.0.0.1", 9000)
		sys.remoteConfig = remote.NewConfig("127.0.0.1", 9000, remote.WithContextPropagator(propagator))

		got, err := sys.extractContextWithPropagator(ctx)
		require.NoError(t, err)
		assert.Equal(t, ctx, got)
	})

	t.Run("propagator set with metadata in context calls Extract", func(t *testing.T) {
		// headerPropagator.Extract copies the "x-trace-id" header from the
		// request metadata into the context under the "trace" key. Verifying
		// that key appears on the returned context confirms Extract was invoked.
		type traceKey = string
		propagator := &headerPropagator{headerKey: "x-trace-id", ctxKey: traceKey("trace")}
		sys := newRemoteServerTestSystem("127.0.0.1", 9000)
		sys.remoteConfig = remote.NewConfig("127.0.0.1", 9000, remote.WithContextPropagator(propagator))

		md := inet.NewMetadata()
		md.Set("x-trace-id", "abc123")
		ctxWithMD := inet.ContextWithMetadata(ctx, md)

		got, err := sys.extractContextWithPropagator(ctxWithMD)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "abc123", got.Value(traceKey("trace")))
	})
}

func TestProtoServerOptions(t *testing.T) {
	sys := newRemoteServerTestSystem("127.0.0.1", 9000)
	opts := sys.protoServerOptions()
	// There must be exactly as many options as registered handlers in the function body.
	assert.NotEmpty(t, opts)
}

func TestStopRemoteServer(t *testing.T) {
	t.Run("nil remoteServer is a no-op", func(t *testing.T) {
		sys := newRemoteServerTestSystem("127.0.0.1", 9000)
		// remoteServer is nil by default
		err := sys.stopRemoteServer(time.Second)
		require.NoError(t, err)
	})
}

func TestRemoteLookupHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9000
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteLookupHandler(ctx, nullConn, new(internalpb.RemoteLookupResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteLookupRequest{Host: host, Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteLookupHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteLookupRequest{Host: "10.0.0.1", Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteLookupHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteLookupRequest{Host: host, Port: int32(port), Name: "missing"}
		resp, err := sys.remoteLookupHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})
}

func TestRemoteAskHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9001
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteAskHandler(ctx, nullConn, new(internalpb.RemoteAskResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		resp, err := sys.remoteAskHandler(ctx, nullConn, new(internalpb.RemoteAskRequest))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("empty message list returns empty response", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteAskRequest{} // no RemoteMessages
		resp, err := sys.remoteAskHandler(ctx, nullConn, req)
		require.NoError(t, err)
		askResp, ok := resp.(*internalpb.RemoteAskResponse)
		require.True(t, ok)
		assert.Empty(t, askResp.GetMessages())
	})

	t.Run("message with unparseable receiver address returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteAskRequest{
			RemoteMessages: []*internalpb.RemoteMessage{
				{Receiver: "not-a-valid-address"},
			},
		}
		resp, err := sys.remoteAskHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("message with wrong host in address returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		addr := fmt.Sprintf("goakt://testSys@10.0.0.1:%d/actor1", port)
		req := &internalpb.RemoteAskRequest{
			RemoteMessages: []*internalpb.RemoteMessage{{Receiver: addr}},
		}
		resp, err := sys.remoteAskHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		addr := fmt.Sprintf("goakt://testSys@%s:%d/actor1", host, port)
		req := &internalpb.RemoteAskRequest{
			RemoteMessages: []*internalpb.RemoteMessage{{Receiver: addr}},
		}
		resp, err := sys.remoteAskHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})
}

func TestRemoteTellHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9002
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteTellHandler(ctx, nullConn, new(internalpb.RemoteTellResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		resp, err := sys.remoteTellHandler(ctx, nullConn, new(internalpb.RemoteTellRequest))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("empty message list returns empty response", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteTellHandler(ctx, nullConn, new(internalpb.RemoteTellRequest))
		require.NoError(t, err)
		_, ok := resp.(*internalpb.RemoteTellResponse)
		assert.True(t, ok)
	})

	t.Run("message with unparseable receiver returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteTellRequest{
			RemoteMessages: []*internalpb.RemoteMessage{{Receiver: "bad-address"}},
		}
		resp, err := sys.remoteTellHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		addr := fmt.Sprintf("goakt://testSys@%s:%d/actor1", host, port)
		req := &internalpb.RemoteTellRequest{
			RemoteMessages: []*internalpb.RemoteMessage{{Receiver: addr}},
		}
		resp, err := sys.remoteTellHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})
}

func TestRemoteReSpawnHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9003
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteReSpawnHandler(ctx, nullConn, new(internalpb.RemoteReSpawnResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteReSpawnRequest{Host: host, Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteReSpawnHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteReSpawnRequest{Host: "10.0.0.1", Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteReSpawnHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("system actor name returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteReSpawnRequest{Host: host, Port: int32(port), Name: reservedNamesPrefix + "guard"}
		resp, err := sys.remoteReSpawnHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteReSpawnRequest{Host: host, Port: int32(port), Name: "missing"}
		resp, err := sys.remoteReSpawnHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})
}

func TestRemoteStopHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9004
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteStopHandler(ctx, nullConn, new(internalpb.RemoteStopResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteStopRequest{Host: host, Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteStopHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteStopRequest{Host: "10.0.0.1", Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteStopHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("system actor name returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteStopRequest{Host: host, Port: int32(port), Name: reservedNamesPrefix + "guard"}
		resp, err := sys.remoteStopHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteStopRequest{Host: host, Port: int32(port), Name: "missing"}
		resp, err := sys.remoteStopHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})
}

func TestRemoteSpawnHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9005
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteSpawnHandler(ctx, nullConn, new(internalpb.RemoteSpawnResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteSpawnRequest{Host: host, Port: int32(port), ActorName: "actor1", ActorType: "SomeType"}
		resp, err := sys.remoteSpawnHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteSpawnRequest{Host: "10.0.0.1", Port: int32(port), ActorName: "actor1", ActorType: "SomeType"}
		resp, err := sys.remoteSpawnHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("system actor name returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteSpawnRequest{
			Host: host, Port: int32(port),
			ActorName: reservedNamesPrefix + "guard",
			ActorType: "SomeType",
		}
		resp, err := sys.remoteSpawnHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("unregistered actor type returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.reflection = newReflection(types.NewRegistry())
		req := &internalpb.RemoteSpawnRequest{
			Host: host, Port: int32(port),
			ActorName: "actor1",
			ActorType: "UnregisteredType",
		}
		resp, err := sys.remoteSpawnHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})
}

func TestRemoteReinstateHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9006
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteReinstateHandler(ctx, nullConn, new(internalpb.RemoteReinstateResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteReinstateRequest{Host: host, Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteReinstateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteReinstateRequest{Host: "10.0.0.1", Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteReinstateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("system actor name returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteReinstateRequest{Host: host, Port: int32(port), Name: reservedNamesPrefix + "guard"}
		resp, err := sys.remoteReinstateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteReinstateRequest{Host: host, Port: int32(port), Name: "missing"}
		resp, err := sys.remoteReinstateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})
}

func TestRemoteAskGrainHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9007
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteAskGrainHandler(ctx, nullConn, new(internalpb.RemoteAskGrainResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteAskGrainRequest{
			Grain: &internalpb.Grain{Host: host, Port: int32(port)},
		}
		resp, err := sys.remoteAskGrainHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteAskGrainRequest{
			Grain: &internalpb.Grain{Host: "10.0.0.1", Port: int32(port)},
		}
		resp, err := sys.remoteAskGrainHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})
}

func TestRemoteTellGrainHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9008
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteTellGrainHandler(ctx, nullConn, new(internalpb.RemoteTellGrainResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteTellGrainRequest{
			Grain: &internalpb.Grain{Host: host, Port: int32(port)},
		}
		resp, err := sys.remoteTellGrainHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteTellGrainRequest{
			Grain: &internalpb.Grain{Host: "10.0.0.1", Port: int32(port)},
		}
		resp, err := sys.remoteTellGrainHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})
}

func TestRemoteActivateGrainHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9009
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteActivateGrainHandler(ctx, nullConn, new(internalpb.RemoteActivateGrainResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteActivateGrainRequest{
			Grain: &internalpb.Grain{Host: host, Port: int32(port)},
		}
		resp, err := sys.remoteActivateGrainHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteActivateGrainRequest{
			Grain: &internalpb.Grain{Host: "10.0.0.1", Port: int32(port)},
		}
		resp, err := sys.remoteActivateGrainHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})
}

func TestPersistPeerStateHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9010
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.persistPeerStateHandler(ctx, nullConn, new(internalpb.PersistPeerStateResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		resp, err := sys.persistPeerStateHandler(ctx, nullConn, new(internalpb.PersistPeerStateRequest))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("cluster disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		// remotingEnabled is true, clusterEnabled is false by default
		resp, err := sys.persistPeerStateHandler(ctx, nullConn, new(internalpb.PersistPeerStateRequest))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})
}

func TestGetNodeMetricHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9011
	ctx := context.Background()
	nodeAddr := fmt.Sprintf("%s:%d", host, port)

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.getNodeMetricHandler(ctx, nullConn, new(internalpb.GetNodeMetricResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("cluster disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.GetNodeMetricRequest{NodeAddress: nodeAddr}
		resp, err := sys.getNodeMetricHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched node address returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.clusterEnabled.Store(true)
		req := &internalpb.GetNodeMetricRequest{NodeAddress: "10.0.0.1:9999"}
		resp, err := sys.getNodeMetricHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("success returns node metric response", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.clusterEnabled.Store(true)
		req := &internalpb.GetNodeMetricRequest{NodeAddress: nodeAddr}
		resp, err := sys.getNodeMetricHandler(ctx, nullConn, req)
		require.NoError(t, err)
		metric, ok := resp.(*internalpb.GetNodeMetricResponse)
		require.True(t, ok)
		assert.Equal(t, nodeAddr, metric.GetNodeAddress())
		assert.Zero(t, metric.GetLoad())
	})
}

func TestGetKindsHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9012
	ctx := context.Background()
	nodeAddr := fmt.Sprintf("%s:%d", host, port)

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.getKindsHandler(ctx, nullConn, new(internalpb.GetKindsResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("cluster disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.GetKindsRequest{NodeAddress: nodeAddr}
		resp, err := sys.getKindsHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched node address returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.clusterEnabled.Store(true)
		sys.clusterConfig = NewClusterConfig()
		req := &internalpb.GetKindsRequest{NodeAddress: "10.0.0.1:9999"}
		resp, err := sys.getKindsHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("success returns list of registered kinds", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.clusterEnabled.Store(true)
		// NewClusterConfig pre-registers FuncActor; add one more to confirm the list grows.
		cfg := NewClusterConfig()
		cfg.kinds.Set("MockActor", NewMockActor())
		sys.clusterConfig = cfg
		req := &internalpb.GetKindsRequest{NodeAddress: nodeAddr}
		resp, err := sys.getKindsHandler(ctx, nullConn, req)
		require.NoError(t, err)
		kindsResp, ok := resp.(*internalpb.GetKindsResponse)
		require.True(t, ok)
		assert.NotEmpty(t, kindsResp.GetKinds())
	})
}

func TestRemoteAskGrainHandlerDeserializationError(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9007
	ctx := context.Background()

	t.Run("deserialization failure returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)

		mockSerializer := &testSerializer{err: fmt.Errorf("bad bytes")}
		mockClient := &testRemoteClient{serializer: mockSerializer}
		sys.remoting = mockClient

		req := &internalpb.RemoteAskGrainRequest{
			Grain: &internalpb.Grain{
				Host:    host,
				Port:    int32(port),
				GrainId: &internalpb.GrainId{Value: ""},
			},
			Message: []byte("garbage"),
		}
		resp, err := sys.remoteAskGrainHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("invalid grain identity returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)

		mockSerializer := &testSerializer{msg: new(internalpb.RemoteAskGrainResponse)}
		mockClient := &testRemoteClient{serializer: mockSerializer}
		sys.remoting = mockClient

		req := &internalpb.RemoteAskGrainRequest{
			Grain: &internalpb.Grain{
				Host:    host,
				Port:    int32(port),
				GrainId: &internalpb.GrainId{Value: "invalid-no-separator"},
			},
			Message: []byte("any"),
		}
		resp, err := sys.remoteAskGrainHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})
}

func TestRemoteTellGrainHandlerDeserializationError(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9008
	ctx := context.Background()

	t.Run("deserialization failure returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)

		mockSerializer := &testSerializer{err: fmt.Errorf("bad bytes")}
		mockClient := &testRemoteClient{serializer: mockSerializer}
		sys.remoting = mockClient

		req := &internalpb.RemoteTellGrainRequest{
			Grain: &internalpb.Grain{
				Host:    host,
				Port:    int32(port),
				GrainId: &internalpb.GrainId{Value: ""},
			},
			Message: []byte("garbage"),
		}
		resp, err := sys.remoteTellGrainHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("invalid grain identity returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)

		mockSerializer := &testSerializer{msg: new(internalpb.RemoteTellGrainResponse)}
		mockClient := &testRemoteClient{serializer: mockSerializer}
		sys.remoting = mockClient

		req := &internalpb.RemoteTellGrainRequest{
			Grain: &internalpb.Grain{
				Host:    host,
				Port:    int32(port),
				GrainId: &internalpb.GrainId{Value: "invalid-no-separator"},
			},
			Message: []byte("any"),
		}
		resp, err := sys.remoteTellGrainHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})
}

// testSerializer is a minimal remote.Serializer test double.
// If err is non-nil, Deserialize returns it. Otherwise it returns msg.
type testSerializer struct {
	msg any
	err error
}

func (s *testSerializer) Serialize(_ any) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []byte("serialized"), nil
}

func (s *testSerializer) Deserialize(_ []byte) (any, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.msg, nil
}

// testRemoteClient is a minimal remote.Client test double that returns a
// configured testSerializer from Serializer(). All other methods are no-ops.
type testRemoteClient struct {
	remoteclient.Client // embed to avoid implementing all interface methods
	serializer          remote.Serializer
}

func (c *testRemoteClient) Serializer(_ any) remote.Serializer {
	return c.serializer
}
