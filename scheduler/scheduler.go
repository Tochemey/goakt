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
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/reugn/go-quartz/quartz"
	"github.com/tochemey/goakt/log"
	messagespb "github.com/tochemey/goakt/pb/messages/v1"
	"go.uber.org/atomic"
)

// MessagesScheduler defines the Go-Akt scheduler.
// Its job is to help stack messages that will be delivered in the future to actors.
// It does not work as a normal cron scheduler
type MessagesScheduler struct {
	// helps lock concurrent access
	mu sync.Mutex

	scheduler quartz.Scheduler
	// states whether the scheduler has started or not
	started *atomic.Bool
	// define the logger
	logger log.Logger
}

// NewMessagesScheduler creates an instance of MessagesScheduler
func NewMessagesScheduler() *MessagesScheduler {
	// create an instance of scheduler
	scheduler := &MessagesScheduler{
		mu:        sync.Mutex{},
		started:   atomic.NewBool(false),
		scheduler: quartz.NewStdScheduler(),
		logger:    log.DefaultLogger,
	}

	// return the instance of the scheduler
	return scheduler
}

// Start starts the scheduler
// nolint
func (x *MessagesScheduler) Start(ctx context.Context) {
	// acquire the lock
	x.mu.Lock()
	// release the lock once done
	defer x.mu.Unlock()
	// start the scheduler
	x.scheduler.Start(ctx)
	// set the started
	x.started.Store(x.scheduler.IsStarted())
}

// Stop stops the scheduler
func (x *MessagesScheduler) Stop(ctx context.Context) {
	// acquire the lock
	x.mu.Lock()
	// release the lock once done
	defer x.mu.Unlock()

	// stop the scheduler
	x.scheduler.Stop()
	// wait for all workers to exit
	x.scheduler.Wait(ctx)
}

// Schedule schedules a message to be sent to an actor in the future
// nolint
func (x *MessagesScheduler) Schedule(ctx context.Context, scheduled *messagespb.Scheduled) error {
	// acquire the lock
	x.mu.Lock()
	// release the lock once done
	defer x.mu.Unlock()

	// check whether the scheduler has started or not
	if !x.started.Load() {
		// TODO: add a custom error
		return errors.New("messages scheduler is not started")
	}

	// grab the frequency
	frequency := scheduled.GetFrequency()

	// let us construct the cron expression
	var cronExpression string
	var location *time.Location

	switch v := frequency.GetValue().(type) {
	case *messagespb.ScheduledFrequency_Once:
		// grab the time the message is supposed to be delivered
		timestamp := v.Once.GetWhen().AsTime()
		// grab the various date part
		second := timestamp.Second()
		minute := timestamp.Minute()
		hour := timestamp.Hour()
		day := timestamp.Day()
		month := timestamp.Month()
		year := timestamp.Year()
		location = timestamp.Location()
		// build the cron expression
		cronExpression = fmt.Sprintf("%s %s %s %s %s %s %s",
			strconv.Itoa(second),
			strconv.Itoa(minute),
			strconv.Itoa(hour),
			strconv.Itoa(day),
			strconv.Itoa(int(month)),
			"?",
			strconv.Itoa(year))
	case *messagespb.ScheduledFrequency_Repeatedly:
	}

	// create the cron trigger
	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, location)
	// handle the error
	if err != nil {
		x.logger.Error(errors.Wrap(err, "failed to schedule message"))
		return err
	}

	// create the job
	job := quartz.NewFunctionJob[bool](func(ctx context.Context) (bool, error) {
		// TODO implement me
		return true, nil
	})

	// schedule the job
	return x.scheduler.ScheduleJob(ctx, job, trigger)
}
