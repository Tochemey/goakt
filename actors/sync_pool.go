/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package actors

import (
	"context"
	"sync"

	"google.golang.org/protobuf/proto"
)

// syncPool is only implemented to house reusable
// ReceiveContext for PID to reduce GC activities
var syncPool = sync.Pool{
	New: func() any {
		return &ReceiveContext{
			response: make(chan proto.Message, 1),
		}
	},
}

// fromPool returns a ReceiveContext instance from the pool
func fromPool(ctx context.Context, from, to *PID, message proto.Message) *ReceiveContext {
	receiveContext := syncPool.Get().(*ReceiveContext)
	receiveContext.self = to
	receiveContext.sender = from
	receiveContext.ctx = ctx
	receiveContext.message = message
	return receiveContext
}

// toPool sends back to the pool the earlier ReceiveContext retrieved
func toPool(receiveContext *ReceiveContext) {
	syncPool.Put(receiveContext)
}
