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

package multidatacentermanager

import (
	"context"
	"time"

	gerrors "github.com/tochemey/goakt/v3/errors"
)

// MockControlPlane is a test double for ControlPlane with overridable hooks.
type MockControlPlane struct {
	registerFn   func(context.Context, DataCenterRecord) (string, uint64, error)
	heartbeatFn  func(context.Context, string, uint64) (uint64, time.Time, error)
	setStateFn   func(context.Context, string, DataCenterState, uint64) (uint64, error)
	listActiveFn func(context.Context) ([]DataCenterRecord, error)
	watchFn      func(context.Context) (<-chan ControlPlaneEvent, error)
}

func (m *MockControlPlane) Register(ctx context.Context, record DataCenterRecord) (string, uint64, error) {
	if m.registerFn != nil {
		return m.registerFn(ctx, record)
	}
	id := record.ID
	if id == "" {
		id = "dc-1"
	}
	return id, 1, nil
}

func (m *MockControlPlane) Heartbeat(ctx context.Context, id string, version uint64) (uint64, time.Time, error) {
	if m.heartbeatFn != nil {
		return m.heartbeatFn(ctx, id, version)
	}
	return version + 1, time.Now(), nil
}

func (m *MockControlPlane) SetState(ctx context.Context, id string, state DataCenterState, version uint64) (uint64, error) {
	if m.setStateFn != nil {
		return m.setStateFn(ctx, id, state, version)
	}
	return version + 1, nil
}

func (m *MockControlPlane) ListActive(ctx context.Context) ([]DataCenterRecord, error) {
	if m.listActiveFn != nil {
		return m.listActiveFn(ctx)
	}
	return nil, nil
}

func (m *MockControlPlane) Watch(ctx context.Context) (<-chan ControlPlaneEvent, error) {
	if m.watchFn != nil {
		return m.watchFn(ctx)
	}
	return nil, gerrors.ErrWatchNotSupported
}
