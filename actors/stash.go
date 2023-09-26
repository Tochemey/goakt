package actors

// stash adds the current message to the stash buffer
func (p *pid) stash(ctx ReceiveContext) error {
	// return an error when the stash buffer is not set
	if p.stashBuffer == nil {
		return ErrStashBufferNotSet
	}

	p.stashSemaphore.Lock()
	defer p.stashSemaphore.Unlock()

	// add the message to stash buffer
	return p.stashBuffer.Push(ctx)
}

// unstash unstashes the oldest message in the stash and prepends to the mailbox
func (p *pid) unstash() error {
	// return an error when the stash buffer is not set
	if p.stashBuffer == nil {
		return ErrStashBufferNotSet
	}

	p.stashSemaphore.Lock()
	defer p.stashSemaphore.Unlock()

	if !p.stashBuffer.IsEmpty() {
		// grab the message from the stash buffer. Ignore the error when the mailbox is empty
		received, _ := p.stashBuffer.Pop()
		// send it to the mailbox processing
		p.doReceive(received)
	}
	return nil
}

// unstashAll unstashes all messages from the stash buffer and prepends in the mailbox
// (it keeps the messages in the same order as received, unstashing older messages before newer).
func (p *pid) unstashAll() error {
	// return an error when the stash buffer is not set
	if p.stashBuffer == nil {
		return ErrStashBufferNotSet
	}

	p.stashSemaphore.Lock()
	defer p.stashSemaphore.Unlock()

	// send all the messages in the stash buffer to the mailbox
	for !p.stashBuffer.IsEmpty() {
		// grab the message from the stash buffer. Ignore the error when the mailbox is empty
		received, _ := p.stashBuffer.Pop()
		// send it to the mailbox for processing
		p.doReceive(received)
	}

	return nil
}
