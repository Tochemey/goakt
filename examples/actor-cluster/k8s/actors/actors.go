package actors

import (
	"context"
	"sync"

	goakt "github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/pb/v1"
	"github.com/tochemey/goakt/log"
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
			// here we are handling just an ask
			ctx.Response(&samplepb.Account{
				AccountId:      accountID,
				AccountBalance: p.balance.Load(),
			})
		}
	case *samplepb.CreditAccount:
		p.logger.Info("crediting balance...")
		// get the data
		accountID := msg.GetAccountId()
		balance := msg.GetBalance()
		// first check whether the accountID is mine
		if p.accountID == accountID {
			p.balance.Add(balance)
			ctx.Response(&samplepb.Account{
				AccountId:      accountID,
				AccountBalance: p.balance.Load(),
			})
		}
	case *samplepb.GetAccount:
		p.logger.Info("get account...")
		// get the data
		accountID := msg.GetAccountId()
		ctx.Response(&samplepb.Account{
			AccountId:      accountID,
			AccountBalance: p.balance.Load(),
		})

	default:
		p.logger.Panic(goakt.ErrUnhandled)
	}
}

func (p *AccountEntity) PostStop(ctx context.Context) error {
	return nil
}
