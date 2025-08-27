/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package grpcc

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrEmptyAddress = errors.New("empty address")
)

// Error represents an error that carries both a gRPC status code and an
// optional underlying error.
//
// It is intended for use in gRPC services and clients to propagate
// structured errors while retaining the original cause. The error can be
// unwrapped (via errors.Unwrap) to access the underlying error, and it
// implements the GRPCStatus method so it can be returned directly from
// gRPC handlers.
//
// Example:
//
//	if err := doSomething(); err != nil {
//	    return nil, NewError(codes.InvalidArgument, err)
//	}
//
// When returned from a gRPC server method, the runtime will automatically
// convert it into a proper gRPC status error.
type Error struct {
	code       codes.Code
	underlying error
}

// NewError creates a new Error with the given gRPC status code and
// underlying error. The underlying error may be nil.
//
// Use this constructor when you want to associate a gRPC status code with
// an error that will be propagated through gRPC.
//
// Example:
//
//	return NewError(codes.NotFound, fmt.Errorf("user %q not found", id))
func NewError(code codes.Code, underlying error) *Error {
	return &Error{
		code:       code,
		underlying: underlying,
	}
}

// Error returns the string representation of the underlying error, if
// present. If no underlying error is set, it returns a generic
// "unknown grpc error" string.
//
// This makes *Error compatible with the built-in error interface.
func (e *Error) Error() string {
	if e.underlying != nil {
		return e.underlying.Error()
	}
	return "unknown grpc error"
}

// Unwrap returns the underlying error, if any. This enables integration
// with the standard library's errors package, allowing use of
// errors.Is and errors.As for error inspection.
func (e *Error) Unwrap() error {
	return e.underlying
}

// GRPCStatus returns a gRPC status constructed from the error's code and
// message. This makes *Error compatible with gRPC's status handling,
// allowing it to be returned directly from a gRPC service method.
//
// Example:
//
//	func (s *Server) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.User, error) {
//	    user, err := s.repo.Find(req.Id)
//	    if err != nil {
//	        return nil, NewError(codes.NotFound, err)
//	    }
//	    return user, nil
//	}
func (e *Error) GRPCStatus() *status.Status {
	return status.New(e.code, e.Error())
}

// Code returns the gRPC status code associated with this error.
//
// This is useful when you want to branch logic based on the gRPC code
// without converting the error to a *status.Status.
//
// Example:
//
//	if e, ok := err.(*Error); ok {
//	    switch e.Code() {
//	    case codes.NotFound:
//	        // handle not found
//	    case codes.PermissionDenied:
//	        // handle permission issues
//	    }
//	}
func (e *Error) Code() codes.Code {
	return e.code
}
