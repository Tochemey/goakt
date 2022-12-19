package actors

import (
	"fmt"

	actorsv1 "github.com/tochemey/goakt/gen/actors/v1"
)

// receive handles every mail in the actor mailbox
func (p *pid) receive() {
	// run the processing loop
	for {
		select {
		case <-p.shutdownSignal:
			return
		case received := <-p.mailbox:
			switch received.Message().(type) {
			case *actorsv1.PoisonPill:
				if err := p.Shutdown(received.Context()); err != nil {
					// FIXME fix the panic
					panic(err)
				}
			default:
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
}
