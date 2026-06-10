package api

import (
	"database/sql"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/sources"
)

// silentAfter is how long without data before an enabled source is "silent".
const silentAfter = 5 * time.Minute

// sourceView is the API representation of a source: its stored config plus
// derived live status, rate, and mismatch flag.
type sourceView struct {
	sources.Source
	Status      string `json:"status"`
	Mismatch    bool   `json:"mismatch"`
	FlowsPerSec uint64 `json:"flows_per_sec"`
	TotalBytes  uint64 `json:"total_bytes"`
}

func buildSourceView(s sources.Source, stats *sources.Stats) sourceView {
	snap := stats.Snapshot(s.Address)
	status := "active"
	switch {
	case !s.Enabled:
		status = "disabled"
	case time.Since(s.LastSeen) > silentAfter:
		status = "silent"
	}
	return sourceView{
		Source:      s,
		Status:      status,
		Mismatch:    s.ExpectedType != "" && s.ExpectedType != s.Type,
		FlowsPerSec: snap.FlowsPerSec,
		TotalBytes:  snap.TotalBytes,
	}
}

func listSourcesHandler(repo *sources.Repository, stats *sources.Stats) fiber.Handler {
	return func(c *fiber.Ctx) error {
		list, err := repo.List(c.Context())
		if err != nil {
			return serverErr(c)
		}
		views := make([]sourceView, 0, len(list))
		for _, s := range list {
			views = append(views, buildSourceView(s, stats))
		}
		return c.JSON(views)
	}
}

func getSourceHandler(repo *sources.Repository, stats *sources.Stats) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := c.ParamsInt("id")
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
		}
		s, err := repo.Get(c.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "source not found"})
		}
		if err != nil {
			return serverErr(c)
		}
		return c.JSON(buildSourceView(s, stats))
	}
}

func patchSourceHandler(reg *sources.Registry, repo *sources.Repository, stats *sources.Stats) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := c.ParamsInt("id")
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
		}
		var patch sources.ConfigPatch
		if err := c.BodyParser(&patch); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
		}
		s, err := repo.UpdateConfig(c.Context(), id, patch)
		if errors.Is(err, sql.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "source not found"})
		}
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		reg.Refresh(s) // keep the hot-path cache in sync immediately
		return c.JSON(buildSourceView(s, stats))
	}
}
