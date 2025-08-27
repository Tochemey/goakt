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
	"crypto/tls"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/tochemey/goakt/v3/internal/size"
)

type Conn struct {
	addr             string
	maxReceivMsgSize int
	maxSendMsgSize   int
	options          []grpc.DialOption
	tlsConfig        *tls.Config
}

// NewConn creates a new Conn instance with the provided address and options.
func NewConn(addr string, opts ...ConnOption) *Conn {
	const (
		defaultMaxMsgSize      = 10 * size.MB
		defaultBackoffMaxDelay = 5 * time.Second
		defaultKeepaliveTime   = 1200 * time.Second
	)

	// create Conn with sensible defaults; do NOT assemble DialOptions yet
	conn := &Conn{
		addr:             addr,
		maxReceivMsgSize: defaultMaxMsgSize,
		maxSendMsgSize:   defaultMaxMsgSize,
		options:          nil,
		tlsConfig:        nil,
	}

	// apply functional options to allow overriding defaults (e.g. tlsConfig, msg sizes)
	for _, opt := range opts {
		opt(conn)
	}

	// backoff config
	bc := backoff.DefaultConfig
	bc.MaxDelay = defaultBackoffMaxDelay

	// assemble DialOptions based on the final Conn state
	var dialOpts []grpc.DialOption

	dialOpts = append(dialOpts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:                defaultKeepaliveTime,
		PermitWithoutStream: true,
	}))

	dialOpts = append(dialOpts, grpc.WithConnectParams(grpc.ConnectParams{
		Backoff: bc,
	}))

	// transport credentials: prefer TLS when provided, otherwise fall back to insecure
	if conn.tlsConfig != nil {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(conn.tlsConfig)))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// set message size limits based on final values
	dialOpts = append(dialOpts, grpc.WithDefaultCallOptions(
		grpc.MaxCallRecvMsgSize(conn.maxReceivMsgSize),
		grpc.MaxCallSendMsgSize(conn.maxSendMsgSize),
	))

	conn.options = dialOpts
	return conn
}

// Dial establishes a gRPC connection using the configured address and options.
func (c *Conn) Dial() (*grpc.ClientConn, error) {
	if c.addr == "" {
		return nil, ErrEmptyAddress
	}
	return grpc.NewClient(c.addr, c.options...)
}
