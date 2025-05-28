/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/reugn/go-quartz/job"
	quartzlogger "github.com/reugn/go-quartz/logger"
	"github.com/reugn/go-quartz/quartz"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/log"
)

// schedulerOption represents the scheduler option
// to set custom settings
type schedulerOption func(scheduler *scheduler)

// withSchedulerRemoting sets the scheduler remoting
func withSchedulerRemoting(remoting *Remoting) schedulerOption {
	return func(scheduler *scheduler) {
		scheduler.remoting = remoting
	}
}

// scheduler defines the Go-Akt scheduler.
// Its job is to help stack messages that will be delivered in the future to actors.
type scheduler struct {
	// helps lock concurrent access
	mu sync.Mutex
	// underlying Scheduler
	quartzScheduler quartz.Scheduler
	// states whether the quartzScheduler has started or not
	started *atomic.Bool
	// define the logger
	logger log.Logger
	// define the shutdown timeout
	stopTimeout time.Duration
	remoting    *Remoting
}

// newScheduler creates an instance of scheduler
func newScheduler(logger log.Logger, stopTimeout time.Duration, opts ...schedulerOption) *scheduler {
	// create an instance of quartz scheduler with logger off
	quartzScheduler, _ := quartz.NewStdScheduler(quartz.WithLogger(quartzlogger.NewSimpleLogger(nil, quartzlogger.LevelOff)))

	// create an instance of scheduler
	scheduler := &scheduler{
		mu:              sync.Mutex{},
		started:         atomic.NewBool(false),
		quartzScheduler: quartzScheduler,
		logger:          logger,
		stopTimeout:     stopTimeout,
	}

	// set the custom options to override the default values
	for _, opt := range opts {
		opt(scheduler)
	}

	// return the instance of the scheduler
	return scheduler
}

// Start starts the scheduler
func (x *scheduler) Start(ctx context.Context) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.logger.Info("starting messages scheduler...")
	x.quartzScheduler.Start(ctx)
	x.started.Store(x.quartzScheduler.IsStarted())
	x.logger.Info("messages scheduler started.:)")
}

// Stop stops the scheduler
func (x *scheduler) Stop(ctx context.Context) {
	if !x.started.Load() {
		return
	}

	x.logger.Info("stopping messages scheduler...")
	x.mu.Lock()
	defer x.mu.Unlock()
	_ = x.quartzScheduler.Clear()
	x.quartzScheduler.Stop()
	x.started.Store(x.quartzScheduler.IsStarted())

	ctx, cancel := context.WithTimeout(ctx, x.stopTimeout)
	defer cancel()
	x.quartzScheduler.Wait(ctx)

	x.logger.Info("messages scheduler stopped...:)")
}

// ScheduleOnce schedules a message that will be delivered to the receiver actor
// This will send the given message to the actor after the given interval specified.
// The message will be sent once
func (x *scheduler) ScheduleOnce(message proto.Message, pid *PID, interval time.Duration, opts ...SenderOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	senderConfig := newSenderConfig(opts...)
	sender := senderConfig.Sender()

	job := job.NewFunctionJob[bool](
		func(ctx context.Context) (bool, error) {
			var err error
			if !sender.Equals(NoSender) {
				err = sender.Tell(ctx, pid, message)
			} else {
				err = Tell(ctx, pid, message)
			}
			return err == nil, err
		},
	)

	key := newJobKey()
	detail := quartz.NewJobDetail(job, quartz.NewJobKey(key))
	return x.quartzScheduler.ScheduleJob(detail, quartz.NewRunOnceTrigger(interval))
}

// Schedule schedules a message that will be delivered to the receiving actor using the given interval specified
func (x *scheduler) Schedule(message proto.Message, pid *PID, interval time.Duration, opts ...SenderOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	senderConfig := newSenderConfig(opts...)
	sender := senderConfig.Sender()

	job := job.NewFunctionJob[bool](
		func(ctx context.Context) (bool, error) {
			var err error
			if !sender.Equals(NoSender) {
				err = sender.Tell(ctx, pid, message)
			} else {
				err = Tell(ctx, pid, message)
			}
			return err == nil, err
		},
	)

	key := newJobKey()
	detail := quartz.NewJobDetail(job, quartz.NewJobKey(key))
	return x.quartzScheduler.ScheduleJob(detail, quartz.NewSimpleTrigger(interval))
}

// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (x *scheduler) ScheduleWithCron(message proto.Message, pid *PID, cronExpression string, opts ...SenderOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	senderConfig := newSenderConfig(opts...)
	sender := senderConfig.Sender()

	job := job.NewFunctionJob[bool](
		func(ctx context.Context) (bool, error) {
			var err error
			if !sender.Equals(NoSender) {
				err = sender.Tell(ctx, pid, message)
			} else {
				err = Tell(ctx, pid, message)
			}
			return err == nil, err
		},
	)

	key := newJobKey()
	detail := quartz.NewJobDetail(job, quartz.NewJobKey(key))
	location := time.Now().Location()
	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, location)
	if err != nil {
		x.logger.Error(fmt.Errorf("failed to schedule message: %w", err))
		return err
	}

	return x.quartzScheduler.ScheduleJob(detail, trigger)
}

// RemoteScheduleOnce schedules a message to be sent to a remote actor in the future.
// This requires remoting to be enabled on the actor system.
// This will send the given message to the actor after the given interval specified
// The message will be sent once
func (x *scheduler) RemoteScheduleOnce(message proto.Message, to *address.Address, interval time.Duration, opts ...SenderOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	if x.remoting == nil {
		return ErrRemotingDisabled
	}

	senderConfig := newSenderConfig(opts...)
	job := job.NewFunctionJob[bool](
		func(ctx context.Context) (bool, error) {
			from := senderConfig.SenderAddr()
			err := x.remoting.RemoteTell(ctx, from, to, message)
			return err == nil, err
		},
	)

	key := newJobKey()
	detail := quartz.NewJobDetail(job, quartz.NewJobKey(key))
	// schedule the job
	return x.quartzScheduler.ScheduleJob(detail, quartz.NewRunOnceTrigger(interval))
}

// RemoteSchedule schedules a message to be sent to a remote actor in the future.
// This requires remoting to be enabled on the actor system.
// This will send the given message to the actor at the given interval specified
func (x *scheduler) RemoteSchedule(message proto.Message, to *address.Address, interval time.Duration, opts ...SenderOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	if x.remoting == nil {
		return ErrRemotingDisabled
	}

	senderConfig := newSenderConfig(opts...)
	job := job.NewFunctionJob[bool](
		func(ctx context.Context) (bool, error) {
			from := senderConfig.SenderAddr()
			err := x.remoting.RemoteTell(ctx, from, to, message)
			return err == nil, err
		},
	)

	key := newJobKey()
	detail := quartz.NewJobDetail(job, quartz.NewJobKey(key))
	// schedule the job
	return x.quartzScheduler.ScheduleJob(detail, quartz.NewSimpleTrigger(interval))
}

// RemoteScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (x *scheduler) RemoteScheduleWithCron(message proto.Message, to *address.Address, cronExpression string, opts ...SenderOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	if x.remoting == nil {
		return ErrRemotingDisabled
	}

	senderConfig := newSenderConfig(opts...)
	job := job.NewFunctionJob[bool](
		func(ctx context.Context) (bool, error) {
			from := senderConfig.SenderAddr()
			err := x.remoting.RemoteTell(ctx, from, to, message)
			return err == nil, err
		},
	)

	key := newJobKey()
	detail := quartz.NewJobDetail(job, quartz.NewJobKey(key))
	location := time.Now().Location()
	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, location)
	if err != nil {
		x.logger.Error(fmt.Errorf("failed to schedule message: %w", err))
		return err
	}

	return x.quartzScheduler.ScheduleJob(detail, trigger)
}

// newJobKey creates a new job key
func newJobKey() string {
	return uuid.NewString()
}
