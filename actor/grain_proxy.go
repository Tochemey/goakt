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

// MessageGrain sends a request message to a Grain identified by the given identity.
//
// This method locates or spawns the target Grain (either locally or in the cluster), sends the provided
// protobuf message, and waits for a response or error. The request timeout can be customized using
// the WithRequestTimeout GrainOption; otherwise, a default timeout is used.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - identity: the unique identity string of the Grain.
//   - message: the protobuf message to send to the Grain.
//   - opts: optional GrainOptions to configure the Grain (e.g., dependencies, timeout).
//
// Returns:
//   - *GrainResponse: the response from the Grain, if successful.
//   - error: an error if the request fails, times out, or the system is not started.
func (x *actorSystem) MessageGrain(ctx context.Context, identity string, message proto.Message, opts ...GrainOption) (response proto.Message, err error) {
	if !x.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	// TODO: perform some cleaning

	// extract the Grain kind from its identity
	id, err := toIdentity(identity)
	if err != nil {
		return nil, err
	}

	// make sure we don't interfere with system actors.
	if isReservedName(id.Name()) {
		return nil, NewErrGrainNotFound(identity)
	}

	config := newGrainConfig(opts...)
	timeout := config.RequestTimeout()
	sender := config.RequestSender()
	if sender != nil {
		if err = sender.Validate(); err != nil {
			return nil, NewErrInvalidGrainIdentity(err)
		}
	}

	// TODO: revisit this
	passivationAfter := DefaultPassivationTimeout
	if config.PassivateAfter() != nil {
		passivationAfter = *config.PassivateAfter()
	}

	if x.InCluster() {
		gw, err := x.getCluster().GetGrain(ctx, id.String())
		if err != nil {
			if errors.Is(err, cluster.ErrGrainNotFound) {
				goto Locally
			}
			return nil, err
		}

		dependencies, err := encodeDependencies(config.Dependencies()...)
		if err != nil {
			return nil, err
		}

		msg, _ := anypb.New(message)

		remoteClient := x.remoting.remotingServiceClient(gw.GetHost(), int(gw.GetPort()))
		request := connect.NewRequest(&internalpb.RemoteMessageGrainRequest{
			Grain:          gw,
			Sender:         &internalpb.GrainId{Value: sender.String()},
			RequestTimeout: durationpb.New(timeout),
			PassivateAfter: durationpb.New(passivationAfter),
			Dependencies:   dependencies,
			Message:        msg,
		})

		res, err := remoteClient.RemoteMessageGrain(ctx, request)
		if err != nil {
			return nil, err
		}
		return res.Msg.GetMessage().UnmarshalNew()
	}

Locally:
	grain, err := x.reflection.NewGrain(id.Kind())
	if err != nil {
		return nil, err
	}

	process, err := x.createGrain(ctx, id, grain, opts...)
	if err != nil {
		return nil, err
	}

	request := getGrainRequest()
	request.build(sender, message)

	process.doReceive(request)
	timer := timers.Get(timeout)

	select {
	case res := <-request.Response():
		timers.Put(timer)
		return res.Message(), nil
	case err := <-request.Err():
		timers.Put(timer)
		return nil, err
	case <-timer.C:
		err = ErrRequestTimeout
		timers.Put(timer)
		return nil, err
	}
}

// RemoteMessageGrain handles grain remote message
func (x *actorSystem) RemoteMessageGrain(ctx context.Context, request *connect.Request[internalpb.RemoteMessageGrainRequest]) (*connect.Response[internalpb.RemoteMessageGrainResponse], error) {
	logger := x.logger

	msg := request.Msg
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	host := msg.GetGrain().GetHost()
	port := msg.GetGrain().GetPort()
	name := msg.GetGrain().GetGrainId().GetName()
	kind := msg.GetGrain().GetGrainId().GetKind()
	grainid := msg.GetGrain().GetGrainId().GetValue()
	message, _ := msg.GetMessage().UnmarshalNew()

	addr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if addr != net.JoinHostPort(host, strconv.Itoa(int(port))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	// make sure we don't interfere with system actors.
	if isReservedName(name) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, NewErrGrainNotFound(grainid))
	}

	identity, err := toIdentity(grainid)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.Join(err, ErrInvalidGrainIdentity))
	}

	grain, err := x.reflection.NewGrain(kind)
	if err != nil {
		logger.Errorf("failed to create grain (%s) on [host=%s, port=%d]: reason: (%v)", grainid, host, port, err)
		if errors.Is(err, ErrTypeNotRegistered) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, ErrTypeNotRegistered)
		}

		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var (
		opts           []GrainOption
		sender         *Identity
		requestTimeout time.Duration
	)

	// set the request timeout
	if msg.GetRequestTimeout() != nil {
		requestTimeout = msg.GetRequestTimeout().AsDuration()
	}

	// set the passivation time
	if msg.GetPassivateAfter() != nil {
		opts = append(opts, WithGrainPassivation(msg.GetPassivateAfter().AsDuration()))
	}

	// set the dependencies if any
	if len(msg.GetDependencies()) > 0 {
		dependencies, err := x.reflection.DependenciesFromProtobuf(msg.GetDependencies()...)
		if err != nil {
			logger.Errorf("failed to create grain (%s) on [host=%s, port=%d]: reason: (%v)", grainid, host, port, err)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		opts = append(opts, WithGrainDependencies(dependencies...))
	}

	if msg.GetSender() != nil {
		sender, err = toIdentity(msg.GetSender().GetValue())
		if err != nil {
			logger.Errorf("failed to create grain (%s) on [host=%s, port=%d]: reason: (%v)", grainid, host, port, err)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	process, err := x.createGrain(ctx, identity, grain, opts...)
	if err != nil {
		logger.Errorf("failed to create grain (%s) on [host=%s, port=%d]: reason: (%v)", grainid, host, port, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	grainRequest := getGrainRequest()
	grainRequest.build(sender, message)

	process.doReceive(grainRequest)
	timer := timers.Get(requestTimeout)

	select {
	case res := <-grainRequest.Response():
		timers.Put(timer)
		response, _ := anypb.New(res.Message())
		return connect.NewResponse(&internalpb.RemoteMessageGrainResponse{Message: response}), nil
	case err := <-grainRequest.Err():
		timers.Put(timer)
		return nil, err
	case <-timer.C:
		err = ErrRequestTimeout
		timers.Put(timer)
		return nil, err
	}
}

func (x *actorSystem) createGrain(ctx context.Context, identity *Identity, grain Grain, opts ...GrainOption) (*grainProcess, error) {
	// create an instance of grain process
	process := newGrainProcess(identity, grain, x, opts...)
	// activate the grain process
	if err := process.Activate(ctx); err != nil {
		return nil, err
	}

	// it is ok to always overwrite an existing one
	x.grains.Set(*identity, process)
	if x.InCluster() {
		// register the grain in the cluster
		if err := x.getCluster().PutGrain(ctx, &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  identity.Kind(),
				Name:  identity.Name(),
				Value: identity.String(),
			},
			Host: x.Host(),
			Port: int32(x.Port()),
		}); err != nil {
			return nil, err
		}
	}

	return process, nil
}
