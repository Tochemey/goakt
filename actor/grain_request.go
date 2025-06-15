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
	"sync"

	"google.golang.org/protobuf/proto"
)

// pool holds a pool of ReceiveContext
var grainRequestPool = sync.Pool{
	New: func() any {
		return new(grainRequest)
	},
}

func getGrainRequest() *grainRequest {
	return grainRequestPool.Get().(*grainRequest)
}

// releaseContext sends the message context back to the pool
func releaseGrainRequest(request *grainRequest) {
	request.reset()
	grainRequestPool.Put(request)
}

type grainRequest struct {
	sender   *Identity
	message  proto.Message
	response chan *GrainResponse
	err      chan error
	ctx      context.Context
	sync     bool
}

func (req *grainRequest) getResponse() chan *GrainResponse {
	return req.response
}

func (req *grainRequest) getErr() chan error {
	return req.err
}

func (req *grainRequest) getSender() *Identity {
	return req.sender
}

func (req *grainRequest) getMessage() proto.Message {
	return req.message
}

func (req *grainRequest) getContext() context.Context {
	return req.ctx
}

func (req *grainRequest) isSynchronous() bool {
	return req.sync
}

// setResponse sets the message response
func (req *grainRequest) setResponse(resp *GrainResponse) {
	req.response <- resp
	close(req.response)
}

func (req *grainRequest) setError(err error) {
	req.err <- err
	close(req.err)
}

func (req *grainRequest) reset() {
	var id *Identity
	req.message = nil
	req.sender = id
	req.response = nil
	req.err = nil
	req.ctx = nil
	req.sync = false
}

func (req *grainRequest) build(ctx context.Context, sender *Identity, message proto.Message, sync bool) {
	req.ctx = ctx
	req.sender = sender
	req.message = message
	req.sync = sync
	req.err = make(chan error, 1)
	if req.sync {
		req.response = make(chan *GrainResponse, 1)
	}
}
