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

type filterCapturingReader struct {
	fakeReader
	gotFilter storage.FlowFilter
}

func (f *filterCapturingReader) FlowsFiltered(_ context.Context, flt storage.FlowFilter) (uint64, []storage.FlowRow, error) {
	f.gotFilter = flt
	return 1, []storage.FlowRow{{SrcIP: "10.0.0.1"}}, nil
}

func TestFlowsHandlerParsesFilters(t *testing.T) {
	fr := &filterCapturingReader{}
	app := fiber.New()
	app.Get("/api/flows", flowsHandler(fr))

	resp, _ := app.Test(httptest.NewRequest("GET",
		"/api/flows?range=1h&src_ip=10.0.0.1&port=443&app=tls&country=US&limit=20&offset=40", nil))
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out struct {
		Total uint64            `json:"total"`
		Items []storage.FlowRow `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Total != 1 || len(out.Items) != 1 {
		t.Fatalf("unexpected payload: %+v", out)
	}
	f := fr.gotFilter
	if f.SrcIP != "10.0.0.1" || f.Port != 443 || f.App != "tls" || f.Country != "US" {
		t.Errorf("filters not parsed: %+v", f)
	}
	if f.Limit != 20 || f.Offset != 40 {
		t.Errorf("pagination not parsed: limit=%d offset=%d", f.Limit, f.Offset)
	}
	if time.Since(f.Since) < 59*time.Minute {
		t.Errorf("since should be ~1h ago, got %v", f.Since)
	}
}
