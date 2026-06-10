package api

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/storage"
)

func alertsHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		limit := clampLimit(c.QueryInt("limit", 50))
		offset := c.QueryInt("offset", 0)
		total, items, err := r.AlertsHistory(c.Context(), since, c.Query("source"), limit, offset)
		if err != nil {
			return serverErr(c)
		}
		if items == nil {
			items = []storage.AlertRow{}
		}
		return c.JSON(fiber.Map{"total": total, "items": items})
	}
}
