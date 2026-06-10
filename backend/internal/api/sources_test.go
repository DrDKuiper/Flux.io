package api

import (
	"testing"
	"time"

	"fluxio-backend/internal/sources"
)

func TestSourceView_StatusAndMismatch(t *testing.T) {
	now := time.Now()
	stats := sources.NewStats()
	stats.Record("10.0.0.1", 500)
	stats.Roll() // 1 flow/s

	active := sources.Source{Address: "10.0.0.1", Type: "netflow", Enabled: true, LastSeen: now}
	v := buildSourceView(active, stats)
	if v.Status != "active" {
		t.Errorf("expected active, got %q", v.Status)
	}
	if v.FlowsPerSec != 1 {
		t.Errorf("expected 1 flow/s, got %d", v.FlowsPerSec)
	}

	silent := sources.Source{Address: "x", Type: "netflow", Enabled: true, LastSeen: now.Add(-10 * time.Minute)}
	if buildSourceView(silent, stats).Status != "silent" {
		t.Errorf("stale source should be silent")
	}

	disabled := sources.Source{Address: "y", Type: "netflow", Enabled: false, LastSeen: now}
	if buildSourceView(disabled, stats).Status != "disabled" {
		t.Errorf("disabled source should report disabled")
	}

	mm := sources.Source{Address: "z", Type: "tzsp", ExpectedType: "netflow", Enabled: true, LastSeen: now}
	if !buildSourceView(mm, stats).Mismatch {
		t.Errorf("expected_type != type should set mismatch")
	}
}
