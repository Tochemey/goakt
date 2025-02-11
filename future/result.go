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

package future

import "google.golang.org/protobuf/proto"

// Result represents the outcome of a completed Future.
//
// It encapsulates both the success and failure states, ensuring that
// a Future can communicate either a valid result or an error.
//
// If the task succeeds, Success returns the result, and Failure returns nil.
// If the task fails, Failure returns the error, and Success returns nil.
type Result struct {
	success proto.Message
	failure error
}

// Success returns the successful result of the Future, if available.
//
// If the task failed, this method returns nil. Call Failure to check for errors.
func (x *Result) Success() proto.Message {
	return x.success
}

// Failure returns the error encountered during task execution, if any.
//
// If the task completed successfully, this method returns nil. Call Success
// to retrieve the result in case of success.
func (x *Result) Failure() error {
	return x.failure
}
