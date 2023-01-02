package actors

import (
	"context"

	pb "github.com/tochemey/goakt/pb/goakt/v1"
)

func (p *pid) supervise(cid PID, watcher *Watcher) {
	for {
		select {
		case <-watcher.Done:
			return
		case err := <-watcher.ErrChan:
			p.logger.Errorf("child actor=%s is panicking: Err=%v", cid.Address(), err)
			switch p.supervisorStrategy {
			case pb.StrategyDirective_STOP_DIRECTIVE:
				// shutdown the actor and panic in case of error
				if err := cid.Shutdown(context.Background()); err != nil {
					panic(err)
				}
				// unwatch the given actor
				p.UnWatch(cid)
				// remove the actor from the children map
				p.children.Delete(cid.Address())
			case pb.StrategyDirective_RESTART_DIRECTIVE:
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
				p.children.Delete(cid.Address())
			}
		}
	}
}
