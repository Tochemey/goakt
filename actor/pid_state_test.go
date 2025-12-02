package actor

import "testing"

func TestPIDStateStoreAndEnabled(t *testing.T) {
	pid := &PID{}

	if pid.isStateSet(runningState) {
		t.Fatalf("expected running flag to be false by default")
	}

	pid.flipState(runningState, true)
	if !pid.isStateSet(runningState) {
		t.Fatalf("expected running flag to be true after store")
	}

	pid.flipState(runningState, false)
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
