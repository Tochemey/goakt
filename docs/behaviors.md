## Behaviors

Actors in Go-Akt have the power to switch their behaviors at any point in time. When you change the actor behavior, the new
behavior will take effect for all subsequent messages until the behavior is changed again. The current message will
continue processing with the existing behavior. You can use [Stashing](#stashing) to reprocess the current
message with the new behavior.

To change the behavior, call the following methods on the [ReceiveContext interface](./actors/context.go) when handling a message:

- `Become` - switches the current behavior of the actor to a new behavior.
- `UnBecome` - resets the actor behavior to the default one which is the Actor.Receive method.
- `BecomeStacked` - sets a new behavior to the actor to the top of the behavior stack, while maintaining the previous ones.
- `UnBecomeStacked()` - sets the actor behavior to the previous behavior before `BecomeStacked()` was called. This only works with `BecomeStacked()`.

### Define the Actor

```go
// UserActor is used to test the actor behavior
type UserActor struct{}

// enforce compilation error
var _ Actor = &UserActor{}

func (x *UserActor) PreStart(_ context.Context) error {
	return nil
}

func (x *UserActor) PostStop(_ context.Context) error {
	return nil
}

func (x *UserActor) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestLogin:
		ctx.Response(new(testspb.TestLoginSuccess))
		ctx.Become(x.Authenticated)
	case *testspb.CreateAccount:
		ctx.Response(new(testspb.AccountCreated))
		ctx.BecomeStacked(x.CreditAccount)
	}
}

// Authenticated behavior is executed when the actor receive the TestAuth message
func (x *UserActor) Authenticated(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestReadiness:
		ctx.Response(new(testspb.TestReady))
		ctx.UnBecome()
	}
}

// CreditAccount behavior
func (x *UserActor) CreditAccount(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.CreditAccount:
		ctx.Response(new(testspb.AccountCredited))
		ctx.BecomeStacked(x.DebitAccount)
	case *testspb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

// DebitAccount behavior
func (x *UserActor) DebitAccount(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.DebitAccount:
		ctx.Response(new(testspb.AccountDebited))
		ctx.UnBecomeStacked()
	}
}
```
