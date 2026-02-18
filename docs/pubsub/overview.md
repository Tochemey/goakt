# PubSub Overview

GoAkt provides a built-in publish-subscribe (pub/sub) messaging system so actors can communicate through topics. Producers and consumers are decoupled; delivery is asynchronous and fire-and-forget.

## Table of Contents

- 🤔 [What is Pub/Sub?](#what-is-pubsub)
- 🏗️ [Architecture](#architecture)
- ⚡ [Quick reference](#quick-reference)
- ⚙️ [Enabling Pub/Sub](#enabling-pubsub)
- 📤 [Basic Operations](#basic-operations)
- 📨 [Message Flow](#message-flow)
- 🌐 [Cluster Pub/Sub](#cluster-pubsub)
- ✅ [Best Practices](#best-practices)
- ⚡ [Performance & Troubleshooting](#performance--troubleshooting)
- ⚠️ [Limitations](#limitations)

---

## What is Pub/Sub?

- **Topic-based**: Actors publish to named topics; subscribers receive all messages for topics they subscribe to.
- **Subscriber registry**: A system-managed topic actor per system routes messages to subscribers.
- **Cluster-aware**: In cluster mode, messages are propagated to topic actors on other nodes so all subscribers receive them.
- **Asynchronous**: Fire-and-forget; no delivery guarantee or subscriber acknowledgment.

## Architecture

- **Topic actor**: System actor that holds subscriptions and forwards messages to subscribers.
- **Publishers**: Any actor that sends `Publish` to the topic actor.
- **Subscribers**: Actors that send `Subscribe` to the topic actor and receive published messages in `Receive`.
- **Cluster**: Each node has a topic actor; publishes are forwarded to other nodes’ topic actors, which then deliver to local subscribers.

## Quick reference

| What you need       | API                                                                                                                                 |
|---------------------|-------------------------------------------------------------------------------------------------------------------------------------|
| Enable pub/sub      | `actor.WithPubSub()` when creating the actor system                                                                                 |
| Get topic actor     | `ctx.ActorSystem().TopicActor()` (in `Receive`)                                                                                     |
| Subscribe           | `ctx.Tell(topicActor, &goaktpb.Subscribe{Topic: "name"})` (e.g. in `Receive` on `*goaktpb.PostStart`)                               |
| Unsubscribe         | `ctx.Tell(topicActor, &goaktpb.Unsubscribe{Topic: "name"})`                                                                         |
| Publish             | `ctx.Tell(topicActor, &goaktpb.Publish{Id: id, Topic: "name", Message: anypb.New(msg)})` (msg must be `proto.Message`)              |
| System event stream | `subscriber, err := system.Subscribe()` then `system.Unsubscribe(subscriber)` when done (actor lifecycle events, not topic pub/sub) |

## Enabling Pub/Sub

Enable when creating the actor system. In cluster mode, pub/sub is enabled automatically.

```go
system, err := actor.NewActorSystem("my-system", actor.WithPubSub())
// ...
system.Start(ctx)
defer system.Stop(ctx)
```

## Basic Operations

**Subscribe** in `Receive` on `*goaktpb.PostStart` (so you have `Self()` and topic actor). **Publish** by sending `goaktpb.Publish` with `Topic`, `Message` (as `anypb.Any` from a `proto.Message`), and a unique `Id`. **Unsubscribe** with `goaktpb.Unsubscribe{Topic: "name"}`.

Minimal example:

```go
func (a *SubscriberActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        ctx.Tell(ctx.ActorSystem().TopicActor(), &goaktpb.Subscribe{Topic: "notifications"})
    case *goaktpb.SubscribeAck:
        // subscribed
    case *MyEvent:
        // handle published message
    default:
        ctx.Unhandled()
    }
}

func (a *PublisherActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *DoPublish:
        payload, _ := anypb.New(&MyEvent{Data: msg.Data})
        ctx.Tell(ctx.ActorSystem().TopicActor(), &goaktpb.Publish{
            Id: uuid.New().String(), Topic: "notifications", Message: payload,
        })
    default:
        ctx.Unhandled()
    }
}
```

## Message Flow

- **Local:** Publisher → topic actor → all local subscribers of that topic.
- **Cluster:** Publisher → local topic actor → topic actors on other nodes → each node’s local subscribers. Message IDs are used to deduplicate.

## Cluster Pub/Sub

With clustering, pub/sub works across nodes: each node has a topic actor, and publishes are forwarded so every subscriber (on any node) receives the message. Use the same system name on all nodes; ensure remoting and cluster are configured.

```go
system, _ := actor.NewActorSystem("cluster-system",
    actor.WithRemote("node1", 3321),
    actor.WithCluster(clusterConfig), // pub/sub enabled
)
system.Start(ctx)
// Spawn subscribers and publishers on any node; messages propagate automatically
```

## Best Practices

- **Topic naming:** Use clear, hierarchical names (e.g. `notifications`, `order.created`).
- **Message design:** Use well-defined protobuf messages so they can be packed into `anypb.Any`.
- **Subscription:** Subscribe in `Receive` on `*goaktpb.PostStart`; unsubscribe on shutdown if needed (topic actor also cleans up when subscribers terminate).
- **Errors:** Delivery is fire-and-forget; handle processing errors inside the subscriber (log, dead-letter, or retry) without failing the actor if not critical.
- **Idempotency:** Use message IDs and track processed IDs in subscribers to avoid duplicate handling when messages can be delivered more than once.

## Performance & Troubleshooting

- **Volume:** Broadcasting goes to all subscribers; use more specific topics or partition by topic to limit fan-out.
- **Subscribers not receiving:** Ensure `WithPubSub()` is set, subscription is done (e.g. after `SubscribeAck`), and topic names match exactly.
- **Duplicates:** Implement idempotency; ensure publish IDs are unique.
- **Cluster:** If remote subscribers don’t receive messages, verify cluster health, remoting, and connectivity.

## Limitations

- **At-most-once delivery**: No guarantee; messages are not persisted or replayed.
- **Fire-and-forget**: No subscriber acknowledgment or backpressure.
