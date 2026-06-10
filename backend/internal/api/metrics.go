package api

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/storage"
)

func badRange(c *fiber.Ctx, err error) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
}

func serverErr(c *fiber.Ctx) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "query failed"})
}

func overviewHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		o, err := r.Overview(c.Context(), since, c.Query("source"))
		if err != nil {
			return serverErr(c)
		}
		return c.JSON(o)
	}
}

func topTalkersHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		limit := clampLimit(c.QueryInt("limit", 10))
		items, err := r.TopTalkers(c.Context(), since, c.Query("source"), limit)
		if err != nil {
			return serverErr(c)
		}
		if items == nil {
			items = []storage.Talker{}
		}
		return c.JSON(items)
	}
}

func topAppsHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		limit := clampLimit(c.QueryInt("limit", 10))
		items, err := r.TopApps(c.Context(), since, c.Query("source"), limit)
		if err != nil {
			return serverErr(c)
		}
		if items == nil {
			items = []storage.AppCount{}
		}
		return c.JSON(items)
	}
}

func throughputHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		buckets := c.QueryInt("buckets", 60)
		items, err := r.Throughput(c.Context(), since, c.Query("source"), buckets)
		if err != nil {
			return serverErr(c)
		}
		if items == nil {
			items = []storage.ThroughputPoint{}
		}
		return c.JSON(items)
	}
}
