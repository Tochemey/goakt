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
			msg := received.Payload()
			switch msg.(type) {
			case *actorsv1.PoisonPill:
				if err := p.Shutdown(received.Context()); err != nil {
					// FIXME fix the panic
					panic(err)
				}
			default:
				done := make(chan struct{})
				go func() {
					// close the msg error channel
					defer close(p.errChan)
					defer close(done)
					// recover from a panic attack
					defer func() {
						if r := recover(); r != nil {
							// construct the error to return
							err := fmt.Errorf("%s", r)
							// send the error to the channels
							p.errChan <- err
							// send the error to the watchers
							for item := range p.watchers.Iter() {
								item.Value.ErrChan <- err
							}
							// increase the panic counter
							p.panicCounter.Inc()
						}
					}()
					// send the message to actor to receive
					err := p.Receive(received)
					// set the error channels
					p.errChan <- err

					// only send error messages to the watcher
					if err != nil {
						// send the error to the watchers
						for item := range p.watchers.Iter() {
							item.Value.ErrChan <- err
						}
					}
				}()
			}
		}
	}
}
