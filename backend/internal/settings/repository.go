package settings

import (
	"context"
	"database/sql"
	"fmt"
)

// validDPIModes are the only values the system knows how to act on.
// "none" disables DPI entirely; "suricata" correlates with eve.json events;
// "tzsp" captures and parses raw packets via TZSP.
var validDPIModes = map[string]bool{
	"none":     true,
	"suricata": true,
	"tzsp":     true,
}

// Repository persists Flux.io's runtime settings in Postgres.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// GetDPIMode returns the currently configured DPI source. If no row exists
// yet (a fresh database without the seeded default), it returns "none".
func (r *Repository) GetDPIMode(ctx context.Context) (string, error) {
	var mode string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'dpi_mode'`).Scan(&mode)
	if err == sql.ErrNoRows {
		return "none", nil
	}
	if err != nil {
		return "", fmt.Errorf("settings: query dpi_mode: %w", err)
	}
	return mode, nil
}

// SetDPIMode persists the active DPI source. It rejects unknown values so
// the system never ends up in a mode no listener knows how to honor.
func (r *Repository) SetDPIMode(ctx context.Context, mode string) error {
	if !validDPIModes[mode] {
		return fmt.Errorf("settings: unknown dpi_mode %q (valid: none, suricata, tzsp)", mode)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES ('dpi_mode', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, mode)
	if err != nil {
		return fmt.Errorf("settings: persist dpi_mode: %w", err)
	}
	return nil
}
