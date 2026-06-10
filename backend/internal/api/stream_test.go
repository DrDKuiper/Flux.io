package api

import (
	"encoding/json"
	"testing"

	"fluxio-backend/internal/storage"
)

func TestAlertEnvelope(t *testing.T) {
	msg := alertEnvelope(storage.AlertRow{SrcIP: "1.1.1.1", Signature: "ET X", Severity: 2})
	var env struct {
		Type string           `json:"type"`
		Data storage.AlertRow `json:"data"`
	}
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Type != "alert" {
		t.Errorf("expected type alert, got %q", env.Type)
	}
	if env.Data.Signature != "ET X" {
		t.Errorf("alert payload not preserved: %+v", env.Data)
	}
}

func TestMetricsEnvelope(t *testing.T) {
	msg := metricsEnvelope(metricsSnapshot{Overview: storage.Overview{Flows: 5}})
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Type != "metrics" {
		t.Errorf("expected type metrics, got %q", env.Type)
	}
}
