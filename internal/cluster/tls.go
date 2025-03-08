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

package cluster

import (
	"crypto/tls"

	"github.com/tochemey/goakt/v3/internal/validation"
)

// TLS defines both server and client TLS configuration
type TLS struct {
	server *tls.Config
	client *tls.Config
}

// ensure TLS implements the validation.Validator interface
var _ validation.Validator = (*TLS)(nil)

// NewTLS creates a new TLS configuration with the given server and client configurations.
func NewTLS(server, client *tls.Config) *TLS {
	return &TLS{
		server: server,
		client: client,
	}
}

// Server returns the server TLS configuration.
func (t *TLS) Server() *tls.Config {
	if t == nil {
		return nil
	}
	return t.server
}

// Client returns the client TLS configuration.
func (t *TLS) Client() *tls.Config {
	if t == nil {
		return nil
	}
	return t.client
}

func (t *TLS) Validate() error {
	if t.server == nil || t.client == nil {
		return ErrInvalidTLSConfiguration
	}

	return nil
}
