package actors

import (
	"context"

	actorsv1 "github.com/tochemey/goakt/gen/actors/v1"
)

func (p *PID) supervise(cid *PID, watcher *Watcher) {
	for {
		select {
		case <-watcher.Done:
			return
		case err := <-watcher.ErrChan:
			p.logger.Errorf("child actor=%s is panicing: Err=%v", cid.addr, err)
			switch p.supervisorStrategy {
			case actorsv1.Strategy_STOP:
				// shutdown the actor and panic in case of error
				if err := cid.Shutdown(context.Background()); err != nil {
					panic(err)
				}
				// unwatch the given actor
				p.UnWatch(cid)
				// remove the actor from the children map
				p.children.Delete(cid.addr)
			case actorsv1.Strategy_RESTART:
				// restart the actor
				if err := cid.Restart(context.Background()); err != nil {
					panic(err)
				}
			default:
				// shutdown the actor and panic in case of error
				if err := cid.Shutdown(context.Background()); err != nil {
					panic(err)
				}
				// unwatch the given actor
				p.UnWatch(cid)
				// remove the actor from the children map
				p.children.Delete(cid.addr)
			}
		}
	}
}
