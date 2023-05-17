package actors

import (
	"context"
	"sync"

	goakt "github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/pb/v1"
	"github.com/tochemey/goakt/log"
	messagespb "github.com/tochemey/goakt/messages/v1"
	"go.uber.org/atomic"
)

type AccountEntity struct {
	accountID string
	balance   *atomic.Float64
	logger    log.Logger
	mu        sync.Mutex
	created   *atomic.Bool
}

var _ goakt.Actor = (*AccountEntity)(nil)

// NewAccountEntity creates an instance of AccountEntity
func NewAccountEntity(id string) *AccountEntity {
	return &AccountEntity{
		accountID: id,
		balance:   atomic.NewFloat64(float64(0)),
		created:   atomic.NewBool(false),
	}
}

func (p *AccountEntity) PreStart(ctx context.Context) error {
	// set the log
	p.logger = log.DefaultLogger
	return nil
}

func (p *AccountEntity) Receive(ctx goakt.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *samplepb.CreateAccount:
		p.logger.Info("creating account by setting the balance...")
		// check whether the create operation has been done already
		if p.created.Load() {
			p.logger.Infof("account=%s has been created already", p.accountID)
			return
		}
		// get the data
		accountID := msg.GetAccountId()
		balance := msg.GetAccountBalance()
		// first check whether the accountID is mine
		if p.accountID == accountID {
			p.balance.Store(balance)
			p.created.Store(true)
			// send the reply to the sender in case there is one
			sender := ctx.Sender()
			// here we handle an actor Tell/Ask reply
			if sender != goakt.NoSender {
				_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), &samplepb.Account{
					AccountId:      accountID,
					AccountBalance: p.balance.Load(),
				})
			} else {
				// here we are handling just an ask
				ctx.Response(&samplepb.Account{
					AccountId:      accountID,
					AccountBalance: p.balance.Load(),
				})
			}
		}
	case *samplepb.CreditAccount:
		p.logger.Info("crediting balance...")
		// get the data
		accountID := msg.GetAccountId()
		balance := msg.GetBalance()
		// first check whether the accountID is mine
		if p.accountID == accountID {
			p.balance.Add(balance)
			// send the reply to the sender
			sender := ctx.Sender()
			// no sender
			if sender != goakt.NoSender {
				_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), &samplepb.Account{
					AccountId:      accountID,
					AccountBalance: p.balance.Load(),
				})
			} else {
				ctx.Response(&samplepb.Account{
					AccountId:      accountID,
					AccountBalance: p.balance.Load(),
				})
			}
		}

	case *messagespb.RemoteMessage:
		p.logger.Info("crediting balance...")
		message, _ := msg.GetMessage().UnmarshalNew()
		// pattern-match the message
		switch x := message.(type) {
		case *samplepb.CreateAccount:
			p.logger.Info("creating account by setting the balance...")
			// check whether the create operation has been done already
			if p.created.Load() {
				p.logger.Infof("account=%s has been created already", p.accountID)
				return
			}
			// get the data
			accountID := x.GetAccountId()
			balance := x.GetAccountBalance()
			// first check whether the accountID is mine
			if p.accountID == accountID {
				p.balance.Store(balance)
				p.created.Store(true)
				// send the response back to the caller
				_ = ctx.Self().RemoteTell(context.Background(), msg.GetSender(), &samplepb.Account{
					AccountId:      accountID,
					AccountBalance: p.balance.Load(),
				})
			}
		case *samplepb.CreditAccount:
			p.logger.Info("crediting balance...")
			// get the data
			accountID := x.GetAccountId()
			balance := x.GetBalance()
			// first check whether the accountID is mine
			if p.accountID == accountID {
				p.balance.Add(balance)
				// send the response back to the caller
				_ = ctx.Self().RemoteTell(context.Background(), msg.GetSender(), &samplepb.Account{
					AccountId:      accountID,
					AccountBalance: p.balance.Load(),
				})
			}
		}

	default:
		p.logger.Panic(goakt.ErrUnhandled)
	}
}

func (p *AccountEntity) PostStop(ctx context.Context) error {
	return nil
}
