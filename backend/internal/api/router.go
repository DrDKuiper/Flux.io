package api

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"

	"fluxio-backend/internal/auth"
	"fluxio-backend/internal/sources"
	"fluxio-backend/internal/storage"
)

// Deps bundles everything the routes need.
type Deps struct {
	Reader      storage.Reader
	Signer      *auth.JWT
	UserRepo    *auth.Repository
	Hub         *Hub
	SourceReg   *sources.Registry
	SourceRepo  *sources.Repository
	SourceStats *sources.Stats
}

// RegisterRoutes mounts the auth, read, source, and WebSocket routes on app.
// Public: GET /api/health, POST /api/auth/login, GET /ws (token-gated).
// Everything else under /api requires a valid JWT.
func RegisterRoutes(app *fiber.App, d Deps) {
	app.Get("/api/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	app.Post("/api/auth/login", loginHandler(d.UserRepo, d.Signer))

	// WebSocket: validate the token at the upgrade gate (query param), then upgrade.
	app.Use("/ws", func(c *fiber.Ctx) error {
		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}
		if !auth.ValidateToken(d.Signer, c.Query("token")) {
			return c.Status(fiber.StatusUnauthorized).SendString("invalid token")
		}
		return c.Next()
	})
	app.Get("/ws", streamHandler(d.Hub))

	// Authenticated API group.
	api := app.Group("/api", auth.Middleware(d.Signer))

	api.Get("/metrics/overview", overviewHandler(d.Reader))
	api.Get("/metrics/top-talkers", topTalkersHandler(d.Reader))
	api.Get("/metrics/top-apps", topAppsHandler(d.Reader))
	api.Get("/metrics/throughput", throughputHandler(d.Reader))
	api.Get("/geo/flows", geoHandler(d.Reader))
	api.Get("/alerts", alertsHandler(d.Reader))
	api.Get("/flows", flowsHandler(d.Reader))

	api.Get("/sources", listSourcesHandler(d.SourceRepo, d.SourceStats))
	api.Get("/sources/:id", getSourceHandler(d.SourceRepo, d.SourceStats))
	api.Patch("/sources/:id", patchSourceHandler(d.SourceReg, d.SourceRepo, d.SourceStats))
}

// loginHandler validates credentials against the user repo and issues a JWT.
func loginHandler(repo *auth.Repository, signer *auth.JWT) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.BodyParser(&body); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
		}
		u, err := repo.GetByUsername(c.Context(), body.Username)
		if err != nil || !auth.CheckPassword(u.PasswordHash, body.Password) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
		}
		tok, expires, err := signer.Issue(u.Username)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "could not issue token"})
		}
		return c.JSON(fiber.Map{"token": tok, "expires_at": expires})
	}
}
