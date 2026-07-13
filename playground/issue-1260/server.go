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
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
)

// HTTP routes exposed by every pod.
const (
	routeHealth = "/health"
	routeReady  = "/ready"
	routeSpawn  = "/spawn"
	routeVerify = "/verify"
	routeEvents = "/events"
	routeCrash  = "/crash"
)

// crashExitCode mimics the exit code of a SIGKILL-ed process.
const crashExitCode = 137

const (
	// queryCount is the query parameter naming how many workers to spawn or
	// verify; defaultWorkerCount applies when it is absent.
	queryCount         = "count"
	defaultWorkerCount = 30

	// readHeaderTimeout bounds header reads on the demo HTTP server.
	readHeaderTimeout = 5 * time.Second
)

// newHTTPServer builds the HTTP server exposing the self-validation surface of
// the reproduction: spawn workers, verify their existence in the cluster
// registry, and dump the observed cluster events.
func newHTTPServer(actorSystem goakt.ActorSystem, recorder *eventRecorder, port int, logger log.Logger) *http.Server {
	mux := http.NewServeMux()

	// health answers as soon as the process runs (liveness), ready only once
	// the actor system started (readiness): a pod must not be killed while it
	// bootstraps, and must not receive traffic before it joined the cluster
	mux.HandleFunc(routeHealth, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc(routeReady, func(w http.ResponseWriter, _ *http.Request) {
		if !actorSystem.Running() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc(routeSpawn, spawnHandler(actorSystem, logger))
	mux.HandleFunc(routeVerify, verifyHandler(actorSystem))

	mux.HandleFunc(routeEvents, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, recorder.snapshot())
	})

	// crash kills the process with kill -9 semantics: os.Exit runs no deferred
	// functions and no signal handlers, so no graceful shutdown and no
	// PeerState snapshot happen. This is deterministic where sending SIGKILL
	// from inside the container is not (the kernel discards SIGKILL sent to
	// PID 1 from its own PID namespace). The exit is immediate by design; the
	// HTTP response may never flush and callers must tolerate that.
	mux.HandleFunc(routeCrash, func(http.ResponseWriter, *http.Request) {
		logger.Warn("crashing on request: exiting without graceful shutdown")
		os.Exit(crashExitCode)
	})

	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}
}

// spawnHandler spawns the requested number of relocatable workers, spread
// round-robin across the cluster so the crashed pod necessarily hosts a share
// of them.
func spawnHandler(actorSystem goakt.ActorSystem, logger log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		count := queryIntOr(r, queryCount, defaultWorkerCount)
		spawned := 0

		for i := range count {
			name := workerName(i)

			_, err := actorSystem.SpawnOn(r.Context(), name, new(Worker),
				goakt.WithLongLived(),
				goakt.WithPlacement(goakt.RoundRobin))
			if err != nil {
				logger.Warnf("failed to spawn %s: %v", name, err)
				continue
			}

			spawned++
		}

		writeJSON(w, map[string]any{"requested": count, "spawned": spawned})
	}
}

// verifyHandler checks every worker against the cluster registry and reports
// the ones that are missing. After a crash this is the completeness check for
// issue #1260: on the default configuration nothing may be missing.
func verifyHandler(actorSystem goakt.ActorSystem) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		count := queryIntOr(r, queryCount, defaultWorkerCount)
		missing := make([]string, 0)
		found := 0

		for i := range count {
			name := workerName(i)

			exists, err := actorSystem.ActorExists(r.Context(), name)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if exists {
				found++
				continue
			}

			missing = append(missing, name)
		}

		writeJSON(w, map[string]any{
			"expected": count,
			"found":    found,
			"missing":  missing,
			"complete": found == count,
		})
	}
}

// workerName returns the deterministic name of the i-th worker.
func workerName(i int) string {
	return fmt.Sprintf("%s-%d", workerPrefix, i)
}

// writeJSON encodes payload as the JSON response body.
func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// envIntOr returns the integer value of the environment variable key, or
// fallback when it is not set or not a number.
func envIntOr(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

// queryIntOr returns the integer value of the query parameter key, or fallback
// when it is absent or not a number.
func queryIntOr(r *http.Request, key string, fallback int) int {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}
