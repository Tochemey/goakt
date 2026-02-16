# PubSub Usage Patterns

This guide demonstrates practical patterns and real-world use cases for the pub/sub system in GoAkt.

## Event Broadcasting Patterns

### Event Notification System

Broadcast events to multiple interested parties:

```go
package main

import (
    "context"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/goaktpb"
    "google.golang.org/protobuf/types/known/anypb"
)

// Domain events
type OrderCreatedEvent struct {
    OrderID    string
    CustomerID string
    Total      float64
    CreatedAt  time.Time
}

type OrderCancelledEvent struct {
    OrderID     string
    Reason      string
    CancelledAt time.Time
}

// Publisher: Order service
type OrderServiceActor struct{}

func (a *OrderServiceActor) PreStart(ctx *actor.Context) error {
    return nil
}

func (a *OrderServiceActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *CreateOrderRequest:
        // Create order
        order := a.createOrder(msg)

        // Publish event
        event := &OrderCreatedEvent{
            OrderID:    order.ID,
            CustomerID: msg.CustomerID,
            Total:      order.Total,
            CreatedAt:  time.Now(),
        }

        a.publishEvent(ctx, "order.events", event)
        ctx.Response(&OrderCreated{OrderID: order.ID})

    case *CancelOrderRequest:
        // Cancel order
        event := &OrderCancelledEvent{
            OrderID:     msg.OrderID,
            Reason:      msg.Reason,
            CancelledAt: time.Now(),
        }

        a.publishEvent(ctx, "order.events", event)
        ctx.Response(&OrderCancelled{})

    default:
        ctx.Unhandled()
    }
}

func (a *OrderServiceActor) publishEvent(ctx *actor.ReceiveContext, topic string, event proto.Message) {
    topicActor := ctx.ActorSystem().TopicActor()

    payload, err := anypb.New(event)
    if err != nil {
        ctx.Logger().Errorf("Failed to pack event: %v", err)
        return
    }

    ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Publish{
        Id:      uuid.New().String(),
        Topic:   topic,
        Message: payload,
    })
}

func (a *OrderServiceActor) PostStop(ctx *actor.Context) error {
    return nil
}

// Subscriber 1: Inventory service
type InventoryActor struct{}

func (a *InventoryActor) PreStart(ctx *actor.Context) error {
    topicActor := ctx.ActorSystem().TopicActor()
    return ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Subscribe{
        Topic: "order.events",
    })
}

func (a *InventoryActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.SubscribeAck:
        ctx.Logger().Info("Inventory service subscribed to order events")

    case *OrderCreatedEvent:
        // Reserve inventory
        ctx.Logger().Infof("Reserving inventory for order %s", msg.OrderID)
        // ... inventory logic

    case *OrderCancelledEvent:
        // Release inventory
        ctx.Logger().Infof("Releasing inventory for order %s", msg.OrderID)
        // ... inventory logic

    default:
        ctx.Unhandled()
    }
}

func (a *InventoryActor) PostStop(ctx *actor.Context) error {
    return nil
}

// Subscriber 2: Analytics service
type AnalyticsActor struct{}

func (a *AnalyticsActor) PreStart(ctx *actor.Context) error {
    topicActor := ctx.ActorSystem().TopicActor()
    return ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Subscribe{
        Topic: "order.events",
    })
}

func (a *AnalyticsActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.SubscribeAck:
        ctx.Logger().Info("Analytics service subscribed to order events")

    case *OrderCreatedEvent:
        // Track metrics
        ctx.Logger().Infof("Recording order creation: %s, total: %.2f", msg.OrderID, msg.Total)
        // ... analytics logic

    case *OrderCancelledEvent:
        // Track cancellations
        ctx.Logger().Infof("Recording cancellation: %s", msg.OrderID)
        // ... analytics logic

    default:
        ctx.Unhandled()
    }
}

func (a *AnalyticsActor) PostStop(ctx *actor.Context) error {
    return nil
}
```

### Multi-Topic Subscriber

Actor subscribing to multiple topics:

```go
type MultiTopicActor struct {
    subscribed map[string]bool
}

func (a *MultiTopicActor) PreStart(ctx *actor.Context) error {
    a.subscribed = make(map[string]bool)

    topicActor := ctx.ActorSystem().TopicActor()
    topics := []string{"orders", "payments", "shipments"}

    for _, topic := range topics {
        err := ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Subscribe{
            Topic: topic,
        })
        if err != nil {
            return err
        }
    }

    return nil
}

func (a *MultiTopicActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.SubscribeAck:
        a.subscribed[msg.Topic] = true
        ctx.Logger().Infof("Subscribed to topic: %s", msg.Topic)

        // Check if all subscriptions complete
        if len(a.subscribed) == 3 {
            ctx.Logger().Info("All subscriptions completed")
        }

    case *OrderEvent:
        ctx.Logger().Infof("Order event from topic 'orders': %v", msg)

    case *PaymentEvent:
        ctx.Logger().Infof("Payment event from topic 'payments': %v", msg)

    case *ShipmentEvent:
        ctx.Logger().Infof("Shipment event from topic 'shipments': %v", msg)

    default:
        ctx.Unhandled()
    }
}

func (a *MultiTopicActor) PostStop(ctx *actor.Context) error {
    return nil
}
```

## Advanced Patterns

### Fan-Out Processing

Distribute work across multiple worker actors via pub/sub:

```go
// Publisher: Job queue
type JobQueueActor struct{}

func (a *JobQueueActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *EnqueueJob:
        topicActor := ctx.ActorSystem().TopicActor()

        job := &JobMessage{
            JobID:     msg.JobID,
            Type:      msg.Type,
            Payload:   msg.Payload,
            Timestamp: time.Now(),
        }

        payload, _ := anypb.New(job)

        // Publish to workers topic
        ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Publish{
            Id:      msg.JobID,
            Topic:   "jobs.pending",
            Message: payload,
        })

        ctx.Response(&JobEnqueued{JobID: msg.JobID})
    }
}

// Subscriber: Worker actors (multiple instances)
type WorkerActor struct {
    workerID string
}

func (a *WorkerActor) PreStart(ctx *actor.Context) error {
    a.workerID = ctx.ActorName()

    topicActor := ctx.ActorSystem().TopicActor()
    return ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Subscribe{
        Topic: "jobs.pending",
    })
}

func (a *WorkerActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.SubscribeAck:
        ctx.Logger().Infof("Worker %s ready", a.workerID)

    case *JobMessage:
        ctx.Logger().Infof("Worker %s processing job %s", a.workerID, msg.JobID)

        // Process job
        result, err := a.processJob(msg)
        if err != nil {
            ctx.Logger().Errorf("Job %s failed: %v", msg.JobID, err)
            // Publish to failed jobs topic
            a.publishJobResult(ctx, "jobs.failed", msg.JobID, err)
        } else {
            ctx.Logger().Infof("Job %s completed", msg.JobID)
            // Publish to completed jobs topic
            a.publishJobResult(ctx, "jobs.completed", msg.JobID, nil)
        }
    }
}

func (a *WorkerActor) PostStop(ctx *actor.Context) error {
    return nil
}
```

### Event Aggregation

Collect and aggregate events from multiple sources:

```go
type EventAggregatorActor struct {
    events []Event
    buffer int
}

func (a *EventAggregatorActor) PreStart(ctx *actor.Context) error {
    a.events = make([]Event, 0)
    a.buffer = 100

    topicActor := ctx.ActorSystem().TopicActor()

    // Subscribe to multiple event topics
    topics := []string{
        "user.events",
        "order.events",
        "payment.events",
    }

    for _, topic := range topics {
        ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Subscribe{
            Topic: topic,
        })
    }

    return nil
}

func (a *EventAggregatorActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.SubscribeAck:
        ctx.Logger().Infof("Subscribed to: %s", msg.Topic)

    case *UserEvent:
        a.events = append(a.events, Event{
            Type:      "user",
            Timestamp: time.Now(),
            Data:      msg,
        })
        a.checkFlush(ctx)

    case *OrderEvent:
        a.events = append(a.events, Event{
            Type:      "order",
            Timestamp: time.Now(),
            Data:      msg,
        })
        a.checkFlush(ctx)

    case *PaymentEvent:
        a.events = append(a.events, Event{
            Type:      "payment",
            Timestamp: time.Now(),
            Data:      msg,
        })
        a.checkFlush(ctx)

    case *FlushEvents:
        a.flush(ctx)

    default:
        ctx.Unhandled()
    }
}

func (a *EventAggregatorActor) checkFlush(ctx *actor.ReceiveContext) {
    if len(a.events) >= a.buffer {
        a.flush(ctx)
    }
}

func (a *EventAggregatorActor) flush(ctx *actor.ReceiveContext) {
    if len(a.events) == 0 {
        return
    }

    // Aggregate and write to analytics database
    ctx.Logger().Infof("Flushing %d events to analytics", len(a.events))

    // ... write to database

    a.events = a.events[:0]
}

func (a *EventAggregatorActor) PostStop(ctx *actor.Context) error {
    return nil
}
```

### State Replication

Use pub/sub for eventually consistent state replication:

```go
type CacheReplicator struct {
    localCache Cache
}

func (a *CacheReplicator) PreStart(ctx *actor.Context) error {
    topicActor := ctx.ActorSystem().TopicActor()
    return ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Subscribe{
        Topic: "cache.updates",
    })
}

func (a *CacheReplicator) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.SubscribeAck:
        ctx.Logger().Info("Cache replicator subscribed")

    case *CacheUpdateEvent:
        // Apply update to local cache
        switch msg.Operation {
        case "set":
            a.localCache.Set(msg.Key, msg.Value, msg.TTL)
            ctx.Logger().Debugf("Cache updated: %s", msg.Key)

        case "delete":
            a.localCache.Delete(msg.Key)
            ctx.Logger().Debugf("Cache key deleted: %s", msg.Key)

        case "clear":
            a.localCache.Clear()
            ctx.Logger().Debug("Cache cleared")
        }

    case *LocalCacheUpdate:
        // Local update - broadcast to other nodes
        topicActor := ctx.ActorSystem().TopicActor()

        event := &CacheUpdateEvent{
            Key:       msg.Key,
            Value:     msg.Value,
            Operation: msg.Operation,
            TTL:       msg.TTL,
            NodeID:    ctx.ActorSystem().Name(),
        }

        payload, _ := anypb.New(event)

        ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Publish{
            Id:      uuid.New().String(),
            Topic:   "cache.updates",
            Message: payload,
        })

    default:
        ctx.Unhandled()
    }
}

func (a *CacheReplicator) PostStop(ctx *actor.Context) error {
    return nil
}
```

## Integration Patterns

### Webhook Dispatcher

Publish events that trigger webhooks:

```go
type WebhookDispatcher struct {
    httpClient *http.Client
    webhooks   map[string][]WebhookConfig
}

func (a *WebhookDispatcher) PreStart(ctx *actor.Context) error {
    a.httpClient = &http.Client{Timeout: 10 * time.Second}
    a.webhooks = loadWebhookConfig()

    topicActor := ctx.ActorSystem().TopicActor()

    // Subscribe to all event topics
    for topic := range a.webhooks {
        ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Subscribe{
            Topic: topic,
        })
    }

    return nil
}

func (a *WebhookDispatcher) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.SubscribeAck:
        ctx.Logger().Infof("Subscribed to topic: %s", msg.Topic)

    case *OrderCreatedEvent:
        a.dispatchWebhooks(ctx, "order.created", msg)

    case *PaymentProcessedEvent:
        a.dispatchWebhooks(ctx, "payment.processed", msg)

    default:
        ctx.Unhandled()
    }
}

func (a *WebhookDispatcher) dispatchWebhooks(ctx *actor.ReceiveContext, eventType string, event proto.Message) {
    webhooks, ok := a.webhooks[eventType]
    if !ok {
        return
    }

    for _, webhook := range webhooks {
        go func(wh WebhookConfig) {
            if err := a.sendWebhook(ctx.Context(), wh, event); err != nil {
                ctx.Logger().Errorf("Webhook failed for %s: %v", wh.URL, err)
            }
        }(webhook)
    }
}

func (a *WebhookDispatcher) sendWebhook(ctx context.Context, webhook WebhookConfig, event proto.Message) error {
    // Serialize event
    data, err := json.Marshal(event)
    if err != nil {
        return err
    }

    // Send HTTP POST
    req, _ := http.NewRequestWithContext(ctx, "POST", webhook.URL, bytes.NewReader(data))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-Webhook-Secret", webhook.Secret)

    resp, err := a.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode >= 400 {
        return fmt.Errorf("webhook returned %d", resp.StatusCode)
    }

    return nil
}

func (a *WebhookDispatcher) PostStop(ctx *actor.Context) error {
    return nil
}
```

### Audit Logger

Log all system events for compliance:

```go
type AuditLoggerActor struct {
    auditLog AuditLog
    buffer   []AuditEntry
}

func (a *AuditLoggerActor) PreStart(ctx *actor.Context) error {
    a.buffer = make([]AuditEntry, 0, 1000)

    topicActor := ctx.ActorSystem().TopicActor()

    // Subscribe to all audit-relevant topics
    auditTopics := []string{
        "user.events",
        "order.events",
        "payment.events",
        "auth.events",
    }

    for _, topic := range auditTopics {
        ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Subscribe{
            Topic: topic,
        })
    }

    // Schedule periodic flush
    ctx.ScheduleOnce(ctx.Context(), 30*time.Second, ctx.Self(), &FlushAuditLog{})

    return nil
}

func (a *AuditLoggerActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.SubscribeAck:
        ctx.Logger().Infof("Audit logger subscribed to: %s", msg.Topic)

    case *UserEvent:
        a.buffer = append(a.buffer, AuditEntry{
            Timestamp: time.Now(),
            Category:  "user",
            Action:    msg.Action,
            ActorID:   msg.UserID,
            Details:   msg,
        })

    case *OrderEvent:
        a.buffer = append(a.buffer, AuditEntry{
            Timestamp: time.Now(),
            Category:  "order",
            Action:    msg.Action,
            ActorID:   msg.OrderID,
            Details:   msg,
        })

    case *FlushAuditLog:
        a.flushToStorage(ctx)

        // Schedule next flush
        ctx.ScheduleOnce(ctx.Context(), 30*time.Second, ctx.Self(), &FlushAuditLog{})

    default:
        ctx.Unhandled()
    }
}

func (a *AuditLoggerActor) flushToStorage(ctx *actor.ReceiveContext) {
    if len(a.buffer) == 0 {
        return
    }

    ctx.Logger().Infof("Flushing %d audit entries", len(a.buffer))

    if err := a.auditLog.WriteBatch(ctx.Context(), a.buffer); err != nil {
        ctx.Logger().Errorf("Failed to write audit log: %v", err)
        return
    }

    a.buffer = a.buffer[:0]
}

func (a *AuditLoggerActor) PostStop(ctx *actor.Context) error {
    // Flush any remaining entries
    if len(a.buffer) > 0 {
        a.auditLog.WriteBatch(ctx.Context(), a.buffer)
    }
    return nil
}
```

### Dynamic Subscription

Subscribe/unsubscribe based on runtime conditions:

```go
type DynamicSubscriberActor struct {
    activeTopics map[string]bool
    interests    []string
}

func (a *DynamicSubscriberActor) PreStart(ctx *actor.Context) error {
    a.activeTopics = make(map[string]bool)
    return nil
}

func (a *DynamicSubscriberActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *UpdateInterests:
        // Determine which topics to add/remove
        toSubscribe := difference(msg.Topics, a.interests)
        toUnsubscribe := difference(a.interests, msg.Topics)

        topicActor := ctx.ActorSystem().TopicActor()

        // Subscribe to new topics
        for _, topic := range toSubscribe {
            ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Subscribe{
                Topic: topic,
            })
        }

        // Unsubscribe from old topics
        for _, topic := range toUnsubscribe {
            ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Unsubscribe{
                Topic: topic,
            })
        }

        a.interests = msg.Topics
        ctx.Response(&InterestsUpdated{})

    case *goaktpb.SubscribeAck:
        a.activeTopics[msg.Topic] = true
        ctx.Logger().Infof("Subscribed to: %s", msg.Topic)

    case *goaktpb.UnsubscribeAck:
        delete(a.activeTopics, msg.Topic)
        ctx.Logger().Infof("Unsubscribed from: %s", msg.Topic)

    default:
        // Handle messages from active topics
        ctx.Logger().Infof("Received message: %T", ctx.Message())
    }
}

func (a *DynamicSubscriberActor) PostStop(ctx *actor.Context) error {
    return nil
}

func difference(a, b []string) []string {
    mb := make(map[string]bool, len(b))
    for _, x := range b {
        mb[x] = true
    }

    diff := make([]string, 0)
    for _, x := range a {
        if !mb[x] {
            diff = append(diff, x)
        }
    }
    return diff
}
```

## Message Patterns

### Command-Event Pattern

Separate commands from events:

```go
// Command handler publishes events
type OrderCommandHandler struct{}

func (a *OrderCommandHandler) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *CreateOrderCommand:
        // Validate and process command
        if err := a.validateOrder(msg); err != nil {
            ctx.Err(err)
            return
        }

        // Create order
        order := a.createOrder(msg)

        // Publish event
        event := &OrderCreatedEvent{
            OrderID:   order.ID,
            Items:     order.Items,
            Total:     order.Total,
            Timestamp: time.Now(),
        }

        a.publishEvent(ctx, "order.events", event)

        ctx.Response(&OrderCreated{OrderID: order.ID})
    }
}

// Event listeners react to events
type EmailNotifier struct{}

func (a *EmailNotifier) PreStart(ctx *actor.Context) error {
    topicActor := ctx.ActorSystem().TopicActor()
    return ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Subscribe{
        Topic: "order.events",
    })
}

func (a *EmailNotifier) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *OrderCreatedEvent:
        // Send order confirmation email
        a.sendOrderConfirmation(msg)
    }
}
```

### Request-Event-Response Pattern

Publish events and aggregate responses:

```go
type QuoteAggregator struct {
    pendingQuotes map[string]*QuoteRequest
}

func (a *QuoteAggregator) PreStart(ctx *actor.Context) error {
    a.pendingQuotes = make(map[string]*QuoteRequest)

    topicActor := ctx.ActorSystem().TopicActor()
    return ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Subscribe{
        Topic: "quotes.responses",
    })
}

func (a *QuoteAggregator) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *RequestQuote:
        requestID := uuid.New().String()

        // Store request
        a.pendingQuotes[requestID] = &QuoteRequest{
            RequestID: requestID,
            Item:      msg.Item,
            Responses: make([]*QuoteResponse, 0),
        }

        // Publish quote request
        topicActor := ctx.ActorSystem().TopicActor()

        event := &QuoteRequestEvent{
            RequestID: requestID,
            Item:      msg.Item,
        }

        payload, _ := anypb.New(event)

        ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Publish{
            Id:      requestID,
            Topic:   "quotes.requests",
            Message: payload,
        })

        // Schedule timeout
        ctx.ScheduleOnce(ctx.Context(), 5*time.Second, ctx.Self(), &QuoteTimeout{
            RequestID: requestID,
        })

    case *QuoteResponseEvent:
        // Aggregate responses
        if req, ok := a.pendingQuotes[msg.RequestID]; ok {
            req.Responses = append(req.Responses, &QuoteResponse{
                VendorID: msg.VendorID,
                Price:    msg.Price,
            })
        }

    case *QuoteTimeout:
        // Return aggregated quotes
        if req, ok := a.pendingQuotes[msg.RequestID]; ok {
            ctx.Logger().Infof("Received %d quotes for request %s", len(req.Responses), msg.RequestID)
            delete(a.pendingQuotes, msg.RequestID)
        }

    default:
        ctx.Unhandled()
    }
}

func (a *QuoteAggregator) PostStop(ctx *actor.Context) error {
    return nil
}

// Vendor actors subscribe and respond
type VendorActor struct {
    vendorID string
}

func (a *VendorActor) PreStart(ctx *actor.Context) error {
    topicActor := ctx.ActorSystem().TopicActor()
    return ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Subscribe{
        Topic: "quotes.requests",
    })
}

func (a *VendorActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *QuoteRequestEvent:
        // Calculate quote
        price := a.calculatePrice(msg.Item)

        // Publish response
        topicActor := ctx.ActorSystem().TopicActor()

        response := &QuoteResponseEvent{
            RequestID: msg.RequestID,
            VendorID:  a.vendorID,
            Price:     price,
        }

        payload, _ := anypb.New(response)

        ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Publish{
            Id:      uuid.New().String(),
            Topic:   "quotes.responses",
            Message: payload,
        })
    }
}

func (a *VendorActor) PostStop(ctx *actor.Context) error {
    return nil
}
```

## Best Practices

### 1. Topic Organization

Use hierarchical topic names:

```go
// Good: Organized hierarchy
"user.created"
"user.updated"
"user.deleted"
"order.created"
"order.shipped"
"payment.processed"

// Bad: Flat structure
"user_event"
"order_event"
"payment"
```

### 2. Message Uniqueness

Always generate unique message IDs:

```go
import "github.com/google/uuid"

func publishEvent(ctx *actor.ReceiveContext, topic string, event proto.Message) {
    topicActor := ctx.ActorSystem().TopicActor()

    payload, _ := anypb.New(event)

    ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Publish{
        Id:      uuid.New().String(), // Unique ID prevents duplicates
        Topic:   topic,
        Message: payload,
    })
}
```

### 3. Handle Subscription Acks

Always handle subscription acknowledgments:

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.SubscribeAck:
        // Confirm subscription ready
        ctx.Logger().Infof("Ready for topic: %s", msg.Topic)

    case *goaktpb.UnsubscribeAck:
        // Confirm unsubscription
        ctx.Logger().Infof("Unsubscribed from: %s", msg.Topic)
    }
}
```

### 4. Error Resilience

Don't let subscriber errors affect publishers:

```go
func (a *ResilientSubscriber) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *EventMessage:
        // Process with error handling
        if err := a.processEvent(msg); err != nil {
            // Log and continue - don't propagate error
            ctx.Logger().Errorf("Failed to process event: %v", err)

            // Optionally retry or send to DLQ
            a.retryQueue.Add(msg)
        }
    }
}
```

### 5. Graceful Degradation

Handle missing topic actor:

```go
func publishSafely(ctx *actor.ReceiveContext, topic string, event proto.Message) error {
    topicActor := ctx.ActorSystem().TopicActor()
    if topicActor == nil {
        return errors.New("pub/sub not enabled")
    }

    payload, err := anypb.New(event)
    if err != nil {
        return err
    }

    return ctx.Self().Tell(ctx.Context(), topicActor, &goaktpb.Publish{
        Id:      uuid.New().String(),
        Topic:   topic,
        Message: payload,
    })
}
```

## Testing

### Unit Testing Publishers

```go
func TestPublisher(t *testing.T) {
    ctx := context.Background()

    // Create system with pub/sub
    system, err := actor.NewActorSystem(
        "test",
        actor.WithPubSub(),
    )
    require.NoError(t, err)

    err = system.Start(ctx)
    require.NoError(t, err)
    defer system.Stop(ctx)

    // Spawn publisher
    publisher, err := system.Spawn(ctx, "publisher", &MyPublisher{})
    require.NoError(t, err)

    // Trigger publish
    _, err = publisher.Ask(ctx, &PublishEvent{}, time.Second)
    require.NoError(t, err)
}
```

### Integration Testing Pub/Sub

```go
func TestPubSubIntegration(t *testing.T) {
    ctx := context.Background()

    system, _ := actor.NewActorSystem("test", actor.WithPubSub())
    system.Start(ctx)
    defer system.Stop(ctx)

    // Spawn subscriber with counter
    counter := &atomic.Int32{}
    subscriber, _ := system.Spawn(ctx, "subscriber", &CountingSubscriber{
        counter: counter,
    })

    // Wait for subscription
    time.Sleep(100 * time.Millisecond)

    // Spawn publisher
    publisher, _ := system.Spawn(ctx, "publisher", &MyPublisher{})

    // Publish message
    publisher.Tell(ctx, &PublishMessage{})

    // Wait for delivery
    time.Sleep(100 * time.Millisecond)

    // Verify message received
    assert.Equal(t, int32(1), counter.Load())
}
```

## Troubleshooting

### Messages Not Delivered

**Symptoms**: Subscribers don't receive published messages

**Solutions**:

- Verify pub/sub is enabled (`WithPubSub()`)
- Check topic names match exactly
- Ensure subscription completed (`SubscribeAck` received)
- Verify subscriber actor is running
- Check topic actor is present (`system.TopicActor() != nil`)

### Duplicate Messages

**Symptoms**: Subscribers receive same message multiple times

**Solutions**:

- Ensure message IDs are unique
- Implement idempotency in subscribers
- Check for multiple subscriptions to same topic

### High Latency

**Symptoms**: Slow message delivery to subscribers

**Solutions**:

- Reduce subscriber count per topic
- Optimize subscriber message handlers
- Consider topic partitioning
- Monitor topic actor mailbox size

### Memory Growth

**Symptoms**: Topic actor memory increases over time

**Solutions**:

- Topic actor tracks processed message IDs indefinitely
- Currently no automatic cleanup
- Restart actor system periodically if needed
- Consider contributing cleanup feature

## Limitations

1. **Fire-and-forget**: No delivery guarantees or acknowledgments
2. **No persistence**: Messages not stored
3. **No replay**: Can't retrieve historical messages
4. **At-most-once**: Messages may be lost on failures
5. **Memory-based**: Processed IDs kept in memory

## When to Use

**Use pub/sub when:**

- Decoupling producers from consumers
- Broadcasting events to multiple listeners
- Event notification patterns
- Fan-out processing
- Audit logging and monitoring

**Don't use pub/sub when:**

- Need guaranteed delivery (use message queues)
- Need message persistence (use event store)
- Need request-response (use Ask/Tell directly)
- Need message replay (use event sourcing)
- Need exactly-once semantics (use external messaging system)

## Next Steps

- [Usage Patterns](usage.md): More practical examples
- [Event Stream](../events_stream/overview.md): Alternative for local events
- [Actors Overview](../actors/overview.md): Actor fundamentals
- [Cluster Overview](../cluster/overview.md): Cluster pub/sub behavior
