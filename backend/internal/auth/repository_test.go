package auth

import (
	"context"
	"testing"
)

type fakeUserStore struct {
	count   int
	created []string
}

func (f *fakeUserStore) Count(context.Context) (int, error) { return f.count, nil }
func (f *fakeUserStore) Create(_ context.Context, username, hash string) error {
	f.created = append(f.created, username)
	f.count++
	return nil
}

func TestSeedAdminOnlyWhenEmpty(t *testing.T) {
	empty := &fakeUserStore{}
	pw, err := SeedAdmin(context.Background(), empty, "admin", "given-pass")
	if err != nil {
		t.Fatalf("SeedAdmin error: %v", err)
	}
	if pw != "" {
		t.Errorf("a provided password should return empty (nothing to print), got %q", pw)
	}
	if len(empty.created) != 1 || empty.created[0] != "admin" {
		t.Fatalf("expected one admin created, got %v", empty.created)
	}

	full := &fakeUserStore{count: 1}
	if _, err := SeedAdmin(context.Background(), full, "admin", "x"); err != nil {
		t.Fatalf("SeedAdmin error: %v", err)
	}
	if len(full.created) != 0 {
		t.Errorf("should not create a user when the table is non-empty")
	}
}

func TestSeedAdminGeneratesPasswordWhenBlank(t *testing.T) {
	empty := &fakeUserStore{}
	pw, err := SeedAdmin(context.Background(), empty, "admin", "")
	if err != nil {
		t.Fatalf("SeedAdmin error: %v", err)
	}
	if len(pw) < 12 {
		t.Errorf("a generated password should be returned (>=12 chars), got %q", pw)
	}
}
