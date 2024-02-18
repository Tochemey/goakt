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
	"fmt"
	"net/http"
	"sync"

	"github.com/caarlos0/env/v10"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/pb/v1"
	"github.com/tochemey/goakt/log"
)

const (
	wsPath = "/ws"
)

// Config defines the server config
type Config struct {
	Port int32 `env:"PORT"`
}

// getConfig returns the server config
func getConfig() (*Config, error) {
	// load the configuration
	config := &Config{}
	opts := env.Options{RequiredIfNoDef: true, UseFieldNameByDefault: false}
	if err := env.ParseWithOptions(config, opts); err != nil {
		return nil, err
	}
	return config, nil
}

// Server represents the websocket server
// This is a wrapper around the gorilla websocket: https://github.com/gorilla/websocket
type Server struct {
	// specifies the websocket server port
	port int32
	// specifies client sessions
	sessions map[string]actors.PID
	// specifies the logger
	logger log.Logger

	// pid represents the server PID
	pid actors.PID

	mu *sync.RWMutex
}

// make sure that Server implements the actors.Actor interface fully
// or throw a compilation error
var _ actors.Actor = &Server{}

// NewServer creates an instance of Server
func NewServer() *Server {
	return &Server{
		logger: log.DefaultLogger,
		mu:     &sync.RWMutex{},
	}
}

// PreStart handles pre-start process
func (x *Server) PreStart(ctx context.Context) error {
	// let us load the config
	config, err := getConfig()
	// handle the error
	if err != nil {
		x.logger.Error(errors.Wrap(err, "failed to load the WebSocket Server configuration"))
		return err
	}

	// set the port
	x.port = config.Port
	return nil
}

// Receive handle messages sent to the server
func (x *Server) Receive(ctx actors.ReceiveContext) {
	switch m := ctx.Message().(type) {
	case *samplepb.StartServer:
		x.pid = ctx.Self()
		go x.serve()
	case *samplepb.StopServer:
		ctx.Shutdown()
	case *samplepb.Broadcast:
		// sender
		sender := ctx.Sender()
		// acquire the lock
		x.mu.RLock()
		pid, ok := x.sessions[m.GetSessionId()]
		x.mu.RUnlock()
		// found the session
		if ok {
			// TODO: add Equal method to PID
			if sender.ActorPath().String() != pid.ActorPath().String() {
				// forward the message
				ctx.Forward(pid)
			}
		}
	default:
		ctx.Unhandled()
	}
}

// PostStop handles post shutdown process
func (x *Server) PostStop(ctx context.Context) error {
	return nil
}

// serve http request
func (x *Server) serve() {
	go func() {
		http.HandleFunc(wsPath, x.handleWebsocket)
		x.logger.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", x.port), nil))
	}()
}

// handleWebsocket handles websocket requests
func (x *Server) handleWebsocket(writer http.ResponseWriter, request *http.Request) {
	// acquire the lock
	x.mu.Lock()
	// release the lock
	defer x.mu.Unlock()

	// add some debug logging
	x.logger.Debug("new client connection")

	// get a websocket websocket upgrader
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
	// get the connection
	conn, err := upgrader.Upgrade(writer, request, nil)
	// handle the error
	if err != nil {
		x.logger.Error(errors.Wrap(err, "failed to get the websocket client connection"))
		return
	}

	// get the client ID from the incoming request
	sessionID := request.URL.Query().Get("sessionId")
	// create a session
	session := NewSession(sessionID, conn, x.pid)
	// create a session by spawn a child actor for the session
	cid, err := x.pid.SpawnChild(request.Context(), sessionID, session)
	if err != nil {
		x.logger.Error(errors.Wrap(err, "failed to create a websocket client connection"))
		return
	}
	// start the session
	if err := x.pid.Tell(request.Context(), cid, new(samplepb.StartSession)); err != nil {
		x.logger.Error(errors.Wrap(err, "failed to start the session"))
		return
	}
	// add the session to the map
	x.sessions[sessionID] = cid
}
