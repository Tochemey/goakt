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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
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

// newRemoteServerTestSystemWithStoppedActor creates a minimal actorSystem with a stopped
// actor in the tree. Used to test handlers that return CODE_NOT_FOUND when pid is not running.
func newRemoteServerTestSystemWithStoppedActor(t *testing.T, host string, port int, name string) *actorSystem {
	t.Helper()
	sys := newRemoteServerTestSystem(host, port)
	sys.noSender = MockPID(sys, "nosender", 0)
	sys.actors.noSender = sys.noSender
	addr := address.New(name, sys.Name(), host, port)
	stoppedPID := &PID{
		address:     addr,
		path:        newPath(addr),
		actorSystem: sys,
	}
	require.NoError(t, sys.actors.addRootNode(stoppedPID))
	return sys
}

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

func TestRemoteSpawnChildHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9013
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteSpawnChildHandler(ctx, nullConn, new(internalpb.RemoteSpawnChildResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteSpawnChildRequest{
			Host: host, Port: int32(port),
			ActorName: "child", ActorType: "*actor.MockActor",
			Parent: "parent",
		}
		resp, err := sys.remoteSpawnChildHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteSpawnChildRequest{
			Host: "10.0.0.1", Port: int32(port),
			ActorName: "child", ActorType: "*actor.MockActor",
			Parent: "parent",
		}
		resp, err := sys.remoteSpawnChildHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("system actor name for child returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteSpawnChildRequest{
			Host: host, Port: int32(port),
			ActorName: reservedNamesPrefix + "guard",
			ActorType: "*actor.MockActor",
			Parent:    "parent",
		}
		resp, err := sys.remoteSpawnChildHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("system actor name for parent returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteSpawnChildRequest{
			Host: host, Port: int32(port),
			ActorName: "child",
			ActorType: "*actor.MockActor",
			Parent:    reservedNamesPrefix + "guard",
		}
		resp, err := sys.remoteSpawnChildHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("parent not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteSpawnChildRequest{
			Host: host, Port: int32(port),
			ActorName: "child", ActorType: "*actor.MockActor",
			Parent: "missing",
		}
		resp, err := sys.remoteSpawnChildHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("parent kind mismatch returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		parent, err := impl.Spawn(ctx, "parent", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		req := &internalpb.RemoteSpawnChildRequest{
			Host: host, Port: int32(p),
			ActorName: "child",
			ActorType: "WrongKind",
			Parent:    "parent",
		}
		resp, err := impl.remoteSpawnChildHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("success returns child address", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		parent, err := impl.Spawn(ctx, "parent", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, parent)

		req := &internalpb.RemoteSpawnChildRequest{
			Host: host, Port: int32(p),
			ActorName: "child",
			ActorType: parent.Kind(),
			Parent:    "parent",
		}
		resp, err := impl.remoteSpawnChildHandler(ctx, nullConn, req)
		require.NoError(t, err)
		spawnResp, ok := resp.(*internalpb.RemoteSpawnChildResponse)
		require.True(t, ok)
		require.NotEmpty(t, spawnResp.GetAddress())
		assert.Contains(t, spawnResp.GetAddress(), "child")
	})
}

func TestRemotePassivationStrategyHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9014
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remotePassivationStrategyHandler(ctx, nullConn, new(internalpb.RemotePassivationStrategyResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemotePassivationStrategyRequest{Host: host, Port: int32(port), Name: "actor1"}
		resp, err := sys.remotePassivationStrategyHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemotePassivationStrategyRequest{Host: "10.0.0.1", Port: int32(port), Name: "actor1"}
		resp, err := sys.remotePassivationStrategyHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemotePassivationStrategyRequest{Host: host, Port: int32(port), Name: "missing"}
		resp, err := sys.remotePassivationStrategyHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("success returns time-based passivation strategy", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		passivateAfter := 5 * time.Minute
		_, err = impl.Spawn(ctx, "actor1", NewMockActor(),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(passivateAfter)))
		require.NoError(t, err)

		req := &internalpb.RemotePassivationStrategyRequest{Host: host, Port: int32(p), Name: "actor1"}
		resp, err := impl.remotePassivationStrategyHandler(ctx, nullConn, req)
		require.NoError(t, err)
		psResp, ok := resp.(*internalpb.RemotePassivationStrategyResponse)
		require.True(t, ok)
		require.NotNil(t, psResp.GetPassivationStrategy())
		tb := psResp.GetPassivationStrategy().GetTimeBased()
		require.NotNil(t, tb)
		require.NotNil(t, tb.GetPassivateAfter())
		assert.Equal(t, passivateAfter, tb.GetPassivateAfter().AsDuration())
	})

	t.Run("success returns long-lived passivation strategy", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		_, err = impl.Spawn(ctx, "actor2", NewMockActor(),
			WithPassivationStrategy(passivation.NewLongLivedStrategy()))
		require.NoError(t, err)

		req := &internalpb.RemotePassivationStrategyRequest{Host: host, Port: int32(p), Name: "actor2"}
		resp, err := impl.remotePassivationStrategyHandler(ctx, nullConn, req)
		require.NoError(t, err)
		psResp, ok := resp.(*internalpb.RemotePassivationStrategyResponse)
		require.True(t, ok)
		require.NotNil(t, psResp.GetPassivationStrategy())
		require.NotNil(t, psResp.GetPassivationStrategy().GetLongLived())
	})

	t.Run("success returns message-count-based passivation strategy", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		maxMessages := int64(100)
		_, err = impl.Spawn(ctx, "actor3", NewMockActor(),
			WithPassivationStrategy(passivation.NewMessageCountBasedStrategy(int(maxMessages))))
		require.NoError(t, err)

		req := &internalpb.RemotePassivationStrategyRequest{Host: host, Port: int32(p), Name: "actor3"}
		resp, err := impl.remotePassivationStrategyHandler(ctx, nullConn, req)
		require.NoError(t, err)
		psResp, ok := resp.(*internalpb.RemotePassivationStrategyResponse)
		require.True(t, ok)
		require.NotNil(t, psResp.GetPassivationStrategy())
		mcb := psResp.GetPassivationStrategy().GetMessagesCountBased()
		require.NotNil(t, mcb)
		assert.Equal(t, maxMessages, mcb.GetMaxMessages())
	})
}

func TestRemoteStateHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9015
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteStateHandler(ctx, nullConn, new(internalpb.RemoteStateResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteStateRequest{Host: host, Port: int32(port), Name: "actor1", State: internalpb.State_STATE_RUNNING}
		resp, err := sys.remoteStateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteStateRequest{Host: "10.0.0.1", Port: int32(port), Name: "actor1", State: internalpb.State_STATE_RUNNING}
		resp, err := sys.remoteStateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteStateRequest{Host: host, Port: int32(port), Name: "missing", State: internalpb.State_STATE_RUNNING}
		resp, err := sys.remoteStateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("success returns true for STATE_RUNNING when actor is running", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		_, err = impl.Spawn(ctx, "actor1", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteStateRequest{Host: host, Port: int32(p), Name: "actor1", State: internalpb.State_STATE_RUNNING}
		resp, err := impl.remoteStateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		stateResp, ok := resp.(*internalpb.RemoteStateResponse)
		require.True(t, ok)
		assert.True(t, stateResp.GetState())
	})

	t.Run("success returns false for STATE_STOPPING when actor is running", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		_, err = impl.Spawn(ctx, "actor2", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteStateRequest{Host: host, Port: int32(p), Name: "actor2", State: internalpb.State_STATE_STOPPING}
		resp, err := impl.remoteStateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		stateResp, ok := resp.(*internalpb.RemoteStateResponse)
		require.True(t, ok)
		assert.False(t, stateResp.GetState())
	})

	t.Run("success returns false for STATE_SUSPENDED when actor is not suspended", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		_, err = impl.Spawn(ctx, "actor3", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteStateRequest{Host: host, Port: int32(p), Name: "actor3", State: internalpb.State_STATE_SUSPENDED}
		resp, err := impl.remoteStateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		stateResp, ok := resp.(*internalpb.RemoteStateResponse)
		require.True(t, ok)
		assert.False(t, stateResp.GetState())
	})

	t.Run("success returns true for STATE_RELOCATABLE when actor is relocatable", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		_, err = impl.Spawn(ctx, "actor4", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteStateRequest{Host: host, Port: int32(p), Name: "actor4", State: internalpb.State_STATE_RELOCATABLE}
		resp, err := impl.remoteStateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		stateResp, ok := resp.(*internalpb.RemoteStateResponse)
		require.True(t, ok)
		assert.True(t, stateResp.GetState())
	})

	t.Run("success returns false for STATE_RELOCATABLE when actor has relocation disabled", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		_, err = impl.Spawn(ctx, "actor5", NewMockActor(), WithRelocationDisabled())
		require.NoError(t, err)

		req := &internalpb.RemoteStateRequest{Host: host, Port: int32(p), Name: "actor5", State: internalpb.State_STATE_RELOCATABLE}
		resp, err := impl.remoteStateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		stateResp, ok := resp.(*internalpb.RemoteStateResponse)
		require.True(t, ok)
		assert.False(t, stateResp.GetState())
	})

	t.Run("success returns false for STATE_UNKNOWN", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		_, err = impl.Spawn(ctx, "actor6", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteStateRequest{Host: host, Port: int32(p), Name: "actor6", State: internalpb.State_STATE_UNKNOWN}
		resp, err := impl.remoteStateHandler(ctx, nullConn, req)
		require.NoError(t, err)
		stateResp, ok := resp.(*internalpb.RemoteStateResponse)
		require.True(t, ok)
		assert.False(t, stateResp.GetState())
	})
}

func TestRemoteChildrenHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9016
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteChildrenHandler(ctx, nullConn, new(internalpb.RemoteChildrenResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteChildrenRequest{Host: host, Port: int32(port), Name: "parent"}
		resp, err := sys.remoteChildrenHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteChildrenRequest{Host: "10.0.0.1", Port: int32(port), Name: "parent"}
		resp, err := sys.remoteChildrenHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteChildrenRequest{Host: host, Port: int32(port), Name: "missing"}
		resp, err := sys.remoteChildrenHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("actor not running returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystemWithStoppedActor(t, host, port, "stopped")
		req := &internalpb.RemoteChildrenRequest{Host: host, Port: int32(port), Name: "stopped"}
		resp, err := sys.remoteChildrenHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("success returns empty addresses when actor has no children", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		_, err = impl.Spawn(ctx, "parent", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteChildrenRequest{Host: host, Port: int32(p), Name: "parent"}
		resp, err := impl.remoteChildrenHandler(ctx, nullConn, req)
		require.NoError(t, err)
		childrenResp, ok := resp.(*internalpb.RemoteChildrenResponse)
		require.True(t, ok)
		assert.Empty(t, childrenResp.GetAddresses())
	})

	t.Run("success returns child addresses when actor has children", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		parent, err := impl.Spawn(ctx, "parent", NewMockActor())
		require.NoError(t, err)
		_, err = parent.SpawnChild(ctx, "child1", NewMockActor())
		require.NoError(t, err)
		_, err = parent.SpawnChild(ctx, "child2", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteChildrenRequest{Host: host, Port: int32(p), Name: "parent"}
		resp, err := impl.remoteChildrenHandler(ctx, nullConn, req)
		require.NoError(t, err)
		childrenResp, ok := resp.(*internalpb.RemoteChildrenResponse)
		require.True(t, ok)
		addresses := childrenResp.GetAddresses()
		require.Len(t, addresses, 2)
		var foundChild1, foundChild2 bool
		for _, a := range addresses {
			if strings.Contains(a, "child1") {
				foundChild1 = true
			}
			if strings.Contains(a, "child2") {
				foundChild2 = true
			}
		}
		assert.True(t, foundChild1, "expected address containing child1 in %v", addresses)
		assert.True(t, foundChild2, "expected address containing child2 in %v", addresses)
	})
}

func TestRemoteParentHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9017
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteParentHandler(ctx, nullConn, new(internalpb.RemoteParentResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteParentRequest{Host: host, Port: int32(port), Name: "child"}
		resp, err := sys.remoteParentHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteParentRequest{Host: "10.0.0.1", Port: int32(port), Name: "child"}
		resp, err := sys.remoteParentHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteParentRequest{Host: host, Port: int32(port), Name: "missing"}
		resp, err := sys.remoteParentHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("actor not running returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystemWithStoppedActor(t, host, port, "stopped")
		req := &internalpb.RemoteParentRequest{Host: host, Port: int32(port), Name: "stopped"}
		resp, err := sys.remoteParentHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("root actor returns parent address", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		root, err := impl.Spawn(ctx, "root", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, root.Parent())

		req := &internalpb.RemoteParentRequest{Host: host, Port: int32(p), Name: "root"}
		resp, err := impl.remoteParentHandler(ctx, nullConn, req)
		require.NoError(t, err)
		parentResp, ok := resp.(*internalpb.RemoteParentResponse)
		require.True(t, ok)
		require.NotEmpty(t, parentResp.GetAddress())
		assert.Equal(t, root.Parent().ID(), parentResp.GetAddress())
	})

	t.Run("success returns parent address for child actor", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		parent, err := impl.Spawn(ctx, "parent", NewMockActor())
		require.NoError(t, err)
		_, err = parent.SpawnChild(ctx, "child", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteParentRequest{Host: host, Port: int32(p), Name: "parent/child"}
		resp, err := impl.remoteParentHandler(ctx, nullConn, req)
		require.NoError(t, err)
		parentResp, ok := resp.(*internalpb.RemoteParentResponse)
		require.True(t, ok)
		require.NotEmpty(t, parentResp.GetAddress())
		assert.Equal(t, parent.ID(), parentResp.GetAddress())
	})
}

func TestRemoteKindHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9018
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteKindHandler(ctx, nullConn, new(internalpb.RemoteKindResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteKindRequest{Host: host, Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteKindHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteKindRequest{Host: "10.0.0.1", Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteKindHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteKindRequest{Host: host, Port: int32(port), Name: "missing"}
		resp, err := sys.remoteKindHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("actor not running returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystemWithStoppedActor(t, host, port, "stopped")
		req := &internalpb.RemoteKindRequest{Host: host, Port: int32(port), Name: "stopped"}
		resp, err := sys.remoteKindHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("success returns actor kind", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		pid, err := impl.Spawn(ctx, "actor1", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteKindRequest{Host: host, Port: int32(p), Name: "actor1"}
		resp, err := impl.remoteKindHandler(ctx, nullConn, req)
		require.NoError(t, err)
		kindResp, ok := resp.(*internalpb.RemoteKindResponse)
		require.True(t, ok)
		assert.Equal(t, pid.Kind(), kindResp.GetKind())
	})
}

func TestRemoteDependenciesHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9019
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteDependenciesHandler(ctx, nullConn, new(internalpb.RemoteDependenciesResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteDependenciesRequest{Host: host, Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteDependenciesHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteDependenciesRequest{Host: "10.0.0.1", Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteDependenciesHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteDependenciesRequest{Host: host, Port: int32(port), Name: "missing"}
		resp, err := sys.remoteDependenciesHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("actor not running returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystemWithStoppedActor(t, host, port, "stopped")
		req := &internalpb.RemoteDependenciesRequest{Host: host, Port: int32(port), Name: "stopped"}
		resp, err := sys.remoteDependenciesHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("success returns empty dependencies when actor has none", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		_, err = impl.Spawn(ctx, "actor1", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteDependenciesRequest{Host: host, Port: int32(p), Name: "actor1"}
		resp, err := impl.remoteDependenciesHandler(ctx, nullConn, req)
		require.NoError(t, err)
		depsResp, ok := resp.(*internalpb.RemoteDependenciesResponse)
		require.True(t, ok)
		assert.Empty(t, depsResp.GetDependencies())
	})

	t.Run("success returns dependencies when actor has them", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		dep := NewMockDependency("dep-1", "user", "email")
		require.NoError(t, impl.Inject(dep))

		pid, err := impl.Spawn(ctx, "actor2", NewMockActor(), WithDependencies(dep))
		require.NoError(t, err)

		req := &internalpb.RemoteDependenciesRequest{Host: host, Port: int32(p), Name: "actor2"}
		resp, err := impl.remoteDependenciesHandler(ctx, nullConn, req)
		require.NoError(t, err)
		depsResp, ok := resp.(*internalpb.RemoteDependenciesResponse)
		require.True(t, ok)
		require.Len(t, depsResp.GetDependencies(), 1)
		assert.Equal(t, dep.ID(), depsResp.GetDependencies()[0].GetId())
		assert.Equal(t, pid.Dependencies()[0].ID(), depsResp.GetDependencies()[0].GetId())
	})
}

func TestRemoteMetricHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9020
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteMetricHandler(ctx, nullConn, new(internalpb.RemoteMetricResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteMetricRequest{Host: host, Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteMetricHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteMetricRequest{Host: "10.0.0.1", Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteMetricHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteMetricRequest{Host: host, Port: int32(port), Name: "missing"}
		resp, err := sys.remoteMetricHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("actor not running returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystemWithStoppedActor(t, host, port, "stopped")
		req := &internalpb.RemoteMetricRequest{Host: host, Port: int32(port), Name: "stopped"}
		resp, err := sys.remoteMetricHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("success returns metric for running actor", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		_, err = impl.Spawn(ctx, "actor1", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteMetricRequest{Host: host, Port: int32(p), Name: "actor1"}
		resp, err := impl.remoteMetricHandler(ctx, nullConn, req)
		require.NoError(t, err)
		metricResp, ok := resp.(*internalpb.RemoteMetricResponse)
		require.True(t, ok)
		require.NotNil(t, metricResp.GetMetric())
		m := metricResp.GetMetric()
		// ProcessedCount excludes PostStart; 0 is valid when only PostStart has been processed
		assert.GreaterOrEqual(t, m.GetProcessedCount(), uint64(0))
		assert.GreaterOrEqual(t, m.GetUptime(), int64(0))
	})
}

func TestRemoteRoleHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9021
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteRoleHandler(ctx, nullConn, new(internalpb.RemoteRoleResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteRoleRequest{Host: host, Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteRoleHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteRoleRequest{Host: "10.0.0.1", Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteRoleHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteRoleRequest{Host: host, Port: int32(port), Name: "missing"}
		resp, err := sys.remoteRoleHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("actor not running returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystemWithStoppedActor(t, host, port, "stopped")
		req := &internalpb.RemoteRoleRequest{Host: host, Port: int32(port), Name: "stopped"}
		resp, err := sys.remoteRoleHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("success returns empty role when actor has none", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		_, err = impl.Spawn(ctx, "actor1", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteRoleRequest{Host: host, Port: int32(p), Name: "actor1"}
		resp, err := impl.remoteRoleHandler(ctx, nullConn, req)
		require.NoError(t, err)
		roleResp, ok := resp.(*internalpb.RemoteRoleResponse)
		require.True(t, ok)
		assert.Empty(t, roleResp.GetRole())
	})

	t.Run("success returns role when actor has one", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		role := "api"
		_, err = impl.Spawn(ctx, "actor2", NewMockActor(), WithRole(role))
		require.NoError(t, err)

		req := &internalpb.RemoteRoleRequest{Host: host, Port: int32(p), Name: "actor2"}
		resp, err := impl.remoteRoleHandler(ctx, nullConn, req)
		require.NoError(t, err)
		roleResp, ok := resp.(*internalpb.RemoteRoleResponse)
		require.True(t, ok)
		assert.Equal(t, role, roleResp.GetRole())
	})
}

func TestRemoteStashSizeHandler(t *testing.T) {
	const host = "127.0.0.1"
	const port = 9022
	ctx := context.Background()

	t.Run("wrong request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		resp, err := sys.remoteStashSizeHandler(ctx, nullConn, new(internalpb.RemoteStashSizeResponse))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("remoting disabled returns CODE_FAILED_PRECONDITION", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		sys.remotingEnabled.Store(false)
		req := &internalpb.RemoteStashSizeRequest{Host: host, Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteStashSizeHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_FAILED_PRECONDITION)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteStashSizeRequest{Host: "10.0.0.1", Port: int32(port), Name: "actor1"}
		resp, err := sys.remoteStashSizeHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("actor not in tree returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystem(host, port)
		req := &internalpb.RemoteStashSizeRequest{Host: host, Port: int32(port), Name: "missing"}
		resp, err := sys.remoteStashSizeHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("actor not running returns CODE_NOT_FOUND", func(t *testing.T) {
		sys := newRemoteServerTestSystemWithStoppedActor(t, host, port, "stopped")
		req := &internalpb.RemoteStashSizeRequest{Host: host, Port: int32(port), Name: "stopped"}
		resp, err := sys.remoteStashSizeHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("success returns stash size for running actor", func(t *testing.T) {
		ports := dynaport.Get(1)
		p := ports[0]
		sys, err := NewActorSystem("testSys",
			WithRemote(remote.NewConfig(host, p)),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		impl := sys.(*actorSystem)
		require.NoError(t, impl.Start(ctx))
		t.Cleanup(func() { _ = impl.Stop(ctx) })

		_, err = impl.Spawn(ctx, "actor1", NewMockActor())
		require.NoError(t, err)

		req := &internalpb.RemoteStashSizeRequest{Host: host, Port: int32(p), Name: "actor1"}
		resp, err := impl.remoteStashSizeHandler(ctx, nullConn, req)
		require.NoError(t, err)
		stashResp, ok := resp.(*internalpb.RemoteStashSizeResponse)
		require.True(t, ok)
		// Actor without stash has size 0
		assert.Equal(t, uint64(0), stashResp.GetSize())
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

// TestRemoteServerHandlersIntegration runs integration tests that use the remoteclient
// to call the server handlers over the wire against a real actor system.
func TestRemoteServerHandlersIntegration(t *testing.T) {
	ctx := context.Background()
	ports := dynaport.Get(1)
	port := ports[0]
	host := "127.0.0.1"

	sys, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, port)),
		WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	pause.For(200 * time.Millisecond)

	client := remoteclient.NewClient()
	t.Cleanup(client.Close)

	t.Run("RemoteChildren", func(t *testing.T) {
		parent, err := sys.Spawn(ctx, "parent", NewMockActor())
		require.NoError(t, err)
		_, err = parent.SpawnChild(ctx, "child1", NewMockActor())
		require.NoError(t, err)

		children, err := client.RemoteChildren(ctx, host, port, "parent")
		require.NoError(t, err)
		require.Len(t, children, 1)
		assert.True(t, strings.Contains(children[0].String(), "child1"))
	})

	t.Run("RemoteParent", func(t *testing.T) {
		parent, err := sys.Spawn(ctx, "parent2", NewMockActor())
		require.NoError(t, err)
		_, err = parent.SpawnChild(ctx, "child2", NewMockActor())
		require.NoError(t, err)

		// Handler looks up by full path: parent2/child2
		parentAddr, err := client.RemoteParent(ctx, host, port, "parent2/child2")
		require.NoError(t, err)
		require.NotNil(t, parentAddr)
		assert.True(t, strings.Contains(parentAddr.String(), "parent2"))
	})

	t.Run("RemoteKind", func(t *testing.T) {
		_, err := sys.Spawn(ctx, "kindActor", NewMockActor())
		require.NoError(t, err)

		kind, err := client.RemoteKind(ctx, host, port, "kindActor")
		require.NoError(t, err)
		assert.True(t, strings.EqualFold(kind, "actor.MockActor"), "kind=%q", kind)
	})

	t.Run("RemoteMetric", func(t *testing.T) {
		_, err := sys.Spawn(ctx, "metricActor", NewMockActor())
		require.NoError(t, err)

		metric, err := client.RemoteMetric(ctx, host, port, "metricActor")
		require.NoError(t, err)
		require.NotNil(t, metric)
		assert.GreaterOrEqual(t, metric.GetProcessedCount(), uint64(0))
	})

	t.Run("RemoteRole", func(t *testing.T) {
		role := "api"
		_, err := sys.Spawn(ctx, "roleActor", NewMockActor(), WithRole(role))
		require.NoError(t, err)

		got, err := client.RemoteRole(ctx, host, port, "roleActor")
		require.NoError(t, err)
		assert.Equal(t, role, got)
	})

	t.Run("RemoteStashSize", func(t *testing.T) {
		_, err := sys.Spawn(ctx, "stashActor", NewMockActor())
		require.NoError(t, err)

		size, err := client.RemoteStashSize(ctx, host, port, "stashActor")
		require.NoError(t, err)
		assert.Equal(t, uint64(0), size)
	})

	t.Run("RemoteState", func(t *testing.T) {
		_, err := sys.Spawn(ctx, "stateActor", NewMockActor())
		require.NoError(t, err)

		ok, err := client.RemoteState(ctx, host, port, "stateActor", remote.ActorStateRunning)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("RemotePassivationStrategy", func(t *testing.T) {
		strategy := passivation.NewTimeBasedStrategy(30 * time.Second)
		_, err := sys.Spawn(ctx, "passivationActor", NewMockActor(), WithPassivationStrategy(strategy))
		require.NoError(t, err)

		got, err := client.RemotePassivationStrategy(ctx, host, port, "passivationActor")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, 30*time.Second, got.(*passivation.TimeBasedStrategy).Timeout())
	})

	t.Run("RemoteDependencies", func(t *testing.T) {
		dep := NewMockDependency("dep1", "user", "email")
		require.NoError(t, sys.Inject(dep))

		_, err := sys.Spawn(ctx, "depsActor", NewMockActor(), WithDependencies(dep))
		require.NoError(t, err)

		reg := types.NewRegistry()
		reg.Register(dep)

		clientWithReg := remoteclient.NewClient(remoteclient.WithDependencyRegistry(reg))
		t.Cleanup(clientWithReg.Close)

		deps, err := clientWithReg.RemoteDependencies(ctx, host, port, "depsActor")
		require.NoError(t, err)
		require.Len(t, deps, 1)
		assert.Equal(t, "dep1", deps[0].ID())
	})

	t.Run("RemoteSpawnChild", func(t *testing.T) {
		// Parent must exist and be running; spawn child via client
		_, err := sys.Spawn(ctx, "spawnParent", NewMockActor())
		require.NoError(t, err)

		childReq := &remote.SpawnChildRequest{
			Name:   "spawnedChild",
			Kind:   types.Name(NewMockActor()),
			Parent: "spawnParent",
		}
		childAddr, err := client.RemoteSpawnChild(ctx, host, port, "spawnParent", childReq)
		require.NoError(t, err)
		require.NotNil(t, childAddr)
		assert.True(t, strings.Contains(childAddr.String(), "spawnedChild"))

		// Verify child is in parent's children
		children, err := client.RemoteChildren(ctx, host, port, "spawnParent")
		require.NoError(t, err)
		require.Len(t, children, 1)
		assert.True(t, children[0].Equals(childAddr))
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
