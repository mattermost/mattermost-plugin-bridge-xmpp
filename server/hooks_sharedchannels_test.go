package main

import (
	"testing"
	"time"
)

func TestPingFailureGracePeriod(t *testing.T) {
	p := &Plugin{}

	// A reconnect lasts seconds, so consecutive failures inside the window must keep
	// reporting healthy; going offline here is what strands a mapping as a pending
	// invite that is never retried.
	if !p.notePingFailure() {
		t.Fatal("first failure should be inside the grace period")
	}
	if !p.notePingFailure() {
		t.Fatal("second failure should still be inside the grace period")
	}

	// A genuine outage has to surface once the window closes.
	p.pingFailingSince = time.Now().Add(-pingFailureGracePeriod - time.Second)
	if p.notePingFailure() {
		t.Fatal("failure older than the grace period should report unhealthy")
	}

	// Recovering has to reset the streak, or the next blip inherits the old timestamp
	// and reports unhealthy immediately.
	p.notePingSuccess()
	if !p.pingFailingSince.IsZero() {
		t.Fatal("success should clear the failure timestamp")
	}
	if !p.notePingFailure() {
		t.Fatal("failure after a success should start a fresh grace period")
	}
}
