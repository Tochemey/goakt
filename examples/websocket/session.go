/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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

package main

import (
	"context"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/pb/v1"
	"github.com/tochemey/goakt/log"
)

type Session struct {
	wsConnection *websocket.Conn
	sessionID    string
	server       actors.PID
	self         actors.PID
	logger       log.Logger
}

var _ actors.Actor = &Session{}

// NewSession creates an instance of Session
func NewSession(sessionID string, connection *websocket.Conn, server actors.PID) *Session {
	return &Session{
		wsConnection: connection,
		sessionID:    sessionID,
		server:       server,
		logger:       log.DefaultLogger,
	}
}

// PreStart handles pre-start process
func (s *Session) PreStart(ctx context.Context) error {
	return nil
}

// Receive handle messages sent to the server
func (s *Session) Receive(ctx actors.ReceiveContext) {
	switch m := ctx.Message().(type) {
	case *samplepb.StartSession:
		// set the self
		s.self = ctx.Self()
		// start listening
		go s.read()
	case *samplepb.Broadcast:
		// write the message to the underlying connection
		bytea, err := proto.Marshal(m)
		// handle the error
		if err != nil {
			s.logger.Error(errors.Wrap(err, "failed to write message to the underlying connection"))
			// mark the message unhandled
			ctx.Unhandled()
			return
		}

		// write the message
		if err := s.wsConnection.WriteMessage(websocket.BinaryMessage, bytea); err != nil {
			s.logger.Error(errors.Wrap(err, "failed to write message to the underlying connection"))
			// mark the message unhandled
			ctx.Unhandled()
			return
		}

	default:
		ctx.Unhandled()
	}
}

// PostStop handles post shutdown process
func (s *Session) PostStop(ctx context.Context) error {
	// close the socket connection
	return s.wsConnection.Close()
}

func (s *Session) read() {
	for {
		// read the connection message
		_, bytea, err := s.wsConnection.ReadMessage()
		// handle the error
		if err != nil {
			s.logger.Error(errors.Wrap(err, "failed to read message"))
			return
		}
		// unmarshal the message
		message := new(samplepb.Broadcast)
		if err := proto.Unmarshal(bytea, message); err != nil {
			s.logger.Error(errors.Wrap(err, "failed to read message"))
			return
		}

		// hande the message given the type
		switch message.BroadCastType {
		case samplepb.BroadCastType_BROAD_CAST_TYPE_EXIT:
			_ = s.self.Shutdown(context.Background())
		default:
			// send a new message to broadcast
			message.SessionId = s.sessionID
			if err := s.self.Tell(context.Background(), s.server, message); err != nil {
				s.logger.Error(errors.Wrap(err, "failed to send a message to broadcast"))
				return
			}
		}
	}
}
