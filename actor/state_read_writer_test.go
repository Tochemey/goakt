/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actor

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/internal/collection/syncmap"
)

type mockStateReaderWriter struct {
	db *syncmap.Map[string, proto.Message]
}

func newMockStateReaderWriter() *mockStateReaderWriter {
	return &mockStateReaderWriter{
		db: syncmap.New[string, proto.Message](),
	}
}

// nolint
func (x *mockStateReaderWriter) WriteState(ctx context.Context, persistenceID string, state proto.Message) error {
	x.db.Set(persistenceID, state)
	return nil
}

// nolint
func (x *mockStateReaderWriter) GetState(ctx context.Context, persistenceID string) (proto.Message, error) {
	if val, ok := x.db.Get(persistenceID); ok {
		return val, nil
	}
	return nil, errors.New("state not found")
}

func (x *mockStateReaderWriter) Close() {
	x.db.Reset()
}
