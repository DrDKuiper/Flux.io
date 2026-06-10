package api

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/storage"
)

func flowsHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		f := storage.FlowFilter{
			Since:   since,
			Source:  c.Query("source"),
			SrcIP:   c.Query("src_ip"),
			DstIP:   c.Query("dst_ip"),
			App:     c.Query("app"),
			Country: c.Query("country"),
			Port:    uint16(c.QueryInt("port", 0)),
			Limit:   clampLimit(c.QueryInt("limit", 50)),
			Offset:  c.QueryInt("offset", 0),
		}
		total, items, err := r.FlowsFiltered(c.Context(), f)
		if err != nil {
			return serverErr(c)
		}
		if items == nil {
			items = []storage.FlowRow{}
		}
		return c.JSON(fiber.Map{"total": total, "items": items})
	}
}
