# PubSub Overview

GoAkt provides a built-in publish-subscribe (pub/sub) messaging system that enables actors to communicate through topics. This decouples message producers from consumers, allowing dynamic and scalable message distribution patterns.

## Table of Contents

- 🤔 [What is Pub/Sub?](#what-is-pubsub)
- 🏗️ [Architecture](#architecture)
- ⚡ [Quick reference](#quick-reference)
- ⚙️ [Enabling Pub/Sub](#enabling-pubsub)
- 📤 [Basic Operations](#basic-operations)
- 📨 [Message Flow](#message-flow)
- 💡 [Complete Example](#complete-example)
- 🌐 [Cluster Pub/Sub](#cluster-pubsub)
- ✅ [Best Practices](#best-practices)
- ⚡ [Performance Considerations](#performance-considerations)
- 🔧 [Troubleshooting](#troubleshooting)
- ⚠️ [Limitations](#limitations)
- 🔀 [Alternatives](#alternatives)
- ➡️ [Next Steps](#next-steps)

---

## What is Pub/Sub?

Pub/Sub in GoAkt is:

- **Topic-based messaging**: Actors publish messages to named topics
- **Subscriber registry**: Topic actor manages subscribers for each topic
- **Broadcast delivery**: Messages sent to all subscribers of a topic
- **Cluster-aware**: Automatically propagates messages across cluster nodes
- **Asynchronous**: Fire-and-forget message delivery
- **Decoupled**: Publishers don't need to know about subscribers

## Architecture

```
  Publishers              Topic Actor             Subscribers

+----------------+     +------------------+     +----------------+
|  Publisher 1   |---->|                  |---->|  Subscriber A  |
+----------------+     |                  |     +----------------+
                       |                  |
+----------------+     |   Topic Actor    |     +----------------+
|  Publisher 2   |---->|    (System)      |---->|  Subscriber B  |
+----------------+     |                  |     +----------------+
                       |                  |
                       |                  |     +----------------+
                       |                  |---->|  Subscriber C  |
                       +--------+---------+     +----------------+
                                |
                                | (In Cluster Mode)
                                v
                       +------------------+
                       | Remote Topic     |
                       | Actors           |
                       | (Other Nodes)    |
                       +------------------+
```

### Components

1. **Topic Actor**: System-managed actor that routes messages to subscribers
2. **Topics**: Named channels for message distribution (strings)
3. **Publishers**: Actors that send messages to topics
4. **Subscribers**: Actors that receive messages from topics
5. **Cluster Propagation**: Automatic message forwarding across nodes

## Quick reference

| What you need       | API                                                                                                                                     |
|---------------------|-----------------------------------------------------------------------------------------------------------------------------------------|
| Enable pub/sub      | `actor.WithPubSub()` when creating the actor system                                                                                     |
| Get topic actor     | `ctx.ActorSystem().TopicActor()` (in `Receive`)                                                                                         |
| Subscribe           | `ctx.Tell(topicActor, &goaktpb.Subscribe{Topic: "name"})` (e.g. in `Receive` on `*goaktpb.PostStart`)                                   |
| Unsubscribe         | `ctx.Tell(topicActor, &goaktpb.Unsubscribe{Topic: "name"})`                                                                             |
| Publish             | `ctx.Tell(topicActor, &goaktpb.Publish{Id: id, Topic: "name", Message: anypb.New(msg)})` (msg must be `proto.Message`)                  |
| System event stream | `subscriber, err := system.Subscribe()` then `system.Unsubscribe(subscriber)` when done (for actor lifecycle events, not topic pub/sub) |

## Enabling Pub/Sub

Pub/Sub must be explicitly enabled when creating the actor system:

```go
import "github.com/tochemey/goakt/v3/actor"

system, err := actor.NewActorSystem(
    "my-system",
    actor.WithPubSub(), // Enable pub/sub
)
if err != nil {
    log.Fatal(err)
}

if err := system.Start(ctx); err != nil {
    log.Fatal(err)
}
defer system.Stop(ctx)
```

**Important**:

- Without `WithPubSub()`, the topic actor is not created
- In cluster mode, pub/sub is automatically enabled on all nodes
- The topic actor is a long-lived system actor

## Basic Operations

### Subscribe to a Topic

Actors subscribe to topics by sending a `Subscribe` message to the topic actor:

```go
package main

import (
    "context"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/goaktpb"
)

type SubscriberActor struct{}

func (a *SubscriberActor) PreStart(ctx *actor.Context) error {
    return nil
}

func (a *SubscriberActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        // Subscribe when actor has started (PreStart has no Self())
        topicActor := ctx.ActorSystem().TopicActor()
        ctx.Tell(topicActor, &goaktpb.Subscribe{Topic: "notifications"})
    case *goaktpb.SubscribeAck:
        ctx.Logger().Infof("Subscribed to topic: %s", msg.Topic)

    case *NotificationMessage:
        // Handle published message
        ctx.Logger().Infof("Received notification: %v", msg)

    default:
        ctx.Unhandled()
    }
}

func (a *SubscriberActor) PostStop(ctx *actor.Context) error {
    return nil
}
```

### Publish to a Topic

Actors publish messages by sending a `Publish` message to the topic actor. The payload must be a **protobuf message** (implement `proto.Message`) so it can be packed into `anypb.Any` and delivered to subscribers:

```go
type PublisherActor struct{}

func (a *PublisherActor) PreStart(ctx *actor.Context) error {
    return nil
}

func (a *PublisherActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *SendNotification:
        // Get topic actor
        topicActor := ctx.ActorSystem().TopicActor()

        // Pack message into Any
        payload, err := anypb.New(&NotificationMessage{
            Title:   msg.Title,
            Content: msg.Content,
        })
        if err != nil {
            ctx.Err(err)
            return
        }

        // Publish to topic
        ctx.Tell(topicActor, &goaktpb.Publish{
            Id:      generateMessageID(),
            Topic:   "notifications",
            Message: payload,
        })
        ctx.Response(&PublishSuccess{})

    default:
        ctx.Unhandled()
    }
}

func (a *PublisherActor) PostStop(ctx *actor.Context) error {
    return nil
}

func generateMessageID() string {
    return uuid.New().String()
}
```

### Unsubscribe from a Topic

Actors unsubscribe by sending an `Unsubscribe` message:

```go
func (a *SubscriberActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *StopListening:
        topicActor := ctx.ActorSystem().TopicActor()

        ctx.Tell(topicActor, &goaktpb.Unsubscribe{Topic: "notifications"})

    case *goaktpb.UnsubscribeAck:
        ctx.Logger().Infof("Unsubscribed from topic: %s", msg.Topic)

    default:
        ctx.Unhandled()
    }
}
```

## Message Flow

### Local Pub/Sub

In a single-node setup:

```
Publisher             Topic Actor          Subscriber A
    │                      │                      │
    │  Publish(topic,msg)  │                      │
    ├─────────────────────>│                      │
    │                      │   Forward msg        │
    │                      ├─────────────────────>│
    │                      │                      │
    │                      │   Forward msg    Subscriber B
    │                      ├───────────────────────────>│
    │                      │                            │
```

### Cluster Pub/Sub

In cluster mode, messages are propagated to all nodes:

```
       Node 1                   Node 2                   Node 3
+------------------+     +------------------+     +------------------+
|                  |     |                  |     |                  |
| +-------------+  |     |                  |     |                  |
| | Publisher   |  |     |                  |     |                  |
| +------+------+  |     |                  |     |                  |
|        |         |     |                  |     |                  |
|        v         |     |                  |     |                  |
| +-------------+  | Fwd | +-------------+  | Fwd | +-------------+  |
| | Topic Actor +--+---->| | Topic Actor +--+---->| | Topic Actor |  |
| +------+------+  |     | +------+------+  |     | +------+------+  |
|        |         |     |        |         |     |        |         |
|        v         |     |        v         |     |        v         |
| +-------------+  |     | +-------------+  |     | +-------------+  |
| |Subscriber A |  |     | |Subscriber B |  |     | |Subscriber C |  |
| +-------------+  |     | +-------------+  |     | +-------------+  |
|                  |     |                  |     |                  |
+------------------+     +------------------+     +------------------+
```

**Key Points**:

- Each node has its own topic actor
- Messages are forwarded to topic actors on other nodes
- Each topic actor delivers to its local subscribers
- Duplicate detection prevents message loops

## Complete Example

### Notification System

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/goaktpb"
    "google.golang.org/protobuf/types/known/anypb"
)

// NotificationMessage is the message published to the notification topic
type NotificationMessage struct {
    Type    string
    Title   string
    Content string
    UserID  string
}

// EmailSubscriber listens for notifications and sends emails
type EmailSubscriber struct {
    emailService EmailService
}

func (a *EmailSubscriber) PreStart(ctx *actor.Context) error {
    return nil
}

func (a *EmailSubscriber) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        topicActor := ctx.ActorSystem().TopicActor()
        ctx.Tell(topicActor, &goaktpb.Subscribe{Topic: "notifications"})
    case *goaktpb.SubscribeAck:
        ctx.Logger().Infof("Email subscriber ready for topic: %s", msg.Topic)

    case *NotificationMessage:
        // Send email
        err := a.emailService.SendEmail(
            msg.UserID,
            msg.Title,
            msg.Content,
        )
        if err != nil {
            ctx.Logger().Errorf("Failed to send email: %v", err)
        } else {
            ctx.Logger().Infof("Email sent to user %s", msg.UserID)
        }

    default:
        ctx.Unhandled()
    }
}

func (a *EmailSubscriber) PostStop(ctx *actor.Context) error {
    return nil
}

// PushNotificationSubscriber listens for notifications and sends push notifications
type PushNotificationSubscriber struct {
    pushService PushService
}

func (a *PushNotificationSubscriber) PreStart(ctx *actor.Context) error {
    return nil
}

func (a *PushNotificationSubscriber) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        topicActor := ctx.ActorSystem().TopicActor()
        ctx.Tell(topicActor, &goaktpb.Subscribe{Topic: "notifications"})
    case *goaktpb.SubscribeAck:
        ctx.Logger().Infof("Push subscriber ready for topic: %s", msg.Topic)

    case *NotificationMessage:
        // Send push notification
        err := a.pushService.SendPush(
            msg.UserID,
            msg.Title,
            msg.Content,
        )
        if err != nil {
            ctx.Logger().Errorf("Failed to send push: %v", err)
        } else {
            ctx.Logger().Infof("Push sent to user %s", msg.UserID)
        }

    default:
        ctx.Unhandled()
    }
}

func (a *PushNotificationSubscriber) PostStop(ctx *actor.Context) error {
    return nil
}

// NotificationPublisher publishes notifications
type NotificationPublisher struct{}

func (a *NotificationPublisher) PreStart(ctx *actor.Context) error {
    return nil
}

func (a *NotificationPublisher) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *SendNotificationRequest:
        topicActor := ctx.ActorSystem().TopicActor()

        // Create notification
        notification := &NotificationMessage{
            Type:    msg.Type,
            Title:   msg.Title,
            Content: msg.Content,
            UserID:  msg.UserID,
        }

        // Pack into Any
        payload, err := anypb.New(notification)
        if err != nil {
            ctx.Err(err)
            return
        }

        // Publish to topic
        ctx.Tell(topicActor, &goaktpb.Publish{
            Id:      generateMessageID(),
            Topic:   "notifications",
            Message: payload,
        })
        ctx.Response(&NotificationSent{})

    default:
        ctx.Unhandled()
    }
}

func (a *NotificationPublisher) PostStop(ctx *actor.Context) error {
    return nil
}

func main() {
    ctx := context.Background()

    // Create actor system with pub/sub enabled
    system, err := actor.NewActorSystem(
        "notification-system",
        actor.WithPubSub(), // Enable pub/sub
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    // Spawn subscribers
    emailSub, err := system.Spawn(ctx, "email-subscriber", &EmailSubscriber{
        emailService: NewEmailService(),
    })
    if err != nil {
        log.Fatal(err)
    }

    pushSub, err := system.Spawn(ctx, "push-subscriber", &PushNotificationSubscriber{
        pushService: NewPushService(),
    })
    if err != nil {
        log.Fatal(err)
    }

    // Spawn publisher
    publisher, err := system.Spawn(ctx, "publisher", &NotificationPublisher{})
    if err != nil {
        log.Fatal(err)
    }

    // Wait for subscriptions to complete
    time.Sleep(100 * time.Millisecond)

    // Publish a notification
    _, err = publisher.Ask(ctx, &SendNotificationRequest{
        Type:    "welcome",
        Title:   "Welcome!",
        Content: "Thanks for joining",
        UserID:  "user-123",
    }, 5*time.Second)
    if err != nil {
        log.Fatal(err)
    }

    // Both email and push subscribers receive the message
    time.Sleep(time.Second)
}
```

## Cluster Pub/Sub

When cluster mode is enabled, pub/sub automatically works across all nodes:

```go
// Node 1
system1, _ := actor.NewActorSystem(
    "cluster-system",
    actor.WithRemote("node1", 3321),
    actor.WithCluster(clusterConfig), // Pub/sub auto-enabled in cluster
)
system1.Start(ctx)

// Spawn subscriber on Node 1
subscriber1, _ := system1.Spawn(ctx, "subscriber-1", &MySubscriber{})

// Node 2
system2, _ := actor.NewActorSystem(
    "cluster-system",
    actor.WithRemote("node2", 3321),
    actor.WithCluster(clusterConfig),
)
system2.Start(ctx)

// Spawn publisher on Node 2
publisher, _ := system2.Spawn(ctx, "publisher", &MyPublisher{})

// Publish from Node 2
publisher.Tell(ctx, &PublishMessage{})

// Subscriber on Node 1 receives the message automatically
```

**Important**:

- All cluster nodes must have the same system name
- Topic actors are automatically created on all nodes
- Messages are deduplicated to prevent loops
- Network partitions may cause temporary message loss

## Best Practices

### Topic Naming

```go
// Good: Descriptive, hierarchical names
"notifications"
"user.events"
"order.created"
"payment.processed"

// Bad: Generic or unclear names
"topic1"
"messages"
"events"
```

### Message Design

```go
// Good: Well-defined protobuf messages
type OrderCreatedEvent struct {
    OrderID    string
    CustomerID string
    Total      float64
    CreatedAt  time.Time
}

// Bad: Generic or unstructured
type GenericEvent struct {
    Data map[string]interface{}
}
```

### Subscription Management

```go
// Subscribe in Receive on PostStart (PreStart has no Self())
func (a *MyActor) PreStart(ctx *actor.Context) error {
    return nil
}

func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        topicActor := ctx.ActorSystem().TopicActor()
        ctx.Tell(topicActor, &goaktpb.Subscribe{Topic: "my-topic"})
    case *goaktpb.SubscribeAck:
        // subscribed
    default:
        ctx.Unhandled()
    }
}

// Unsubscribe before shutdown (optional, handled automatically)
func (a *MyActor) PostStop(ctx *actor.Context) error {
    // Topic actor automatically removes terminated subscribers
    return nil
}
```

### Error Handling

```go
func (a *SubscriberActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ImportantEvent:
        if err := a.processEvent(msg); err != nil {
            // Log error but don't fail - message delivery is fire-and-forget
            ctx.Logger().Errorf("Failed to process event: %v", err)
            // Optionally send to dead letter or retry queue
        }
    }
}
```

### Idempotency

```go
type IdempotentSubscriber struct {
    processedIDs map[string]bool
}

func (a *IdempotentSubscriber) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *EventMessage:
        // Check if already processed
        if a.processedIDs[msg.ID] {
            ctx.Logger().Debugf("Skipping duplicate event: %s", msg.ID)
            return
        }

        // Process event
        a.handleEvent(msg)

        // Mark as processed
        a.processedIDs[msg.ID] = true
    }
}
```

## Performance Considerations

### Message Volume

- **High volume**: Pub/sub broadcasts to all subscribers
- **Selective subscription**: Use multiple specific topics instead of one broad topic
- **Batching**: Group related events when possible

### Subscriber Count

- Many subscribers increase delivery latency
- Topic actor sends to all subscribers concurrently
- Consider partitioning by topic or actor groups

### Cluster Scale

- Messages are forwarded to all nodes
- Network bandwidth grows with cluster size
- Use targeted messaging for point-to-point communication

## Troubleshooting

### Subscribers Not Receiving Messages

**Symptoms**:

- Published messages don't reach subscribers

**Solutions**:

- Verify pub/sub is enabled (`WithPubSub()`)
- Check subscription completed (`SubscribeAck` received)
- Verify topic names match exactly
- Check subscriber actor is running

### Messages Received Multiple Times

**Symptoms**:

- Duplicate message delivery

**Solutions**:

- Implement idempotency checking
- Verify message ID uniqueness
- Check for multiple subscriptions to same topic

### High Memory Usage

**Symptoms**:

- Topic actor consuming excessive memory

**Solutions**:

- Topic actor tracks processed message IDs to prevent duplicates
- Old message IDs accumulate over time
- Consider implementing periodic cleanup (currently not built-in)

### Messages Not Propagating in Cluster

**Symptoms**:

- Local subscribers receive messages, remote don't

**Solutions**:

- Verify cluster is healthy (`Peers()` returns all nodes)
- Check remoting is enabled on all nodes
- Verify network connectivity between nodes
- Check firewall rules allow remoting ports

## Limitations

1. **At-most-once delivery**: No delivery guarantees
2. **No message persistence**: Messages not stored
3. **No replay**: Historical messages not available
4. **Fire-and-forget**: No acknowledgment from subscribers
5. **Memory-bound**: Processed message IDs kept in memory

## Alternatives

For different requirements, consider:

- **Event Store**: For event sourcing and replay
- **Message Queue**: For guaranteed delivery (RabbitMQ, Kafka)
- **Actor messaging**: For point-to-point with acks
- **Event Stream**: For local event broadcasting (non-clustered)

## Next Steps

- [Usage Patterns](usage.md): Practical pub/sub examples
- [Event Stream](../events_stream/overview.md): Local event broadcasting
- [Cluster Overview](../cluster/overview.md): Understanding cluster mode
- [Actors Overview](../actors/overview.md): Actor fundamentals
