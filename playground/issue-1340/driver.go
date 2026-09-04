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

package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"

	goakt "github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
)

const (
	// probeInterval is the pause between two requests to the same target.
	probeInterval = 200 * time.Millisecond

	// requestTimeout bounds one request, resolution included.
	requestTimeout = 2 * time.Second

	// grainCount and grainPrefix name the grains the driver cycles through.
	grainCount  = 30
	grainPrefix = "grain"

	// steadyStreak is the number of consecutive successful requests per target
	// that make this node ready for the kill: it proves that both the
	// singleton and the grains answer before anything is broken.
	steadyStreak = 10

	// maxRecordedErrors is how many of the most recent failures each target
	// keeps for the report.
	maxRecordedErrors = 20
)

// timeline records the outcome of one stream of requests: the counters, the
// moment the requests started failing, and the moment they succeeded again.
type timeline struct {
	mu sync.Mutex

	successes int
	failures  int

	// streak counts the consecutive successes; steady latches once the streak
	// reached steadyStreak, so a later outage does not un-ready the node.
	streak int
	steady bool

	// sawSuccess is set by the first success. A failure only counts as the
	// start of an outage once the stream worked at least once, which keeps the
	// requests sent while the cluster is still forming out of the measurement.
	sawSuccess bool

	firstFailure time.Time
	recovered    time.Time

	lastResponse          string
	responseAfterRecovery string
	lastErrors            []string
}

// timelineSnapshot is the copy of a timeline the report is built from.
type timelineSnapshot struct {
	successes             int
	failures              int
	firstFailure          time.Time
	recovered             time.Time
	lastResponse          string
	responseAfterRecovery string
	lastErrors            []string
}

// newTimeline creates an empty timeline.
func newTimeline() *timeline {
	return &timeline{}
}

// success records a successful request and, when it is the first one after an
// outage, the recovery time.
func (x *timeline) success(response string) {
	x.mu.Lock()
	defer x.mu.Unlock()

	x.successes++
	x.streak++
	x.sawSuccess = true
	x.lastResponse = response

	if x.streak >= steadyStreak {
		x.steady = true
	}

	if !x.firstFailure.IsZero() && x.recovered.IsZero() {
		x.recovered = time.Now().UTC()
		x.responseAfterRecovery = response
	}
}

// failure records a failed request and, when it is the first one after a
// period of success, the start of the outage.
func (x *timeline) failure(err error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	x.failures++
	x.streak = 0

	if x.sawSuccess && x.firstFailure.IsZero() {
		x.firstFailure = time.Now().UTC()
	}

	x.lastErrors = append(x.lastErrors, err.Error())
	if len(x.lastErrors) > maxRecordedErrors {
		x.lastErrors = x.lastErrors[len(x.lastErrors)-maxRecordedErrors:]
	}
}

// steadyReached reports whether this stream ever reached a streak of
// steadyStreak successful requests.
func (x *timeline) steadyReached() bool {
	x.mu.Lock()
	defer x.mu.Unlock()

	return x.steady
}

// snapshot returns a copy of the timeline.
func (x *timeline) snapshot() timelineSnapshot {
	x.mu.Lock()
	defer x.mu.Unlock()

	errors := make([]string, len(x.lastErrors))
	copy(errors, x.lastErrors)

	return timelineSnapshot{
		successes:             x.successes,
		failures:              x.failures,
		firstFailure:          x.firstFailure,
		recovered:             x.recovered,
		lastResponse:          x.lastResponse,
		responseAfterRecovery: x.responseAfterRecovery,
		lastErrors:            errors,
	}
}

// driver keeps sending requests to the singleton and to the grains from a
// surviving node and records what the kill of the singleton host does to them.
type driver struct {
	system goakt.ActorSystem
	node   string
	logger log.Logger

	singleton *timeline
	grains    *timeline
}

// newDriver creates the request driver of a surviving node.
func newDriver(system goakt.ActorSystem, node string, logger log.Logger) *driver {
	return &driver{
		system:    system,
		node:      node,
		logger:    logger,
		singleton: newTimeline(),
		grains:    newTimeline(),
	}
}

// run starts both request streams. They run independently so that an outage of
// one does not slow the other one down.
func (x *driver) run(ctx context.Context) {
	go x.probeSingleton(ctx)
	go x.probeGrains(ctx)
}

// ready reports whether both streams reached their streak of successful
// requests, which is what the demo waits for before killing the singleton
// host.
func (x *driver) ready() bool {
	return x.singleton.steadyReached() && x.grains.steadyReached()
}

// probeSingleton asks the singleton every probeInterval.
func (x *driver) probeSingleton(ctx context.Context) {
	for {
		x.askSingleton(ctx)

		if !pause(ctx, probeInterval) {
			return
		}
	}
}

// probeGrains asks one grain every probeInterval, cycling through all of them.
func (x *driver) probeGrains(ctx context.Context) {
	index := 0

	for {
		x.askGrain(ctx, index)
		index = (index + 1) % grainCount

		if !pause(ctx, probeInterval) {
			return
		}
	}
}

// askSingleton resolves the singleton by name and asks it. Resolution is part
// of the request: the singleton moves to another node when its host is killed,
// and a caller that kept the old reference would only measure the staleness of
// its own cache.
func (x *driver) askSingleton(ctx context.Context) {
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	pid, err := x.system.ActorOf(callCtx, singletonName)
	if err != nil {
		x.singleton.failure(err)
		return
	}

	response, err := goakt.Ask(callCtx, pid, wrapperspb.String(x.node), requestTimeout)
	if err != nil {
		x.singleton.failure(err)
		return
	}

	value, ok := response.(*wrapperspb.StringValue)
	if !ok {
		x.singleton.failure(fmt.Errorf("unexpected response type %T", response))
		return
	}

	x.singleton.success(value.GetValue())
}

// askGrain asks the grain at the given index and records the outcome.
func (x *driver) askGrain(ctx context.Context, index int) {
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	name := fmt.Sprintf("%s-%d", grainPrefix, index)

	identity, err := goakt.GrainOf[*Worker](callCtx, x.system, name)
	if err != nil {
		x.grains.failure(err)
		return
	}

	response, err := x.system.AskGrain(callCtx, identity, wrapperspb.String(x.node), requestTimeout)
	if err != nil {
		x.grains.failure(err)
		return
	}

	value, ok := response.(*wrapperspb.StringValue)
	if !ok {
		x.grains.failure(fmt.Errorf("unexpected response type %T", response))
		return
	}

	x.grains.success(value.GetValue())
}

// pause waits for the given duration and reports whether the caller should
// keep going.
func pause(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
