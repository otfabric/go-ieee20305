// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"log/slog"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

func TestPollRate(t *testing.T) {
	a := &Agent{}
	if a.pollRate() != 900*time.Second {
		t.Errorf("default %v", a.pollRate())
	}
	rate := ieee20305.UInt32(60)
	a.state.DeviceCapability = &ieee20305.DeviceCapability{}
	a.state.DeviceCapability.PollRate = &rate
	if a.pollRate() != time.Minute {
		t.Errorf("from DeviceCapability %v", a.pollRate())
	}
	a.cfg.PollRate = time.Second
	if a.pollRate() != time.Second {
		t.Errorf("configured %v", a.pollRate())
	}
}

func TestDiscardHandler(t *testing.T) {
	l := slog.New(discardHandler{}).WithGroup("g").With("k", "v")
	l.Info("dropped")
	if l.Enabled(context.Background(), slog.LevelError) {
		t.Error("enabled")
	}
	if err := (discardHandler{}).Handle(context.Background(), slog.Record{}); err != nil {
		t.Error(err)
	}
}
