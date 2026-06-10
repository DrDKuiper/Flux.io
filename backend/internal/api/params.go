// Package api exposes the authenticated REST + WebSocket surface over the
// collected flow/alert data and the source registry.
package api

import (
	"fmt"
	"time"
)

var rangeDurations = map[string]time.Duration{
	"15m": 15 * time.Minute,
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// parseRange maps a range token to the "since" timestamp relative to now.
// An empty token defaults to 1h. Unknown tokens are an error.
func parseRange(token string, now time.Time) (time.Time, error) {
	if token == "" {
		token = "1h"
	}
	d, ok := rangeDurations[token]
	if !ok {
		return time.Time{}, fmt.Errorf("invalid range %q (valid: 15m, 1h, 6h, 24h, 7d)", token)
	}
	return now.Add(-d), nil
}

// clampLimit defaults to 50 and caps at 500.
func clampLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > 500 {
		return 500
	}
	return n
}
