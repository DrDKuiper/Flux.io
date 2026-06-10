package auth

import (
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestMiddlewareAllowsValidRejectsInvalid(t *testing.T) {
	signer := NewJWT("secret", time.Hour)
	app := fiber.New()
	app.Use(Middleware(signer))
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })

	tok, _, _ := signer.Issue("alice")

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("valid token should pass, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("unexpected body %q", body)
	}

	resp, _ = app.Test(httptest.NewRequest("GET", "/x", nil))
	if resp.StatusCode != 401 {
		t.Fatalf("missing token should 401, got %d", resp.StatusCode)
	}

	bad := httptest.NewRequest("GET", "/x", nil)
	bad.Header.Set("Authorization", "Bearer garbage")
	resp, _ = app.Test(bad)
	if resp.StatusCode != 401 {
		t.Fatalf("bad token should 401, got %d", resp.StatusCode)
	}
}
