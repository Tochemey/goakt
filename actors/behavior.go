package actors

import "github.com/tochemey/goakt/pkg/stack"

// Behavior defines an actor behavior
type Behavior func(ctx ReceiveContext)

// BehaviorStack defines a stack of Behavior
type BehaviorStack struct {
	*stack.Stack[Behavior]
}

// NewBehaviorStack creates an instance of BehaviorStack
func NewBehaviorStack() *BehaviorStack {
	return &BehaviorStack{
		stack.New[Behavior](),
	}
}
