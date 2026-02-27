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

package remoteclient

import (
	"context"
	"errors"
	nethttp "net/http"

	"github.com/tochemey/goakt/v4/extension"
)

// failingDependency implements extension.Dependency but MarshalBinary always fails.
// Used to exercise getGrainFromRequest's codec.EncodeDependencies error path.
type failingDependency struct{ err error }

func (f *failingDependency) ID() string { return "failing-dep" }

func (f *failingDependency) MarshalBinary() ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return nil, errors.New("marshal failed")
}

func (f *failingDependency) UnmarshalBinary([]byte) error { return nil }

var _ extension.Dependency = (*failingDependency)(nil)

// nonProtoMsg is an arbitrary type with no registered serializer.
type nonProtoMsg struct{ value string }

// testInterface and nonProtoImpl are used to verify interface-based serializer
// registration and forwarding via ClientSerializerOptions.
type testInterface interface{ testMarker() }

type nonProtoImpl struct{}

func (nonProtoImpl) testMarker() {}

type mockPropagator struct{}

func (mockPropagator) Inject(context.Context, nethttp.Header) error {
	return nil
}

func (mockPropagator) Extract(ctx context.Context, _ nethttp.Header) (context.Context, error) {
	return ctx, nil
}

// errInjectPropagator is a ContextPropagator whose Inject always returns an error.
type errInjectPropagator struct{}

func (errInjectPropagator) Inject(context.Context, nethttp.Header) error {
	return errors.New("inject error")
}

func (errInjectPropagator) Extract(ctx context.Context, _ nethttp.Header) (context.Context, error) {
	return ctx, nil
}

// headerPropagator injects a fixed header so the header-copy loop in enrichContext is exercised.
type headerPropagator struct{ key, value string }

func (h headerPropagator) Inject(_ context.Context, headers nethttp.Header) error {
	headers.Set(h.key, h.value)
	return nil
}

func (h headerPropagator) Extract(ctx context.Context, _ nethttp.Header) (context.Context, error) {
	return ctx, nil
}

// mockDependencyForRemote is a minimal Dependency for RemoteDependencies tests.
// It can be registered with types.Registry and decoded from internalpb.Dependency.
type mockDependencyForRemote struct {
	id string
}

func (m *mockDependencyForRemote) MarshalBinary() ([]byte, error) {
	return []byte(m.id), nil
}

func (m *mockDependencyForRemote) UnmarshalBinary(data []byte) error {
	m.id = string(data)
	return nil
}

func (m *mockDependencyForRemote) ID() string { return m.id }

var _ extension.Dependency = (*mockDependencyForRemote)(nil)
