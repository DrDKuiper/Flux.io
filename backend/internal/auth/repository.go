package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
)

// User is a row of the users table (password hash omitted from any API output).
type User struct {
	ID           int
	Username     string
	PasswordHash string
}

// userStore is the subset of operations SeedAdmin needs; *Repository satisfies it.
type userStore interface {
	Count(ctx context.Context) (int, error)
	Create(ctx context.Context, username, hash string) error
}

// Repository persists users in Postgres.
type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

func (r *Repository) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (r *Repository) Create(ctx context.Context, username, hash string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2)`, username, hash)
	return err
}

// GetByUsername returns the user, or sql.ErrNoRows if absent.
func (r *Repository) GetByUsername(ctx context.Context, username string) (User, error) {
	var u User
	err := r.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash FROM users WHERE username = $1`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: get user: %w", err)
	}
	return u, nil
}

// SeedAdmin creates an initial admin user when the table is empty. If password
// is blank, a random one is generated and returned so the caller can log it
// once. When a user already exists, it returns ("", nil) and does nothing.
func SeedAdmin(ctx context.Context, store userStore, username, password string) (string, error) {
	n, err := store.Count(ctx)
	if err != nil {
		return "", fmt.Errorf("auth: count users: %w", err)
	}
	if n > 0 {
		return "", nil
	}
	generated := ""
	if password == "" {
		password = randomPassword()
		generated = password
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", err
	}
	if err := store.Create(ctx, username, hash); err != nil {
		return "", fmt.Errorf("auth: create admin: %w", err)
	}
	return generated, nil
}

func randomPassword() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
