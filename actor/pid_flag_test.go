package actor

import "testing"

func TestPIDFlagStoreAndEnabled(t *testing.T) {
	pid := &PID{}

	if pid.isFlagEnabled(runningFlag) {
		t.Fatalf("expected running flag to be false by default")
	}

	pid.toggleFlag(runningFlag, true)
	if !pid.isFlagEnabled(runningFlag) {
		t.Fatalf("expected running flag to be true after store")
	}

	pid.toggleFlag(runningFlag, false)
	if pid.isFlagEnabled(runningFlag) {
		t.Fatalf("expected running flag to be false after clearing")
	}
}

func TestPIDFlagCompareAndSwap(t *testing.T) {
	pid := &PID{}

	if swapped := pid.compareAndSwapFlag(stoppingFlag, false, true); !swapped {
		t.Fatalf("expected compareAndSwap to set flag from false to true")
	}
	if !pid.isFlagEnabled(stoppingFlag) {
		t.Fatalf("expected stopping flag to be true after compareAndSwap")
	}

	if swapped := pid.compareAndSwapFlag(stoppingFlag, false, true); swapped {
		t.Fatalf("expected compareAndSwap to fail when old state does not match")
	}

	if swapped := pid.compareAndSwapFlag(stoppingFlag, true, false); !swapped {
		t.Fatalf("expected compareAndSwap to clear flag from true to false")
	}
	if pid.isFlagEnabled(stoppingFlag) {
		t.Fatalf("expected stopping flag to be false after clearing")
	}
}
