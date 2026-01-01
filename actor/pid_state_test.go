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

import "testing"

func TestPIDStateStoreAndEnabled(t *testing.T) {
	pid := &PID{}

	if pid.isStateSet(runningState) {
		t.Fatalf("expected running flag to be false by default")
	}

	pid.setState(runningState, true)
	if !pid.isStateSet(runningState) {
		t.Fatalf("expected running flag to be true after store")
	}

	pid.setState(runningState, false)
	if pid.isStateSet(runningState) {
		t.Fatalf("expected running flag to be false after clearing")
	}
}

func TestPIDFlagCompareAndSwap(t *testing.T) {
	pid := &PID{}

	if swapped := pid.compareAndSwapState(stoppingState, false, true); !swapped {
		t.Fatalf("expected compareAndSwap to set flag from false to true")
	}
	if !pid.isStateSet(stoppingState) {
		t.Fatalf("expected stopping flag to be true after compareAndSwap")
	}

	if swapped := pid.compareAndSwapState(stoppingState, false, true); swapped {
		t.Fatalf("expected compareAndSwap to fail when old state does not match")
	}

	if swapped := pid.compareAndSwapState(stoppingState, true, false); !swapped {
		t.Fatalf("expected compareAndSwap to clear flag from true to false")
	}
	if pid.isStateSet(stoppingState) {
		t.Fatalf("expected stopping flag to be false after clearing")
	}
}
