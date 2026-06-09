package main

import (
	"context"

	"github.com/gofiber/fiber/v2"
)

// modeStore is the minimal interface the settings routes need — satisfied by
// *settings.Repository in production and by a fake in tests.
type modeStore interface {
	GetDPIMode(ctx context.Context) (string, error)
	SetDPIMode(ctx context.Context, mode string) error
}

// modeSwitcher is satisfied by *collector.DPIManager — separated as an
// interface so the route can be tested without starting real listeners.
type modeSwitcher interface {
	SetMode(ctx context.Context, mode string) error
}

// registerSettingsRoutes wires GET/PUT /settings onto the given router group
// (mounted at /api by the caller, so the final paths are /api/settings).
func registerSettingsRoutes(router fiber.Router, store modeStore, switcher modeSwitcher) {
	router.Get("/settings", func(c *fiber.Ctx) error {
		mode, err := store.GetDPIMode(c.Context())
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to read settings"})
		}
		return c.JSON(fiber.Map{"dpi_mode": mode})
	})

	router.Put("/settings", func(c *fiber.Ctx) error {
		var body struct {
			DPIMode string `json:"dpi_mode"`
		}
		if err := c.BodyParser(&body); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
		}
		if err := store.SetDPIMode(c.Context(), body.DPIMode); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err := switcher.SetMode(c.Context(), body.DPIMode); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "saved, but failed to switch the live listener: " + err.Error()})
		}
		return c.JSON(fiber.Map{"dpi_mode": body.DPIMode})
	})
}
