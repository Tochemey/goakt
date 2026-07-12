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
	"fmt"
	"sync"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/log"
)

// drainInterval is how often the event stream subscriber is drained. The
// subscriber buffers events internally; Iterator only returns what is buffered
// at call time, so the recorder polls it.
const drainInterval = 500 * time.Millisecond

// event names as they appear in the GET /events output.
const (
	eventNodeJoined        = "NodeJoined"
	eventNodeLeft          = "NodeLeft"
	eventRelocationDerived = "RelocationDerived"
	eventRelocationFailed  = "RelocationFailed"
)

// recordedEvent is the JSON shape returned by GET /events.
type recordedEvent struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Address string    `json:"address,omitempty"`
	Actors  []string  `json:"actors,omitempty"`
	Grains  []string  `json:"grains,omitempty"`
	Error   string    `json:"error,omitempty"`
}

// eventRecorder accumulates the cluster and relocation events observed on this
// node so the demo can assert on them over HTTP.
type eventRecorder struct {
	mu     sync.Mutex
	events []recordedEvent
}

// newEventRecorder creates an empty event recorder.
func newEventRecorder() *eventRecorder {
	return &eventRecorder{}
}

// consume drains the event stream subscriber on a fixed interval and records
// the events this reproduction cares about. RelocationDerived is the
// issue-1260 observability feature: it lists exactly what the leader
// reconstructed from the registry after a crash.
func (r *eventRecorder) consume(consumer eventstream.Subscriber, logger log.Logger) {
	ticker := time.NewTicker(drainInterval)
	defer ticker.Stop()

	for range ticker.C {
		r.drain(consumer, logger)
	}
}

// drain empties the subscriber buffer and records the relevant events.
func (r *eventRecorder) drain(consumer eventstream.Subscriber, logger log.Logger) {
	for message := range consumer.Iterator() {
		switch event := message.Payload().(type) {
		case *goakt.NodeJoined:
			r.add(recordedEvent{Time: time.Now().UTC(), Type: eventNodeJoined, Address: event.Address()})
		case *goakt.NodeLeft:
			r.add(recordedEvent{Time: time.Now().UTC(), Type: eventNodeLeft, Address: event.Address()})
			logger.Infof("observed NodeLeft for %s", event.Address())
		case *goakt.RelocationDerived:
			r.add(recordedEvent{
				Time:    time.Now().UTC(),
				Type:    eventRelocationDerived,
				Address: event.Address(),
				Actors:  event.Actors(),
				Grains:  event.Grains(),
			})
			logger.Infof("observed RelocationDerived for %s: actors=%d grains=%d", event.Address(), len(event.Actors()), len(event.Grains()))
		case *goakt.RelocationFailed:
			r.add(recordedEvent{
				Time:    time.Now().UTC(),
				Type:    eventRelocationFailed,
				Address: event.Address(),
				Actors:  event.Actors(),
				Grains:  event.Grains(),
				Error:   fmt.Sprintf("%v", event.Error()),
			})
			logger.Warnf("observed RelocationFailed for %s: %v", event.Address(), event.Error())
		}
	}
}

// add appends an event to the recorder.
func (r *eventRecorder) add(event recordedEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.events = append(r.events, event)
}

// snapshot returns a copy of the recorded events.
func (r *eventRecorder) snapshot() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]recordedEvent, len(r.events))
	copy(out, r.events)
	return out
}
