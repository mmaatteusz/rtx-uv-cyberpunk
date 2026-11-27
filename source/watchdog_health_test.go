package main

import (
	"testing"
	"time"
)

func TestWatchdogMustBeAliveWithFreshPulse(t *testing.T) {
	now := time.Now()
	alive := make(chan struct{})
	if err := checkWatchdogSignal(alive, now.Add(-time.Second), now); err != nil {
		t.Fatal(err)
	}
	for _, stamp := range []time.Time{{}, now.Add(-13 * time.Second), now.Add(time.Minute)} {
		if checkWatchdogSignal(alive, stamp, now) == nil {
			t.Fatalf("accepted invalid heartbeat: %v", stamp)
		}
	}
	close(alive)
	if checkWatchdogSignal(alive, now, now) == nil {
		t.Fatal("allowed further writes after the watchdog exited")
	}
	if checkWatchdogSignal(nil, now, now) == nil {
		t.Fatal("allowed a write without an independent process")
	}
}
