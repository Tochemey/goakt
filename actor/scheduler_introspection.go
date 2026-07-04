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

import "time"

// TriggerKind identifies the kind of trigger backing a scheduled message.
type TriggerKind string

const (
	// TriggerKindOnce identifies a schedule created via ScheduleOnce: it fires exactly once after a delay.
	TriggerKindOnce TriggerKind = "once"
	// TriggerKindInterval identifies a schedule created via Schedule: it fires repeatedly at a fixed interval.
	TriggerKindInterval TriggerKind = "interval"
	// TriggerKindCron identifies a schedule created via ScheduleWithCron: it fires according to a cron expression.
	TriggerKindCron TriggerKind = "cron"
)

// ScheduleInfo is a read-only snapshot describing a single scheduled message known to the scheduler.
//
// It is returned by ListSchedules and is meant for observability/admin tooling (dashboards, operational
// debugging, etc.). Mutating the schedule (cancel/pause/resume) still goes through the existing
// reference-based APIs; ScheduleInfo only reports what those APIs would otherwise operate on blindly.
type ScheduleInfo struct {
	// Reference is the schedule reference, either user-supplied via WithReference or auto-generated.
	Reference string
	// TriggerKind reports which of ScheduleOnce/Schedule/ScheduleWithCron created this schedule.
	TriggerKind TriggerKind
	// Expression is the cron expression used to create the schedule. It is only set when
	// TriggerKind is TriggerKindCron.
	Expression string
	// Interval is the fixed delivery interval for TriggerKindInterval, or the original delay for
	// TriggerKindOnce. It is zero when TriggerKind is TriggerKindCron.
	Interval time.Duration
	// NextFireTime is the next time, as reported by the underlying scheduler, at which the message
	// will be delivered.
	NextFireTime time.Time
	// Address is the target actor address the message will be delivered to.
	Address string
}

// scheduleMeta captures the information about a schedule that the underlying quartz job cannot
// report back on its own: the trigger's origin (kind + expression/interval) and the delivery
// target's address. The quartz scheduled job itself remains the source of truth for the next
// fire time and for whether the schedule still exists.
type scheduleMeta struct {
	kind       TriggerKind
	expression string
	interval   time.Duration
	address    string
}

// recordSchedule stores the introspection metadata for a newly created schedule.
// Called by ScheduleOnce, Schedule and ScheduleWithCron right after the job key is registered.
func (x *scheduler) recordSchedule(reference string, meta *scheduleMeta) {
	x.scheduledMeta.Set(reference, meta)
}

// ListSchedules returns a snapshot of every schedule currently known to the scheduler: reference,
// trigger kind, cron expression or interval, next fire time, and target actor address.
//
// It is purely read-only and has no effect on the schedules themselves. A schedule stops appearing
// once it has been canceled (CancelSchedule) or, for one-shot schedules created via ScheduleOnce,
// once it has fired and been delivered.
//
// Returns an empty slice when the scheduler has not started or when nothing is currently scheduled.
func (x *scheduler) ListSchedules() []ScheduleInfo {
	if !x.started.Load() {
		return []ScheduleInfo{}
	}

	infos := make([]ScheduleInfo, 0, x.scheduledMeta.Len())
	x.scheduledMeta.Range(func(reference string, meta *scheduleMeta) {
		jobKey, ok := x.scheduledKeys.Get(reference)
		if !ok {
			return
		}

		scheduledJob, err := x.quartzScheduler.GetScheduledJob(jobKey)
		if err != nil {
			// the underlying quartz job is gone: either it was a one-shot that already fired,
			// or it was removed through a path that did not clean up scheduledMeta.
			return
		}

		infos = append(infos, ScheduleInfo{
			Reference:    reference,
			TriggerKind:  meta.kind,
			Expression:   meta.expression,
			Interval:     meta.interval,
			NextFireTime: time.Unix(0, scheduledJob.NextRunTime()),
			Address:      meta.address,
		})
	})

	return infos
}
