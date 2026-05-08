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

package stream

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/flowchartsman/retry"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	// Short prefixes keep endpoint names compact (~16 chars total). Long
	// names interact badly with the cluster registry lookup path; the
	// prefix is only retained for diagnostic clarity in logs.
	sourceRefNamePrefix = "src-ref-"
	sinkRefNamePrefix   = "sink-ref-"
)

// SourceRef is a wire-portable handle to a stream source endpoint running
// on a specific node. Use ref.Source(sys) to plug it back into a graph as a
// Source[T]. Refs can be sent inside any registered message and across
// nodes; the receiving node resolves the endpoint by direct address (no
// reliance on async cluster broadcast). One subscription per ref.
//
// Name / Host / Port together identify the endpoint actor and are exported
// only so the GoAkt CBOR serializer can marshal them via reflection —
// callers must not construct refs by hand.
type SourceRef[T any] struct {
	Name string
	Host string
	Port int
}

// SinkRef is the symmetric handle for a stream sink endpoint. See SourceRef
// for the lifecycle and serialisation contract.
type SinkRef[T any] struct {
	Name string
	Host string
	Port int
}

// SourceRef publishes the source as a wire-portable SourceRef[T] that can be
// sent to any node and adapted back into a Source[T] via ref.Source(sys).
// The underlying source pipeline is materialised lazily, only when a
// subscriber connects: the ref itself is cheap to create and ship.
//
// The endpoint accepts a single subscription. Subsequent ref.Source(...)
// materialisations receive a stream-level error.
func (s Source[T]) SourceRef(ctx context.Context, sys actor.ActorSystem) (SourceRef[T], error) {
	name := newSourceRefName()
	endpoint := newSourceRefEndpointActor[T](s.stages)
	if _, err := sys.Spawn(ctx, name, endpoint, actor.WithLongLived()); err != nil {
		return SourceRef[T]{}, fmt.Errorf("stream: source ref: %w", err)
	}
	return SourceRef[T]{Name: name, Host: sys.Host(), Port: sys.Port()}, nil
}

// SinkRef publishes the sink as a wire-portable SinkRef[T] that can be sent
// to any node and adapted back into a Sink[T] via ref.Sink(sys). The
// underlying sink pipeline is materialised lazily, only when a producer
// connects.
func (s Sink[T]) SinkRef(ctx context.Context, sys actor.ActorSystem) (SinkRef[T], error) {
	name := newSinkRefName()
	endpoint := newSinkRefEndpointActor[T](s.desc)
	if _, err := sys.Spawn(ctx, name, endpoint, actor.WithLongLived()); err != nil {
		return SinkRef[T]{}, fmt.Errorf("stream: sink ref: %w", err)
	}
	return SinkRef[T]{Name: name, Host: sys.Host(), Port: sys.Port()}, nil
}

// Source adapts the ref into a Source[T] usable in any local graph on sys.
// Resolution happens at materialisation time: the bridge looks up the
// endpoint by direct address. Cross-node lookups talk to the producer node's
// remote server directly — no cluster registry / olric replication is on
// the critical path, so resolution succeeds as soon as the producer is
// reachable rather than after async broadcast propagates.
func (r SourceRef[T]) Source(sys actor.ActorSystem) Source[T] {
	config := defaultStageConfig()
	config.System = sys
	name := r.Name
	host := r.Host
	port := r.Port
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newRemoteSourceBridgeActor[T](name, host, port, cfg)
		},
		config: config,
	}
	return Source[T]{stages: []*stageDesc{desc}}
}

// Sink adapts the ref into a Sink[T] usable in any local graph on sys. The
// producer-side bridge ships every element it consumes to the endpoint over
// the wire and translates wire credit into local upstream demand. Like
// SourceRef.Source, resolution is by direct address — the consumer node's
// remote server is asked for the named actor without going through the
// cluster registry.
func (r SinkRef[T]) Sink(sys actor.ActorSystem) Sink[T] {
	config := defaultStageConfig()
	config.System = sys
	name := r.Name
	host := r.Host
	port := r.Port
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newRemoteSinkBridgeActor[T](name, host, port, cfg)
		},
		config: config,
	}
	return Sink[T]{desc: desc}
}

// RemoteOptions registers the stream package's control-plane wire protocol
// with the actor system's remoting configuration. Append the result to the
// rest of your remote.NewConfig options:
//
//	cfg := remote.NewConfig("0.0.0.0", 9000, append(
//	    []remote.Option{ /* your options */ },
//	    stream.RemoteOptions(),
//	)...)
//
// User element types T must additionally be registered with the same
// remoting layer (e.g. via remote.WithSerializables(new(MyEvent))) so that
// elements round-trip across nodes — they travel as ordinary remote messages
// without an extra envelope.
func RemoteOptions() remote.Option {
	return remote.WithSerializables(
		new(streamSubscribeWire),
		new(streamRequestWire),
		new(streamCompleteWire),
		new(streamErrorWire),
		new(streamCancelWire),
	)
}

func newSourceRefName() string { return sourceRefNamePrefix + newStageID() }
func newSinkRefName() string   { return sinkRefNamePrefix + newStageID() }

// wireForm returns a value suitable for sending over goakt remoting. The
// remoting layer keys its serializer registry on the exact type passed to
// remote.WithSerializables — typically a pointer (new(T) ⇒ *T) — so a value
// type would miss the lookup. Wrapping non-pointer values in a pointer makes
// the registered *T entry match without requiring users to register T twice.
// Already-pointer values are returned unchanged.
func wireForm(v any) any {
	if v == nil {
		return v
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		return v
	}
	p := reflect.New(rv.Type())
	p.Elem().Set(rv)
	return p.Interface()
}

// fromWire returns the user value carried by a remote message. The remoting
// layer's CBOR serializer auto-dereferences primitive types but returns
// non-primitive types as pointers, so a T-typed receiver must handle either
// form: T directly, or *T to deref. Returns the zero T and false when the
// message is neither.
func fromWire[T any](msg any) (T, bool) {
	if v, ok := msg.(T); ok {
		return v, true
	}
	if pv, ok := msg.(*T); ok && pv != nil {
		return *pv, true
	}
	var zero T
	return zero, false
}

// resolveEndpoint resolves a ref's endpoint actor by direct address.
//
// When the endpoint lives on the local node the local actor tree is
// queried; otherwise the bridge's own PID is used to issue a RemoteLookup
// against the producer node's remote server, which deterministically
// answers from its own actor tree without consulting any cluster registry.
// Eliminating the cluster-registry round-trip makes resolution independent
// of the asynchronous putActorOnCluster broadcast and the underlying olric
// replication latency, both of which can spike past 30s on a loaded CI host
// even though the actor exists and is reachable.
//
// A short retry loop (jittered exponential backoff via the same
// flowchartsman/retry package the rest of goakt uses) absorbs the brief
// window between the producer's Spawn and its remote server starting to
// answer for the new actor, and any transient connectivity failures.
func resolveEndpoint(ctx context.Context, sys actor.ActorSystem, self *actor.PID, host string, port int, name string) (*actor.PID, error) {
	const (
		maxAttempts    = 50
		initialBackoff = 100 * time.Millisecond
		maxBackoff     = time.Second
		totalBudget    = 10 * time.Second
	)

	// Local fast path: same node — skip remoting entirely.
	if host == sys.Host() && port == sys.Port() {
		return sys.ActorOf(ctx, name)
	}

	ctx, cancel := context.WithTimeout(ctx, totalBudget)
	defer cancel()

	var pid *actor.PID
	retrier := retry.NewRetrier(maxAttempts, initialBackoff, maxBackoff)
	err := retrier.RunContext(ctx, func(ctx context.Context) error {
		var err error
		pid, err = self.RemoteLookup(ctx, host, port, name)
		return err
	})
	if err != nil {
		return nil, err
	}
	return pid, nil
}
