/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package ticker

import (
	"sync"
	"time"
)

// Ticker defines time ticker that delivers ticks at intervals
type Ticker struct {
	Ticks     chan time.Time
	intervals time.Duration
	mutex     sync.Mutex
	ticking   bool
	stopCh    chan bool
}

// New creates an instance of Ticker that ticks every intervals.
// It includes some kind of back-pressure for slow receivers
func New(intervals time.Duration) *Ticker {
	if intervals <= 0 {
		panic("intervals must be greater than zero")
	}
	return &Ticker{
		Ticks:     make(chan time.Time),
		intervals: intervals,
		stopCh:    make(chan bool),
		ticking:   false,
	}
}

// Start the ticker. Ticks are delivered on the ticker's
// channel until Stop is called
func (t *Ticker) Start() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if !t.ticking {
		go t.tickingLoop()
		t.ticking = true
	}
}

// Stop stops the ticker. No ticks will be delivered on ticker's channel
// after Stop returns and before Start is call again
func (t *Ticker) Stop() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.ticking {
		t.ticking = false
		t.stopCh <- true
	}
}

// Ticking returns true when the ticker is ticking
// and false when it is stopped
func (t *Ticker) Ticking() bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.ticking
}

func (t *Ticker) tickingLoop() {
	ticker := time.NewTicker(t.intervals)
	for {
		select {
		case tc := <-ticker.C:
			select {
			case t.Ticks <- tc:
			default:
			}
		case <-t.stopCh:
			ticker.Stop()
			return
		}
	}
}
