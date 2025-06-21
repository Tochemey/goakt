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

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/internalpb"
)

type GrainFactory func(ctx context.Context) (Grain, error)

// GetGrain retrieves or activates a Grain (virtual actor) identified by the given name.
func (x *actorSystem) GetGrain(ctx context.Context, name string, factory GrainFactory) (*Identity, error) {
	if !x.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	logger := x.logger

	grain, err := factory(ctx)
	if err != nil {
		return nil, err
	}

	identity := newIdentity(grain, name)
	logger.Infof("activating grain (%s)...", identity.String())
	if err := identity.Validate(); err != nil {
		return nil, err
	}

	// make sure we don't interfere with system actors.
	if isReservedName(identity.Name()) {
		return nil, NewErrReservedName(identity.String())
	}

	if x.InCluster() {
		grainInfo, err := x.getCluster().GetGrain(ctx, identity.String())
		if err != nil {
			if !errors.Is(err, cluster.ErrGrainNotFound) {
				return nil, err
			}
		}

		if grainInfo != nil && !proto.Equal(grainInfo, new(internalpb.GrainId)) {
			remoteClient := x.remoting.remotingServiceClient(grainInfo.GetHost(), int(grainInfo.GetPort()))
			request := connect.NewRequest(&internalpb.RemoteActivateGrainRequest{
				Grain:        grainInfo,
				Dependencies: grainInfo.GetDependencies(),
			})

			if _, err := remoteClient.RemoteActivateGrain(ctx, request); err != nil {
				return nil, err
			}
			return identity, nil
		}
	}

	process, ok := x.grains.Get(*identity)
	if !ok {
		process = newGrainProcess(identity, grain, x)
	}

	if !x.registry.Exists(grain) {
		x.registry.Register(grain)
	}

	if err := process.activate(ctx); err != nil {
		return nil, err
	}

	x.grains.Set(*identity, process)
	return identity, x.putGrainOnCluster(process)
}
