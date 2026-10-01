package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLaunchChannelRecoversPanic(t *testing.T) {
	var out lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mon := NewMonitor()
	launchChannel(ctx, &fakeChannel{
		name: "testchan",
		run: func(ctx context.Context) error {
			panic("channel bug")
		},
	}, testLogger(&out), mon)

	// The panic must be contained: the monitor records it as failed and the
	// log carries the stack trace.
	waitFor(t, "panic report", func() bool {
		for _, rec := range logRecords(t, &out) {
			if rec["msg"] == "channel panicked" && rec["channel"] == "testchan" &&
				strings.Contains(rec["panic"].(string), "channel bug") &&
				strings.Contains(rec["stack"].(string), "goroutine") {
				return true
			}
		}
		return false
	})
	waitFor(t, "failed state", func() bool {
		for _, cs := range mon.Snapshot() {
			if cs.Name == "testchan" && cs.State == ChannelFailed &&
				strings.Contains(cs.Error, "panic: channel bug") && cs.FailedAt != "" {
				return true
			}
		}
		return false
	})
}

func TestMonitorTracksFailedAndStopped(t *testing.T) {
	var out lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mon := NewMonitor()
	launchChannel(ctx, &fakeChannel{
		name: "failer",
		run:  func(ctx context.Context) error { return errors.New("nope") },
	}, testLogger(&out), mon)
	launchChannel(ctx, &fakeChannel{
		name: "quitter",
		run: func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		},
	}, testLogger(&out), mon)

	waitFor(t, "failed state", func() bool {
		for _, cs := range mon.Snapshot() {
			if cs.Name == "failer" && cs.State == ChannelFailed && cs.Error == "nope" {
				return true
			}
		}
		return false
	})
	cancel()
	waitFor(t, "stopped state", func() bool {
		for _, cs := range mon.Snapshot() {
			if cs.Name == "quitter" && cs.State == ChannelStopped {
				return true
			}
		}
		return false
	})
	// A running channel is visible before it finishes.
	mon2 := NewMonitor()
	release := make(chan struct{})
	launchChannel(ctx, &fakeChannel{
		name: "worker",
		run: func(ctx context.Context) error {
			<-release
			return nil
		},
	}, testLogger(&out), mon2)
	waitFor(t, "running state", func() bool {
		snap := mon2.Snapshot()
		return len(snap) == 1 && snap[0].Name == "worker" && snap[0].State == ChannelRunning
	})
	close(release)
}

func TestMonitorSnapshotSorted(t *testing.T) {
	mon := NewMonitor()
	mon.set("zeta", ChannelState{State: ChannelRunning})
	mon.set("alpha", ChannelState{State: ChannelFailed, Err: "x", FailedAt: time.Now()})
	snap := mon.Snapshot()
	if len(snap) != 2 || snap[0].Name != "alpha" || snap[1].Name != "zeta" {
		t.Fatalf("snapshot not sorted: %+v", snap)
	}
}
