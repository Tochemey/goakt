/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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

package scheduler

import (
	"sync"
	"time"

	"github.com/tochemey/goakt/log"
	messagespb "github.com/tochemey/goakt/pb/messages/v1"
	"github.com/tochemey/goakt/pkg/types"
	"go.uber.org/atomic"
)

// Scheduler defines the Go-Akt scheduler.
// Its job is to help stack messages that will be delivered in the future to actors.
// It does not work as a normal cron scheduler
type Scheduler struct {
	// helps lock concurrent access
	mu sync.Mutex

	// list of scheduled messages
	messages map[string]*messagespb.Scheduled
	// states whether the scheduler has started or not
	started *atomic.Bool
	// signal stop
	stopSignal chan types.Unit
	// define the logger
	logger log.Logger
}

// NewScheduler creates an instance of Scheduler
func NewScheduler() *Scheduler {
	// create an instance of scheduler
	scheduler := &Scheduler{
		mu:         sync.Mutex{},
		messages:   make(map[string]*messagespb.Scheduled),
		started:    atomic.NewBool(false),
		stopSignal: make(chan types.Unit, 1),
		logger:     log.DefaultLogger,
	}

	// return the instance of the scheduler
	return scheduler
}

// Start starts the scheduler
func (x *Scheduler) Start() {

}

// Stop stops the scheduler
func (x *Scheduler) Stop() {

}

// timerLoop will be running until the scheduler is stopped.
func (x *Scheduler) timerLoop() {
	// create the ticker
	ticker := time.NewTicker(30 * time.Millisecond)
	// create the stop ticker signal
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				// iterate the list of scheduled messages and process them
			case <-x.stopSignal:
				// set the done channel to stop the ticker
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()
	// wait for the stop signal to stop the ticker
	<-tickerStopSig
	// stop the ticker
	ticker.Stop()
}
