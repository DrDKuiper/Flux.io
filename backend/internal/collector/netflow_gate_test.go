package collector

import (
	"testing"

	"fluxio-backend/internal/processor"
)

func TestApplyGateStampsAndDrops(t *testing.T) {
	rec := processor.FlowRecord{SourceIP: "1.2.3.4"}

	// Enabled gate stamps the exporter address and keeps the record.
	out, keep := applyGate("10.0.0.1", rec, func(addr string) (bool, bool) {
		return true, true
	})
	if !keep {
		t.Fatal("enabled source should be kept")
	}
	if out.Source != "10.0.0.1" {
		t.Fatalf("expected stamped source 10.0.0.1, got %q", out.Source)
	}

	// Disabled gate drops the record.
	_, keep = applyGate("10.0.0.9", rec, func(addr string) (bool, bool) {
		return false, false
	})
	if keep {
		t.Fatal("disabled source should be dropped")
	}
}
