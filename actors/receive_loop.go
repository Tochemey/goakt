package actors

import "fmt"

// receive handles every mail in the actor mailbox
func (p *pid) receive() {
	// run the processing loop
	for {
		select {
		case <-p.shutdownSignal:
			return
		case received := <-p.mailbox:
			func() {
				// recover from a panic attack
				defer func() {
					if r := recover(); r != nil {
						// construct the error to return
						err := fmt.Errorf("%s", r)
						// send the error to the watchers
						for item := range p.watchers.Iter() {
							item.Value.ErrChan <- err
						}
						// increase the panic counter
						p.panicCounter.Inc()
					}
				}()
				// send the message to the current actor behavior
				if behavior, ok := p.behaviorStack.Peek(); ok {
					behavior(received)
				}
			}()
		}
	}
}
