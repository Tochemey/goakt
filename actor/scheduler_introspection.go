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
	"errors"

	"github.com/reugn/go-quartz/quartz"
)

// ScheduleInfo is a read-only snapshot describing a single scheduled message known to the scheduler.
//
// It is returned by ListSchedules and is meant for observability/admin tooling (dashboards, operational
// debugging, etc.). Mutating the schedule (cancel/pause/resume) still goes through the existing
// reference-based APIs; ScheduleInfo only reports what those APIs would otherwise operate on blindly.
type ScheduleInfo struct {
	// Reference is the schedule reference, either user-supplied via WithReference or auto-generated.
	Reference string
	// Path is the target actor path the message will be delivered to.
	Path string
}

// scheduleMeta captures what the underlying quartz job cannot report back on its own: the
// delivery target's path. The quartz scheduled job itself remains the source of truth for
// whether the schedule still exists.
type scheduleMeta struct {
	path string
}

// recordSchedule stores the introspection metadata for a newly created schedule.
// Called by ScheduleOnce, Schedule and ScheduleWithCron right after the job key is registered.
func (x *scheduler) recordSchedule(reference string, meta *scheduleMeta) {
	x.scheduledMeta.Set(reference, meta)
}

// ListSchedules returns a snapshot of every schedule currently known to the scheduler:
// its reference and the target actor path.
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

		if _, err := x.quartzScheduler.GetScheduledJob(jobKey); err != nil {
			// only job-not-found means the schedule legitimately ended (fired one-shot or a
			// removal path that skipped scheduledMeta); anything else is unexpected and logged
			if !errors.Is(err, quartz.ErrJobNotFound) {
				x.logger.Warnf("failed to look up scheduled job reference=%s: %v", reference, err)
			}
			return
		}

		infos = append(infos, ScheduleInfo{
			Reference: reference,
			Path:      meta.path,
		})
	})

	return infos
}
