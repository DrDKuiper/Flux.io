package api

import (
	"testing"
	"time"
)

func TestParseRange(t *testing.T) {
	now := time.Now()
	cases := map[string]time.Duration{
		"15m": 15 * time.Minute,
		"1h":  time.Hour,
		"6h":  6 * time.Hour,
		"24h": 24 * time.Hour,
		"7d":  7 * 24 * time.Hour,
		"":    time.Hour,
	}
	for in, want := range cases {
		since, err := parseRange(in, now)
		if err != nil {
			t.Fatalf("parseRange(%q) error: %v", in, err)
		}
		if got := now.Sub(since); got != want {
			t.Errorf("parseRange(%q) = %v ago, want %v", in, got, want)
		}
	}
	if _, err := parseRange("bogus", now); err == nil {
		t.Error("invalid range should error")
	}
}

func TestClampLimit(t *testing.T) {
	if clampLimit(0) != 50 {
		t.Error("zero should default to 50")
	}
	if clampLimit(1000) != 500 {
		t.Error("over-max should clamp to 500")
	}
	if clampLimit(25) != 25 {
		t.Error("in-range should pass through")
	}
}
