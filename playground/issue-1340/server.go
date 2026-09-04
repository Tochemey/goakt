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
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
)

const (
	// HTTP routes exposed by every node.
	routeHealth = "/health"
	routeReady  = "/ready"
	routeReport = "/report"

	// timestampLayout is RFC3339 with milliseconds. Every timestamp in the
	// report is UTC in this layout, so the demo can compare them with the time
	// it killed the node.
	timestampLayout = "2006-01-02T15:04:05.000Z07:00"

	// readHeaderTimeout bounds header reads on the demo HTTP server.
	readHeaderTimeout = 5 * time.Second

	// singletonLookupTimeout bounds the singleton lookup behind /ready on the
	// node that hosts it.
	singletonLookupTimeout = 2 * time.Second
)

// report is the JSON returned by GET /report: the cluster events this node
// observed, the measured request timeline, and the durations derived from
// them. Every timestamp is UTC, RFC3339 with milliseconds, and carries a
// companion field in epoch milliseconds so the demo can do arithmetic on it
// without parsing dates.
type report struct {
	Node   string       `json:"node"`
	Now    string       `json:"now"`
	Config reportConfig `json:"config"`

	NodeLeftAt        string `json:"node_left_at"`
	NodeLeftAtMillis  int64  `json:"node_left_at_ms"`
	NodeLeftAddress   string `json:"node_left_address"`
	RelocationFailure int    `json:"relocation_failures"`

	// the departure as the cluster timed it, and the wait that separates it
	// from the moment this node published NodeLeft: the convergence, which is
	// the only part of the delay the convergence timeout bounds.
	NodeLeftConfirmedAt       string  `json:"node_left_confirmed_at"`
	NodeLeftConfirmedAtMillis int64   `json:"node_left_confirmed_at_ms"`
	ConvergenceWait           float64 `json:"convergence_wait"`

	SingletonFirstFailure       string   `json:"singleton_first_failure"`
	SingletonFirstFailureMillis int64    `json:"singleton_first_failure_ms"`
	SingletonRecovered          string   `json:"singleton_recovered"`
	SingletonRecoveredMillis    int64    `json:"singleton_recovered_ms"`
	SingletonSuccesses          int      `json:"singleton_successes"`
	SingletonFailures           int      `json:"singleton_failures"`
	SingletonLastResponse       string   `json:"singleton_last_response"`
	SingletonAfterRecovery      string   `json:"singleton_response_after_recovery"`
	SingletonLastErrors         []string `json:"singleton_last_errors"`

	GrainFirstFailure       string   `json:"grain_first_failure"`
	GrainFirstFailureMillis int64    `json:"grain_first_failure_ms"`
	GrainRecovered          string   `json:"grain_recovered"`
	GrainRecoveredMillis    int64    `json:"grain_recovered_ms"`
	GrainSuccesses          int      `json:"grain_successes"`
	GrainFailures           int      `json:"grain_failures"`
	GrainLastResponse       string   `json:"grain_last_response"`
	GrainAfterRecovery      string   `json:"grain_response_after_recovery"`
	GrainLastErrors         []string `json:"grain_last_errors"`

	NodeLeftAfterFirstFailure float64 `json:"node_left_after_first_failure"`
	SingletonOutage           float64 `json:"singleton_outage"`
	GrainOutage               float64 `json:"grain_outage"`

	Events []recordedEvent `json:"events"`
}

// reportConfig describes the cluster settings this run used, so a measurement
// is always read together with the configuration that produced it. Each value
// is "default" when the run left the setting to the framework.
type reportConfig struct {
	ConvergenceTimeout string `json:"convergence_timeout"`
	NetworkProfile     string `json:"network_profile"`
}

// newHTTPServer builds the HTTP surface of a node: the two probes the compose
// file and the demo wait on, and the report the demo reads after the kill.
func newHTTPServer(actorSystem goakt.ActorSystem, recorder *eventRecorder, runner *driver, runScenario *scenario, port int) *http.Server {
	mux := http.NewServeMux()

	// health answers once the node joined the cluster. The compose file starts
	// the nodes one after the other on this probe, which makes node1 the
	// oldest member, and with it the cluster leader and the singleton host.
	mux.HandleFunc(routeHealth, func(w http.ResponseWriter, _ *http.Request) {
		if !actorSystem.Running() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	// ready answers once this node has proven that the scenario is set up: on
	// a survivor both request streams answered steadyStreak times in a row, on
	// the singleton host the singleton is spawned.
	mux.HandleFunc(routeReady, func(w http.ResponseWriter, r *http.Request) {
		if !actorSystem.Running() || !ready(r, actorSystem, runner) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc(routeReport, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(newReport(recorder, runner, runScenario))
	})

	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}
}

// ready reports whether the node is ready for the kill.
func ready(r *http.Request, actorSystem goakt.ActorSystem, runner *driver) bool {
	if runner != nil {
		return runner.ready()
	}

	ctx, cancel := context.WithTimeout(r.Context(), singletonLookupTimeout)
	defer cancel()

	exists, err := actorSystem.ActorExists(ctx, singletonName)
	return err == nil && exists
}

// newReport assembles the report from the recorded events and, on a survivor,
// the driver's timelines.
func newReport(recorder *eventRecorder, runner *driver, runScenario *scenario) *report {
	nodeLeftAt, nodeLeftConfirmedAt, nodeLeftAddress := recorder.departure()

	out := &report{
		Node: envOr(envNodeName, defaultNodeName),
		Now:  formatTime(time.Now().UTC()),
		Config: reportConfig{
			ConvergenceTimeout: runScenario.convergenceTimeoutLabel(),
			NetworkProfile:     runScenario.networkProfileLabel(),
		},
		NodeLeftAt:                formatTime(nodeLeftAt),
		NodeLeftAtMillis:          epochMillis(nodeLeftAt),
		NodeLeftAddress:           nodeLeftAddress,
		RelocationFailure:         recorder.failedRelocations(),
		NodeLeftConfirmedAt:       formatTime(nodeLeftConfirmedAt),
		NodeLeftConfirmedAtMillis: epochMillis(nodeLeftConfirmedAt),
		ConvergenceWait:           seconds(nodeLeftConfirmedAt, nodeLeftAt),
		Events:                    recorder.snapshot(),
	}

	if runner == nil {
		return out
	}

	singleton := runner.singleton.snapshot()
	grains := runner.grains.snapshot()

	out.SingletonFirstFailure = formatTime(singleton.firstFailure)
	out.SingletonFirstFailureMillis = epochMillis(singleton.firstFailure)
	out.SingletonRecovered = formatTime(singleton.recovered)
	out.SingletonRecoveredMillis = epochMillis(singleton.recovered)
	out.SingletonSuccesses = singleton.successes
	out.SingletonFailures = singleton.failures
	out.SingletonLastResponse = singleton.lastResponse
	out.SingletonAfterRecovery = singleton.responseAfterRecovery
	out.SingletonLastErrors = singleton.lastErrors

	out.GrainFirstFailure = formatTime(grains.firstFailure)
	out.GrainFirstFailureMillis = epochMillis(grains.firstFailure)
	out.GrainRecovered = formatTime(grains.recovered)
	out.GrainRecoveredMillis = epochMillis(grains.recovered)
	out.GrainSuccesses = grains.successes
	out.GrainFailures = grains.failures
	out.GrainLastResponse = grains.lastResponse
	out.GrainAfterRecovery = grains.responseAfterRecovery
	out.GrainLastErrors = grains.lastErrors

	out.NodeLeftAfterFirstFailure = seconds(singleton.firstFailure, nodeLeftAt)
	out.SingletonOutage = seconds(singleton.firstFailure, singleton.recovered)
	out.GrainOutage = seconds(grains.firstFailure, grains.recovered)

	return out
}

// formatTime renders a time as UTC RFC3339 with milliseconds, and an unset
// time as an empty string.
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}

	return value.UTC().Format(timestampLayout)
}

// epochMillis renders a time as epoch milliseconds, and an unset time as zero.
func epochMillis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}

	return value.UnixMilli()
}

// seconds returns the number of seconds between two times, rounded to
// milliseconds, and zero when either of them is unset.
func seconds(from, to time.Time) float64 {
	if from.IsZero() || to.IsZero() {
		return 0
	}

	return math.Round(to.Sub(from).Seconds()*1000) / 1000
}
