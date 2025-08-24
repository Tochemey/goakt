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

package breaker

import (
	"fmt"
	"time"
)

// Metrics represents a snapshot of rolling counts and state.
type Metrics struct {
	State       State
	Successes   uint64
	Failures    uint64
	Total       uint64
	FailureRate float64
	Window      time.Duration
	WindowStart time.Time
	WindowEnd   time.Time
	LastFailure time.Time
	LastSuccess time.Time
}

// String returns human-readable metrics for debugging.
func (m Metrics) String() string {
	return fmt.Sprintf("state=%s total=%d success=%d fail=%d failRate=%.2f window=%s",
		m.State, m.Total, m.Successes, m.Failures, m.FailureRate, m.Window)
}
