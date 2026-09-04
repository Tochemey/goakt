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
	"sync"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/log"
)

const (
	// drainInterval is how often the event stream subscriber is drained. The
	// subscriber buffers events internally and its iterator only returns what
	// is buffered at call time, so the recorder polls it.
	drainInterval = 100 * time.Millisecond

	// event names as they appear in the report.
	eventNodeJoined        = "NodeJoined"
	eventNodeLeft          = "NodeLeft"
	eventLeaderChanged     = "LeaderChanged"
	eventRelocationStarted = "RelocationStarted"
	eventRelocationFailed  = "RelocationFailed"
)

// recordedEvent is one cluster event as it appears in the report. Time is the
// moment this node observed the event, which is what the demo measures against
// the kill; Timestamp is the moment carried by the event itself.
type recordedEvent struct {
	Time       string   `json:"time"`
	Timestamp  string   `json:"timestamp,omitempty"`
	Type       string   `json:"type"`
	Address    string   `json:"address,omitempty"`
	Actors     []string `json:"actors,omitempty"`
	Grains     []string `json:"grains,omitempty"`
	BestEffort bool     `json:"best_effort,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// eventRecorder accumulates the cluster events observed on this node, and
// keeps the departure of the killed node at hand: its observation time is the
// number the issue is about.
type eventRecorder struct {
	mu   sync.Mutex
	node string

	events []recordedEvent

	// nodeLeftAt and nodeLeftAddress describe the first departure this node
	// observed. The demo kills exactly one node, so this is that node.
	nodeLeftAt      time.Time
	nodeLeftAddress string

	// nodeLeftConfirmedAt is the timestamp the departure itself carries: the
	// moment the cluster confirmed the loss, which the event keeps while it
	// waits for the cluster state to converge on it. The distance between the
	// two is the convergence wait, the only part of the delay the convergence
	// timeout bounds.
	nodeLeftConfirmedAt time.Time

	// relocationFailures counts the RelocationFailed events. A failed
	// relocation means the singleton and the grains of the killed node were
	// not fully re-established.
	relocationFailures int
}

// newEventRecorder creates an empty event recorder for the given node.
func newEventRecorder(node string) *eventRecorder {
	return &eventRecorder{node: node}
}

// consume drains the event stream subscriber on a fixed interval.
func (x *eventRecorder) consume(consumer eventstream.Subscriber, logger log.Logger) {
	ticker := time.NewTicker(drainInterval)
	defer ticker.Stop()

	for range ticker.C {
		x.drain(consumer, logger)
	}
}

// drain empties the subscriber buffer and records the cluster events this
// reproduction reports on.
func (x *eventRecorder) drain(consumer eventstream.Subscriber, logger log.Logger) {
	for message := range consumer.Iterator() {
		observed := time.Now().UTC()

		switch event := message.Payload().(type) {
		case *goakt.NodeJoined:
			x.add(recordedEvent{
				Time:      formatTime(observed),
				Timestamp: formatTime(event.Timestamp()),
				Type:      eventNodeJoined,
				Address:   event.Address(),
			})

		case *goakt.NodeLeft:
			x.add(recordedEvent{
				Time:      formatTime(observed),
				Timestamp: formatTime(event.Timestamp()),
				Type:      eventNodeLeft,
				Address:   event.Address(),
			})
			x.recordDeparture(observed, event.Timestamp(), event.Address())
			logger.Infof("%s observed NodeLeft for %s", x.node, event.Address())

		case *goakt.LeaderChanged:
			x.add(recordedEvent{
				Time:      formatTime(observed),
				Timestamp: formatTime(event.Timestamp()),
				Type:      eventLeaderChanged,
				Address:   event.Address(),
			})
			logger.Infof("%s observed LeaderChanged to %s", x.node, event.Address())

		case *goakt.RelocationStarted:
			x.add(recordedEvent{
				Time:       formatTime(observed),
				Timestamp:  formatTime(event.Timestamp()),
				Type:       eventRelocationStarted,
				Address:    event.Address(),
				Actors:     event.Actors(),
				Grains:     event.Grains(),
				BestEffort: event.BestEffort(),
			})
			logger.Infof("%s observed RelocationStarted for %s: actors=%d grains=%d", x.node, event.Address(), len(event.Actors()), len(event.Grains()))

		case *goakt.RelocationFailed:
			x.add(recordedEvent{
				Time:      formatTime(observed),
				Timestamp: formatTime(event.Timestamp()),
				Type:      eventRelocationFailed,
				Address:   event.Address(),
				Actors:    event.Actors(),
				Grains:    event.Grains(),
				Error:     event.Error().Error(),
			})
			x.recordRelocationFailure()
			logger.Warnf("%s observed RelocationFailed for %s: %v", x.node, event.Address(), event.Error())
		}
	}
}

// add appends an event to the recorder.
func (x *eventRecorder) add(event recordedEvent) {
	x.mu.Lock()
	defer x.mu.Unlock()

	x.events = append(x.events, event)
}

// recordDeparture keeps the first observed departure, both when this node
// published it and when the cluster confirmed the loss.
func (x *eventRecorder) recordDeparture(observed, confirmed time.Time, address string) {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.nodeLeftAt.IsZero() {
		x.nodeLeftAt = observed
		x.nodeLeftConfirmedAt = confirmed
		x.nodeLeftAddress = address
	}
}

// recordRelocationFailure counts one failed relocation.
func (x *eventRecorder) recordRelocationFailure() {
	x.mu.Lock()
	defer x.mu.Unlock()

	x.relocationFailures++
}

// snapshot returns a copy of the recorded events.
func (x *eventRecorder) snapshot() []recordedEvent {
	x.mu.Lock()
	defer x.mu.Unlock()

	events := make([]recordedEvent, len(x.events))
	copy(events, x.events)

	return events
}

// departure returns the time this node observed the first departure, the time
// the cluster confirmed it, and the address of the departed node.
func (x *eventRecorder) departure() (observed, confirmed time.Time, address string) {
	x.mu.Lock()
	defer x.mu.Unlock()

	return x.nodeLeftAt, x.nodeLeftConfirmedAt, x.nodeLeftAddress
}

// failedRelocations returns the number of RelocationFailed events observed.
func (x *eventRecorder) failedRelocations() int {
	x.mu.Lock()
	defer x.mu.Unlock()

	return x.relocationFailures
}
