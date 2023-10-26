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

package actors

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/reugn/go-quartz/quartz"
	"github.com/tochemey/goakt/log"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	schedulerpb "github.com/tochemey/goakt/pb/scheduler/v1"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
)

type scheduled struct {
	frequency *schedulerpb.Frequency
	job       quartz.Job
}

// Scheduler defines the Go-Akt scheduler.
// Its job is to help stack messages that will be delivered in the future to actors.
type Scheduler struct {
	// helps lock concurrent access
	mu sync.Mutex
	// list of scheduled
	scheduledList []*scheduled
	// underlying scheduler
	scheduler quartz.Scheduler
	// states whether the scheduler has started or not
	started *atomic.Bool
	// define the logger
	logger log.Logger
}

// NewScheduler creates an instance of Scheduler
func NewScheduler() *Scheduler {
	// create an instance of scheduler
	scheduler := &Scheduler{
		mu:            sync.Mutex{},
		started:       atomic.NewBool(false),
		scheduler:     quartz.NewStdScheduler(),
		logger:        log.DefaultLogger,
		scheduledList: make([]*scheduled, 0),
	}

	// return the instance of the scheduler
	return scheduler
}

// Start starts the scheduler
func (x *Scheduler) Start(ctx context.Context) {
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
func (x *Scheduler) Stop(ctx context.Context) {
	// acquire the lock
	x.mu.Lock()
	// release the lock once done
	defer x.mu.Unlock()

	// stop the scheduler
	x.scheduler.Stop()
	// wait for all workers to exit
	x.scheduler.Wait(ctx)
}

// RemoteSchedule schedules a message to be sent to a remote actor in the future. This requires remoting to be enabled on the actor system
func (x *Scheduler) RemoteSchedule(ctx context.Context, message proto.Message, address *addresspb.Address, frequency *schedulerpb.Frequency) error {
	// acquire the lock
	x.mu.Lock()
	// release the lock once done
	defer x.mu.Unlock()

	// check whether the scheduler has started or not
	if !x.started.Load() {
		// TODO: add a custom error
		return errors.New("messages scheduler is not started")
	}

	// create the cron trigger
	trigger, err := x.createCronTrigger(frequency)
	// handle the error
	if err != nil {
		x.logger.Error(errors.Wrap(err, "failed to schedule message"))
		return err
	}

	// create the job
	job := quartz.NewFunctionJob[bool](func(ctx context.Context) (bool, error) {
		// when the job run send the message to actor
		if err := RemoteTell(ctx, address, message); err != nil {
			return false, err
		}
		// return true when successful
		return true, nil
	})

	// schedule the job
	return x.doScheduleJob(ctx, job, frequency, trigger)
}

// Schedule schedules a message to be sent to an actor in the future
func (x *Scheduler) Schedule(ctx context.Context, message proto.Message, pid PID, frequency *schedulerpb.Frequency) error {
	// acquire the lock
	x.mu.Lock()
	// release the lock once done
	defer x.mu.Unlock()

	// check whether the scheduler has started or not
	if !x.started.Load() {
		// TODO: add a custom error
		return errors.New("messages scheduler is not started")
	}

	// create the cron trigger
	trigger, err := x.createCronTrigger(frequency)
	// handle the error
	if err != nil {
		x.logger.Error(errors.Wrap(err, "failed to schedule message"))
		return err
	}

	// create the job
	job := quartz.NewFunctionJob[bool](func(ctx context.Context) (bool, error) {
		// when the job run send the message to actor
		if err := Tell(ctx, pid, message); err != nil {
			return false, err
		}
		// return true when successful
		return true, nil
	})

	// schedule the job
	return x.doScheduleJob(ctx, job, frequency, trigger)
}

// createCronTrigger creates a cron trigger
func (x *Scheduler) createCronTrigger(frequency *schedulerpb.Frequency) (quartz.Trigger, error) {
	// let us construct the cron expression
	var cronExpression string
	// get the system time location
	location := time.Now().Location()

	switch v := frequency.GetValue().(type) {
	case *schedulerpb.Frequency_Once:
		// grab the time the message is supposed to be delivered
		timestamp := v.Once.GetWhen().AsTime()
		// grab the location
		location = timestamp.Location()
		// build the cron expression
		cronExpression = fmt.Sprintf("%s %s %s %s %s %s %s",
			strconv.Itoa(timestamp.Second()),
			strconv.Itoa(timestamp.Minute()),
			strconv.Itoa(timestamp.Hour()),
			strconv.Itoa(timestamp.Day()),
			strconv.Itoa(int(timestamp.Month())),
			"?",
			strconv.Itoa(timestamp.Year()))
	case *schedulerpb.Frequency_Repeatedly:
		// grab the time the message is supposed to be delivered
		timestamp := v.Repeatedly.GetWhen()
		// build the cron expression
		cronExpression = fmt.Sprintf("%s %s %s %s %s %s",
			strconv.Itoa(int(timestamp.GetSeconds())),
			strconv.Itoa(int(timestamp.GetMinutes())),
			strconv.Itoa(int(timestamp.GetHours())),
			strconv.Itoa(int(timestamp.GetDay())),
			strconv.Itoa(int(timestamp.GetMonth())),
			"?")
	}

	// create the cron trigger
	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, location)
	// handle the error
	if err != nil {
		x.logger.Error(errors.Wrap(err, "failed to schedule message"))
		return nil, err
	}
	return trigger, nil
}

// doScheduleJob schedule the job to run
func (x *Scheduler) doScheduleJob(ctx context.Context, job quartz.Job, frequency *schedulerpb.Frequency, trigger quartz.Trigger) error {
	// wrap the job and add it the list
	x.scheduledList = append(x.scheduledList, &scheduled{
		frequency: frequency,
		job:       job,
	})
	// schedule the job
	return x.scheduler.ScheduleJob(ctx, job, trigger)
}

// scheduledWatcher is a loop that constantly run and check job status
func (x *Scheduler) scheduledWatcher() {

}
