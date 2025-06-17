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
	"crypto/tls"
	"fmt"
	"testing"
	"time"

	"github.com/kapetan-io/tackle/autotls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestRebalancing(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor-1%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	for j := 1; j <= 5; j++ {
		actorName := fmt.Sprintf("Actor-2%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor-3%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for cluster rebalancing
	util.Pause(time.Minute)

	sender, err := node1.LocalActor("Actor-11")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1 and  node 3
	actorName := "Actor-21"
	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.NoError(t, err)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRebalancingWithTLSEnabled(t *testing.T) {
	t.SkipNow()
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// AutoGenerate TLS certs
	conf := autotls.Config{
		AutoTLS:            true,
		ClientAuth:         tls.RequireAndVerifyClientCert,
		InsecureSkipVerify: true,
	}
	require.NoError(t, autotls.Setup(&conf))

	// create and start system cluster
	node1, sd1 := testCluster(t, srv.Addr().String(), withTestTSL(conf))
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start system cluster
	node2, sd2 := testCluster(t, srv.Addr().String(), withTestTSL(conf))
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start system cluster
	node3, sd3 := testCluster(t, srv.Addr().String(), withTestTSL(conf))
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node1-Actor-%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node2-Actor-%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node3-Actor-%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for cluster rebalancing
	util.Pause(time.Minute)

	sender, err := node1.LocalActor("Node1-Actor-1")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1 and  node 3
	actorName := "Node2-Actor-1"
	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.NoError(t, err)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRebalancingWithSingletonActor(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start system cluster
	node1, sd1 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start system cluster
	node2, sd2 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start system cluster
	node3, sd3 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// create a singleton actor
	err := node1.SpawnSingleton(ctx, "actorName", NewMockActor())
	require.NoError(t, err)

	util.Pause(time.Second)

	// take down node1 since it is the first node created in the cluster
	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, sd1.Close())

	util.Pause(2 * time.Minute)

	_, _, err = node2.ActorOf(ctx, "actorName")
	require.NoError(t, err)

	assert.NoError(t, node2.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd2.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRebalancingWithActorRelocationDisabled(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start system cluster
	node1, sd1 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start system cluster
	node2, sd2 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start system cluster
	node3, sd3 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node1-Actor-%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node2-Actor-%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithRelocationDisabled())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node3-Actor-%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for cluster rebalancing
	util.Pause(time.Minute)

	sender, err := node1.LocalActor("Node1-Actor-1")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1 and  node 3
	actorName := "Node2-Actor-1"
	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.Error(t, err)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRebalancingWithSystemRelocationDisabled(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testCluster(t, srv.Addr().String(), withoutTestRelocation())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testCluster(t, srv.Addr().String(), withoutTestRelocation())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testCluster(t, srv.Addr().String(), withoutTestRelocation())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node1-Actor-%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node2-Actor-%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node3-Actor-%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for cluster rebalancing
	util.Pause(time.Second)

	actorName := "Node1-Actor-1"
	sender, err := node1.LocalActor(actorName)
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1 and  node 3
	actorName = "Node2-Actor-1"
	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.Error(t, err)
	require.ErrorIs(t, err, ErrActorNotFound)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRebalancingWithExtension(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create the state store extension
	stateStoreExtension := NewMockExtension()

	// create and start a system cluster
	node1, sd1 := testCluster(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testCluster(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testCluster(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 entities on each node
	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node1-entity-%d", j)
		pid, err := node1.Spawn(ctx, entityID, NewMockEntity())
		require.NoError(t, err)
		require.NotNil(t, pid)

		command := &testpb.CreateAccount{
			AccountBalance: 500.00,
		}
		_, err = Ask(ctx, pid, command, time.Minute)
		require.NoError(t, err)
	}

	util.Pause(time.Second)

	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node2-entity-%d", j)
		pid, err := node2.Spawn(ctx, entityID, NewMockEntity())
		require.NoError(t, err)
		require.NotNil(t, pid)

		command := &testpb.CreateAccount{
			AccountBalance: 600.00,
		}
		_, err = Ask(ctx, pid, command, time.Minute)
		require.NoError(t, err)
	}

	util.Pause(time.Second)

	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node3-entity-%d", j)
		pid, err := node3.Spawn(ctx, entityID, NewMockEntity())
		require.NoError(t, err)
		require.NotNil(t, pid)

		command := &testpb.CreateAccount{
			AccountBalance: 700.00,
		}
		_, err = Ask(ctx, pid, command, time.Minute)
		require.NoError(t, err)
	}

	util.Pause(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for cluster rebalancing
	util.Pause(time.Minute)

	sender, err := node1.LocalActor("node1-entity-1")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1
	entityID := "node2-entity-1"
	response, err := sender.SendSync(ctx, entityID, new(testpb.GetAccount), time.Minute)
	require.NoError(t, err)
	account, ok := response.(*testpb.Account)
	require.True(t, ok)

	// the balance when creating that entity is 600
	require.EqualValues(t, 600, account.GetAccountBalance())

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRebalancingWithDependency(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	dependencyID := "dependency"
	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node1-actor-%d", j)
		// create the dependency
		dependency := NewMockDependency(dependencyID, entityID, "email")
		pid, err := node1.Spawn(ctx, entityID, NewMockActor(), WithDependencies(dependency))
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node2-actor-%d", j)
		// create the dependency
		dependency := NewMockDependency(dependencyID, entityID, "email")
		pid, err := node2.Spawn(ctx, entityID, NewMockActor(), WithDependencies(dependency))
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	util.Pause(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for cluster rebalancing
	util.Pause(time.Minute)

	sender, err := node1.LocalActor("node1-actor-1")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1 and node 3
	actorName := "node2-actor-1"

	// we know the actor will be on node 1
	pid, err := node1.LocalActor(actorName)
	require.NoError(t, err)
	require.NotNil(t, pid)
	actual := pid.Dependencies()
	require.NotNil(t, actual)
	require.Len(t, actual, 1)

	dep := pid.Dependency(dependencyID)
	require.NotNil(t, dep)
	mockdep := dep.(*MockDependency)
	require.Equal(t, actorName, mockdep.Username)
	require.Equal(t, "email", mockdep.Email)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, sd1.Close())
	srv.Shutdown()
}

func TestGrainsRebalancing(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testCluster(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	for j := range 4 {
		identity := NewIdentity(NewMockGrain(), fmt.Sprintf("Grain-1%d", j))
		require.NotNil(t, identity)
		message := new(testpb.TestSend)
		err := node1.SendGrainAsync(ctx, identity, message)
		require.NoError(t, err)
	}

	util.Pause(time.Second)

	for j := range 5 {
		identity := NewIdentity(NewMockGrain(), fmt.Sprintf("Grain-2%d", j))
		require.NotNil(t, identity)
		message := new(testpb.TestSend)
		err := node2.SendGrainAsync(ctx, identity, message)
		require.NoError(t, err)
	}

	util.Pause(time.Second)

	for j := range 4 {
		identity := NewIdentity(NewMockGrain(), fmt.Sprintf("Grain-3%d", j))
		require.NotNil(t, identity)
		message := new(testpb.TestSend)
		err := node3.SendGrainAsync(ctx, identity, message)
		require.NoError(t, err)
	}

	util.Pause(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for cluster rebalancing
	util.Pause(time.Minute)

	identity := NewIdentity(new(MockGrain), "Grain-20")
	message := new(testpb.TestSend)
	err := node3.SendGrainAsync(ctx, identity, message)
	require.NoError(t, err)

	identity = NewIdentity(new(MockGrain), "Grain-21")
	message = new(testpb.TestSend)
	err = node1.SendGrainAsync(ctx, identity, message)
	require.NoError(t, err)

	identity = NewIdentity(new(MockGrain), "Grain-22")
	message = new(testpb.TestSend)
	err = node3.SendGrainAsync(ctx, identity, message)
	require.NoError(t, err)

	identity = NewIdentity(new(MockGrain), "Grain-23")
	message = new(testpb.TestSend)
	err = node1.SendGrainAsync(ctx, identity, message)
	require.NoError(t, err)

	identity = NewIdentity(new(MockGrain), "Grain-24")
	message = new(testpb.TestSend)
	err = node1.SendGrainAsync(ctx, identity, message)
	require.NoError(t, err)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}
