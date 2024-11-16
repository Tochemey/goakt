# GoAkt

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/goakt/build.yml?branch=main)](https://github.com/Tochemey/goakt/actions/workflows/build.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tochemey/goakt.svg)](https://pkg.go.dev/github.com/tochemey/goakt)
![GitHub Release](https://img.shields.io/github/v/release/Tochemey/goakt)
![GitHub Tag](https://img.shields.io/github/v/tag/Tochemey/goakt)
[![Go Report Card](https://goreportcard.com/badge/github.com/tochemey/goakt)](https://goreportcard.com/report/github.com/tochemey/goakt)
[![codecov](https://codecov.io/gh/Tochemey/goakt/graph/badge.svg?token=J0p9MzwSRH)](https://codecov.io/gh/Tochemey/goakt)

Distributed [Go](https://go.dev/) actor framework to build reactive and distributed system in golang using
_**protocol buffers as actor messages**_.

GoAkt is highly scalable and available when running in cluster mode. It comes with the necessary features require to
build a distributed actor-based system without sacrificing performance and reliability. With GoAkt, you can instantly create a fast, scalable, distributed system
across a cluster of computers.

If you are not familiar with the actor model, the blog post from Brian Storti [here](https://www.brianstorti.com/the-actor-model/) is an excellent and short introduction to the actor model.
Also, check reference section at the end of the post for more material regarding actor model.

## Table of Content

- [Design Principles](#design-principles)
- [Use Cases](#use-cases)
- [Installation](#installation)
- [Versioning](#versioning)
- [Examples](#examples)
- [Features](#features)
  - [Actors](#actors)
  - [Passivation](#passivation)
  - [Supervision](#supervision)
  - [Actor System](#actor-system)
  - [Behaviors](#behaviors)
  - [Router](#router)
  - [Mailbox](#mailbox)
  - [Events Stream](#events-stream)
    - [Supported events](#supported-events)
  - [Messaging](#messaging)
  - [Scheduler](#scheduler)
    - [Cron Expression Format](#cron-expression-format)
    - [Note](#note)
  - [Stashing](#stashing)
  - [Remoting](#remoting)
  - [Cluster](#cluster)
  - [Observability](#observability)
    - [Metrics](#metrics)
    - [Logging](#logging)
  - [Security](#encryption-and-mtls)
  - [Testkit](#testkit)
- [API](#api)
- [Client](#client)
  - [Balancer strategies](#balancer-strategies)
  - [Features](#features-1)
- [Clustering](#clustering)
  - [Operations Guide](#operations-guide)
  - [Redeployment](#redeployment)
  - [Built-in Discovery Providers](#built-in-discovery-providers)
    - [Kubernetes Discovery Provider Setup](#kubernetes-discovery-provider-setup)
      - [Get Started](#get-started)
      - [Role Based Access](#role-based-access)
    - [mDNS Discovery Provider Setup](#mdns-discovery-provider-setup)
    - [NATS Discovery Provider Setup](#nats-discovery-provider-setup)
    - [DNS Provider Setup](#dns-provider-setup)
    - [Static Provider Setup](#static-provider-setup)
- [Contribution](#contribution)
  - [Test \& Linter](#test--linter)
- [Benchmark](#benchmark)

## Design Principles

This framework has been designed:

- to be very simple - it caters for the core component of an actor framework as stated by the father of the actor framework [here](https://youtu.be/7erJ1DV_Tlo).
- to be very easy to use.
- to have a clear and defined contract for messages - no need to implement/hide any sort of serialization.
- to make use existing battle-tested libraries in the go ecosystem - no need to reinvent solved problems.
- to be very fast.
- to expose interfaces for custom integrations rather than making it convoluted with unnecessary features.

## Use Cases

- Event-Driven programming
- Event Sourcing and CQRS - [eGo](https://github.com/Tochemey/ego)
- Highly Available, Fault-Tolerant Distributed Systems

## Installation

```bash
go get github.com/tochemey/goakt/v2
```

## Versioning

The version system adopted in GoAkt deviates a bit from the standard semantic versioning system.
The version format is as follows:

- The `MAJOR` part of the version will stay at `v2` for the meantime.
- The `MINOR` part of the version will cater for any new _features_, _breaking changes_  with a note on the breaking changes.
- The `PATCH` part of the version will cater for dependencies upgrades, bug fixes, security patches and co.

The versioning will remain like `v2.x.x` until further notice.

## Examples

Kindly check out the [examples'](https://github.com/Tochemey/goakt-examples) repository.

## Features

### Actors

The fundamental building blocks of GoAkt are actors.

- They are independent, isolated unit of computation with their own state.
- They can be _long-lived_ actors or be _passivated_ after some period of time that is configured during their
  creation. Use this feature with care when dealing with persistent actors (actors that require their state to be persisted).
- They are automatically thread-safe without having to use locks or any other shared-memory synchronization
  mechanisms.
- They can be stateful and stateless depending upon the system to build.
- Every actor in GoAkt:
  - has a process id [`PID`](./actors/pid.go). Via the process id any allowable action can be executed by the
    actor.
  - has a lifecycle via the following methods: [`PreStart`](./actors/actor.go), [`PostStop`](./actors/actor.go). 
      - `PreStart` hook is used to initialise actor state. It is like the actor constructor.
      - `PostStop` hook is used to clean up resources used by the Actor. It is like the actor destructor.
    It means it can live and die like any other process.
  - handles and responds to messages via the method [`Receive`](./actors/actor.go). While handling messages it
    can:
  - create other (child) actors via their process id [`PID`](./actors/pid.go) `SpawnChild` method
  - send messages to other actors locally or remotely via their process
    id [`PID`](./actors/pid.go) `Ask`, `RemoteAsk`(request/response
    fashion) and `Tell`, `RemoteTell`(fire-and-forget fashion) methods
  - stop (child) actors via their process id [`PID`](./actors/pid.go)
  - watch/unwatch (child) actors via their process id [`PID`](./actors/pid.go) `Watch` and `UnWatch` methods
  - supervise the failure behavior of (child) actors. 
  - remotely lookup for an actor on another node via their process id [`PID`](./actors/pid.go) `RemoteLookup`.
    This
    allows it to send messages remotely via `RemoteAsk` or `RemoteTell` methods
  - stash/unstash messages. See [Stashing](#stashing)
  - can adopt various form using the [Behavior](#behaviors) feature
  - can be restarted (respawned)
  - can be gracefully stopped (killed). Every message in the mailbox prior to stoppage will be processed within a
    configurable time period.

### Passivation

Actors can be passivated when they are idle after some period of time. Passivated actors are removed from the actor system to free-up resources.
When cluster mode is enabled, passivated actors are removed from the entire cluster. To bring back such actors to live, one needs to
`Spawn` them again. By default, all actors are passivated and the passivation time is `two minutes`.

- To enable passivation use the actor system option `WithExpireActorAfter(duration time.Duration)` when creating the actor system. See actor system [options](./actors/option.go).
- To disable passivation use the actor system option `WithPassivationDisabled` when creating the actor system. See actor system [options](./actors/option.go).

### Supervision

In GoAkt, supervision allows to define the various strategies to apply when a given actor is faulty.
The supervisory strategy to adopt is set during the creation of the actor system.
In GoAkt each child actor is treated separately. There is no concept of one-for-one and one-for-all strategies.
The following directives are supported:
- [`Restart`](./actors/supervisor_directive.go): to restart the child actor. One can control how the restart is done using the following options: - `maxNumRetries`: defines the maximum of restart attempts - `timeout`: how to attempt restarting the faulty actor.
- [`Stop`](./actors/supervisor_directive.go): to stop the child actor which is the default one
- [`Resume`](./actors/supervisor_directive.go): ignores the failure and process the next message, instead.

With the `Restart` directive, every child actor of the faulty is stopped and garbage-collected when the given parent is restarted. This helps avoid resources leaking.
There are only two scenarios where an actor can supervise another actor:
- It watches the given actor via the `Watch` method. With this method the parent actor can also listen to the `Terminated` message to decide what happens next to the child actor.
- The actor to be supervised is a child of the given actor.

### Actor System

Without an actor system, it is not possible to create actors in GoAkt. Only a single actor system
is recommended to be created per application when using GoAkt. At the moment the single instance is not enforced in GoAkt, this simple implementation is left to the discretion of the developer. To
create an actor system one just need to use
the [`NewActorSystem`](./actors/actor_system.go) method with the various [Options](./actors/option.go). GoAkt
ActorSystem has the following characteristics:

- Actors lifecycle management (Spawn, Kill, ReSpawn)
- Concurrency and Parallelism - Multiple actors can be managed and execute their tasks independently and
  concurrently. This helps utilize multicore processors efficiently.
- Location Transparency - The physical location of actors is abstracted when cluster mode is enabled. Remote actors can be accessed via their
  address once _remoting_ is enabled. 
- Fault Tolerance and Supervision - Set during the creation of the actor system.
- Actor Addressing - Every actor in the ActorSystem has an address.

### Behaviors

Actors in GoAkt have the power to switch their behaviors at any point in time. When you change the actor behavior, the new
behavior will take effect for all subsequent messages until the behavior is changed again. The current message will
continue processing with the existing behavior. You can use [Stashing](#stashing) to reprocess the current
message with the new behavior.

To change the behavior, call the following methods on the [ReceiveContext interface](./actors/context.go) when handling a message:

- `Become` - switches the current behavior of the actor to a new behavior.
- `UnBecome` - resets the actor behavior to the default one which is the Actor.Receive method.
- `BecomeStacked` - sets a new behavior to the actor to the top of the behavior stack, while maintaining the previous ones.
- `UnBecomeStacked()` - sets the actor behavior to the previous behavior before `BecomeStacked()` was called. This only works with `BecomeStacked()`.

### Router

Routers help send the same type of message to a set of actors to be processed in parallel depending upon
the type of the router used. Routers should be used with caution because they can hinder performance.
When the router receives a message to broadcast, every routee is checked whether alive or not.
When a routee is not alive the router removes it from its set of routees.
When the last routee stops the router itself stops.

GoAkt comes shipped with the following routing strategies:

- `Fan-Out`: This strategy broadcasts the given message to all its available routees in parallel.
- `Random`: This strategy randomly picks a routee in its set of routees and send the message to it.
- `Round-Robin`: This strategy sends messages to its routee in a round-robin way. For n messages sent through the router, each actor is forwarded one message.

A router a just like any other actor that can be spawned. To spawn router just call the [ActorSystem](./actors/actor_system.go) `SpawnRouter` method.
Router as well as their routees are not passivated.

### Mailbox

Once can implement a custom mailbox. See [Mailbox](./actors/mailbox.go).
GoAkt comes with the following mailboxes built-in:

- [`UnboundedMailbox`](./actors/unbounded_mailbox.go): this is the default mailbox. It is implemented using the lock-free Multi-Producer-Single-Consumer Queue.
- [`BoundedMailbox`](./actors/bounded_mailbox.go): this is a thread-safe mailbox implemented using the Ring-Buffer Queue. When the mailbox is full any new message is sent to the deadletter queue. Setting a reasonable capacity for the queue can enhance throughput.

### Events Stream

To receive some system events and act on them for some particular business cases, you just need to call the actor system `Subscribe`.
Make sure to `Unsubscribe` whenever the subscription is no longer needed to free allocated resources.
The subscription methods can be found on the `ActorSystem` interface.

#### Supported events

- [`ActorStarted`](./protos/goakt/goakt.proto): emitted when an actor has started
- [`ActorStopped`](./protos/goakt/goakt.proto): emitted when an actor has stopped
- [`ActorPassivated`](./protos/goakt/goakt.proto): emitted when an actor is passivated
- [`ActorChildCreated`](./protos/goakt/goakt.proto): emitted when a child actor is created
- [`ActorRestarted`](./protos/goakt/events/v1/events.proto): emitted when an actor has restarted
- [`NodeJoined`](./protos/goakt/goakt.proto): cluster event emitted when a node joins the cluster. This only happens when cluster mode is enabled
- [`NodeLeft`](./protos/goakt/goakt.proto): cluster event emitted when a node leaves the cluster. This only happens when cluster mode is enabled
- [`Deadletter`](./protos/goakt/goakt.proto): emitted when a message cannot be delivered or that were not handled by a given actor.
  Dead letters are automatically emitted when a message cannot be delivered to actors' mailbox or when an Ask times out.
  Also, one can emit dead letters from the receiving actor by using the `ctx.Unhandled()` method. This is useful instead of panicking when
  the receiving actor does not know how to handle a particular message. Dead letters are not propagated over the network, there are tied to the local actor system.

### Messaging

Communication between actors is achieved exclusively through message passing. In GoAkt _Google
Protocol Buffers_ is used to define messages.
The choice of protobuf is due to easy serialization over wire and strong schema definition. As stated previously the following messaging patterns are supported:

- `Tell/RemoteTell` - send a message to an actor and forget it. `Tell` is used for local messaging.
- `Ask/RemoteAsk` - send a message to an actor and expect a reply within a time period. `Ask` is used for local messaging.
- `SendAsync` - behave the same way as `Tell`. This call is location transparent which means that the system will locate the given actor whether locally or remotely to send the message. This
  is possible when cluster mode is enabled.
- `SendSync` - behave the same way as `Asks` except the location of the provided actor is transparent. This is possible when cluster mode is enabled.
- `Forward` - pass a message from one actor to the actor by preserving the initial sender of the message.
  At the moment you can only forward messages from the `ReceiveContext` when handling a message within an actor and this to a local actor.
- `BatchTell` - send a bulk of messages to actor in a fire-forget manner. Messages are processed one after the other in the other they have been sent.
- `BatchAsk` - send a bulk of messages to an actor and expect responses for each message sent within a time period. Messages are processed one after the other in the other they were sent.
  This help return the response of each message in the same order that message was sent. This method hinders performance drastically when the number of messages to sent is high.
  Kindly use this method with caution.
- `PipeTo` - send the successful result of a future(long-running task) to self or a given actor. This can be achieved from the [`PID`](./actors/pid.go) as well as from the [ReceiveContext](./actors/context.go)

### Scheduler

You can schedule sending messages to actor that will be acted upon in the future. To achieve that you can use the following methods on the [Actor System](./actors/actor_system.go):

- `ScheduleOnce` - will send the given message to a local actor _**once**_ after a given interval
- `Schedule` - will sendd the given message to a local actor at a given interval
- `RemoteSchedule` - will send the given message to a remote actor at a given interval. This requires remoting to be enabled on the actor system.
- `RemoteScheduleOnce` - will send the given message to a remote actor _**once**_ after a given interval. This requires remoting to be enabled on the actor system.
- `ScheduleWithCron` - will send the given message to a local actor using a [cron expression](#cron-expression-format).
- `RemoteScheduleWithCron` - will send the given message to a remote actor using a [cron expression](#cron-expression-format). This requires remoting to be enabled on the actor system.

#### Cron Expression Format

| Field        | Required | Allowed Values  | Allowed Special Characters |
|--------------|----------|-----------------|----------------------------|
| Seconds      | yes      | 0-59            | , - \* /                   |
| Minutes      | yes      | 0-59            | , - \* /                   |
| Hours        | yes      | 0-23            | , - \* /                   |
| Day of month | yes      | 1-31            | , - \* ? /                 |
| Month        | yes      | 1-12 or JAN-DEC | , - \* /                   |
| Day of week  | yes      | 1-7 or SUN-SAT  | , - \* ? /                 |
| Year         | no       | empty, 1970-    | , - \* /                   |

#### Note

When running the actor system in a cluster only one instance of a given scheduled message will be running across the entire cluster.

### Stashing

Stashing is a mechanism you can enable in your actors, so they can temporarily stash away messages they cannot or should
not handle at the moment.
Another way to see it is that stashing allows you to keep processing messages you can handle while saving for later
messages you can't.
Stashing are handled by GoAkt out of the actor instance just like the mailbox, so if the actor dies while processing a
message, all messages in the stash are processed.
This feature is usually used together with [Become/UnBecome](#behaviors), as they fit together very well, but this is
not a requirement.

It’s recommended to avoid stashing too many messages to avoid too much memory usage. If you try to stash more
messages than the capacity the actor will panic.
To use the stashing feature, call the following methods on the [ReceiveContext](./actors/context.go) when handling a message:

- `Stash()` - adds the current message to the stash buffer.
- `Unstash()` - unstashes the oldest message in the stash and prepends to the stash buffer.
- `UnstashAll()` - unstashes all messages from the stash buffer and prepends in the mailbox. Messages will be processed
  in the same order they arrived. The stash buffer will be empty after processing all messages, unless an exception is
  thrown or messages are stashed while unstashing.

### Remoting

[Remoting](./actors/remoting.go) allows remote actors to communicate. The underlying technology is gRPC. To enable remoting just use the `WithRemoting` option when
creating the actor system. See actor system [options](./actors/option.go). These are the following remoting features available:

- `RemoteTell`: to send a fire-and-forget message to an actor remotely
- `RemoteAsk`: to send a request/response type of message to a remote actor
- `RemoteBatchTell`: to send a fire-and-forget bulk of messages to a remote actor
- `RemoteBatchAsk`: to send a bulk messages to a remote actor with replies
- `RemoteLookup`: to lookup for an actor on a remote host
- `RemoteReSpawn`: to restarts an actor on a remote machine
- `RemoteStop`: to stop an actor on a remote machine
- `RemoteSpawn`: to start an actor on a remote machine. The given actor implementation must be registered using the [`Register`](./actors/actor_system.go) method of the actor system on the remote machine for this call to succeed.

These methods can be found as well as on the [PID](./actors/pid.go) which is the actor reference when an actor is created.

### Cluster

This offers simple scalability, partitioning (sharding), and re-balancing out-of-the-box. GoAkt nodes are automatically discovered. See [Clustering](#clustering).
Beware that at the moment, within the cluster the existence of an actor is unique.

### Observability

Observability is key in distributed system. It helps to understand and track the performance of a system.
GoAkt offers out of the box features that can help track, monitor and measure the performance of a GoAkt based system.

#### Metrics

The following methods have been implemented to help push some metrics to any observability tool:
  - Total Number of children at a given point in time [PID](./actors/pid.go)
  - Number of messages stashed at a given point in time [PID](./actors/pid.go)
  - Number of Restarts at a given point in time [PID](./actors/pid.go)
  - Latest message received processing duration in milliseconds [PID](./actors/pid.go)
  - Total Number of Actors at a given point in time [ActorSystem](./actors/actor_system.go)

#### Logging

A simple logging interface to allow custom logger to be implemented instead of using the default logger.

### Encryption and mTLS

GoAkt does not support at the moment any form of data encryption or TLS to prevent any form of mitm attack. This feature may come in the future.
At the moment, I will recommend a GoAkt-based application should be deployed behind a vpc or using a service mesh like Linkerd or Istio which offers great mTLS support when it comes to service communucation.

### Testkit

GoAkt comes packaged with a testkit that can help test that actors receive expected messages within _unit tests_.
The teskit in GoAkt uses underneath the [`https://github.com/stretchr/testify`](https://github.com/stretchr/testify) package.
To test that an actor receive and respond to messages one will have to:

1. Create an instance of the testkit: `testkit := New(ctx, t)` where `ctx` is a go context and `t` the instance of `*testing.T`. This can be done in setup before the run of each test.
2. Create the instance of the actor under test. Example: `pinger := testkit.Spawn(ctx, "pinger", &pinger{})`
3. Create an instance of test probe: `probe := testkit.NewProbe(ctx)` where `ctx` is a go context. One can set some [options](./testkit/option.go)
4. Use the probe to send a message to the actor under test. Example: `probe.Send(pinger, new(testpb.Ping))`
5. Assert that the actor under test has received the message and responded as expected using the probe methods:

- `ExpectMessage(message proto.Message)`: asserts that the message received from the test actor is the expected one
- `ExpectMessageWithin(duration time.Duration, message proto.Message)`: asserts that the message received from the test actor is the expected one within a time duration
- `ExpectNoMessage()`: asserts that no message is expected
- `ExpectAnyMessage() proto.Message`: asserts that any message is expected
- `ExpectAnyMessageWithin(duration time.Duration) proto.Message`: asserts that any message within a time duration
- `ExpectMessageOfType(messageType protoreflect.MessageType)`: asserts the expectation of a given message type
- `ExpectMessageOfTypeWithin(duration time.Duration, messageType protoreflect.MessageType)`: asserts the expectation of a given message type within a time duration

6. Make sure to shut down the testkit and the probe. Example: `probe.Stop()`, `testkit.Shutdown(ctx)` where `ctx` is a go context. These two calls can be in a tear down after all tests run.

To help implement unit tests in GoAkt-based applications. See [Testkit](./testkit)

## API

The [API](./actors/api.go) interface helps interact with a GoAkt actor system as kind of client. The following features are available:

- `Tell`: to send a message to an actor in a fire-and-forget manner.
- `Ask`: to send a message to an actor and expect a response within a given timeout.
- `BatchAsk`: to send a batch of requests to an actore remotely and expect responses back for each request.
- `BatchTell`: to send a batch of fire-and-forget messages to an actor remotely.
- `RemoteTell`: to send a fire-and-forget message to an actor remotely using the [Remoting](./actors/remoting.go) API.
- `RemoteAsk`: to send a request/response type of message to a remote actor using the [Remoting](./actors/remoting.go) API.
- `RemoteBatchTell`: to send a fire-and-forget bulk of messages to a remote actor using the [Remoting](./actors/remoting.go) API.
- `RemoteBatchAsk`: to send a bulk messages to a remote actor with replies using the [Remoting](./actors/remoting.go) API.
- `RemoteLookup`: to lookup for an actor on a remote host using the [Remoting](./actors/remoting.go) API.
- `RemoteReSpawn`: to restarts an actor on a remote machine using the [Remoting](./actors/remoting.go) API.
- `RemoteStop`: to stop an actor on a remote machine using the [Remoting](./actors/remoting.go) API.
- `RemoteSpawn`: to start an actor on a remote machine using the [Remoting](./actors/remoting.go) API. The given actor implementation must be registered using the [`Register`](./actors/actor_system.go) method of the actor system on the remote machine for this call to succeed.

## Client

The GoAkt client facilitates interaction with a specified GoAkt cluster, contingent upon the activation of cluster mode.
The client operates without knowledge of the specific node within the cluster that will process the request.
This feature is particularly beneficial when interfacing with a GoAkt cluster from an external system.
GoAkt client is equipped with a mini load-balancer that helps route requests to the appropriate node.

### Balancer strategies

- [Round Robin](./client/round_robin.go) - a given node is chosen using the round-robin strategy
- [Random](./client/random.go) - a given node is chosen randomly
- [Least Load](./client/least_load.go) - the node with the least number of actors is chosen

### Features

- `Kinds` - to list all the actor kinds in the cluster
- `Spawn` - to spawn an actor in the cluster
- `SpawnWithBalancer` - to spawn an actor in the cluster with a given balancer strategy
- `Stop` - to kill/stop an actor in the cluster
- `Ask` - to send a message to a given actor in the cluster and expect a response
- `Tell` - to send a fire-forget message to a given actor in the cluster
- `Whereis` - to locate and get the address of a given actor
- `ReSpawn` - to restart a given actor

## Clustering

The cluster engine depends upon the [discovery](./discovery/provider.go) mechanism to find other nodes in the cluster.
Under the hood, it leverages [Olric](https://github.com/buraksezer/olric)
to scale out and guarantee performant, reliable persistence, simple scalability, partitioning (sharding), and
re-balancing out-of-the-box. _**It requires remoting to be enabled**_. One can implement a custom hasher for the partitioning using
the [Hasher](./hash/hasher.go) interface and the `Actor System` [option](./actors/option.go) to set it. The default hasher uses the `XXH3 algorithm`.

At the moment the following providers are implemented:

- [kubernetes](https://kubernetes.io/docs/home/) [api integration](discovery/kubernetes) is fully functional
- [mDNS](https://datatracker.ietf.org/doc/html/rfc6762) and [DNS-SD](https://tools.ietf.org/html/rfc6763)
- [NATS](https://nats.io/) [integration](discovery/nats) is fully functional
- [DNS](discovery/dnssd) is fully functional
- [Static](discovery/static) is fully functional and for demo purpose

Note: One can add additional discovery providers using the following [discovery provider](./discovery/provider.go).

### Operations Guide

The following outlines the cluster mode operations which can help have a healthy GoAkt cluster:

- One can start a single node cluster or a multiple nodes cluster.
- One can add more nodes to the cluster which will automatically discover the cluster.
- One can remove nodes. However, to avoid losing data, one need to scale down the cluster to the minimum number of nodes
  which started the cluster.

### Redeployment

When a node leaves the cluster, as long as the cluster quorum is stable, its actors are redeployed on the remaining nodes of the cluster.
The redeployed actors are created with **_their initial state_**. Every field of the Actor set using the `PreStart` will have their value set
as expected. On the contrary every field of the Actor will be set to their default go type value because actors are created using reflection.

### Built-in Discovery Providers

#### Kubernetes Discovery Provider Setup

To get the kubernetes discovery working as expected, the following pod labels need to be set:

- `app.kubernetes.io/part-of`: set this label with the actor system name
- `app.kubernetes.io/component`: set this label with the application name
- `app.kubernetes.io/name`: set this label with the application name

##### Get Started

```go
package main

import "github.com/tochemey/goakt/v2/discovery/kubernetes"

const (
    namespace          = "default"
    applicationName    = "accounts"
    actorSystemName    = "AccountsSystem"
    discoveryPortName  = "discovery-port"
    peersPortName      = "peers-port"
    remotingPortName   = "remoting-port"
)

// define the discovery config
config := kubernetes.Config{
    ApplicationName:  applicationName,
    ActorSystemName:  actorSystemName,
    Namespace:        namespace,
    DiscoveryPortName:   gossipPortName,
    RemotingPortName: remotingPortName,
    PeersPortName:  peersPortName,
}

// instantiate the k8 discovery provider
disco := kubernetes.NewDiscovery(&config)

// pass the service discovery when enabling cluster mode in the actor system
```

##### Role Based Access

You’ll also have to grant the Service Account that your pods run under access to list pods. The following configuration
can be used as a starting point.
It creates a Role, pod-reader, which grants access to query pod information. It then binds the default Service Account
to the Role by creating a RoleBinding.
Adjust as necessary:

```
kind: Role
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: pod-reader
rules:
  - apiGroups: [""] # "" indicates the core API group
    resources: ["pods"]
    verbs: ["get", "watch", "list"]
---
kind: RoleBinding
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: read-pods
subjects:
  # Uses the default service account. Consider creating a new one.
  - kind: ServiceAccount
    name: default
roleRef:
  kind: Role
  name: pod-reader
  apiGroup: rbac.authorization.k8s.io
```

A working example can be found [here](https://github.com/Tochemey/goakt-examples/tree/main/actor-cluster/k8s)

#### mDNS Discovery Provider Setup

- `ServiceName`: the service name
- `Service`: the service type
- `Domain`: the mDNS discovery domain
- `Port`: the mDNS discovery port
- `IPv6`: it states whether to lookup for IPv6 addresses.

#### NATS Discovery Provider Setup

To use the NATS discovery provider one needs to provide the following:

- `NatsServer`: the NATS Server address
- `NatsSubject`: the NATS subject to use
- `ActorSystemName`: the actor system name
- `ApplicationName`: the application name
- `Timeout`: the nodes discovery timeout
- `MaxJoinAttempts`: the maximum number of attempts to connect an existing NATs server. Defaults to `5`
- `ReconnectWait`: the time to backoff after attempting a reconnect to a server that we were already connected to previously. Default to `2 seconds`
- `Host`: the given node host address
- `DiscoveryPort`: the discovery port of the given node

```go
package main

import "github.com/tochemey/goakt/v2/discovery/nats"

const (
    natsServerAddr   = "nats://127.0.0.1:4248"
    natsSubject      = "goakt-gossip"
    applicationName  = "accounts"
    actorSystemName  = "AccountsSystem"
)

// define the discovery options
config := nats.Config{
    ApplicationName: applicationName,
    ActorSystemName: actorSystemName,
    NatsServer:      natsServerAddr,
    NatsSubject:     natsSubject,
    Host:            "127.0.0.1",
    DiscoveryPort:   20380,
}

// instantiate the NATS discovery provider by passing the config and the hostNode
disco := nats.NewDiscovery(&config)

// pass the service discovery when enabling cluster mode in the actor system
```

#### DNS Provider Setup

This provider performs nodes discovery based upon the domain name provided. This is very useful when doing local development
using docker.

To use the DNS discovery provider one needs to provide the following:

- `DomainName`: the domain name
- `IPv6`: it states whether to lookup for IPv6 addresses.

```go
package main

import "github.com/tochemey/goakt/v2/discovery/dnssd"

const domainName = "accounts"

// define the discovery options
config := dnssd.Config{
    dnssd.DomainName: domainName,
    dnssd.IPv6:       false,
}
// instantiate the dnssd discovery provider
disco := dnssd.NewDiscovery(&config)

// pass the service discovery when enabling cluster mode in the actor system
```

A working example can be found [here](https://github.com/Tochemey/goakt-examples/tree/main/actor-cluster/dnssd)

#### Static Provider Setup

This provider performs nodes discovery based upon the list of static hosts addresses.
The address of each host is the form of `host:port` where `port` is the gossip protocol port.

```go
package main

import "github.com/tochemey/goakt/v2/discovery/static"

// define the discovery configuration
config := static.Config{
    Hosts: []string{
    "node1:3322",
    "node2:3322",
    "node3:3322",
    },
}
// instantiate the dnssd discovery provider
disco := static.NewDiscovery(&config)

// pass the service discovery when enabling cluster mode in the actor system
```

A working example can be found [here](https://github.com/Tochemey/goakt-examples/tree/main/actor-cluster/static)

## Contribution

Contributions are welcome!
The project adheres to [Semantic Versioning](https://semver.org)
and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
This repo uses [Earthly](https://earthly.dev/get-earthly).

To contribute please:

- Fork the repository
- Create a feature branch
- Submit a [pull request](https://help.github.com/articles/using-pull-requests)

### Test & Linter

Prior to submitting a [pull request](https://help.github.com/articles/using-pull-requests), please run:

```bash
earthly +test
```

## Benchmark

One can run the benchmark test from the [bench package](./bench):

- `make bench` to run the benchmark
- `make bench-stats` to see the benchmark stats