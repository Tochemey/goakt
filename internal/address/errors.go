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

package address

import "errors"

var (
	// ErrInvalidParent is returned when the parent address is invalid.
	ErrInvalidParent = errors.New("parent address is invalid")

	// ErrInvalidKinds is returned when the given address kind and its parent kind are different.
	ErrInvalidKinds = errors.New("child and parent kinds must be the same")

	// ErrInvalidHostAddress is returned when the given address host and its parent host are different.
	ErrInvalidHostAddress = errors.New("child and parent host addresses must be the same")

	// ErrInvalidName is returned when the given address name and its parent name are the same.
	ErrInvalidName = errors.New("child name and parent name must be different")

	// ErrInvalidActorSystem is returned when the given address actor system and its parent actor system are different.
	ErrInvalidActorSystem = errors.New("child and parent actor systems must be the same")
)
