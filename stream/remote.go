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
	// Short prefixes that tag endpoint actor names for log readability.
	sourceRefNamePrefix = "src-ref-"
	sinkRefNamePrefix   = "sink-ref-"
)

// SourceRef is a wire-portable handle to a stream source endpoint. It can
// be sent inside any registered message and adapted back into a Source[T]
// via ref.Source(sys). Each ref accepts a single subscription.
//
// Name, Host and Port are exported only so the GoAkt CBOR serializer can
// marshal them; callers must not construct refs by hand.
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

// SourceRef publishes the source as a wire-portable SourceRef[T]. Creating
// the ref is cheap; the underlying pipeline is materialised lazily when a
// subscriber connects. Only one subscription is accepted — later
// materialisations receive a stream-level error.
func (s Source[T]) SourceRef(ctx context.Context, sys actor.ActorSystem) (SourceRef[T], error) {
	name := newSourceRefName()
	endpoint := newSourceRefEndpointActor[T](s.stages)
	if _, err := sys.Spawn(ctx, name, endpoint, actor.WithLongLived()); err != nil {
		return SourceRef[T]{}, fmt.Errorf("stream: source ref: %w", err)
	}
	return SourceRef[T]{Name: name, Host: sys.Host(), Port: sys.Port()}, nil
}

// SinkRef publishes the sink as a wire-portable SinkRef[T]. The underlying
// pipeline is materialised lazily when a producer connects.
func (s Sink[T]) SinkRef(ctx context.Context, sys actor.ActorSystem) (SinkRef[T], error) {
	name := newSinkRefName()
	endpoint := newSinkRefEndpointActor[T](s.desc)
	if _, err := sys.Spawn(ctx, name, endpoint, actor.WithLongLived()); err != nil {
		return SinkRef[T]{}, fmt.Errorf("stream: sink ref: %w", err)
	}
	return SinkRef[T]{Name: name, Host: sys.Host(), Port: sys.Port()}, nil
}

// Source adapts the ref into a Source[T] usable in any local graph on sys.
// Endpoint resolution happens at materialisation time, by direct address
// against the producer node's remote server.
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
// producer-side bridge ships every consumed element to the endpoint and
// translates wire credit into local upstream demand. Endpoint resolution
// is by direct address, like SourceRef.Source.
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

// wireForm wraps non-pointer values in a pointer so the remoting serializer
// registry — keyed on the exact type passed to remote.WithSerializables,
// typically *T from new(T) — finds a match. Already-pointer values are
// returned unchanged.
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

// fromWire returns the user value carried by a remote message. The CBOR
// serializer delivers primitives as T and non-primitives as *T, so the
// receiver must handle both. Returns the zero T and false otherwise.
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

// resolveEndpoint returns the PID of the endpoint actor identified by
// host, port and name. Local refs are looked up via the actor tree; remote
// refs via RemoteLookup against the producer node's remote server. A short
// retry loop with exponential backoff covers the brief window between
// Spawn and the actor becoming reachable, and transient network failures.
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
