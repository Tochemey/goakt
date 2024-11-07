/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package discovery

import "github.com/pkg/errors"

var (
	// ErrAlreadyInitialized is used when attempting to re-initialize the discovery provider
	ErrAlreadyInitialized = errors.New("provider already initialized")
	// ErrNotInitialized is used when the provider is not initialized
	ErrNotInitialized = errors.New("provider not initialized")
	// ErrAlreadyRegistered is used when attempting to re-register the provider
	ErrAlreadyRegistered = errors.New("provider already registered")
	// ErrNotRegistered is used when attempting to de-register the provider
	ErrNotRegistered = errors.New("provider is not registered")
	// ErrInvalidConfig is used when the discovery provider configuration is invalid
	ErrInvalidConfig = errors.New("invalid discovery provider configuration")
)
