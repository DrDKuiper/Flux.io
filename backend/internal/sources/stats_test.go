package sources

import "testing"

func TestStatsRateAndTotals(t *testing.T) {
	s := NewStats()
	s.Record("10.0.0.1", 100)
	s.Record("10.0.0.1", 200)
	s.Record("10.0.0.2", 50)

	// Before any Roll, the current second holds the counts.
	if got := s.Snapshot("10.0.0.1"); got.TotalBytes != 300 || got.WindowFlows != 2 {
		t.Fatalf("10.0.0.1 snapshot wrong: %+v", got)
	}

	// Roll moves the current second into the "per-second rate" reading.
	s.Roll()
	if got := s.Snapshot("10.0.0.1"); got.FlowsPerSec != 2 {
		t.Fatalf("expected rate 2 after roll, got %d", got.FlowsPerSec)
	}
	// Totals persist across rolls.
	if got := s.Snapshot("10.0.0.1"); got.TotalBytes != 300 {
		t.Fatalf("totals should persist, got %d", got.TotalBytes)
	}
	// A second roll with no new flows drops the rate to 0.
	s.Roll()
	if got := s.Snapshot("10.0.0.1"); got.FlowsPerSec != 0 {
		t.Fatalf("expected rate 0 after idle roll, got %d", got.FlowsPerSec)
	}
}
