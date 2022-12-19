package actors

// Behavior defines an actor behavior
type Behavior func(ctx ReceiveContext)

// BehaviorStack defines a stack of Behavior
type BehaviorStack []Behavior

// NewBehaviorStack creates an instance of BehaviorStack
func NewBehaviorStack() BehaviorStack {
	return make(BehaviorStack, 0)
}

// Clear empty the stack
func (b *BehaviorStack) Clear() {
	if len(*b) == 0 {
		return
	}

	for i := range *b {
		(*b)[i] = nil
	}

	*b = (*b)[:0]
}

// Peek helps view the top item on the stack
func (b *BehaviorStack) Peek() (v Behavior, ok bool) {
	if length := b.Len(); length > 0 {
		ok = true
		v = (*b)[length-1]
	}

	return
}

// Push a new value onto the stack
func (b *BehaviorStack) Push(behavior Behavior) {
	*b = append(*b, behavior)
}

// Pop removes and return top element of stack. Return false if stack is empty.
func (b *BehaviorStack) Pop() (behavior Behavior, ok bool) {
	if length := b.Len(); length > 0 {
		length--

		ok = true
		behavior = (*b)[length]
		(*b)[length] = nil
		*b = (*b)[:length]
	}

	return
}

// Len returns the length of the stack.
func (b *BehaviorStack) Len() int {
	return len(*b)
}

// IsEmpty checks if stack is empty
func (b *BehaviorStack) IsEmpty() bool {
	return b.Len() == 0
}
