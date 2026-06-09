package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/settings"
)

// fakeModeStore is a minimal in-memory stand-in for *settings.Repository,
// letting us test the HTTP layer without a database.
type fakeModeStore struct{ mode string }

func (f *fakeModeStore) GetDPIMode(context.Context) (string, error) { return f.mode, nil }
func (f *fakeModeStore) SetDPIMode(_ context.Context, mode string) error {
	valid := map[string]bool{"none": true, "suricata": true, "tzsp": true}
	if !valid[mode] {
		return fmt.Errorf("unknown dpi_mode %q (valid: none, suricata, tzsp)", mode)
	}
	f.mode = mode
	return nil
}

func TestSettingsHandlers_GetAndPut(t *testing.T) {
	store := &fakeModeStore{mode: "none"}
	app := fiber.New()
	registerSettingsRoutes(app.Group("/api"), store)

	// GET returns the current mode
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("GET /api/settings failed: %v", err)
	}
	var got struct {
		DPIMode string `json:"dpi_mode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding GET response: %v", err)
	}
	if got.DPIMode != "none" {
		t.Fatalf("expected dpi_mode %q, got %q", "none", got.DPIMode)
	}

	// PUT updates the mode
	body, _ := json.Marshal(map[string]string{"dpi_mode": "suricata"})
	req = httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("PUT /api/settings failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200 from PUT, got %d", resp.StatusCode)
	}
	if store.mode != "suricata" {
		t.Fatalf("expected store to be updated to %q, got %q", "suricata", store.mode)
	}

	// PUT with an invalid mode is rejected with 400
	body, _ = json.Marshal(map[string]string{"dpi_mode": "bogus"})
	req = httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("PUT /api/settings (invalid) failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400 for invalid dpi_mode, got %d", resp.StatusCode)
	}
}

var _ = sql.ErrNoRows // keep database/sql imported for clarity of intent in this package's tests
var _ = settings.NewRepository
