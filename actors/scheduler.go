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
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/reugn/go-quartz/quartz"
	qlogger "github.com/reugn/go-quartz/quartz/logger"
	"github.com/tochemey/goakt/log"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
)

// scheduler defines the Go-Akt scheduler.
// Its job is to help stack messages that will be delivered in the future to actors.
type scheduler struct {
	// helps lock concurrent access
	mu sync.Mutex
	// underlying scheduler
	scheduler quartz.Scheduler
	// states whether the scheduler has started or not
	started *atomic.Bool
	// define the logger
	logger log.Logger
	// define the shutdown timeout
	stopTimeout time.Duration
}

// newScheduler creates an instance of scheduler
func newScheduler(logger log.Logger, stopTimeout time.Duration) *scheduler {
	// create an instance of scheduler
	scheduler := &scheduler{
		mu:          sync.Mutex{},
		started:     atomic.NewBool(false),
		scheduler:   quartz.NewStdScheduler(),
		logger:      logger,
		stopTimeout: stopTimeout,
	}
	// disable the underlying scheduler logger
	qlogger.SetDefault(qlogger.NewSimpleLogger(nil, qlogger.LevelOff))
	// return the instance of the scheduler
	return scheduler
}

// Start starts the scheduler
func (x *scheduler) Start(ctx context.Context) {
	// acquire the lock
	x.mu.Lock()
	// release the lock once done
	defer x.mu.Unlock()
	// add logging information
	x.logger.Info("starting messages scheduler...")
	// start the scheduler
	x.scheduler.Start(ctx)
	// set the started
	x.started.Store(x.scheduler.IsStarted())
	// add logging information
	x.logger.Info("messages scheduler started.:)")
}

// Stop stops the scheduler
func (x *scheduler) Stop(ctx context.Context) {
	// add logging information
	x.logger.Info("stopping messages scheduler...")
	// acquire the lock
	x.mu.Lock()
	// release the lock once done
	defer x.mu.Unlock()
	// clear all scheduled jobs
	x.scheduler.Clear()
	// stop the scheduler
	x.scheduler.Stop()
	// set the started
	x.started.Store(x.scheduler.IsStarted())
	// create a cancellation context
	ctx, cancel := context.WithTimeout(ctx, x.stopTimeout)
	defer cancel()
	// wait for all workers to exit
	x.scheduler.Wait(ctx)
	// add logging information
	x.logger.Info("messages scheduler stopped...:)")
}

// ScheduleOnce schedules a message that will be delivered to the receiver actor
// This will send the given message to the actor after the given interval specified.
// The message will be sent once
func (x *scheduler) ScheduleOnce(ctx context.Context, message proto.Message, pid PID, interval time.Duration) error {
	// acquire the lock
	x.mu.Lock()
	// release the lock once done
	defer x.mu.Unlock()
	// check whether the scheduler has started or not
	if !x.started.Load() {
		// TODO: add a custom error
		return errors.New("messages scheduler is not started")
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
	return x.scheduler.ScheduleJob(ctx, job, quartz.NewRunOnceTrigger(interval))
}

// RemoteScheduleOnce schedules a message to be sent to a remote actor in the future.
// This requires remoting to be enabled on the actor system.
// This will send the given message to the actor after the given interval specified
// The message will be sent once
func (x *scheduler) RemoteScheduleOnce(ctx context.Context, message proto.Message, address *addresspb.Address, interval time.Duration) error {
	// acquire the lock
	x.mu.Lock()
	// release the lock once done
	defer x.mu.Unlock()

	// check whether the scheduler has started or not
	if !x.started.Load() {
		// TODO: add a custom error
		return errors.New("messages scheduler is not started")
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
	return x.scheduler.ScheduleJob(ctx, job, quartz.NewRunOnceTrigger(interval))
}

// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (x *scheduler) ScheduleWithCron(ctx context.Context, message proto.Message, pid PID, cronExpression string) error {
	// acquire the lock
	x.mu.Lock()
	// release the lock once done
	defer x.mu.Unlock()
	// check whether the scheduler has started or not
	if !x.started.Load() {
		// TODO: add a custom error
		return errors.New("messages scheduler is not started")
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
	// get the system time location
	location := time.Now().Location()
	// create the cron trigger
	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, location)
	// handle the error
	if err != nil {
		x.logger.Error(errors.Wrap(err, "failed to schedule message"))
		return err
	}
	// schedule the job
	return x.scheduler.ScheduleJob(ctx, job, trigger)
}

// RemoteScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (x *scheduler) RemoteScheduleWithCron(ctx context.Context, message proto.Message, address *addresspb.Address, cronExpression string) error {
	// acquire the lock
	x.mu.Lock()
	// release the lock once done
	defer x.mu.Unlock()
	// check whether the scheduler has started or not
	if !x.started.Load() {
		// TODO: add a custom error
		return errors.New("messages scheduler is not started")
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
	// get the system time location
	location := time.Now().Location()
	// create the cron trigger
	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, location)
	// handle the error
	if err != nil {
		x.logger.Error(errors.Wrap(err, "failed to schedule message"))
		return err
	}
	// schedule the job
	return x.scheduler.ScheduleJob(ctx, job, trigger)
}
