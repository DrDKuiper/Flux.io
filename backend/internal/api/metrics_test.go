package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/storage"
)

// fakeReader implements storage.Reader with canned data and records the last
// source filter it was called with.
type fakeReader struct {
	lastSource string
	overview   storage.Overview
}

func (f *fakeReader) Overview(_ context.Context, _ time.Time, source string) (storage.Overview, error) {
	f.lastSource = source
	return f.overview, nil
}
func (f *fakeReader) TopTalkers(context.Context, time.Time, string, int) ([]storage.Talker, error) {
	return []storage.Talker{{IP: "10.0.0.1", Bytes: 100}}, nil
}
func (f *fakeReader) TopApps(context.Context, time.Time, string, int) ([]storage.AppCount, error) {
	return nil, nil
}
func (f *fakeReader) Throughput(context.Context, time.Time, string, int) ([]storage.ThroughputPoint, error) {
	return nil, nil
}
func (f *fakeReader) GeoByCountry(context.Context, time.Time, string) ([]storage.GeoCount, error) {
	return nil, nil
}
func (f *fakeReader) FlowsFiltered(context.Context, storage.FlowFilter) (uint64, []storage.FlowRow, error) {
	return 0, nil, nil
}
func (f *fakeReader) AlertsHistory(context.Context, time.Time, string, int, int) (uint64, []storage.AlertRow, error) {
	return 0, nil, nil
}

func TestOverviewHandler(t *testing.T) {
	fr := &fakeReader{overview: storage.Overview{Flows: 42, Bytes: 1000}}
	app := fiber.New()
	app.Get("/api/metrics/overview", overviewHandler(fr))

	resp, _ := app.Test(httptest.NewRequest("GET", "/api/metrics/overview?range=24h&source=10.0.0.1", nil))
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var got storage.Overview
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Flows != 42 {
		t.Errorf("expected 42 flows, got %d", got.Flows)
	}
	if fr.lastSource != "10.0.0.1" {
		t.Errorf("expected source filter to be passed through, got %q", fr.lastSource)
	}
}

func TestOverviewHandlerRejectsBadRange(t *testing.T) {
	app := fiber.New()
	app.Get("/api/metrics/overview", overviewHandler(&fakeReader{}))
	resp, _ := app.Test(httptest.NewRequest("GET", "/api/metrics/overview?range=bogus", nil))
	if resp.StatusCode != 400 {
		t.Fatalf("bad range should 400, got %d", resp.StatusCode)
	}
}
