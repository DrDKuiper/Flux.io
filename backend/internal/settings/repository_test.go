package settings

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping Postgres integration test")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("failed to ensure settings table: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM settings`); err != nil {
		t.Fatalf("failed to reset settings table: %v", err)
	}
	return db
}

func TestRepository_DefaultsToNoneThenPersistsUpdates(t *testing.T) {
	db := openTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	mode, err := repo.GetDPIMode(ctx)
	if err != nil {
		t.Fatalf("GetDPIMode returned error: %v", err)
	}
	if mode != "none" {
		t.Fatalf("expected default mode %q, got %q", "none", mode)
	}

	if err := repo.SetDPIMode(ctx, "suricata"); err != nil {
		t.Fatalf("SetDPIMode returned error: %v", err)
	}

	mode, err = repo.GetDPIMode(ctx)
	if err != nil {
		t.Fatalf("GetDPIMode after update returned error: %v", err)
	}
	if mode != "suricata" {
		t.Fatalf("expected updated mode %q, got %q", "suricata", mode)
	}
}

func TestRepository_RejectsUnknownMode(t *testing.T) {
	db := openTestDB(t)
	repo := NewRepository(db)

	err := repo.SetDPIMode(context.Background(), "bogus")
	if err == nil {
		t.Fatal("expected SetDPIMode to reject an unknown mode, got nil error")
	}
}
