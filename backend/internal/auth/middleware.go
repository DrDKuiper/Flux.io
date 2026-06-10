package auth

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Middleware rejects requests without a valid "Authorization: Bearer <jwt>".
// On success it stores the username in c.Locals("username").
func Middleware(signer *JWT) fiber.Handler {
	return func(c *fiber.Ctx) error {
		header := c.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing bearer token"})
		}
		claims, err := signer.Parse(strings.TrimPrefix(header, prefix))
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid or expired token"})
		}
		c.Locals("username", claims.Username)
		return c.Next()
	}
}

// ValidateToken reports whether a raw token string is valid. Used by the
// WebSocket handshake, where the token arrives as a query parameter.
func ValidateToken(signer *JWT, token string) bool {
	_, err := signer.Parse(token)
	return err == nil
}
