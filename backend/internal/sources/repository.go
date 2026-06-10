package sources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ConfigPatch is a partial update to a source's editable fields. A nil pointer
// means "leave unchanged"; a non-nil pointer (including empty string) sets the value.
type ConfigPatch struct {
	Name         *string
	GroupTag     *string
	Enabled      *bool
	DPIMode      *string
	ExpectedType *string
}

// Validate rejects out-of-range enum values before they reach the database.
func (p ConfigPatch) Validate() error {
	if p.DPIMode != nil && !ValidDPIMode(*p.DPIMode) {
		return fmt.Errorf("invalid dpi_mode %q", *p.DPIMode)
	}
	if p.ExpectedType != nil && *p.ExpectedType != "" && !ValidType(*p.ExpectedType) {
		return fmt.Errorf("invalid expected_type %q", *p.ExpectedType)
	}
	return nil
}

// Repository persists sources in Postgres.
type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

// Upsert inserts a source if (address,type) is new, otherwise refreshes last_seen.
// It returns the resulting row so the caller learns the stored config.
func (r *Repository) Upsert(ctx context.Context, address, typ string, seen time.Time) (Source, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO sources (address, type, first_seen, last_seen)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (address, type) DO UPDATE SET last_seen = EXCLUDED.last_seen
		RETURNING id, address, type, name, group_tag, enabled, dpi_mode, expected_type, first_seen, last_seen`,
		address, typ, seen)
	return scanSource(row)
}

// List returns all sources ordered by group then address.
func (r *Repository) List(ctx context.Context) ([]Source, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, address, type, name, group_tag, enabled, dpi_mode, expected_type, first_seen, last_seen
		FROM sources ORDER BY group_tag, address`)
	if err != nil {
		return nil, fmt.Errorf("sources: list: %w", err)
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Get returns a single source by id, or sql.ErrNoRows if absent.
func (r *Repository) Get(ctx context.Context, id int) (Source, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, address, type, name, group_tag, enabled, dpi_mode, expected_type, first_seen, last_seen
		FROM sources WHERE id = $1`, id)
	return scanSource(row)
}

// UpdateConfig applies a validated partial update and returns the updated row.
func (r *Repository) UpdateConfig(ctx context.Context, id int, p ConfigPatch) (Source, error) {
	if err := p.Validate(); err != nil {
		return Source{}, err
	}
	sets := []string{}
	args := []any{}
	i := 1
	add := func(col string, val any) {
		sets = append(sets, fmt.Sprintf("%s = $%d", col, i))
		args = append(args, val)
		i++
	}
	if p.Name != nil {
		add("name", *p.Name)
	}
	if p.GroupTag != nil {
		add("group_tag", *p.GroupTag)
	}
	if p.Enabled != nil {
		add("enabled", *p.Enabled)
	}
	if p.DPIMode != nil {
		add("dpi_mode", *p.DPIMode)
	}
	if p.ExpectedType != nil {
		add("expected_type", *p.ExpectedType)
	}
	if len(sets) == 0 {
		return r.Get(ctx, id)
	}
	args = append(args, id)
	query := fmt.Sprintf(`UPDATE sources SET %s WHERE id = $%d
		RETURNING id, address, type, name, group_tag, enabled, dpi_mode, expected_type, first_seen, last_seen`,
		strings.Join(sets, ", "), i)
	row := r.db.QueryRowContext(ctx, query, args...)
	return scanSource(row)
}

type scanner interface{ Scan(dest ...any) error }

func scanSource(s scanner) (Source, error) {
	var src Source
	err := s.Scan(&src.ID, &src.Address, &src.Type, &src.Name, &src.GroupTag,
		&src.Enabled, &src.DPIMode, &src.ExpectedType, &src.FirstSeen, &src.LastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, err
	}
	if err != nil {
		return Source{}, fmt.Errorf("sources: scan: %w", err)
	}
	return src, nil
}
