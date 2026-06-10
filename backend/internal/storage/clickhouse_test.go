package storage

import (
	"strings"
	"testing"
)

func TestFlowInsertIncludesSource(t *testing.T) {
	if !strings.Contains(flowInsertSQL, "source") {
		t.Fatal("flow INSERT must include the source column")
	}
	// column count in the list must match the number of Append args (24).
	cols := strings.Count(flowInsertSQL, ",") + 1
	if cols != 24 {
		t.Fatalf("expected 24 columns in flow INSERT, got %d", cols)
	}
}

func TestAlertInsertIncludesSource(t *testing.T) {
	if !strings.Contains(alertInsertSQL, "source") {
		t.Fatal("alert INSERT must include the source column")
	}
	cols := strings.Count(alertInsertSQL, ",") + 1
	if cols != 15 {
		t.Fatalf("expected 15 columns in alert INSERT, got %d", cols)
	}
}
