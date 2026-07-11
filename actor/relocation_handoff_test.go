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
	stderrors "errors"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel/metric/noop"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/metric"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

// timeoutError is a net.Error whose Timeout reports true, used to exercise the
// network-timeout branch of isHandoffRetryable.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestIsHandoffRetryable(t *testing.T) {
	var netTimeout net.Error = timeoutError{}

	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "actor not found", err: gerrors.NewErrActorNotFound("a"), want: true},
		{name: "address not found", err: gerrors.ErrAddressNotFound, want: true},
		{name: "remote send failure", err: gerrors.ErrRemoteSendFailure, want: true},
		{name: "request timeout", err: gerrors.ErrRequestTimeout, want: true},
		{name: "relocation in progress", err: gerrors.ErrRelocationInProgress, want: true},
		{name: "context deadline", err: context.DeadlineExceeded, want: true},
		{name: "connection refused", err: syscall.ECONNREFUSED, want: true},
		{name: "network timeout", err: netTimeout, want: true},
		{name: "terminal error", err: stderrors.New("boom"), want: false},
		{name: "dead", err: gerrors.ErrDead, want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isHandoffRetryable(tc.err))
		})
	}
}

func TestSleepWithinHandoff(t *testing.T) {
	t.Run("returns false once the deadline has passed", func(t *testing.T) {
		assert.False(t, sleepWithinHandoff(context.Background(), time.Second, time.Now().Add(-time.Millisecond)))
	})

	t.Run("returns false when the context is done", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assert.False(t, sleepWithinHandoff(ctx, time.Second, time.Now().Add(time.Second)))
	})

	t.Run("returns true after sleeping when within the window", func(t *testing.T) {
		start := time.Now()
		assert.True(t, sleepWithinHandoff(context.Background(), 20*time.Millisecond, time.Now().Add(time.Second)))
		assert.GreaterOrEqual(t, time.Since(start), 15*time.Millisecond)
	})
}

func TestRelocatingEndpointTracking(t *testing.T) {
	system, err := NewActorSystem("test",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig("127.0.0.1", 8080)),
	)
	require.NoError(t, err)
	sys := system.(*actorSystem)

	peerAddress := net.JoinHostPort("127.0.0.9", "3320")
	const remotingPort = 7000

	t.Run("no-op when the remoting port is unknown", func(t *testing.T) {
		sys.markEndpointRelocating(peerAddress)
		assert.False(t, sys.relocationInFlight())
		assert.False(t, sys.isEndpointRelocating(address.New("a", "test", "127.0.0.9", remotingPort)))
	})

	t.Run("no-op on a malformed peer address", func(t *testing.T) {
		sys.markEndpointRelocating("not-an-address")
		assert.False(t, sys.relocationInFlight())
	})

	t.Run("marks and matches the resolved remoting endpoint", func(t *testing.T) {
		sys.peerRemotingPorts.Set(peerAddress, remotingPort)
		sys.markEndpointRelocating(peerAddress)

		assert.True(t, sys.relocationInFlight())
		assert.True(t, sys.isEndpointRelocating(address.New("a", "test", "127.0.0.9", remotingPort)))
		// a different endpoint on the same host is not masked
		assert.False(t, sys.isEndpointRelocating(address.New("a", "test", "127.0.0.9", remotingPort+1)))
		// nil address never matches
		assert.False(t, sys.isEndpointRelocating(nil))
	})

	t.Run("recovery closes the handoff window for a rejoined endpoint", func(t *testing.T) {
		sys.peerRemotingPorts.Set(peerAddress, remotingPort)
		sys.markEndpointRelocating(peerAddress)
		require.True(t, sys.isEndpointRelocating(address.New("a", "test", "127.0.0.9", remotingPort)))

		sys.markEndpointRecovered(peerAddress)

		assert.False(t, sys.isEndpointRelocating(address.New("a", "test", "127.0.0.9", remotingPort)))
		assert.False(t, sys.relocationInFlight())
	})

	t.Run("recovery is a no-op on a malformed or unknown address", func(t *testing.T) {
		sys.peerRemotingPorts.Set(peerAddress, remotingPort)
		sys.markEndpointRelocating(peerAddress)

		sys.markEndpointRecovered("not-an-address")
		sys.markEndpointRecovered(net.JoinHostPort("127.0.0.42", "9999"))

		assert.True(t, sys.isEndpointRelocating(address.New("a", "test", "127.0.0.9", remotingPort)))
	})
}

func TestRecordRelocationHandoff(t *testing.T) {
	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	sys := system.(*actorSystem)

	t.Run("no-op when metrics are disabled", func(t *testing.T) {
		require.Nil(t, sys.relocationMetric)
		assert.NotPanics(t, func() {
			sys.recordRelocationHandoff(context.Background())
		})
	})

	t.Run("records when metrics are enabled", func(t *testing.T) {
		m, merr := metric.NewRelocationMetric(noop.NewMeterProvider().Meter("test"))
		require.NoError(t, merr)
		sys.relocationMetric = m

		assert.NotPanics(t, func() {
			sys.recordRelocationHandoff(context.Background())
		})
	})
}

// fakeHandoffSystem is a minimal ActorSystem test double that implements only
// the methods deliverAcrossHandoff consults. Unimplemented methods panic via
// the nil embedded interface, so any accidental extra dependency is caught.
type fakeHandoffSystem struct {
	ActorSystem
	inCluster  bool
	resolve    func(attempt int) (*PID, error)
	relocating map[string]bool
	inFlight   bool
	handoffs   int
	attempts   int
}

func (f *fakeHandoffSystem) InCluster() bool { return f.inCluster }

func (f *fakeHandoffSystem) ActorOf(context.Context, string) (*PID, error) {
	f.attempts++
	return f.resolve(f.attempts)
}

func (f *fakeHandoffSystem) isEndpointRelocating(addr *address.Address) bool {
	if addr == nil {
		return false
	}
	return f.relocating[address.FormatHostPort(addr.Host(), addr.Port())]
}

func (f *fakeHandoffSystem) relocationInFlight() bool { return f.inFlight }

func (f *fakeHandoffSystem) recordRelocationHandoff(context.Context) { f.handoffs++ }

func remotePIDAt(host string, port int) *PID {
	return newRemotePID(address.New("target", "test", host, port), nil)
}

func TestDeliverAcrossHandoff(t *testing.T) {
	t.Run("not clustered delivers once and returns the result", func(t *testing.T) {
		to := remotePIDAt("127.0.0.1", 9001)
		sys := &fakeHandoffSystem{
			inCluster: false,
			resolve:   func(int) (*PID, error) { return to, nil },
		}
		pid := &PID{actorSystem: sys}

		delivered := 0
		resp, err := pid.deliverAcrossHandoff(context.Background(), "target", 0, func(context.Context, *PID) (any, error) {
			delivered++
			return "ok", nil
		})
		require.NoError(t, err)
		assert.Equal(t, "ok", resp)
		assert.Equal(t, 1, delivered)
		assert.Equal(t, 1, sys.attempts)
		assert.Equal(t, 0, sys.handoffs)
	})

	t.Run("not clustered surfaces the resolution error", func(t *testing.T) {
		sys := &fakeHandoffSystem{
			inCluster: false,
			resolve:   func(int) (*PID, error) { return nil, gerrors.NewErrActorNotFound("target") },
		}
		pid := &PID{actorSystem: sys}

		_, err := pid.deliverAcrossHandoff(context.Background(), "target", 0, func(context.Context, *PID) (any, error) {
			t.Fatal("deliver must not be called when resolution fails")
			return nil, nil
		})
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
	})

	t.Run("clustered live target delivers once without buffering", func(t *testing.T) {
		to := remotePIDAt("127.0.0.1", 9001)
		sys := &fakeHandoffSystem{
			inCluster: true,
			resolve:   func(int) (*PID, error) { return to, nil },
		}
		pid := &PID{actorSystem: sys}

		_, err := pid.deliverAcrossHandoff(context.Background(), "target", 0, func(context.Context, *PID) (any, error) {
			return nil, nil
		})
		require.NoError(t, err)
		assert.Equal(t, 1, sys.attempts)
		assert.Equal(t, 0, sys.handoffs)
	})

	t.Run("buffers a target pinned to a departing endpoint until it re-registers", func(t *testing.T) {
		departing := remotePIDAt("127.0.0.9", 7000)
		survivor := remotePIDAt("127.0.0.2", 9002)
		sys := &fakeHandoffSystem{
			inCluster:  true,
			relocating: map[string]bool{address.FormatHostPort("127.0.0.9", 7000): true},
			resolve: func(attempt int) (*PID, error) {
				if attempt >= 3 {
					return survivor, nil
				}
				return departing, nil
			},
		}
		pid := &PID{actorSystem: sys}

		delivered := 0
		var deliveredTo *PID
		resp, err := pid.deliverAcrossHandoff(context.Background(), "target", 0, func(_ context.Context, to *PID) (any, error) {
			delivered++
			deliveredTo = to
			return "done", nil
		})
		require.NoError(t, err)
		assert.Equal(t, "done", resp)
		assert.Equal(t, 1, delivered)
		assert.Same(t, survivor, deliveredTo)
		assert.Equal(t, 1, sys.handoffs, "the handoff must be recorded exactly once")
		assert.GreaterOrEqual(t, sys.attempts, 3)
	})

	t.Run("buffers a not-found resolution while a relocation is in flight", func(t *testing.T) {
		survivor := remotePIDAt("127.0.0.2", 9002)
		sys := &fakeHandoffSystem{
			inCluster: true,
			inFlight:  true,
			resolve: func(attempt int) (*PID, error) {
				if attempt >= 2 {
					return survivor, nil
				}
				return nil, gerrors.NewErrActorNotFound("target")
			},
		}
		pid := &PID{actorSystem: sys}

		_, err := pid.deliverAcrossHandoff(context.Background(), "target", 0, func(context.Context, *PID) (any, error) {
			return nil, nil
		})
		require.NoError(t, err)
		assert.Equal(t, 1, sys.handoffs)
		assert.GreaterOrEqual(t, sys.attempts, 2)
	})

	t.Run("fails fast on not-found when no relocation is in flight", func(t *testing.T) {
		sys := &fakeHandoffSystem{
			inCluster: true,
			inFlight:  false,
			resolve:   func(int) (*PID, error) { return nil, gerrors.NewErrActorNotFound("target") },
		}
		pid := &PID{actorSystem: sys}

		_, err := pid.deliverAcrossHandoff(context.Background(), "target", 0, func(context.Context, *PID) (any, error) {
			return nil, nil
		})
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Equal(t, 1, sys.attempts, "a steady-state miss must not retry")
		assert.Equal(t, 0, sys.handoffs)
	})

	t.Run("fails fast on a terminal (non-retryable) resolution error", func(t *testing.T) {
		boom := stderrors.New("boom")
		sys := &fakeHandoffSystem{
			inCluster: true,
			inFlight:  true, // in flight, but the error is not handoff-retryable
			resolve:   func(int) (*PID, error) { return nil, boom },
		}
		pid := &PID{actorSystem: sys}

		_, err := pid.deliverAcrossHandoff(context.Background(), "target", 0, func(context.Context, *PID) (any, error) {
			return nil, nil
		})
		require.ErrorIs(t, err, boom)
		assert.Equal(t, 1, sys.attempts)
		assert.Equal(t, 0, sys.handoffs)
	})

	t.Run("surfaces not-found when the window closes with the target still missing", func(t *testing.T) {
		sys := &fakeHandoffSystem{
			inCluster: true,
			inFlight:  true,
			resolve:   func(int) (*PID, error) { return nil, gerrors.NewErrActorNotFound("target") },
		}
		pid := &PID{actorSystem: sys}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
		defer cancel()

		_, err := pid.deliverAcrossHandoff(ctx, "target", 0, func(context.Context, *PID) (any, error) {
			t.Fatal("deliver must not be called while resolution keeps failing")
			return nil, nil
		})
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Equal(t, 1, sys.handoffs)
		assert.GreaterOrEqual(t, sys.attempts, 2)
	})

	t.Run("returns ErrRelocationInProgress when the window closes with the target still departing", func(t *testing.T) {
		departing := remotePIDAt("127.0.0.9", 7000)
		sys := &fakeHandoffSystem{
			inCluster:  true,
			relocating: map[string]bool{address.FormatHostPort("127.0.0.9", 7000): true},
			resolve:    func(int) (*PID, error) { return departing, nil },
		}
		pid := &PID{actorSystem: sys}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
		defer cancel()

		_, err := pid.deliverAcrossHandoff(ctx, "target", 0, func(context.Context, *PID) (any, error) {
			t.Fatal("deliver must not dial a departing endpoint")
			return nil, nil
		})
		require.ErrorIs(t, err, gerrors.ErrRelocationInProgress)
		assert.Equal(t, 1, sys.handoffs)
	})

	t.Run("terminal delivery error short-circuits", func(t *testing.T) {
		to := remotePIDAt("127.0.0.1", 9001)
		boom := stderrors.New("boom")
		sys := &fakeHandoffSystem{
			inCluster: true,
			resolve:   func(int) (*PID, error) { return to, nil },
		}
		pid := &PID{actorSystem: sys}

		_, err := pid.deliverAcrossHandoff(context.Background(), "target", 0, func(context.Context, *PID) (any, error) {
			return nil, boom
		})
		require.ErrorIs(t, err, boom)
		assert.Equal(t, 1, sys.attempts)
	})

	t.Run("delivery is bounded by the caller budget after masking", func(t *testing.T) {
		to := remotePIDAt("127.0.0.1", 9001)
		sys := &fakeHandoffSystem{
			inCluster: true,
			resolve:   func(int) (*PID, error) { return to, nil },
		}
		pid := &PID{actorSystem: sys}

		var gotDeadline bool
		var remaining time.Duration
		_, err := pid.deliverAcrossHandoff(context.Background(), "target", 200*time.Millisecond, func(dctx context.Context, _ *PID) (any, error) {
			dl, ok := dctx.Deadline()
			gotDeadline = ok
			if ok {
				remaining = time.Until(dl)
			}
			return nil, nil
		})
		require.NoError(t, err)
		// The final delivery must carry the caller's deadline so time already
		// spent masking is deducted from the delivery timeout rather than added
		// on top of it.
		assert.True(t, gotDeadline, "the delivery context must carry the caller's deadline")
		assert.LessOrEqual(t, remaining, 200*time.Millisecond)
		assert.Greater(t, remaining, time.Duration(0))
	})

	t.Run("caller timeout caps the handoff window", func(t *testing.T) {
		departing := remotePIDAt("127.0.0.9", 7000)
		sys := &fakeHandoffSystem{
			inCluster:  true,
			relocating: map[string]bool{address.FormatHostPort("127.0.0.9", 7000): true},
			resolve:    func(int) (*PID, error) { return departing, nil },
		}
		pid := &PID{actorSystem: sys}

		// A short maxWait must bound the loop well below the full handoff
		// window; with a background context the loop would otherwise run for
		// relocationHandoffWindow.
		start := time.Now()
		_, err := pid.deliverAcrossHandoff(context.Background(), "target", 80*time.Millisecond, func(context.Context, *PID) (any, error) {
			t.Fatal("deliver must not dial a departing endpoint")
			return nil, nil
		})
		elapsed := time.Since(start)
		require.ErrorIs(t, err, gerrors.ErrRelocationInProgress)
		assert.Less(t, elapsed, relocationHandoffWindow, "the caller timeout must cap the wait below the full handoff window")
		assert.Equal(t, 1, sys.handoffs)
	})

	t.Run("not-found masking budget starts at the first failed resolution", func(t *testing.T) {
		departing := remotePIDAt("127.0.0.9", 7000)
		survivor := remotePIDAt("127.0.0.2", 9002)
		start := time.Now()
		var sawNotFound atomic.Bool
		sys := &fakeHandoffSystem{
			inCluster:  true,
			inFlight:   true,
			relocating: map[string]bool{address.FormatHostPort("127.0.0.9", 7000): true},
			resolve: func(int) (*PID, error) {
				// Pinned to the departing endpoint well past the not-found mask
				// window, then the registry entry is removed (the remove-then-
				// respawn gap) before the survivor's entry appears. If the
				// not-found budget were anchored at the send's start it would
				// already be exhausted here and the send would fail spuriously.
				if time.Since(start) < 2*relocationNotFoundMaskWindow {
					return departing, nil
				}
				if sawNotFound.CompareAndSwap(false, true) {
					return nil, gerrors.NewErrActorNotFound("target")
				}
				return survivor, nil
			},
		}
		pid := &PID{actorSystem: sys}

		resp, err := pid.deliverAcrossHandoff(context.Background(), "target", 0, func(_ context.Context, to *PID) (any, error) {
			assert.Same(t, survivor, to)
			return "done", nil
		})
		require.NoError(t, err)
		assert.Equal(t, "done", resp)
		assert.True(t, sawNotFound.Load(), "the resolve sequence must have exercised the not-found branch")
	})
}

func TestDeliverBypassingHandoff(t *testing.T) {
	t.Run("not clustered delivers once and returns the result", func(t *testing.T) {
		to := remotePIDAt("127.0.0.1", 9001)
		sys := &fakeHandoffSystem{
			inCluster: false,
			resolve:   func(int) (*PID, error) { return to, nil },
		}
		pid := &PID{actorSystem: sys}

		resp, err := pid.deliverBypassingHandoff(context.Background(), "target", func(context.Context, *PID) (any, error) {
			return "ok", nil
		})
		require.NoError(t, err)
		assert.Equal(t, "ok", resp)
		assert.Equal(t, 1, sys.attempts)
	})

	t.Run("surfaces the resolution error without retrying", func(t *testing.T) {
		sys := &fakeHandoffSystem{
			inCluster: true,
			inFlight:  true, // even mid-relocation there is no masking here
			resolve:   func(int) (*PID, error) { return nil, gerrors.NewErrActorNotFound("target") },
		}
		pid := &PID{actorSystem: sys}

		_, err := pid.deliverBypassingHandoff(context.Background(), "target", func(context.Context, *PID) (any, error) {
			t.Fatal("deliver must not be called when resolution fails")
			return nil, nil
		})
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Equal(t, 1, sys.attempts, "a non-blocking send must never retry")
		assert.Equal(t, 0, sys.handoffs)
	})

	t.Run("fails fast with ErrRelocationInProgress on a departing endpoint", func(t *testing.T) {
		departing := remotePIDAt("127.0.0.9", 7000)
		sys := &fakeHandoffSystem{
			inCluster:  true,
			relocating: map[string]bool{address.FormatHostPort("127.0.0.9", 7000): true},
			resolve:    func(int) (*PID, error) { return departing, nil },
		}
		pid := &PID{actorSystem: sys}

		start := time.Now()
		_, err := pid.deliverBypassingHandoff(context.Background(), "target", func(context.Context, *PID) (any, error) {
			t.Fatal("deliver must not dial a departing endpoint")
			return nil, nil
		})
		require.ErrorIs(t, err, gerrors.ErrRelocationInProgress)
		assert.Equal(t, 1, sys.attempts)
		assert.Equal(t, 0, sys.handoffs, "a fail-fast send is not a buffered handoff")
		assert.Less(t, time.Since(start), relocationHandoffMinBackoff, "the send must not sleep")
	})

	t.Run("clustered live target delivers and surfaces the outcome", func(t *testing.T) {
		to := remotePIDAt("127.0.0.1", 9001)
		boom := stderrors.New("boom")
		sys := &fakeHandoffSystem{
			inCluster: true,
			resolve:   func(int) (*PID, error) { return to, nil },
		}
		pid := &PID{actorSystem: sys}

		_, err := pid.deliverBypassingHandoff(context.Background(), "target", func(context.Context, *PID) (any, error) {
			return nil, boom
		})
		require.ErrorIs(t, err, boom)
		assert.Equal(t, 1, sys.attempts)
	})
}
