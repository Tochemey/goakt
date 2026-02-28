---
title: PubSub
description: Application-level topic-based publish/subscribe messaging.
sidebarTitle: "📢 PubSub"
---

**PubSub** enables application-level topic-based messaging. Actors subscribe to named topics and receive messages published to those topics by other actors. This is **separate from the event stream**, which only handles system and cluster events (see [Event Streams](event-streams)).

## Architecture

Each node runs one **topic actor** (`GoAktTopicActor`). Local actors subscribe to and publish through their **local** topic actor. The topic actor maintains a registry of topics and their local subscribers.

### Standalone mode

```
+----------------------------------------------------------+
|  Node                                                    |
|                                                          |
|   Publisher A --Publish--> +----------------+            |
|                            | Topic Actor    |            |
|   Subscriber B <--Tell---- | (local)        |            |
|   Subscriber C <--Tell---- +----------------+            |
|                                                          |
+----------------------------------------------------------+
```

Publishers send `Publish` to the local topic actor. The topic actor delivers the message to all local subscribers via `Tell`.

### Cluster mode

In a cluster, each node has its own topic actor. **Topic actors communicate with each other** to disseminate messages. When a publish occurs:

1. The publisher sends `Publish` to its local topic actor.
2. The local topic actor delivers to its local subscribers.
3. The local topic actor sends a `TopicMessage` (serialized) to each peer node's topic actor via remoting.
4. Each remote topic actor receives the `TopicMessage`, deserializes it, and delivers to **its** local subscribers.

```
  NODE A                    NODE B                    NODE C
  -------                   -------                   -------
  Publisher                 Topic Actor               Topic Actor
      |                         ^                         ^
      | Publish                 | TopicMessage            | TopicMessage
      v                         | (remoting)              | (remoting)
  Topic Actor ------------------+-------------------------+
      |                         |                         |
      | Tell (local)            | Tell (local)            | Tell (local)
      v                         v                         v
  Subscriber 1, 2           Subscriber X, Y           Subscriber Z
```

**Key point:** Subscribers always register with their **local** topic actor. Topic actors are responsible for cross-node dissemination; application actors never talk to remote topic actors directly.

## When to use

- Decouple publishers from subscribers—publishers do not need to know who is listening
- Broadcast application events (e.g. order created, inventory updated) to multiple actors
- Event-driven architectures where actors react to domain events

## Enabling PubSub

The **topic actor** is spawned when either:

- `WithPubSub()` is passed when creating the actor system, or
- Cluster mode is enabled (`WithCluster`)

```go
system, _ := actor.NewActorSystem("app", actor.WithPubSub())
// or: actor.WithCluster(config) — topic actor is spawned when cluster is enabled
```

## Topic actor

| Method                     | Purpose                                                                     |
|----------------------------|-----------------------------------------------------------------------------|
| `ActorSystem.TopicActor()` | Returns the topic actor's PID. Use to send Subscribe, Unsubscribe, Publish. |

The topic actor has a reserved name (`GoAktTopicActor`). From within an actor, use `ctx.ActorSystem().TopicActor()`. From outside (e.g. `main`), use `system.TopicActor()`. Returns `nil` if PubSub is not enabled.

## Subscribe and Unsubscribe

Actors send messages to the **local** topic actor to subscribe or unsubscribe from topics:

| Message       | Constructor                   | Purpose                                                                     |
|---------------|-------------------------------|-----------------------------------------------------------------------------|
| `Subscribe`   | `actor.NewSubscribe(topic)`   | Subscribe this actor to a topic. Sender receives `SubscribeAck` on success. |
| `Unsubscribe` | `actor.NewUnsubscribe(topic)` | Unsubscribe from a topic. Sender receives `UnsubscribeAck` on success.      |

The topic actor watches subscribers. When a subscriber terminates, it is automatically removed from all topics.

```go
ctx.Tell(ctx.ActorSystem().TopicActor(), actor.NewSubscribe("orders"))
// later: ctx.Tell(ctx.ActorSystem().TopicActor(), actor.NewUnsubscribe("orders"))
```

## Publish

| Message   | Constructor                            | Purpose                                                                                           |
|-----------|----------------------------------------|---------------------------------------------------------------------------------------------------|
| `Publish` | `actor.NewPublish(id, topic, message)` | Publish a message to all subscribers of the topic. `id` is a unique message ID for deduplication. |

```go
msg := actor.NewPublish(uuid.New().String(), "orders", orderEvent)
ctx.Tell(ctx.ActorSystem().TopicActor(), msg)
```

Subscribers receive the message as a normal `Receive` invocation—the payload is the published `message`, not a wrapper.

## Cluster behavior

- **Local delivery:** The topic actor that receives the `Publish` delivers to its local subscribers immediately.
- **Remote dissemination:** The topic actor sends a serialized `TopicMessage` to each peer's topic actor via remoting. Each peer's topic actor deserializes and delivers to its local subscribers.
- **Deduplication:** Uses sender ID, topic, and message ID to avoid processing the same message twice (e.g. when a topic actor receives a `TopicMessage` from multiple paths).

## Example

```go
// Subscriber actor
func (a *OrderLogger) Receive(ctx *ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *PostStart:
        ctx.Tell(ctx.ActorSystem().TopicActor(), actor.NewSubscribe("orders"))
    case *OrderCreated:
        log.Printf("order created: %s", msg.ID)
    case *actor.UnsubscribeAck:
        // unsubscribed
    }
}

// Publisher actor
func (a *OrderService) Receive(ctx *ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *CreateOrder:
        order := a.createOrder(msg)
        pub := actor.NewPublish(order.ID, "orders", order)
        ctx.Tell(ctx.ActorSystem().TopicActor(), pub)
    }
}
```

## Event stream vs PubSub

| Aspect      | Event stream                          | PubSub                               |
|-------------|---------------------------------------|--------------------------------------|
| Purpose     | System and cluster observability      | Application-level event distribution |
| Topics      | Fixed internal topic                  | Application-defined topics           |
| Publishers  | Framework (PID, cluster, dead-letter) | Your actors                          |
| Subscribers | External (monitoring, logging)        | Your actors                          |
| API         | `Subscribe()` / `Unsubscribe()`       | Messages to `TopicActor()`           |
| Option      | Always present                        | `WithPubSub()` or cluster            |
