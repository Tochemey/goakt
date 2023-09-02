package actors

import "github.com/tochemey/goakt/pkg/stack"

// Behavior defines an actor behavior
type Behavior func(ctx ReceiveContext)

// behaviorStack defines a stack of Behavior
type behaviorStack struct {
	*stack.Stack[Behavior]
}

// newBehaviorStack creates an instance of behaviorStack
func newBehaviorStack() *behaviorStack {
	return &behaviorStack{
		stack.New[Behavior](),
	}
}
