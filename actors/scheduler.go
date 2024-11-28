/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/reugn/go-quartz/job"
	quartzlogger "github.com/reugn/go-quartz/logger"
	"github.com/reugn/go-quartz/quartz"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/internal/cluster"
	"github.com/tochemey/goakt/v2/log"
)

var errSkipJobScheduling = errors.New("skip job scheduling")

// schedulerOption represents the scheduler option
// to set custom settings
type schedulerOption func(scheduler *scheduler)

// withCluster set the scheduler cluster
func withSchedulerCluster(cluster cluster.Interface) schedulerOption {
	return func(scheduler *scheduler) {
		scheduler.cluster = cluster
	}
}

// withSchedulerTls sets the TLS setting
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
	cluster     cluster.Interface
	remoting    *Remoting
}

// newScheduler creates an instance of scheduler
func newScheduler(logger log.Logger, stopTimeout time.Duration, opts ...schedulerOption) *scheduler {
	// create an instance of scheduler
	scheduler := &scheduler{
		mu:              sync.Mutex{},
		started:         atomic.NewBool(false),
		quartzScheduler: quartz.NewStdScheduler(),
		logger:          logger,
		stopTimeout:     stopTimeout,
	}

	// set the custom options to override the default values
	for _, opt := range opts {
		opt(scheduler)
	}

	// disable the underlying scheduler logger
	quartzlogger.SetDefault(quartzlogger.NewSimpleLogger(nil, quartzlogger.LevelOff))
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
func (x *scheduler) ScheduleOnce(ctx context.Context, message proto.Message, pid *PID, interval time.Duration) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	job := job.NewFunctionJob[bool](
		func(ctx context.Context) (bool, error) {
			if err := Tell(ctx, pid, message); err != nil {
				return false, err
			}
			return true, nil
		},
	)

	jobDetails := quartz.NewJobDetail(job, quartz.NewJobKey(pid.Address().String()))
	if err := x.distributeJobKeyOrNot(ctx, jobDetails); err != nil {
		if errors.Is(err, errSkipJobScheduling) {
			return nil
		}
		return err
	}

	return x.quartzScheduler.ScheduleJob(jobDetails, quartz.NewRunOnceTrigger(interval))
}

// Schedule schedules a message that will be delivered to the receiving actor using the given interval specified
func (x *scheduler) Schedule(ctx context.Context, message proto.Message, pid *PID, interval time.Duration) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	job := job.NewFunctionJob[bool](
		func(ctx context.Context) (bool, error) {
			if err := Tell(ctx, pid, message); err != nil {
				return false, err
			}
			return true, nil
		},
	)

	jobDetails := quartz.NewJobDetail(job, quartz.NewJobKey(pid.Address().String()))
	if err := x.distributeJobKeyOrNot(ctx, jobDetails); err != nil {
		if errors.Is(err, errSkipJobScheduling) {
			return nil
		}
		return err
	}

	return x.quartzScheduler.ScheduleJob(jobDetails, quartz.NewSimpleTrigger(interval))
}

// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (x *scheduler) ScheduleWithCron(ctx context.Context, message proto.Message, pid *PID, cronExpression string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	job := job.NewFunctionJob[bool](
		func(ctx context.Context) (bool, error) {
			if err := Tell(ctx, pid, message); err != nil {
				return false, err
			}
			return true, nil
		},
	)

	jobDetails := quartz.NewJobDetail(job, quartz.NewJobKey(pid.Address().String()))
	if err := x.distributeJobKeyOrNot(ctx, jobDetails); err != nil {
		if errors.Is(err, errSkipJobScheduling) {
			return nil
		}
		return err
	}

	location := time.Now().Location()
	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, location)
	if err != nil {
		x.logger.Error(fmt.Errorf("failed to schedule message: %w", err))
		return err
	}

	return x.quartzScheduler.ScheduleJob(jobDetails, trigger)
}

// RemoteScheduleOnce schedules a message to be sent to a remote actor in the future.
// This requires remoting to be enabled on the actor system.
// This will send the given message to the actor after the given interval specified
// The message will be sent once
func (x *scheduler) RemoteScheduleOnce(ctx context.Context, message proto.Message, to *address.Address, interval time.Duration) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	if x.remoting == nil {
		return ErrRemotingDisabled
	}

	job := job.NewFunctionJob[bool](
		func(ctx context.Context) (bool, error) {
			from := address.NoSender()
			if err := x.remoting.RemoteTell(ctx, from, to, message); err != nil {
				return false, err
			}
			return true, nil
		},
	)

	key := fmt.Sprintf("%s@%s", to.GetName(), net.JoinHostPort(to.GetHost(), strconv.Itoa(int(to.GetPort()))))
	jobDetails := quartz.NewJobDetail(job, quartz.NewJobKey(key))
	if err := x.distributeJobKeyOrNot(ctx, jobDetails); err != nil {
		if errors.Is(err, errSkipJobScheduling) {
			return nil
		}
		return err
	}
	// schedule the job
	return x.quartzScheduler.ScheduleJob(jobDetails, quartz.NewRunOnceTrigger(interval))
}

// RemoteSchedule schedules a message to be sent to a remote actor in the future.
// This requires remoting to be enabled on the actor system.
// This will send the given message to the actor at the given interval specified
func (x *scheduler) RemoteSchedule(ctx context.Context, message proto.Message, to *address.Address, interval time.Duration) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	if x.remoting == nil {
		return ErrRemotingDisabled
	}

	job := job.NewFunctionJob[bool](
		func(ctx context.Context) (bool, error) {
			from := address.NoSender()
			if err := x.remoting.RemoteTell(ctx, from, to, message); err != nil {
				return false, err
			}
			return true, nil
		},
	)

	key := fmt.Sprintf("%s@%s", to.GetName(), net.JoinHostPort(to.GetHost(), strconv.Itoa(int(to.GetPort()))))
	jobDetails := quartz.NewJobDetail(job, quartz.NewJobKey(key))
	if err := x.distributeJobKeyOrNot(ctx, jobDetails); err != nil {
		if errors.Is(err, errSkipJobScheduling) {
			return nil
		}
		return err
	}
	// schedule the job
	return x.quartzScheduler.ScheduleJob(jobDetails, quartz.NewSimpleTrigger(interval))
}

// RemoteScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (x *scheduler) RemoteScheduleWithCron(ctx context.Context, message proto.Message, to *address.Address, cronExpression string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	if x.remoting == nil {
		return ErrRemotingDisabled
	}

	job := job.NewFunctionJob[bool](
		func(ctx context.Context) (bool, error) {
			from := address.NoSender()
			if err := x.remoting.RemoteTell(ctx, from, to, message); err != nil {
				return false, err
			}
			return true, nil
		},
	)

	key := fmt.Sprintf("%s@%s", to.GetName(), net.JoinHostPort(to.GetHost(), strconv.Itoa(int(to.GetPort()))))
	jobDetails := quartz.NewJobDetail(job, quartz.NewJobKey(key))

	if err := x.distributeJobKeyOrNot(ctx, jobDetails); err != nil {
		if errors.Is(err, errSkipJobScheduling) {
			return nil
		}
		return err
	}

	location := time.Now().Location()
	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, location)
	if err != nil {
		x.logger.Error(fmt.Errorf("failed to schedule message: %w", err))
		return err
	}

	return x.quartzScheduler.ScheduleJob(jobDetails, trigger)
}

// distributeJobKeyOrNot distribute the job key when it does not exist or skip it
func (x *scheduler) distributeJobKeyOrNot(ctx context.Context, job *quartz.JobDetail) error {
	jobKey := job.JobKey().String()
	if x.cluster != nil {
		ok, err := x.cluster.SchedulerJobKeyExists(ctx, jobKey)
		if err != nil {
			return err
		}
		if ok {
			return errSkipJobScheduling
		}
		if err := x.cluster.SetSchedulerJobKey(ctx, jobKey); err != nil {
			return err
		}
	}
	return nil
}
