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

import "google.golang.org/protobuf/proto"

// GrainResponse represents the response returned from a Grain after processing a request.
//
// It wraps a protobuf message that contains the actual response payload from the Grain.
// Use the Message method to access the underlying proto.Message.
type GrainResponse struct {
	message proto.Message
}

// Message returns the underlying protobuf message contained in the GrainResponse.
//
// This method provides access to the actual response payload sent by the Grain.
func (resp *GrainResponse) Message() proto.Message {
	return resp.message
}

// NewGrainResponse creates a new GrainResponse with the given protobuf message.
//
// Parameters:
//   - message: the proto.Message to wrap in the GrainResponse.
//
// Returns:
//   - A pointer to a new GrainResponse containing the provided message.
func NewGrainResponse(message proto.Message) *GrainResponse {
	return &GrainResponse{
		message: message,
	}
}
