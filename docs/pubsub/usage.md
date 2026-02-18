# PubSub Usage Patterns

Practical patterns for using pub/sub: event broadcasting, fan-out, integration, and message design. See [Overview](overview.md) for API and concepts.

## Table of Contents

- 📢 [Event Broadcasting](#event-broadcasting)
- 🔧 [Advanced & Integration Patterns](#advanced--integration-patterns)
- 📨 [Message Patterns](#message-patterns)
- ✅ [Best Practices](#best-practices)
- 🧪 [Testing & Troubleshooting](#testing--troubleshooting)
- ⚠️ [Limitations & When to Use](#limitations--when-to-use)

---

## Event Broadcasting

Subscribe in `Receive` on `*goaktpb.PostStart`; publish with `goaktpb.Publish` (payload via `anypb.New(proto.Message)`). Multiple subscribers to the same topic each receive the message.

```go
// Subscriber
func (a *OrderEventListener) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *goaktpb.PostStart:
        ctx.Tell(ctx.ActorSystem().TopicActor(), &goaktpb.Subscribe{Topic: "order.events"})
    case *goaktpb.SubscribeAck:
        // ready
    case *OrderCreatedEvent:
        // handle event (e.g. reserve inventory, update analytics)
    default:
        ctx.Unhandled()
    }
}

// Publisher: after processing, publish event
func (a *OrderService) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *CreateOrder:
        order := a.createOrder(msg)
        payload, _ := anypb.New(&OrderCreatedEvent{OrderID: order.ID, Total: order.Total})
        ctx.Tell(ctx.ActorSystem().TopicActor(), &goaktpb.Publish{
            Id: uuid.New().String(), Topic: "order.events", Message: payload,
        })
        ctx.Response(&OrderCreated{OrderID: order.ID})
    default:
        ctx.Unhandled()
    }
}
```

**Multi-topic subscriber:** In `PostStart` handler, send multiple `Subscribe{Topic: "t1"}`, `Subscribe{Topic: "t2"}`, etc.; track `SubscribeAck` per topic if you need to know when all are ready.

## Advanced & Integration Patterns

- **Fan-out:** Publish jobs to a topic; multiple worker actors subscribe and process (each message goes to all workers). Use a single consumer topic or partition by job type with separate topics.
- **Event aggregation:** One actor subscribes to several topics (e.g. `user.events`, `order.events`), appends to a buffer, and flushes to DB or analytics on size/time.
- **State replication:** Subscribe to a topic like `cache.updates`; on received events apply updates to local state; on local writes publish to the same topic so other nodes replicate.
- **Webhooks / audit:** Subscriber subscribes to business-event topics; on each event type call HTTP webhooks or append to an audit log (buffer and batch-write for throughput).

## Message Patterns

- **Command–event:** Command handler validates, performs the change, then publishes an event (e.g. `OrderCreatedEvent`) to a topic; subscribers react without the publisher knowing who they are.
- **Request–event–response:** Aggregator publishes a request event (e.g. `quotes.requests`), stores pending request by ID, and subscribes to response topic (`quotes.responses`). Vendors subscribe to requests and publish responses. Use a timeout (e.g. `ScheduleOnce`) to complete the aggregation and respond to the original caller.

## Best Practices

- **Topic naming:** Use hierarchical names (e.g. `order.created`, `payment.processed`).
- **Message IDs:** Use unique IDs (e.g. `uuid.New().String()`) on every `Publish` to help deduplication and idempotency.
- **Subscribe acks:** Handle `SubscribeAck` (and `UnsubscribeAck` if you unsubscribe) so you know when the subscription is active.
- **Errors:** Handle processing errors inside subscribers (log, retry, or DLQ); don’t let them affect the publisher—delivery is fire-and-forget.
- **Topic actor:** If not using cluster, ensure `WithPubSub()` is set; otherwise `TopicActor()` may be nil.

## Testing & Troubleshooting

- **Unit test publisher:** Create system with `WithPubSub()`, spawn publisher, trigger publish (e.g. via `Ask`), assert no error.
- **Integration test:** Spawn a subscriber that counts received messages (e.g. atomic counter), spawn publisher, publish, wait briefly, assert count.

**Not delivered:** Check `WithPubSub()`, topic names match, `SubscribeAck` received, subscriber running. **Duplicates:** Unique publish IDs; implement idempotency in subscribers. **Latency:** Fewer subscribers per topic; optimize handlers; consider splitting topics. **Memory:** Topic actor retains message IDs for deduplication; no built-in cleanup.

## Limitations & When to Use

- **Limitations:** Fire-and-forget, no persistence or replay, at-most-once delivery, processed IDs kept in memory.
- **Use pub/sub for:** Decoupling producers and consumers, broadcasting to many listeners, event notification, fan-out, audit/monitoring.
- **Use something else when:** You need guaranteed delivery, persistence, replay, or request–response (use Ask/Tell or a queue).

