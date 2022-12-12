package actors

import (
	"context"
	"fmt"
	"sync"

	samplev1 "github.com/tochemey/goakt/_examples/sample/pinger/v1"
	goakt "github.com/tochemey/goakt/actors"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

type Pinger struct {
	id     string
	mu     sync.Mutex
	count  *atomic.Int32
	logger log.Logger
}

var _ goakt.Actor = (*Pinger)(nil)

func NewPinger(id string) *Pinger {
	return &Pinger{
		id: id,
		mu: sync.Mutex{},
	}
}

func (p *Pinger) ID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.id
}

func (p *Pinger) PreStart(ctx context.Context) error {
	// set the logger
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logger = log.DefaultLogger
	p.count = atomic.NewInt32(0)
	p.logger.Info("About to Start")
	return nil
}

func (p *Pinger) Receive(message goakt.Message) error {
	switch received := message.Payload().(type) {
	case *samplev1.Ping:
		p.logger.Infof(fmt.Sprintf("received Ping from %s", received.GetId()))
		// reply the sender in case there is a sender
		if message.Sender() != nil && message.Sender() != goakt.NoSender {
			return message.Sender().Send(
				goakt.NewMessage(message.Context(),
					&samplev1.Pong{Reply: fmt.Sprintf("received Ping from %s", received.GetId())}),
			)
		}
		p.count.Add(1)
		return nil
	default:
		return goakt.ErrUnhandled
	}
}

func (p *Pinger) PostStop(ctx context.Context) error {
	p.logger.Info("About to stop")
	p.logger.Infof("Processed=%d messages", p.count.Load())
	return nil
}
