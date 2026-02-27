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

package actor

import (
	"context"
	"errors"
	"fmt"
	"net"
	nethttp "net/http"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pointer"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/remote"
)

// toProtoError creates an internalpb.Error message with the specified code and error message.
// This is the standard way to return errors from proto TCP handlers to match the error
// semantics of the existing ConnectRPC implementation.
func toProtoError(code internalpb.Code, err error) *internalpb.Error {
	return &internalpb.Error{
		Code:    code,
		Message: err.Error(),
	}
}

// extractContextWithPropagator extracts metadata from the proto TCP context and applies
// the configured ContextPropagator to enrich it with distributed tracing, auth, and other
// cross-cutting concerns.
//
// This bridges the proto TCP Metadata format (map[string]string) with the HTTP-based
// ContextPropagator interface (http.Header) used by the existing remoting infrastructure.
//
// If no metadata is present in the context or no propagator is configured, the original
// context is returned unchanged.
func (x *actorSystem) extractContextWithPropagator(ctx context.Context) (context.Context, error) {
	propagator := x.remoteConfig.ContextPropagator()
	if propagator == nil {
		return ctx, nil
	}

	// Extract metadata from the proto TCP context.
	md, hasMD := inet.FromContext(ctx)
	if !hasMD || md == nil {
		// No metadata in the request — return original context.
		return ctx, nil
	}

	// Convert Metadata headers to http.Header for the ContextPropagator.
	// This enables compatibility with existing tracing/auth propagators that
	// expect HTTP-style headers (e.g., OpenTelemetry, Jaeger, custom auth).
	headers := make(nethttp.Header)
	md.IterateHeaders(func(key, value string) {
		headers.Set(key, value)
	})

	// Apply the propagator to extract context values from the headers.
	return propagator.Extract(ctx, headers)
}

// remoteLookupHandler handles RemoteLookup requests over the proto TCP transport.
// It checks if the specified actor exists and returns its address.
func (x *actorSystem) remoteLookupHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteLookupRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(request.GetHost(), strconv.Itoa(int(request.GetPort()))) {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, gerrors.ErrInvalidHost), nil
	}

	actorName := request.GetName()
	if !isSystemName(actorName) && x.clusterEnabled.Load() {
		actor, err := x.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				err := gerrors.NewErrAddressNotFound(actorName)
				logger.Errorf("remote lookup: actor=%s not found: %v (hint: verify actor exists on target node)", actorName, err)
				return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
			}

			return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
		}
		return &internalpb.RemoteLookupResponse{Address: actor.GetAddress()}, nil
	}

	addr := address.New(actorName, x.Name(), request.GetHost(), int(request.GetPort()))
	pidNode, exist := x.actors.node(addr.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(addr.String())
		logger.Errorf("remote lookup: address=%s not found: %v (hint: verify address, check remoting config)", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := pidNode.value()
	return &internalpb.RemoteLookupResponse{Address: pid.ID()}, nil
}

// remoteAskHandler handles RemoteAsk requests over the proto TCP transport.
// It sends messages to remote actors and expects immediate responses.
func (x *actorSystem) remoteAskHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteAskRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	timeout := x.askTimeout
	if request.GetTimeout() != nil {
		timeout = request.GetTimeout().AsDuration()
	}

	// Extract context metadata and apply context propagation if configured.
	var err error
	ctx, err = x.extractContextWithPropagator(ctx)
	if err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	responses := make([][]byte, 0, len(request.GetRemoteMessages()))
	for _, message := range request.GetRemoteMessages() {
		receiver := message.GetReceiver()
		addr, err := address.Parse(receiver)
		if err != nil {
			return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
		}

		remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
		if remoteAddr != net.JoinHostPort(addr.Host(), strconv.Itoa(addr.Port())) {
			return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, gerrors.ErrInvalidHost), nil
		}

		node, exist := x.actors.node(addr.String())
		if !exist {
			err := gerrors.NewErrAddressNotFound(addr.String())
			logger.Errorf("remote ask: address=%s not found: %v (hint: verify actor exists on target node)", addr.String(), err)
			return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
		}

		pid := node.value()
		if !pid.IsRunning() {
			err := gerrors.NewErrRemoteSendFailure(gerrors.ErrDead)
			logger.Errorf("remote ask: actor=%s not running: %v (hint: actor may have stopped, retry or check target)", addr.String(), err)
			return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
		}

		reply, err := x.handleRemoteAsk(ctx, pid, message, timeout)
		if err != nil {
			err := gerrors.NewErrRemoteSendFailure(err)
			logger.Errorf("remote ask failed: %v", err)
			if errors.Is(err, gerrors.ErrRequestTimeout) {
				return toProtoError(internalpb.Code_CODE_DEADLINE_EXCEEDED, err), nil
			}
			return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
		}

		marshaled, err := x.remoting.Serializer(reply).Serialize(reply)
		if err != nil {
			return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
		}
		responses = append(responses, marshaled)
	}

	return &internalpb.RemoteAskResponse{Messages: responses}, nil
}

// remoteTellHandler handles RemoteTell requests over the proto TCP transport.
// It sends fire-and-forget messages to remote actors.
func (x *actorSystem) remoteTellHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteTellRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	// Extract context metadata and apply context propagation if configured.
	var err error
	ctx, err = x.extractContextWithPropagator(ctx)
	if err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	for _, message := range request.GetRemoteMessages() {
		receiver := message.GetReceiver()
		addr, err := address.Parse(receiver)
		if err != nil {
			return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
		}

		node, exist := x.actors.node(addr.String())
		if !exist {
			err := gerrors.NewErrAddressNotFound(addr.String())
			logger.Errorf("remote tell: address=%s not found: %v (hint: verify actor exists on target node)", addr.String(), err)
			return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
		}

		pid := node.value()
		if !pid.IsRunning() {
			err := gerrors.NewErrRemoteSendFailure(gerrors.ErrDead)
			logger.Errorf("remote tell: actor=%s not running: %v (hint: actor may have stopped)", addr.String(), err)
			return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
		}

		if err := x.handleRemoteTell(ctx, pid, message); err != nil {
			logger.Errorf("remote tell failed: %v", err)
			return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
		}
	}

	return new(internalpb.RemoteTellResponse), nil
}

// remoteReSpawnHandler handles RemoteReSpawn requests over the proto TCP transport.
// It restarts an actor on the remote machine.
func (x *actorSystem) remoteReSpawnHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteReSpawnRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(request.GetHost(), strconv.Itoa(int(request.GetPort()))) {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, gerrors.ErrInvalidHost), nil
	}

	// Make sure we don't interfere with system actors.
	if isSystemName(request.GetName()) {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.NewErrActorNotFound(request.GetName())), nil
	}

	// Fetch the actor address
	actorAddress := address.New(request.GetName(), x.Name(), request.GetHost(), int(request.GetPort()))
	node, exist := x.actors.node(actorAddress.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(actorAddress.String())
		logger.Errorf("remote respawn: address=%s not found: %v", actorAddress.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := node.value()
	if err := pid.Restart(ctx); err != nil {
		err := fmt.Errorf("failed to restart actor=%s: %w", actorAddress.String(), err)
		logger.Errorf("remote respawn failed: %v", err)
		return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
	}

	return &internalpb.RemoteReSpawnResponse{Address: actorAddress.String()}, nil
}

// remoteStopHandler handles RemoteStop requests over the proto TCP transport.
// It stops an actor on the remote machine.
func (x *actorSystem) remoteStopHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteStopRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(request.GetHost(), strconv.Itoa(int(request.GetPort()))) {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, gerrors.ErrInvalidHost), nil
	}

	// Make sure we don't interfere with system actors.
	if isSystemName(request.GetName()) {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.NewErrActorNotFound(request.GetName())), nil
	}

	// Fetch the actor address
	actorAddress := address.New(request.GetName(), x.Name(), request.GetHost(), int(request.GetPort()))
	pidNode, exist := x.actors.node(actorAddress.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(actorAddress.String())
		logger.Errorf("remote stop: address=%s not found: %v", actorAddress.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := pidNode.value()
	if err := pid.Shutdown(ctx); err != nil {
		err := fmt.Errorf("failed to stop actor=%s: %w", actorAddress.String(), err)
		logger.Errorf("remote stop failed: %v", err)
		return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
	}

	return new(internalpb.RemoteStopResponse), nil
}

// remoteSpawnHandler handles RemoteSpawn requests over the proto TCP transport.
// It spawns a new actor on the remote machine.
func (x *actorSystem) remoteSpawnHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteSpawnRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(request.GetHost(), strconv.Itoa(int(request.GetPort()))) {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, gerrors.ErrInvalidHost), nil
	}

	// Make sure we don't interfere with system actors.
	if isSystemName(request.GetActorName()) {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.NewErrActorNotFound(request.GetActorName())), nil
	}

	actor, err := x.reflection.instantiateActor(request.GetActorType())
	if err != nil {
		logger.Errorf(
			"failed to create actor (%s) of type (%s) on host=%s port=%d: %v (hint: verify actor type registered with WithTypes)",
			request.GetActorName(), request.GetActorType(), request.GetHost(), request.GetPort(), err,
		)

		if errors.Is(err, gerrors.ErrTypeNotRegistered) {
			return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrTypeNotRegistered), nil
		}

		return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
	}

	wrapSpawnErr := func(err error) proto.Message {
		if errors.Is(err, gerrors.ErrActorAlreadyExists) || errors.Is(err, gerrors.ErrSingletonAlreadyExists) {
			return toProtoError(internalpb.Code_CODE_ALREADY_EXISTS, err)
		}
		if cluster.IsQuorumError(err) {
			return toProtoError(internalpb.Code_CODE_UNAVAILABLE, cluster.NormalizeQuorumError(err))
		}
		return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err)
	}

	if request.GetSingleton() != nil {
		// Define singleton options
		singletonOpts := []ClusterSingletonOption{
			WithSingletonSpawnTimeout(request.GetSingleton().GetSpawnTimeout().AsDuration()),
			WithSingletonSpawnWaitInterval(request.GetSingleton().GetWaitInterval().AsDuration()),
			WithSingletonSpawnRetries(int(request.GetSingleton().GetMaxRetries())),
		}

		if request.GetRole() != "" {
			singletonOpts = append(singletonOpts, WithSingletonRole(request.GetRole()))
		}

		pid, err := x.SpawnSingleton(ctx, request.GetActorName(), actor, singletonOpts...)
		if err != nil {
			logger.Errorf("failed to create actor (%s) on host=%s port=%d: %v (hint: check cluster quorum, singleton config)", request.GetActorName(), request.GetHost(), request.GetPort(), err)
			return wrapSpawnErr(err), nil
		}

		logger.Infof("actor=%s host=%s port=%d actor created successfully", request.GetActorName(), request.GetHost(), request.GetPort())
		return &internalpb.RemoteSpawnResponse{Address: pid.ID()}, nil
	}

	opts := []SpawnOption{
		WithPassivationStrategy(codec.DecodePassivationStrategy(request.GetPassivationStrategy())),
	}

	if !request.GetRelocatable() {
		opts = append(opts, WithRelocationDisabled())
	}

	if request.GetEnableStash() {
		opts = append(opts, WithStashing())
	}

	if request.GetReentrancy() != nil {
		reentrancy := codec.DecodeReentrancy(request.GetReentrancy())
		opts = append(opts, WithReentrancy(reentrancy))
	}

	if request.GetRole() != "" {
		opts = append(opts, WithRole(request.GetRole()))
	}

	if request.GetSupervisor() != nil {
		if decoded := codec.DecodeSupervisor(request.GetSupervisor()); decoded != nil {
			opts = append(opts, WithSupervisor(decoded))
		}
	}

	// Set the dependencies if any
	if len(request.GetDependencies()) > 0 {
		dependencies, err := x.reflection.dependenciesFromProto(request.GetDependencies()...)
		if err != nil {
			logger.Errorf("failed to create actor (%s) on host=%s port=%d: %v (hint: verify actor type registered, check dependencies)", request.GetActorName(), request.GetHost(), request.GetPort(), err)
			return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
		}
		opts = append(opts, WithDependencies(dependencies...))
	}

	pid, err := x.Spawn(ctx, request.GetActorName(), actor, opts...)
	if err != nil {
		logger.Errorf("failed to create actor (%s) on host=%s port=%d: %v (hint: verify actor type registered, check dependencies)", request.GetActorName(), request.GetHost(), request.GetPort(), err)
		return wrapSpawnErr(err), nil
	}

	logger.Infof("actor=%s created on host=%s port=%d", request.GetActorName(), request.GetHost(), request.GetPort())
	return &internalpb.RemoteSpawnResponse{Address: pid.ID()}, nil
}

// remoteSpawnChildHandler handles RemoteSpawnChild requests over the proto TCP transport.
// It spawns a child actor on this node under an existing parent actor.
func (x *actorSystem) remoteSpawnChildHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteSpawnChildRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	host := request.GetHost()
	port := request.GetPort()
	childName := request.GetActorName()
	parentName := request.GetParent()

	// Validate host and port
	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	if isSystemName(childName) || isSystemName(parentName) {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.NewErrActorNotFound(childName)), nil
	}

	parentAddress := address.New(parentName, x.Name(), host, int(port))
	parentAddrStr := parentAddress.String()
	parentNode, exist := x.actors.node(parentAddrStr)
	if !exist {
		err := gerrors.NewErrAddressNotFound(parentAddrStr)
		x.logger.Errorf("spawn child: parent=%s not found: %v", parentAddrStr, err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := parentNode.value()
	if !pid.IsRunning() {
		err := gerrors.NewErrActorNotFound(parentAddrStr)
		x.logger.Errorf("spawn child: parent=%s not running: %v", parentAddrStr, err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	if pid.Kind() != request.GetActorType() {
		x.logger.Errorf("spawn child: invalid actor kind: %v", gerrors.ErrInvalidKinds)
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrInvalidKinds), nil
	}

	opts := make([]SpawnOption, 0, 6)
	opts = append(opts, WithPassivationStrategy(codec.DecodePassivationStrategy(request.GetPassivationStrategy())))
	if !request.GetRelocatable() {
		opts = append(opts, WithRelocationDisabled())
	}

	if request.GetEnableStash() {
		opts = append(opts, WithStashing())
	}

	if reent := request.GetReentrancy(); reent != nil {
		opts = append(opts, WithReentrancy(codec.DecodeReentrancy(reent)))
	}

	if sup := request.GetSupervisor(); sup != nil {
		if decoded := codec.DecodeSupervisor(sup); decoded != nil {
			opts = append(opts, WithSupervisor(decoded))
		}
	}

	if deps := request.GetDependencies(); len(deps) > 0 {
		dependencies, err := x.reflection.dependenciesFromProto(deps...)
		if err != nil {
			x.logger.Errorf("failed to create child actor=%s of parent=%s on host=%s port=%d: %v", childName, parentName, host, port, err)
			return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
		}
		opts = append(opts, WithDependencies(dependencies...))
	}

	cid, err := pid.SpawnChild(ctx, childName, pid.Actor(), opts...)
	if err != nil {
		x.logger.Errorf("Failed to create child Actor (%s) of parent (%s) on [host=%s, port=%d]: reason: (%v)", childName, parentName, host, port, err)
		if errors.Is(err, gerrors.ErrActorAlreadyExists) {
			return toProtoError(internalpb.Code_CODE_ALREADY_EXISTS, err), nil
		}

		return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
	}

	x.logger.Infof("actor=%s parent=%s host=%s port=%d child actor created successfully", childName, parentName, host, port)
	return &internalpb.RemoteSpawnChildResponse{Address: cid.ID()}, nil
}

// remotePassivationStrategyHandler handles RemotePassivationStrategy requests over the proto TCP transport.
// It returns the passivation strategy of an actor on this node.
func (x *actorSystem) remotePassivationStrategyHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemotePassivationStrategyRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	host := request.GetHost()
	port := request.GetPort()
	name := request.GetName()

	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	addr := address.New(name, x.Name(), host, int(port))
	pidNode, exist := x.actors.node(addr.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(addr.String())
		logger.Errorf("passivation strategy: address=%s not found: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := pidNode.value()
	if !pid.IsRunning() {
		err := gerrors.NewErrActorNotFound(addr.String())
		logger.Errorf("passivation strategy: actor=%s not running: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	return &internalpb.RemotePassivationStrategyResponse{PassivationStrategy: codec.EncodePassivationStrategy(pid.PassivationStrategy())}, nil
}

// remoteStateHandler handles RemoteState requests over the proto TCP transport.
// It returns the state of an actor on this node.
func (x *actorSystem) remoteStateHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteStateRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	host := request.GetHost()
	port := request.GetPort()
	name := request.GetName()
	state := request.GetState()

	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	addr := address.New(name, x.Name(), host, int(port))
	pidNode, exist := x.actors.node(addr.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(addr.String())
		logger.Errorf("remote state: address=%s not found: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := pidNode.value()
	switch state {
	case internalpb.State_STATE_RUNNING:
		return &internalpb.RemoteStateResponse{State: pid.IsRunning()}, nil
	case internalpb.State_STATE_STOPPING:
		return &internalpb.RemoteStateResponse{State: pid.IsStopping()}, nil
	case internalpb.State_STATE_SUSPENDED:
		return &internalpb.RemoteStateResponse{State: pid.IsSuspended()}, nil
	case internalpb.State_STATE_RELOCATABLE:
		return &internalpb.RemoteStateResponse{State: pid.IsRelocatable()}, nil
	case internalpb.State_STATE_SINGLETON:
		return &internalpb.RemoteStateResponse{State: pid.IsSingleton()}, nil
	}

	return &internalpb.RemoteStateResponse{State: false}, nil
}

// remoteChildrenHandler handles RemoteChildren requests over the proto TCP transport.
// It returns the children of an actor on this node.
func (x *actorSystem) remoteChildrenHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteChildrenRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	host := request.GetHost()
	port := request.GetPort()
	name := request.GetName()

	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	addr := address.New(name, x.Name(), host, int(port))
	pidNode, exist := x.actors.node(addr.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(addr.String())
		logger.Errorf("remote children: address=%s not found: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := pidNode.value()
	if !pid.IsRunning() {
		err := gerrors.NewErrActorNotFound(addr.String())
		logger.Errorf("remote children: actor=%s not running: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	children := pid.Children()
	addresses := make([]string, 0, len(children))
	for _, child := range children {
		addresses = append(addresses, child.ID())
	}
	return &internalpb.RemoteChildrenResponse{Addresses: addresses}, nil
}

// remoteParentHandler handles RemoteParent requests over the proto TCP transport.
// It returns the parent of an actor on this node.
func (x *actorSystem) remoteParentHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteParentRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	host := request.GetHost()
	port := request.GetPort()
	name := request.GetName()

	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	addr := address.New(name, x.Name(), host, int(port))
	pidNode, exist := x.actors.node(addr.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(addr.String())
		logger.Errorf("remote parent: address=%s not found: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := pidNode.value()
	if !pid.IsRunning() || pid.Parent() == nil {
		err := gerrors.NewErrActorNotFound(addr.String())
		logger.Errorf("remote parent: actor=%s not running or has no parent: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	return &internalpb.RemoteParentResponse{Address: pid.Parent().ID()}, nil
}

// remoteKindHandler handles RemoteKind requests over the proto TCP transport.
// It returns the kind of an actor on this node.
func (x *actorSystem) remoteKindHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteKindRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	host := request.GetHost()
	port := request.GetPort()
	name := request.GetName()

	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	addr := address.New(name, x.Name(), host, int(port))
	pidNode, exist := x.actors.node(addr.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(addr.String())
		logger.Errorf("passivation strategy: address=%s not found: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := pidNode.value()
	if !pid.IsRunning() {
		err := gerrors.NewErrActorNotFound(addr.String())
		logger.Errorf("passivation strategy: actor=%s not running: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	return &internalpb.RemoteKindResponse{Kind: pid.Kind()}, nil
}

// remoteDependenciesHandler handles RemoteDependencies requests over the proto TCP transport.
// It returns the dependencies of an actor on this node.
func (x *actorSystem) remoteDependenciesHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteDependenciesRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	host := request.GetHost()
	port := request.GetPort()
	name := request.GetName()

	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	addr := address.New(name, x.Name(), host, int(port))
	pidNode, exist := x.actors.node(addr.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(addr.String())
		logger.Errorf("passivation strategy: address=%s not found: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := pidNode.value()
	if !pid.IsRunning() {
		err := gerrors.NewErrActorNotFound(addr.String())
		logger.Errorf("passivation strategy: actor=%s not running: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	dependencies, err := codec.EncodeDependencies(pid.Dependencies()...)
	if err != nil {
		logger.Errorf("failed to encode dependencies for actor=%s on host=%s port=%d: %v", name, host, port, err)
		return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
	}

	return &internalpb.RemoteDependenciesResponse{Dependencies: dependencies}, nil
}

// remoteMetricHandler handles RemoteMetric requests over the proto TCP transport.
// It returns the metric of an actor on this node.
func (x *actorSystem) remoteMetricHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteMetricRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	host := request.GetHost()
	port := request.GetPort()
	name := request.GetName()

	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	addr := address.New(name, x.Name(), host, int(port))
	pidNode, exist := x.actors.node(addr.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(addr.String())
		logger.Errorf("passivation strategy: address=%s not found: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := pidNode.value()
	if !pid.IsRunning() {
		err := gerrors.NewErrActorNotFound(addr.String())
		logger.Errorf("passivation strategy: actor=%s not running: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	metric := pid.Metric(ctx)
	if metric == nil {
		return new(internalpb.RemoteMetricResponse), nil
	}

	return &internalpb.RemoteMetricResponse{Metric: &internalpb.Metric{
		DeadlettersCount:        metric.DeadlettersCount(),
		ChildrenCount:           metric.ChidrenCount(),
		Uptime:                  metric.Uptime(),
		LatestProcessedDuration: durationpb.New(metric.LatestProcessedDuration()),
		RestartCount:            metric.RestartCount(),
		ProcessedCount:          metric.ProcessedCount(),
		StashSize:               metric.StashSize(),
		FailureCount:            metric.FailureCount(),
		ReinstateCount:          metric.ReinstateCount(),
	}}, nil
}

// remoteRoleHandler handles RemoteRole requests over the proto TCP transport.
// It returns the role of an actor on this node.
func (x *actorSystem) remoteRoleHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteRoleRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	host := request.GetHost()
	port := request.GetPort()
	name := request.GetName()

	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	addr := address.New(name, x.Name(), host, int(port))
	pidNode, exist := x.actors.node(addr.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(addr.String())
		logger.Errorf("passivation strategy: address=%s not found: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := pidNode.value()
	if !pid.IsRunning() {
		err := gerrors.NewErrActorNotFound(addr.String())
		logger.Errorf("passivation strategy: actor=%s not running: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	return &internalpb.RemoteRoleResponse{Role: pointer.Deref(pid.Role(), "")}, nil
}

// remoteStashSizeHandler handles RemoteStashSize requests over the proto TCP transport.
// It returns the stash size of an actor on this node.
func (x *actorSystem) remoteStashSizeHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteStashSizeRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	host := request.GetHost()
	port := request.GetPort()
	name := request.GetName()

	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	addr := address.New(name, x.Name(), host, int(port))
	pidNode, exist := x.actors.node(addr.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(addr.String())
		logger.Errorf("passivation strategy: address=%s not found: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	pid := pidNode.value()
	if !pid.IsRunning() {
		err := gerrors.NewErrActorNotFound(addr.String())
		logger.Errorf("passivation strategy: actor=%s not running: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	return &internalpb.RemoteStashSizeResponse{Size: pid.StashSize()}, nil
}

// remoteReinstateHandler handles RemoteReinstate requests over the proto TCP transport.
// It reinstates a previously stopped actor on the remote machine.
func (x *actorSystem) remoteReinstateHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteReinstateRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(request.GetHost(), strconv.Itoa(int(request.GetPort()))) {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, gerrors.ErrInvalidHost), nil
	}

	// Make sure we don't interfere with system actors.
	if isSystemName(request.GetName()) {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.NewErrActorNotFound(request.GetName())), nil
	}

	// Fetch the actor address
	addr := address.New(request.GetName(), x.Name(), request.GetHost(), int(request.GetPort()))
	// Locate the given actor
	pidNode, exist := x.actors.node(addr.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(addr.String())
		logger.Errorf("remote reinstate: address=%s not found: %v", addr.String(), err)
		return toProtoError(internalpb.Code_CODE_NOT_FOUND, err), nil
	}

	// Trigger passivation re-start
	pid := pidNode.value()
	pid.doReinstate()

	return new(internalpb.RemoteReinstateResponse), nil
}

// remoteAskGrainHandler handles RemoteAskGrain requests over the proto TCP transport.
// It sends messages to remote grains and expects immediate responses.
func (x *actorSystem) remoteAskGrainHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteAskGrainRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	// Validate host and port
	host := request.GetGrain().GetHost()
	port := request.GetGrain().GetPort()
	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	// Extract context metadata and apply context propagation if configured.
	var err error
	ctx, err = x.extractContextWithPropagator(ctx)
	if err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	serializer := x.remoting.Serializer(nil)
	message, err := serializer.Deserialize(request.GetMessage())
	if err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	timeout := request.GetRequestTimeout()

	identity, err := toIdentity(request.GetGrain().GetGrainId().GetValue())
	if err != nil {
		if errors.Is(err, gerrors.ErrInvalidGrainIdentity) {
			return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
		}
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.Join(err, gerrors.ErrInvalidGrainIdentity)), nil
	}

	if isSystemName(identity.Name()) {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.NewErrReservedName(identity.String())), nil
	}

	reply, err := x.localSend(ctx, identity, message, timeout.AsDuration(), true)
	if err != nil {
		logger.Errorf("failed to send to grain=%s on host=%s port=%d: %v", identity.String(), request.GetGrain().GetHost(), request.GetGrain().GetPort(), err)
		return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
	}

	response, err := x.remoting.Serializer(reply).Serialize(reply)
	if err != nil {
		return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
	}
	return &internalpb.RemoteAskGrainResponse{Message: response}, nil
}

// remoteTellGrainHandler handles RemoteTellGrain requests over the proto TCP transport.
// It sends fire-and-forget messages to remote grains.
func (x *actorSystem) remoteTellGrainHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteTellGrainRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	// Validate host and port
	host := request.GetGrain().GetHost()
	port := request.GetGrain().GetPort()
	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	// Extract context metadata and apply context propagation if configured.
	var err error
	ctx, err = x.extractContextWithPropagator(ctx)
	if err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	serializer := x.remoting.Serializer(nil)
	message, err := serializer.Deserialize(request.GetMessage())
	if err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	identity, err := toIdentity(request.GetGrain().GetGrainId().GetValue())
	if err != nil {
		if errors.Is(err, gerrors.ErrInvalidGrainIdentity) {
			return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
		}
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.Join(err, gerrors.ErrInvalidGrainIdentity)), nil
	}

	if isSystemName(identity.Name()) {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.NewErrReservedName(identity.String())), nil
	}

	_, err = x.localSend(ctx, identity, message, DefaultGrainRequestTimeout, false)
	if err != nil {
		logger.Errorf("failed to send message to grain=%s on host=%s port=%d: %v", identity.String(), request.GetGrain().GetHost(), request.GetGrain().GetPort(), err)
		return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
	}

	return new(internalpb.RemoteTellGrainResponse), nil
}

// remoteActivateGrainHandler handles RemoteActivateGrain requests over the proto TCP transport.
// It activates a grain on the remote node.
func (x *actorSystem) remoteActivateGrainHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.RemoteActivateGrainRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	grain := request.GetGrain()

	// Validate host and port
	host := grain.GetHost()
	port := grain.GetPort()
	if err := x.validateRemoteHost(host, port); err != nil {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, err), nil
	}

	if err := x.recreateGrain(ctx, grain); err != nil {
		logger.Errorf("failed to recreate grain=%s on host=%s port=%d: %v", grain.GetGrainId().GetValue(), host, port, err)
		return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
	}

	logger.Infof("recreated grain=%s on host=%s port=%d", grain.GetGrainId().GetValue(), host, port)
	return new(internalpb.RemoteActivateGrainResponse), nil
}

// persistPeerStateHandler handles PersistPeerState requests over the proto TCP transport.
// It persists peer state on the remote node.
func (x *actorSystem) persistPeerStateHandler(ctx context.Context, conn inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.PersistPeerStateRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	logger := x.logger

	if !x.remotingEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrRemotingDisabled), nil
	}

	if !x.clusterEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrClusterDisabled), nil
	}

	peerAddr := fmt.Sprintf("%s:%d", request.GetPeerState().GetHost(), request.GetPeerState().GetPeersPort())
	logger.Infof("node=%s persisting peer=%s state", x.PeersAddress(), peerAddr)

	if err := x.clusterStore.PersistPeerState(ctx, request.GetPeerState()); err != nil {
		logger.Errorf("node=%s failed to persist peer=%s state: %v", x.PeersAddress(), peerAddr, err)
		return toProtoError(internalpb.Code_CODE_INTERNAL_ERROR, err), nil
	}

	return new(internalpb.PersistPeerStateResponse), nil
}

// getNodeMetricHandler handles GetNodeMetric requests over the proto TCP transport.
// It returns the node metric (actor + grain load) for this node.
func (x *actorSystem) getNodeMetricHandler(_ context.Context, _ inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.GetNodeMetricRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	if !x.clusterEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrClusterDisabled), nil
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != request.GetNodeAddress() {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, gerrors.ErrInvalidHost), nil
	}

	load := x.actorsCounter.Load() + uint64(x.grains.Len())
	return &internalpb.GetNodeMetricResponse{
		NodeAddress: remoteAddr,
		Load:        load,
	}, nil
}

// getKindsHandler handles GetKinds requests over the proto TCP transport.
// It returns the list of cluster kinds registered on this node.
func (x *actorSystem) getKindsHandler(_ context.Context, _ inet.Connection, req proto.Message) (proto.Message, error) {
	request, ok := req.(*internalpb.GetKindsRequest)
	if !ok {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, errors.New("invalid request type")), nil
	}

	if !x.clusterEnabled.Load() {
		return toProtoError(internalpb.Code_CODE_FAILED_PRECONDITION, gerrors.ErrClusterDisabled), nil
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != request.GetNodeAddress() {
		return toProtoError(internalpb.Code_CODE_INVALID_ARGUMENT, gerrors.ErrInvalidHost), nil
	}

	kinds := make([]string, len(x.clusterConfig.kinds.Values()))
	for i, kind := range x.clusterConfig.kinds.Values() {
		kinds[i] = types.Name(kind)
	}

	return &internalpb.GetKindsResponse{Kinds: kinds}, nil
}

// validateRemoteHost checks if the incoming request is for the correct host/port.
// Returns an error if the request is not for this actor system's configured address.
func (x *actorSystem) validateRemoteHost(host string, port int32) error {
	addr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if addr != net.JoinHostPort(host, strconv.Itoa(int(port))) {
		return gerrors.ErrInvalidHost
	}
	return nil
}

// protoServerOptions returns ProtoServer options with all RemotingService and ClusterService
// handlers registered. This enables the actor system to handle remoting and cluster operations
// over the proto TCP transport.
// This enables the actor system to handle remoting operations over the proto TCP transport.
//
// The returned options should be passed to internalnet.NewProtoServer during actor system
// initialization to configure the proto TCP server with all necessary handlers.
func (x *actorSystem) protoServerOptions() []inet.ProtoServerOption {
	return []inet.ProtoServerOption{
		inet.WithProtoHandler("internalpb.RemoteLookupRequest", x.remoteLookupHandler),
		inet.WithProtoHandler("internalpb.RemoteAskRequest", x.remoteAskHandler),
		inet.WithProtoHandler("internalpb.RemoteTellRequest", x.remoteTellHandler),
		inet.WithProtoHandler("internalpb.RemoteReSpawnRequest", x.remoteReSpawnHandler),
		inet.WithProtoHandler("internalpb.RemoteStopRequest", x.remoteStopHandler),
		inet.WithProtoHandler("internalpb.RemoteSpawnRequest", x.remoteSpawnHandler),
		inet.WithProtoHandler("internalpb.RemoteSpawnChildRequest", x.remoteSpawnChildHandler),
		inet.WithProtoHandler("internalpb.RemotePassivationStrategyRequest", x.remotePassivationStrategyHandler),
		inet.WithProtoHandler("internalpb.RemoteStateRequest", x.remoteStateHandler),
		inet.WithProtoHandler("internalpb.RemoteChildrenRequest", x.remoteChildrenHandler),
		inet.WithProtoHandler("internalpb.RemoteParentRequest", x.remoteParentHandler),
		inet.WithProtoHandler("internalpb.RemoteKindRequest", x.remoteKindHandler),
		inet.WithProtoHandler("internalpb.RemoteDependenciesRequest", x.remoteDependenciesHandler),
		inet.WithProtoHandler("internalpb.RemoteMetricRequest", x.remoteMetricHandler),
		inet.WithProtoHandler("internalpb.RemoteRoleRequest", x.remoteRoleHandler),
		inet.WithProtoHandler("internalpb.RemoteStashSizeRequest", x.remoteStashSizeHandler),
		inet.WithProtoHandler("internalpb.RemoteReinstateRequest", x.remoteReinstateHandler),
		inet.WithProtoHandler("internalpb.RemoteAskGrainRequest", x.remoteAskGrainHandler),
		inet.WithProtoHandler("internalpb.RemoteTellGrainRequest", x.remoteTellGrainHandler),
		inet.WithProtoHandler("internalpb.RemoteActivateGrainRequest", x.remoteActivateGrainHandler),
		inet.WithProtoHandler("internalpb.PersistPeerStateRequest", x.persistPeerStateHandler),
		inet.WithProtoHandler("internalpb.GetNodeMetricRequest", x.getNodeMetricHandler),
		inet.WithProtoHandler("internalpb.GetKindsRequest", x.getKindsHandler),
	}
}

// startRemoteServer initializes and starts the proto TCP server for handling remoting operations.
// It creates a new ProtoServer instance configured with the remote config settings and registers
// all RemotingService handlers.
//
// The server is started in a background goroutine and will serve incoming connections until
// stopped via stopProtoServer.
//
// Returns an error if the server fails to initialize or listen on the configured address.
func (x *actorSystem) startRemoteServer(ctx context.Context) error {
	if !x.remotingEnabled.Load() {
		return nil
	}

	x.logger.Info("Starting remote server...")

	// Build the server address from the remote config.
	hostPort := net.JoinHostPort(x.remoteConfig.BindAddr(), strconv.Itoa(x.remoteConfig.BindPort()))

	// Create proto server options based on the remote config.
	serverOpts := x.protoServerOptions()

	// Add max frame size from config if specified.
	if x.remoteConfig.MaxFrameSize() > 0 {
		serverOpts = append(serverOpts, inet.WithProtoServerMaxFrameSize(x.remoteConfig.MaxFrameSize()))
	}

	// Add idle timeout if configured.
	if x.remoteConfig.IdleTimeout() > 0 {
		serverOpts = append(serverOpts, inet.WithProtoServerIdleTimeout(x.remoteConfig.IdleTimeout()))
	}

	// Add context to the server.
	serverOpts = append(serverOpts, inet.WithProtoServerContext(ctx))

	// Add panic recovery so a misbehaving handler does not crash the connection.
	serverOpts = append(serverOpts, inet.WithProtoServerPanicHandler(func(typeName protoreflect.FullName, recovered any) {
		x.logger.Errorf("Remoting panic in handler for %s: %v", typeName, recovered)
	}))

	// Add compression wrapper if configured.
	switch x.remoteConfig.Compression() {
	case remote.BrotliCompression:
		wrapper := inet.NewBrotliConnWrapper()
		serverOpts = append(serverOpts, inet.WithProtoServerConnWrapper(wrapper))
	case remote.ZstdCompression:
		wrapper, err := inet.NewZstdConnWrapper()
		if err != nil {
			x.logger.Error(fmt.Errorf("failed to create Zstd compression wrapper: %w", err))
			return err
		}
		serverOpts = append(serverOpts, inet.WithProtoServerConnWrapper(wrapper))
	case remote.GzipCompression:
		wrapper, err := inet.NewGzipConnWrapper()
		if err != nil {
			x.logger.Error(fmt.Errorf("failed to create Gzip compression wrapper: %w", err))
			return err
		}
		serverOpts = append(serverOpts, inet.WithProtoServerConnWrapper(wrapper))
	}

	// Add TLS configuration if enabled.
	var useTLS bool
	if x.tlsInfo != nil && x.tlsInfo.ServerConfig != nil {
		serverOpts = append(serverOpts, inet.WithProtoServerTLSConfig(x.tlsInfo.ServerConfig))
		useTLS = true
		x.logger.Info("TLS enabled for proto remote server")
	}

	// Create the proto server.
	protoServer, err := inet.NewProtoServer(hostPort, serverOpts...)
	if err != nil {
		x.logger.Error(fmt.Errorf("failed to create remote server: %w", err))
		return err
	}

	// Store the server instance for later shutdown.
	x.remoteServer = protoServer

	// Start listening (with or without TLS).
	if useTLS {
		if err := protoServer.ListenTLS(); err != nil {
			x.logger.Error(fmt.Errorf("failed to listen on %s with TLS: %w", hostPort, err))
			return err
		}
	} else {
		if err := protoServer.Listen(); err != nil {
			x.logger.Error(fmt.Errorf("failed to listen on %s: %w", hostPort, err))
			return err
		}
	}

	x.logger.Infof("Remote server listening on %s", protoServer.ListenAddr().String())

	// Start serving in a background goroutine.
	go func() {
		if err := protoServer.Serve(); err != nil {
			x.logger.Fatal(fmt.Errorf("remote server failed: %w", err))
		}
	}()

	return nil
}

// stopRemoteServer gracefully shuts down the proto TCP server.
// It waits for the specified timeout for active connections to complete before forcing shutdown.
//
// This method is safe to call multiple times and returns nil if the server was not started.
func (x *actorSystem) stopRemoteServer(timeout time.Duration) error {
	if x.remoteServer == nil {
		return nil
	}

	x.logger.Info("Shutting down remote server...")

	if err := x.remoteServer.Shutdown(timeout); err != nil {
		x.logger.Error(fmt.Errorf("error shutting down remote server: %w", err))
		return err
	}

	x.logger.Info("Remote server shut down successfully")
	return nil
}
