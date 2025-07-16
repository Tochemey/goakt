/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

	"github.com/reugn/go-quartz/job"
	quartzlogger "github.com/reugn/go-quartz/logger"
	"github.com/reugn/go-quartz/quartz"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/address"
	"github.com/tochemey/goakt/v4/internal/collection"
	"github.com/tochemey/goakt/v4/log"
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
	shutdownTimeout time.Duration
	// remoting engine
	remoting *Remoting
	// specifies the job keys mapping
	scheduledKeys *collection.Map[string, *quartz.JobKey]
}

// newScheduler creates an instance of scheduler
func newScheduler(logger log.Logger, shutdownTimeout time.Duration, opts ...schedulerOption) *scheduler {
	// create an instance of quartz scheduler with logger off
	quartzScheduler, _ := quartz.NewStdScheduler(quartz.WithLogger(quartzlogger.NewSimpleLogger(nil, quartzlogger.LevelOff)))

	// create an instance of scheduler
	scheduler := &scheduler{
		mu:              sync.Mutex{},
		started:         atomic.NewBool(false),
		quartzScheduler: quartzScheduler,
		logger:          logger,
		shutdownTimeout: shutdownTimeout,
		scheduledKeys:   collection.NewMap[string, *quartz.JobKey](),
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

	ctx, cancel := context.WithTimeout(ctx, x.shutdownTimeout)
	defer cancel()
	x.quartzScheduler.Wait(ctx)

	x.scheduledKeys.Reset()
	x.logger.Info("messages scheduler stopped...:)")
}

// ScheduleOnce schedules a one-time delivery of a message to the specified actor (PID) after a given delay.
//
// The message will be sent exactly once to the target actor after the specified duration has elapsed.
// This is a fire-and-forget scheduling mechanism — once delivered, the message will not be retried or repeated.
//
// Parameters:
//   - message: The proto.Message to be sent.
//   - pid: The PID of the actor that will receive the message.
//   - delay: The duration to wait before delivering the message.
//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
//
// Returns:
//   - error: An error is returned if scheduling fails due to invalid input or internal errors.
//
// Note:
//   - It's strongly recommended to set a unique reference ID using WithReference if you intend to cancel, pause, or resume the message later.
//   - If no reference is set, an automatic one will be generated, which may not be easily retrievable.
func (x *scheduler) ScheduleOnce(message proto.Message, pid *PID, delay time.Duration, opts ...ScheduleOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	senderConfig := newScheduleConfig(opts...)
	sender := senderConfig.Sender()

	job := job.NewFunctionJob(
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

	reference := senderConfig.Reference()
	jobKey := quartz.NewJobKey(reference)
	x.scheduledKeys.Set(reference, jobKey)

	detail := quartz.NewJobDetail(job, jobKey)
	return x.quartzScheduler.ScheduleJob(detail, quartz.NewRunOnceTrigger(delay))
}

// Schedule schedules a recurring message to be delivered to the specified actor (PID) at a fixed interval.
//
// This function sets up a message to be sent repeatedly to the target actor, with each delivery occurring
// after the specified interval. The scheduling continues until explicitly canceled or if the actor is no longer available.
//
// Parameters:
//   - message: The proto.Message to be delivered at regular intervals.
//   - pid: The PID of the actor that will receive the message.
//   - interval: The time duration between each delivery of the message.
//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
//
// Returns:
//   - error: An error is returned if the message could not be scheduled due to invalid input or internal issues.
//
// Note:
//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the scheduled message.
//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable for later operations.
//   - This function does not provide built-in delivery guarantees such as at-least-once or exactly-once semantics; ensure idempotency where needed.
func (x *scheduler) Schedule(message proto.Message, pid *PID, interval time.Duration, opts ...ScheduleOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	senderConfig := newScheduleConfig(opts...)
	sender := senderConfig.Sender()

	job := job.NewFunctionJob(
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

	reference := senderConfig.Reference()
	jobKey := quartz.NewJobKey(reference)
	x.scheduledKeys.Set(reference, jobKey)

	detail := quartz.NewJobDetail(job, jobKey)
	return x.quartzScheduler.ScheduleJob(detail, quartz.NewSimpleTrigger(interval))
}

// ScheduleWithCron schedules a message to be delivered to the specified actor (PID) using a cron expression.
//
// This method enables flexible time-based scheduling using standard cron syntax, allowing you to specify complex recurring schedules.
// The message will be sent to the target actor according to the schedule defined by the cron expression.
//
// Parameters:
//   - message: The proto.Message to be delivered.
//   - pid: The PID of the actor that will receive the message.
//   - cronExpression: A standard cron-formatted string (e.g., "0 */5 * * * *") representing the schedule.
//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
//
// Returns:
//   - error: An error is returned if the cron expression is invalid or if scheduling fails due to internal errors.
//
// Note:
//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the scheduled message.
//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable for future operations.
//   - The cron expression must follow the format supported by the scheduler (typically 6 or 5 fields depending on implementation).
func (x *scheduler) ScheduleWithCron(message proto.Message, pid *PID, cronExpression string, opts ...ScheduleOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	senderConfig := newScheduleConfig(opts...)
	sender := senderConfig.Sender()

	job := job.NewFunctionJob(
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

	reference := senderConfig.Reference()
	jobKey := quartz.NewJobKey(reference)
	x.scheduledKeys.Set(reference, jobKey)

	detail := quartz.NewJobDetail(job, jobKey)
	location := time.Now().Location()
	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, location)
	if err != nil {
		x.logger.Error(fmt.Errorf("failed to schedule message: %w", err))
		return err
	}

	return x.quartzScheduler.ScheduleJob(detail, trigger)
}

// RemoteScheduleOnce schedules a one-time delivery of a message to a remote actor after a specified delay.
//
// This method schedules a message to be sent to an actor located at a remote address once the given interval has elapsed.
// It requires that remoting is enabled in the actor system configuration.
//
// Parameters:
//   - message: The proto.Message to be delivered.
//   - to: The address.Address of the remote actor that will receive the message.
//   - delay: The time duration to wait before delivering the message.
//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
//
// Returns:
//   - error: An error is returned if remoting is not enabled, the target address is invalid, or scheduling fails.
//
// Note:
//   - Remoting must be enabled in the actor system for this function to work.
//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the message later.
//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable.
func (x *scheduler) RemoteScheduleOnce(message proto.Message, to *address.Address, delay time.Duration, opts ...ScheduleOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	if x.remoting == nil {
		return ErrRemotingDisabled
	}

	senderConfig := newScheduleConfig(opts...)
	from := senderConfig.SenderAddr()
	job := job.NewFunctionJob(
		func(ctx context.Context) (bool, error) {
			err := x.remoting.RemoteTell(ctx, from, to, message)
			return err == nil, err
		},
	)

	reference := senderConfig.Reference()
	jobKey := quartz.NewJobKey(reference)
	x.scheduledKeys.Set(reference, jobKey)

	detail := quartz.NewJobDetail(job, jobKey)
	// schedule the job
	return x.quartzScheduler.ScheduleJob(detail, quartz.NewRunOnceTrigger(delay))
}

// RemoteSchedule schedules a recurring message to be sent to a remote actor at a specified interval.
//
// This method sends the given message repeatedly to the remote actor located at the specified address,
// with each delivery occurring after the configured time interval.
// Remoting must be enabled in the actor system for this functionality to work.
//
// Parameters:
//   - message: The proto.Message to be delivered periodically.
//   - to: The address.Address of the remote actor that will receive the message.
//   - interval: The time duration between each message delivery.
//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
//
// Returns:
//   - error: An error is returned if remoting is not enabled, the target address is invalid, or scheduling fails.
//
// Note:
//   - Remoting must be enabled in the actor system for this method to function correctly.
//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the scheduled message.
//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable for later operations.
func (x *scheduler) RemoteSchedule(message proto.Message, to *address.Address, interval time.Duration, opts ...ScheduleOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	if x.remoting == nil {
		return ErrRemotingDisabled
	}

	senderConfig := newScheduleConfig(opts...)
	from := senderConfig.SenderAddr()
	job := job.NewFunctionJob(
		func(ctx context.Context) (bool, error) {
			err := x.remoting.RemoteTell(ctx, from, to, message)
			return err == nil, err
		},
	)

	reference := senderConfig.Reference()
	jobKey := quartz.NewJobKey(reference)
	x.scheduledKeys.Set(reference, jobKey)

	detail := quartz.NewJobDetail(job, jobKey)
	// schedule the job
	return x.quartzScheduler.ScheduleJob(detail, quartz.NewSimpleTrigger(interval))
}

// RemoteScheduleWithCron schedules a message to be sent to a remote actor according to a cron expression.
//
// This method allows scheduling messages to remote actors using flexible cron-based timing,
// enabling complex recurring schedules for message delivery.
// Remoting must be enabled in the actor system for this method to work.
//
// Parameters:
//   - message: The proto.Message to be delivered according to the cron schedule.
//   - to: The address.Address of the remote actor that will receive the message.
//   - cronExpression: A standard cron-formatted string defining the schedule (e.g., "0 0 * * *").
//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
//
// Returns:
//   - error: An error is returned if the cron expression is invalid, remoting is disabled, or scheduling fails.
//
// Note:
//   - Remoting must be enabled in the actor system for this functionality.
//   - It's strongly recommended to set a unique reference ID using WithReference if you intend to cancel, pause, or resume the scheduled message.
//   - If no reference is set, an automatic one will be generated internally and may not be easily retrievable.
//   - The cron expression must conform to the scheduler’s supported format (usually 5 or 6 fields).
func (x *scheduler) RemoteScheduleWithCron(message proto.Message, to *address.Address, cronExpression string, opts ...ScheduleOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	if x.remoting == nil {
		return ErrRemotingDisabled
	}

	senderConfig := newScheduleConfig(opts...)
	from := senderConfig.SenderAddr()
	job := job.NewFunctionJob(
		func(ctx context.Context) (bool, error) {
			err := x.remoting.RemoteTell(ctx, from, to, message)
			return err == nil, err
		},
	)

	reference := senderConfig.Reference()
	jobKey := quartz.NewJobKey(reference)
	x.scheduledKeys.Set(reference, jobKey)

	detail := quartz.NewJobDetail(job, jobKey)

	location := time.Now().Location()
	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, location)
	if err != nil {
		x.logger.Error(fmt.Errorf("failed to schedule message: %w", err))
		return err
	}

	return x.quartzScheduler.ScheduleJob(detail, trigger)
}

// CancelSchedule cancels a previously scheduled message intended for delivery to a target actor (PID).
//
// It attempts to locate and cancel the scheduled task associated with the specified message reference.
// If the scheduled message cannot be found, has already been delivered, or was previously canceled, an error is returned.
//
// Parameters:
//   - reference: The message reference previously used when scheduling the message
//
// Returns:
//   - error: An error is returned if the scheduled message could not be found or canceled.
func (x *scheduler) CancelSchedule(reference string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	defer x.scheduledKeys.Delete(reference)

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	jobKey, ok := x.scheduledKeys.Get(reference)
	if !ok {
		return ErrScheduledReferenceNotFound
	}

	return x.quartzScheduler.DeleteJob(jobKey)
}

// PauseSchedule pauses a previously scheduled message that was set to be delivered to a target actor (PID).
//
// This function temporarily halts the delivery of the scheduled message. It can be resumed later using a corresponding resume mechanism,
// depending on the scheduler's capabilities. If the message has already been delivered or cannot be found, an error is returned.
//
// Parameters:
//   - reference: The message reference previously used when scheduling the message
//
// Returns:
//   - error: An error is returned if the scheduled message cannot be found, has already been delivered, or cannot be paused.
func (x *scheduler) PauseSchedule(reference string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	jobKey, ok := x.scheduledKeys.Get(reference)
	if !ok {
		return ErrScheduledReferenceNotFound
	}

	return x.quartzScheduler.PauseJob(jobKey)
}

// ResumeSchedule resumes a previously paused scheduled message intended for delivery to a target actor (PID).
//
// This function reactivates a scheduled message that was previously paused, allowing it to continue toward delivery.
// If the message has already been delivered, canceled, or cannot be found, an error is returned.
//
// Parameters:
//   - reference: The message reference previously used when scheduling the message
//
// Returns:
//   - error: An error is returned if the scheduled message cannot be found, was never paused, has already been delivered, or cannot be resumed.
func (x *scheduler) ResumeSchedule(reference string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return ErrSchedulerNotStarted
	}

	jobKey, ok := x.scheduledKeys.Get(reference)
	if !ok {
		return ErrScheduledReferenceNotFound
	}

	return x.quartzScheduler.ResumeJob(jobKey)
}
