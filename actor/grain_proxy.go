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
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/internalpb"
)

// TellGrain sends an asynchronous message to a Grain (virtual actor) identified by the given identity.
//
// This method locates or activates the target Grain (locally or in the cluster) and delivers the provided
// protobuf message without waiting for a response. Use this for fire-and-forget scenarios where no reply is expected.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control.
//   - identity: The unique identity of the Grain.
//   - message: The protobuf message to send to the Grain.
//   - opts: Optional GrainOptions to configure the Grain (e.g., dependencies, timeout).
//
// Returns:
//   - error: An error if the message could not be delivered or the system is not started.
func (x *actorSystem) TellGrain(ctx context.Context, identity *Identity, message proto.Message, opts ...GrainOption) error {
	if !x.started.Load() {
		return ErrActorSystemNotStarted
	}

	// validate the identity
	if err := identity.Validate(); err != nil {
		return NewErrInvalidGrainIdentity(err)
	}

	// make sure we don't interfere with system actors.
	if isReservedName(identity.Name()) {
		return NewErrGrainNotFound(identity.String())
	}

	config := newGrainOptConfig(opts...)
	timeout := config.RequestTimeout()
	sender := config.RequestSender()
	if sender != nil {
		if err := sender.Validate(); err != nil {
			return NewErrInvalidGrainIdentity(err)
		}

		if isReservedName(sender.Name()) {
			return NewErrGrainNotFound(sender.String())
		}
	}

	if x.InCluster() {
		return x.remoteTellGrain(ctx, identity, message, sender, timeout)
	}
	_, err := x.localSend(ctx, identity, message, sender, timeout, false)
	return err
}

// AskGrain sends a request message to a Grain identified by the given identity.
//
// It locates or spawns the target Grain (locally or in the cluster), sends the provided
// protobuf message, and waits for a response or error. The request timeout and other
// options can be customized using GrainOptions.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - identity: unique identity string of the Grain.
//   - message: protobuf message to send to the Grain.
//   - opts: optional GrainOptions (e.g., dependencies, timeout).
//
// Returns:
//   - proto.Message: the response from the Grain, if successful.
//   - error: error if the request fails, times out, or the system is not started.
func (x *actorSystem) AskGrain(ctx context.Context, identity *Identity, message proto.Message, opts ...GrainOption) (response proto.Message, err error) {
	if !x.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	// validate the identity
	if err := identity.Validate(); err != nil {
		return nil, NewErrInvalidGrainIdentity(err)
	}

	// make sure we don't interfere with system actors.
	if isReservedName(identity.Name()) {
		return nil, NewErrGrainNotFound(identity.String())
	}

	config := newGrainOptConfig(opts...)
	timeout := config.RequestTimeout()
	sender := config.RequestSender()
	if sender != nil {
		if err = sender.Validate(); err != nil {
			return nil, NewErrInvalidGrainIdentity(err)
		}
		if isReservedName(sender.Name()) {
			return nil, NewErrGrainNotFound(sender.String())
		}
	}

	if x.InCluster() {
		return x.remoteAskGrain(ctx, identity, message, sender, timeout)
	}
	return x.localSend(ctx, identity, message, sender, timeout, true)
}

// RemoteTellGrain handles remote fire-and-forget messages to a Grain from another node.
//
// It validates the incoming request, locates or activates the target Grain, and delivers
// the provided message without waiting for a response. This is used internally for cluster
// communication to support asynchronous, one-way messaging between nodes.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - c: RemoteTellGrainRequest containing the target Grain info and message.
//
// Returns:
//   - *internalpb.RemoteTellGrainResponse: an empty response indicating delivery.
//   - error: error if the request is invalid or delivery fails.
func (x *actorSystem) RemoteTellGrain(ctx context.Context, request *connect.Request[internalpb.RemoteTellGrainRequest]) (*connect.Response[internalpb.RemoteTellGrainResponse], error) {
	logger := x.logger
	msg := request.Msg

	// Remoting must be enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	// Validate host and port
	host := msg.GetGrain().GetHost()
	port := msg.GetGrain().GetPort()
	if err := x.validateRemoteHost(host, port); err != nil {
		return nil, err
	}

	message, _ := msg.GetMessage().UnmarshalNew()
	timeout := msg.GetRequestTimeout()

	identity, err := toIdentity(msg.GetGrain().GetGrainId().GetValue())
	if err != nil {
		if errors.Is(err, ErrInvalidGrainIdentity) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.Join(err, ErrInvalidGrainIdentity))
	}

	if isReservedName(identity.Name()) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, NewErrGrainNotFound(identity.String()))
	}

	var sender *Identity
	if msg.GetSender() != nil {
		sender, err = toIdentity(msg.GetSender().GetValue())
		if err != nil {
			if errors.Is(err, ErrInvalidGrainIdentity) {
				return nil, connect.NewError(connect.CodeInvalidArgument, err)
			}
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.Join(err, ErrInvalidGrainIdentity))
		}

		if isReservedName(sender.Name()) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, NewErrGrainNotFound(sender.String()))
		}
	}

	_, err = x.localSend(ctx, identity, message, sender, timeout.AsDuration(), false)
	if err != nil {
		logger.Errorf("failed to create grain (%s) on [host=%s, port=%d]: reason: (%v)", identity.String(), msg.GetGrain().GetHost(), msg.GetGrain().GetPort(), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&internalpb.RemoteTellGrainResponse{}), nil
}

// RemoteAskGrain handles remote requests to a Grain from another node.
//
// It validates the request, creates or locates the target Grain, delivers the message,
// and returns the response or an error. Used internally for cluster communication.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - request: RemoteMessageGrainRequest containing target grain info and message.
//
// Returns:
//   - *internalpb.RemoteMessageGrainResponse: response containing the Grain's reply.
//   - error: error if the request fails or is invalid.
func (x *actorSystem) RemoteAskGrain(ctx context.Context, request *connect.Request[internalpb.RemoteAskGrainRequest]) (*connect.Response[internalpb.RemoteAskGrainResponse], error) {
	logger := x.logger
	msg := request.Msg

	// Remoting must be enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	// Validate host and port
	host := msg.GetGrain().GetHost()
	port := msg.GetGrain().GetPort()
	if err := x.validateRemoteHost(host, port); err != nil {
		return nil, err
	}

	message, _ := msg.GetMessage().UnmarshalNew()
	timeout := msg.GetRequestTimeout()

	identity, err := toIdentity(msg.GetGrain().GetGrainId().GetValue())
	if err != nil {
		if errors.Is(err, ErrInvalidGrainIdentity) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.Join(err, ErrInvalidGrainIdentity))
	}

	if isReservedName(identity.Name()) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, NewErrGrainNotFound(identity.String()))
	}

	var sender *Identity
	if msg.GetSender() != nil {
		sender, err = toIdentity(msg.GetSender().GetValue())
		if err != nil {
			if errors.Is(err, ErrInvalidGrainIdentity) {
				return nil, connect.NewError(connect.CodeInvalidArgument, err)
			}
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.Join(err, ErrInvalidGrainIdentity))
		}

		if isReservedName(sender.Name()) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, NewErrGrainNotFound(sender.String()))
		}
	}

	reply, err := x.localSend(ctx, identity, message, sender, timeout.AsDuration(), true)
	if err != nil {
		logger.Errorf("failed to create grain (%s) on [host=%s, port=%d]: reason: (%v)", identity.String(), msg.GetGrain().GetHost(), msg.GetGrain().GetPort(), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, _ := anypb.New(reply)
	return connect.NewResponse(&internalpb.RemoteAskGrainResponse{Message: response}), nil
}

// validateRemoteHost checks if the incoming request is for the correct host/port.
func (x *actorSystem) validateRemoteHost(host string, port int32) error {
	addr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if addr != net.JoinHostPort(host, strconv.Itoa(int(port))) {
		return connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}
	return nil
}

// remoteAskGrain sends a message to a Grain in the cluster.
//
// It locates the Grain via the cluster, sends the message remotely, and returns the response.
// Falls back to local delivery if the Grain is not found in the cluster.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - id: identity of the target Grain.
//   - message: protobuf message to send.
//   - sender: identity of the sender (optional).
//   - timeout: request timeout duration.
//
// Returns:
//   - proto.Message: the response from the Grain.
//   - error: error if the request fails.
func (x *actorSystem) remoteAskGrain(ctx context.Context, id *Identity, message proto.Message, sender *Identity, timeout time.Duration) (proto.Message, error) {
	gw, err := x.getCluster().GetGrain(ctx, id.String())
	if err != nil {
		if errors.Is(err, cluster.ErrGrainNotFound) {
			return x.localSend(ctx, id, message, sender, timeout, true)
		}
		return nil, err
	}

	msg, _ := anypb.New(message)
	remoteClient := x.remoting.remotingServiceClient(gw.GetHost(), int(gw.GetPort()))
	request := connect.NewRequest(&internalpb.RemoteAskGrainRequest{
		Grain:          gw,
		RequestTimeout: durationpb.New(timeout),
		Dependencies:   gw.GetDependencies(),
		Message:        msg,
	})

	res, err := remoteClient.RemoteAskGrain(ctx, request)
	if err != nil {
		return nil, err
	}
	return res.Msg.GetMessage().UnmarshalNew()
}

// remoteTellGrain sends a message to a Grain in the cluster.
//
// It locates the Grain via the cluster, sends the message remotely, and returns the response.
// Falls back to local delivery if the Grain is not found in the cluster.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - id: identity of the target Grain.
//   - message: protobuf message to send.
//   - sender: identity of the sender (optional).
//   - timeout: request timeout duration.
//
// Returns:
//   - error: error if the request fails.
func (x *actorSystem) remoteTellGrain(ctx context.Context, id *Identity, message proto.Message, sender *Identity, timeout time.Duration) error {
	gw, err := x.getCluster().GetGrain(ctx, id.String())
	if err != nil {
		if errors.Is(err, cluster.ErrGrainNotFound) {
			_, err := x.localSend(ctx, id, message, sender, timeout, true)
			return err
		}
		return err
	}

	msg, _ := anypb.New(message)
	remoteClient := x.remoting.remotingServiceClient(gw.GetHost(), int(gw.GetPort()))
	request := connect.NewRequest(&internalpb.RemoteTellGrainRequest{
		Grain:          gw,
		RequestTimeout: durationpb.New(timeout),
		Dependencies:   gw.GetDependencies(),
		Message:        msg,
	})

	_, err = remoteClient.RemoteTellGrain(ctx, request)
	return err
}

// localSend sends a message to a local Grain.
//
// It creates or locates the Grain locally, delivers the message, and waits for a response or error.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - id: identity of the target Grain.
//   - message: protobuf message to send.
//   - sender: identity of the sender (optional).
//   - timeout: request timeout duration.
//   - synchronous: whether to wait for a response (true for Ask, false for Tell).
//
// Returns:
//   - proto.Message: the response from the Grain (if synchronous).
//   - error: error if the request fails.
func (x *actorSystem) localSend(ctx context.Context, id *Identity, message proto.Message, sender *Identity, timeout time.Duration, synchronous bool) (proto.Message, error) {
	// Ensure the grain process exists
	process, err := x.ensureGrainProcess(ctx, id)
	if err != nil {
		return nil, err
	}

	// Ensure the process is running
	if !process.isRunning() {
		process.processState.ClearSuspended()
		process.processState.SetRunning()
	}

	// Build and send the request
	request := getGrainRequest()
	request.build(ctx, sender, message, synchronous)
	process.receive(request)
	timer := timers.Get(timeout)
	defer timers.Put(timer)

	// Handle synchronous (Ask) and asynchronous (Tell) cases
	if synchronous {
		select {
		case res := <-request.getResponse():
			return res.Message(), nil
		case err := <-request.getErr():
			return nil, err
		case <-timer.C:
			return nil, ErrRequestTimeout
		}
	} else {
		select {
		case err := <-request.getErr():
			return nil, err
		case <-timer.C:
			return nil, ErrRequestTimeout
		}
	}
}

// ensureGrainProcess returns an existing grain process or creates and activates a new one.
func (x *actorSystem) ensureGrainProcess(ctx context.Context, id *Identity) (*grainProcess, error) {
	process, ok := x.grains.Get(*id)
	if ok {
		// check whether the Grain type is registered
		if !x.reflection.registry.Exists(process.getGrain()) {
			// Grain type is not registered, delete the process and return an error
			x.grains.Delete(*id)
			return nil, ErrGrainNotRegistered
		}

		return process, nil
	}

	grain, err := x.reflection.NewGrain(id.Kind())
	if err != nil {
		return nil, err
	}

	process, err = x.createGrain(ctx, id, grain)
	if err != nil {
		return nil, err
	}
	return process, nil
}

// createGrain creates and activates a new Grain process.
//
// It instantiates the Grain, activates it, registers it locally, and (if in cluster mode)
// registers it with the cluster.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - identity: identity of the Grain to create.
//   - grain: Grain instance to activate.
//
// Returns:
//   - *grainProcess: the created and activated Grain process.
//   - error: error if creation or activation fails.
func (x *actorSystem) createGrain(ctx context.Context, identity *Identity, grain Grain) (*grainProcess, error) {
	// create an instance of grain process
	process := newGrainProcess(identity, grain, x, grain.Dependencies()...)
	// activate the grain process
	if err := process.activate(ctx); err != nil {
		return nil, err
	}

	// it is ok to always overwrite an existing one
	x.grains.Set(*identity, process)
	if x.InCluster() {
		dependencies, err := encodeDependencies(process.dependencies.Values()...)
		if err != nil {
			return nil, fmt.Errorf("failed to encode dependencies for grain %s: %w", identity.String(), err)
		}

		// register the grain in the cluster
		if err := x.getCluster().PutGrain(ctx, &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  identity.Kind(),
				Name:  identity.Name(),
				Value: identity.String(),
			},
			Host:         x.Host(),
			Port:         int32(x.Port()),
			Dependencies: dependencies,
		}); err != nil {
			return nil, err
		}
	}

	return process, nil
}
