// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package actor

import (
	"context"
	stderrors "errors"
	"fmt"
	"sync"
	"time"

	"github.com/reugn/go-quartz/job"
	quartzlogger "github.com/reugn/go-quartz/logger"
	"github.com/reugn/go-quartz/quartz"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
)

// scheduleOutdatedThreshold is how late a scheduled tick may still execute before quartz
// drops it as outdated (see newScheduler for why the default 100ms threshold is unusable).
const scheduleOutdatedThreshold = 24 * time.Hour

// minScheduleFireClaimTTL and maxScheduleFireClaimTTL bound the derived claim TTL. The floor
// keeps tight cron periods from producing a claim window shorter than realistic node spread;
// the ceiling caps how long claim entries linger for very long cron periods. A tick older
// than the claim TTL is never delivered in cluster mode (claimClusterFire skips it as stale),
// which is what keeps a node lagging past a claim's expiry from re-winning the same tick.
const (
	minScheduleFireClaimTTL = time.Minute
	maxScheduleFireClaimTTL = scheduleOutdatedThreshold
)

// scheduleFireClaimKeyFormat composes the cluster-wide claim key from the schedule reference
// and the tick's scheduled fire time (quartz.JobMetadata.RunTime).
const scheduleFireClaimKeyFormat = "%s@%d"

// scheduleFireClaim carries the cluster-wide single-fire arbitration parameters for a cron
// schedule registered in cluster mode. A nil *scheduleFireClaim means the schedule never
// claims and always fires locally (every non-cron schedule, and every cron schedule outside
// cluster mode).
type scheduleFireClaim struct {
	reference string
	ttl       time.Duration
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
	// specifies the job keys mapping
	scheduledKeys *xsync.Map[string, *quartz.JobKey]
	// actorSystem is needed to resolve NoSender() for remote-PID schedules,
	// since remote PIDs carry no actor-system reference.
	actorSystem ActorSystem
}

// newScheduler creates an instance of scheduler
func newScheduler(logger log.Logger, shutdownTimeout time.Duration, system ActorSystem) *scheduler {
	// create an instance of quartz scheduler with logger off
	// Set a high OutdatedThreshold to prevent RunOnceTrigger jobs from being
	// silently dropped when they become "outdated" (scheduled time passed).
	// The default 100ms threshold causes issues because:
	// 1. RunOnceTrigger marks itself as expired during initial scheduling
	// 2. If the job becomes outdated, go-quartz tries to reschedule it
	// 3. The already-expired trigger returns an error, causing the job to be dropped
	// By setting a 24-hour threshold, we ensure jobs are executed even if delayed.
	// JobMetadata is enabled so a job's Execute closure can read the tick's exact
	// scheduled fire time via quartz.JobMetadataContextKey; cluster-wide cron single fire
	// uses it to key the fire claim on the trigger tick rather than on wall-clock time at
	// invocation, which would drift across nodes.
	quartzScheduler, _ := quartz.NewStdScheduler(
		quartz.WithLogger(quartzlogger.NewSimpleLogger(nil, quartzlogger.LevelOff)),
		quartz.WithOutdatedThreshold(scheduleOutdatedThreshold),
		quartz.WithJobMetadata(),
	)

	// create an instance of scheduler
	scheduler := &scheduler{
		mu:              sync.Mutex{},
		started:         atomic.NewBool(false),
		quartzScheduler: quartzScheduler,
		logger:          logger,
		shutdownTimeout: shutdownTimeout,
		scheduledKeys:   xsync.NewMap[string, *quartz.JobKey](),
		actorSystem:     system,
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
func (x *scheduler) ScheduleOnce(message any, to *PID, delay time.Duration, opts ...ScheduleOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return errors.ErrSchedulerNotStarted
	}

	if to.IsRemote() && !to.remotingEnabled() {
		return errors.ErrRemotingDisabled
	}

	senderConfig := newScheduleConfig(opts...)
	// Interval and one-shot schedules never claim: they stay node-local by design (see ScheduleWithCron).
	jobFn := x.makeJobFn(to, message, senderConfig, nil)

	reference := senderConfig.Reference()
	jobKey := quartz.NewJobKey(reference)
	x.scheduledKeys.Set(reference, jobKey)

	detail := quartz.NewJobDetail(job.NewFunctionJob(jobFn), jobKey)
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
func (x *scheduler) Schedule(message any, to *PID, interval time.Duration, opts ...ScheduleOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return errors.ErrSchedulerNotStarted
	}

	if to.IsRemote() && !to.remotingEnabled() {
		return errors.ErrRemotingDisabled
	}

	senderConfig := newScheduleConfig(opts...)
	// Interval and one-shot schedules never claim: they stay node-local by design (see ScheduleWithCron).
	jobFn := x.makeJobFn(to, message, senderConfig, nil)

	reference := senderConfig.Reference()
	jobKey := quartz.NewJobKey(reference)
	x.scheduledKeys.Set(reference, jobKey)

	detail := quartz.NewJobDetail(job.NewFunctionJob(jobFn), jobKey)
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
//   - In cluster mode the message is delivered exactly once per trigger tick across the
//     cluster, WithReference is required (ErrScheduleReferenceRequired otherwise), and the
//     cron expression is evaluated in UTC so every node computes the same tick instants.
//     Outside cluster mode the expression is evaluated in the process's local timezone.
//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the scheduled message.
//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable for future operations.
//   - The cron expression must follow the format supported by the scheduler (typically 6 or 5 fields depending on implementation).
func (x *scheduler) ScheduleWithCron(message any, to *PID, cronExpression string, opts ...ScheduleOption) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.started.Load() {
		return errors.ErrSchedulerNotStarted
	}

	if to.IsRemote() && !to.remotingEnabled() {
		return errors.ErrRemotingDisabled
	}

	// The cluster engine is wired before the scheduler starts, so gate on it rather than on
	// InCluster(): the latter also requires the actor system's started flag, which is set
	// after the scheduler starts, and a call racing ActorSystem.Start would otherwise
	// register a permanently claim-free schedule.
	inCluster := x.actorSystem.getCluster() != nil

	// In cluster mode the cron expression is evaluated in UTC so every node computes the
	// same tick instants: the fire claim is keyed on the tick's fire time, and
	// location-dependent fire times (e.g. "0 0 9 * * *" on mixed-timezone nodes) would give
	// each timezone group its own key and deliver once per group instead of once per cluster.
	location := time.Now().Location()
	if inCluster {
		location = time.UTC
	}

	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, location)
	if err != nil {
		x.logger.Error(fmt.Errorf("failed to schedule message: %w", err))
		return err
	}

	senderConfig := newScheduleConfig(opts...)

	var claim *scheduleFireClaim
	if inCluster {
		// Interval/one-shot RunTimes are anchored to each node's local registration clock and
		// never align cluster-wide, so single fire is cron-only: a cron trigger's next fire
		// time is wall-clock deterministic, which is what makes cross-node arbitration meaningful.
		if !senderConfig.hasExplicitReference() {
			return errors.ErrScheduleReferenceRequired
		}

		claim = &scheduleFireClaim{
			reference: senderConfig.Reference(),
			ttl:       cronClaimTTL(trigger),
		}
	}

	jobFn := x.makeJobFn(to, message, senderConfig, claim)

	reference := senderConfig.Reference()
	jobKey := quartz.NewJobKey(reference)
	x.scheduledKeys.Set(reference, jobKey)

	detail := quartz.NewJobDetail(job.NewFunctionJob(jobFn), jobKey)
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
		return errors.ErrSchedulerNotStarted
	}

	jobKey, ok := x.scheduledKeys.Get(reference)
	if !ok {
		return errors.ErrScheduledReferenceNotFound
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
		return errors.ErrSchedulerNotStarted
	}

	jobKey, ok := x.scheduledKeys.Get(reference)
	if !ok {
		return errors.ErrScheduledReferenceNotFound
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
		return errors.ErrSchedulerNotStarted
	}

	jobKey, ok := x.scheduledKeys.Get(reference)
	if !ok {
		return errors.ErrScheduledReferenceNotFound
	}

	return x.quartzScheduler.ResumeJob(jobKey)
}

// cronClaimTTL derives the cluster schedule-fire claim TTL from the gap between two
// consecutive fire times of trigger, clamped to [minScheduleFireClaimTTL, maxScheduleFireClaimTTL].
// The TTL must cover the worst-case lag between nodes handling the same tick; see
// cluster.ClaimScheduleFire.
func cronClaimTTL(trigger quartz.Trigger) time.Duration {
	now := quartz.NowNano()
	first, err := trigger.NextFireTime(now)
	if err != nil {
		return minScheduleFireClaimTTL
	}

	second, err := trigger.NextFireTime(first)
	if err != nil {
		return minScheduleFireClaimTTL
	}

	period := time.Duration(second - first)
	switch {
	case period < minScheduleFireClaimTTL:
		return minScheduleFireClaimTTL
	case period > maxScheduleFireClaimTTL:
		return maxScheduleFireClaimTTL
	default:
		return period
	}
}

// makeJobFn returns the job function for a scheduled delivery.
// PID.Tell is already location-transparent (local and remote), so no IsRemote check is needed here.
// The scheduler holds its own actorSystem reference so that NoSender() can be resolved even
// when `to` is a remote PID (remote PIDs carry no ActorSystem reference).
func (x *scheduler) makeJobFn(to *PID, message any, cfg *scheduleConfig, claim *scheduleFireClaim) func(ctx context.Context) (bool, error) {
	noSender := x.actorSystem.NoSender()
	sender := cfg.Sender()
	if sender == nil || sender.Equals(noSender) {
		sender = noSender
	}

	return func(ctx context.Context) (bool, error) {
		if claim != nil {
			won, err := x.claimClusterFire(ctx, claim)
			if err != nil {
				// quartz runs job functions with logging disabled and no retries, so this is
				// the only place a claim failure becomes observable.
				x.logger.Warnf("failed to claim cron tick for schedule=(%s): %v", claim.reference, err)
				return false, err
			}

			if !won {
				// Another node already claimed this tick: skip delivery silently.
				return true, nil
			}
		}

		err := sender.Tell(ctx, to, message)
		return err == nil, err
	}
}

// claimClusterFire arbitrates which node is allowed to deliver the current trigger tick of a
// cron schedule running in cluster mode. It returns true for the caller that should proceed
// with delivery, and false for every other node racing for the same tick. Claim entries are
// reclaimed by their TTL alone; a canceled schedule or a stopping node never deletes them,
// since the entry may be the winning claim other nodes are still arbitrating against.
//
// A tick older than the claim TTL is skipped without claiming: quartz replays missed ticks
// for up to scheduleOutdatedThreshold, so a node lagging past the TTL would otherwise find
// the winner's expired claim and deliver the tick a second time. Skipping keeps the
// at-most-once-per-tick guarantee at the cost of never delivering a tick that every node
// missed for longer than the TTL.
//
// If the tick's scheduled fire time is unavailable, it fails open (returns true) rather than
// silently dropping every delivery cluster-wide.
func (x *scheduler) claimClusterFire(ctx context.Context, claim *scheduleFireClaim) (bool, error) {
	metadata, ok := ctx.Value(quartz.JobMetadataContextKey).(quartz.JobMetadata)
	if !ok {
		return true, nil
	}

	if lag := time.Since(time.Unix(0, metadata.RunTime)); lag > claim.ttl {
		x.logger.Warnf("skipping stale cron tick for schedule=(%s): tick is %s late, claim ttl is %s", claim.reference, lag, claim.ttl)
		return false, nil
	}

	cl := x.actorSystem.getCluster()
	if cl == nil {
		return false, errors.ErrClusterDisabled
	}

	key := fmt.Sprintf(scheduleFireClaimKeyFormat, claim.reference, metadata.RunTime)
	err := cl.ClaimScheduleFire(ctx, key, claim.ttl)
	switch {
	case err == nil:
		return true, nil
	case stderrors.Is(err, cluster.ErrScheduleFireClaimed):
		return false, nil
	default:
		return false, err
	}
}
