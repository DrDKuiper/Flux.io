package auth

import (
	"testing"
	"time"
)

func TestIssueAndParseToken(t *testing.T) {
	signer := NewJWT("test-secret-key", time.Hour)
	tok, expires, err := signer.Issue("alice")
	if err != nil {
		t.Fatalf("Issue error: %v", err)
	}
	if !expires.After(time.Now()) {
		t.Fatal("expiry should be in the future")
	}
	claims, err := signer.Parse(tok)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if claims.Username != "alice" {
		t.Errorf("expected username alice, got %q", claims.Username)
	}
}

func TestParseRejectsBadTokens(t *testing.T) {
	signer := NewJWT("secret-a", time.Hour)
	if _, err := signer.Parse("not.a.jwt"); err == nil {
		t.Error("malformed token should be rejected")
	}
	other := NewJWT("secret-b", time.Hour)
	tok, _, _ := other.Issue("bob")
	if _, err := signer.Parse(tok); err == nil {
		t.Error("token signed with a different secret should be rejected")
	}
}

func TestParseRejectsExpired(t *testing.T) {
	signer := NewJWT("secret", -time.Minute)
	tok, _, _ := signer.Issue("carol")
	if _, err := signer.Parse(tok); err == nil {
		t.Error("expired token should be rejected")
	}
}
