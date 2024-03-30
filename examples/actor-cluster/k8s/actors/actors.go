/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actors

import (
	"context"
	"sync"

	"go.uber.org/atomic"

	goakt "github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/samplepb"
	"github.com/tochemey/goakt/goaktpb"
	"github.com/tochemey/goakt/log"
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
	case *goaktpb.PostStart:
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
		ctx.Unhandled()
	}
}

func (p *AccountEntity) PostStop(ctx context.Context) error {
	return nil
}
