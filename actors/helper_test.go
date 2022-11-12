package actors

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/tochemey/goakt/log"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type BenchActor struct {
	Wg sync.WaitGroup
}

func (p *BenchActor) ID() string {
	return "benchActor"
}

func (p *BenchActor) Init(ctx context.Context) error {
	return nil
}

func (p *BenchActor) Receive(ctx context.Context, message proto.Message) error {
	p.Wg.Done()
	return nil
}

func (p *BenchActor) ReceiveReply(ctx context.Context, message proto.Message) (proto.Message, error) {
	p.Wg.Done()
	return nil, nil
}

func (p *BenchActor) Stop(ctx context.Context) {
}

type TestActor struct {
	id    string
	cache goset.Set[string]
}

var _ Actor = (*TestActor)(nil)

// NewTestActor creates a TestActor
func NewTestActor(id string) *TestActor {
	return &TestActor{
		id: id,
	}
}

func (p *TestActor) ID() string {
	return p.id
}

// Count utility function for test
func (p *TestActor) Count() int {
	return len(p.cache.ToSlice())
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing messages
func (p *TestActor) Init(ctx context.Context) error {
	p.cache = goset.NewSet[string]()
	log.Info("Test Actor init method called")
	return nil
}

// Stop gracefully shuts down the given actor
func (p *TestActor) Stop(ctx context.Context) {
	log.Info("Test Actor stopped...")
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *TestActor) Receive(ctx context.Context, message proto.Message) error {
	switch msg := message.(type) {
	case *wrapperspb.StringValue:
		log.Infof("Test Actor received msg=[%s]", msg.GetValue())
		if strings.Contains(msg.GetValue(), recvMessage) {
			p.cache.Add(msg.GetValue())
			return nil
		}
		if msg.GetValue() == panicAttackMessage {
			panic("Boom")
		}
	default:
		return ErrUnhandled
	}
	return nil
}

// ReceiveReply processes any message dropped into the actor mailbox with a reply
func (p *TestActor) ReceiveReply(ctx context.Context, message proto.Message) (proto.Message, error) {
	switch msg := message.(type) {
	case *wrapperspb.StringValue:
		log.Infof("Test Actor received msg=[%s]", msg.GetValue())
		// receive reply test case
		if msg.GetValue() == recvReplyMessage {
			// persist to the cache
			p.cache.Add(msg.GetValue())
			// prepare for the reply
			reply := &wrapperspb.StringValue{Value: fmt.Sprintf("received=%s", msg.GetValue())}
			// return the reply
			return reply, nil
		}

		// timeout test case
		if msg.GetValue() == timeoutMessage {
			// delay for a while before sending the reply
			wg := sync.WaitGroup{}
			wg.Add(1)
			go func() {
				time.Sleep(recvDelay)
				wg.Done()
			}()
			// block until timer is up
			wg.Wait()
			// prepare for the reply
			reply := msg
			// return the reply
			return reply, nil
		}

		if msg.GetValue() == panicAttackMessage {
			panic("Boom")
		}

		return nil, errors.New("error simulated")

	default:
		return nil, ErrUnhandled
	}
}
