package actors

import (
	"context"
	"time"
)

// passivationListener checks whether the actor is processing messages or not.
// when the actor is idle, it automatically shuts down to free resources
func (p *pid) passivationListener() {
	p.logger.Info("start the passivation listener...")
	// create the ticker
	ticker := time.NewTicker(p.passivateAfter)
	// create the stop ticker signal
	tickerStopSig := make(chan Unit, 1)

	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				// check whether the actor is idle or not
				idleTime := time.Since(p.lastProcessingTime.Load())
				// check whether the actor is idle
				if idleTime >= p.passivateAfter {
					// set the done channel to stop the ticker
					tickerStopSig <- Unit{}
					return
				}
			case <-p.haltPassivationLnr:
				// set the done channel to stop the ticker
				tickerStopSig <- Unit{}
				return
			}
		}
	}()
	// wait for the stop signal to stop the ticker
	<-tickerStopSig
	// stop the ticker
	ticker.Stop()
	// only passivate when actor is alive
	if !p.IsOnline() {
		// add some logging info
		p.logger.Infof("Actor=%s is offline. No need to passivate", p.ActorPath().String())
		return
	}

	// add some logging info
	p.logger.Infof("Passivation mode has been triggered for actor=%s...", p.ActorPath().String())
	// passivate the actor
	p.passivate()
}

func (p *pid) passivate() {
	// create a context
	ctx := context.Background()
	// stop the actor PID
	p.stop(ctx)
	// perform some cleanup with the actor
	if err := p.Actor.PostStop(ctx); err != nil {
		panic(err)
	}
}
