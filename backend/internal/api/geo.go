package api

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/storage"
)

func geoHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		items, err := r.GeoByCountry(c.Context(), since, c.Query("source"))
		if err != nil {
			return serverErr(c)
		}
		if items == nil {
			items = []storage.GeoCount{}
		}
		return c.JSON(items)
	}
}
