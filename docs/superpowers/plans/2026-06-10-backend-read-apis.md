# Backend Read APIs (B2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the collected data through an authenticated REST + WebSocket API — real JWT auth backed by Postgres users, dashboard/geo/alerts/flows read endpoints (filterable by source), source management endpoints, and a single WebSocket that pushes live metrics + alerts.

**Architecture:** Query-on-demand REST handlers read ClickHouse via a `storage.Reader` interface; a WebSocket hub fans out two message types — periodic `metrics` snapshots from a 5s broadcaster goroutine, and `alert` messages bridged from the Suricata correlator. JWT middleware guards `/api/*`; the WebSocket validates its token at the handshake.

**Tech Stack:** Go 1.22, Fiber v2, `gofiber/websocket/v2`, ClickHouse `clickhouse-go/v2`, Postgres `lib/pq`, `github.com/golang-jwt/jwt/v5`, `golang.org/x/crypto/bcrypt`.

**Spec:** `docs/superpowers/specs/2026-06-10-backend-read-apis-design.md`
**Depends on:** B1 (sources). Uses `sources.Registry` (`List`, `Get`, `UpdateConfig`, `Refresh`) and `sources.Stats` (`Snapshot`).

**Verification note:** No local Go toolchain. `go test` runs on the server or in the backend Docker build. The two new deps are resolved by the Dockerfile's `GOFLAGS=-mod=mod`. Do NOT hand-edit `go.sum`. SQL query methods can only be verified end-to-end (Task 14); handler/auth/hub logic is unit-tested against fakes.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `db/postgres/init-db.sql` | Add `users` table (modify). |
| `backend/internal/auth/password.go` | bcrypt hash/verify (create). |
| `backend/internal/auth/jwt.go` | JWT issue/parse, HS256 (create). |
| `backend/internal/auth/repository.go` | Postgres user repo + admin seed (create). |
| `backend/internal/auth/middleware.go` | Fiber JWT middleware + token validator for WS (create). |
| `backend/internal/storage/reader.go` | `Reader` interface + read DTOs (create). |
| `backend/internal/storage/queries.go` | `ClickHouseStore` read-query methods (create). |
| `backend/internal/api/params.go` | `range`→since parsing, limit/offset/source parsing (create). |
| `backend/internal/api/metrics.go` | overview/top-talkers/top-apps/throughput handlers (create). |
| `backend/internal/api/geo.go` | geo handler (create). |
| `backend/internal/api/alerts.go` | alert history handler (create). |
| `backend/internal/api/flows.go` | flow explorer handler (create). |
| `backend/internal/api/sources.go` | source list/detail/patch handlers (create). |
| `backend/internal/api/hub.go` | WebSocket client hub (create). |
| `backend/internal/api/stream.go` | `/ws` handler + metrics broadcaster (create). |
| `backend/internal/api/router.go` | `RegisterRoutes` — mounts everything, CORS, auth (create). |
| `backend/cmd/server/main.go` | Wire auth, hub, broadcaster, alert bridge; remove stubs (modify). |
| `backend/internal/collector/suricata_correlator.go` | Optional alert-bridge callback (modify). |

---

## Task 1: users table + auth password hashing

**Files:**
- Modify: `db/postgres/init-db.sql`
- Create: `backend/internal/auth/password.go`, `backend/internal/auth/password_test.go`

- [ ] **Step 1: Add the `users` table**

Append to `db/postgres/init-db.sql`:

```sql
CREATE TABLE IF NOT EXISTS users (
    id            SERIAL PRIMARY KEY,
    username      TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- [ ] **Step 2: Write the failing test**

Create `backend/internal/auth/password_test.go`:

```go
package auth

import "testing"

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("s3cret-pass")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if hash == "s3cret-pass" {
		t.Fatal("hash must not equal the plaintext")
	}
	if !CheckPassword(hash, "s3cret-pass") {
		t.Error("CheckPassword should accept the correct password")
	}
	if CheckPassword(hash, "wrong") {
		t.Error("CheckPassword should reject an incorrect password")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && go test ./internal/auth/ -run TestHashAndCheck -v`
Expected: FAIL (undefined: HashPassword).

- [ ] **Step 4: Implement**

Create `backend/internal/auth/password.go`:

```go
// Package auth provides JWT authentication backed by Postgres-stored users.
package auth

import "golang.org/x/crypto/bcrypt"

// HashPassword returns a bcrypt hash of the plaintext password.
func HashPassword(plaintext string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword reports whether plaintext matches the bcrypt hash.
func CheckPassword(hash, plaintext string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)) == nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./internal/auth/ -run TestHashAndCheck -v`
Expected: PASS. (In Docker the dep is fetched via `-mod=mod`; locally run `go get golang.org/x/crypto/bcrypt` first if needed.)

- [ ] **Step 6: Commit**

```bash
git add db/postgres/init-db.sql backend/internal/auth/password.go backend/internal/auth/password_test.go
git commit -m "feat(auth): add users table and bcrypt password hashing"
```

---

## Task 2: JWT issue/parse

**Files:**
- Create: `backend/internal/auth/jwt.go`, `backend/internal/auth/jwt_test.go`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/auth/jwt_test.go`:

```go
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
	// Token signed with a different secret must be rejected.
	other := NewJWT("secret-b", time.Hour)
	tok, _, _ := other.Issue("bob")
	if _, err := signer.Parse(tok); err == nil {
		t.Error("token signed with a different secret should be rejected")
	}
}

func TestParseRejectsExpired(t *testing.T) {
	signer := NewJWT("secret", -time.Minute) // already expired
	tok, _, _ := signer.Issue("carol")
	if _, err := signer.Parse(tok); err == nil {
		t.Error("expired token should be rejected")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/auth/ -run TestIssue -v`
Expected: FAIL (undefined: NewJWT).

- [ ] **Step 3: Implement**

Create `backend/internal/auth/jwt.go`:

```go
package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload Flux.io issues.
type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// JWT issues and parses HS256 tokens with a fixed signing secret and TTL.
type JWT struct {
	secret []byte
	ttl    time.Duration
}

func NewJWT(secret string, ttl time.Duration) *JWT {
	return &JWT{secret: []byte(secret), ttl: ttl}
}

// Issue returns a signed token for username and its expiry time.
func (j *JWT) Issue(username string) (string, time.Time, error) {
	expires := time.Now().Add(j.ttl)
	claims := Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expires),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(j.secret)
	return signed, expires, err
}

// Parse validates a token and returns its claims, or an error if invalid/expired.
func (j *JWT) Parse(tokenString string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return j.secret, nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/auth/ -run "TestIssue|TestParse" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/jwt.go backend/internal/auth/jwt_test.go
git commit -m "feat(auth): add HS256 JWT issue/parse"
```

---

## Task 3: user repository + admin seed

**Files:**
- Create: `backend/internal/auth/repository.go`, `backend/internal/auth/repository_test.go`

The SQL methods need a live DB; unit-test the seed *decision* logic (seed only when empty, generate password when blank) against a fake.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/auth/repository_test.go`:

```go
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
	// Empty store → seeds one admin.
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

	// Non-empty store → seeds nothing.
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/auth/ -run TestSeed -v`
Expected: FAIL (undefined: SeedAdmin).

- [ ] **Step 3: Implement**

Create `backend/internal/auth/repository.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/auth/ -run TestSeed -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/repository.go backend/internal/auth/repository_test.go
git commit -m "feat(auth): add user repository and admin seed"
```

---

## Task 4: JWT middleware + WS token validator

**Files:**
- Create: `backend/internal/auth/middleware.go`, `backend/internal/auth/middleware_test.go`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/auth/middleware_test.go`:

```go
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

	// valid
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

	// missing
	resp, _ = app.Test(httptest.NewRequest("GET", "/x", nil))
	if resp.StatusCode != 401 {
		t.Fatalf("missing token should 401, got %d", resp.StatusCode)
	}

	// garbage
	bad := httptest.NewRequest("GET", "/x", nil)
	bad.Header.Set("Authorization", "Bearer garbage")
	resp, _ = app.Test(bad)
	if resp.StatusCode != 401 {
		t.Fatalf("bad token should 401, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/auth/ -run TestMiddleware -v`
Expected: FAIL (undefined: Middleware).

- [ ] **Step 3: Implement**

Create `backend/internal/auth/middleware.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/auth/ -v`
Expected: PASS (all auth tests).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/middleware.go backend/internal/auth/middleware_test.go
git commit -m "feat(auth): add JWT Fiber middleware and WS token validator"
```

---

## Task 5: storage Reader interface + DTOs

**Files:**
- Create: `backend/internal/storage/reader.go`

No test in this task — these are type/interface declarations consumed (and faked) by the api package tests in later tasks.

- [ ] **Step 1: Create the interface and DTOs**

Create `backend/internal/storage/reader.go`:

```go
package storage

import (
	"context"
	"time"
)

// Overview is the dashboard's headline totals.
type Overview struct {
	Flows        uint64 `json:"flows"`
	Bytes        uint64 `json:"bytes"`
	Packets      uint64 `json:"packets"`
	ActiveAlerts uint64 `json:"active_alerts"`
}

type Talker struct {
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
	Bytes    uint64 `json:"bytes"`
	Packets  uint64 `json:"packets"`
	Flows    uint64 `json:"flows"`
}

type AppCount struct {
	Application string `json:"application_id"`
	Bytes       uint64 `json:"bytes"`
	Flows       uint64 `json:"flows"`
}

type ThroughputPoint struct {
	TS      time.Time `json:"ts"`
	Bytes   uint64    `json:"bytes"`
	Packets uint64    `json:"packets"`
}

type GeoCount struct {
	Country string `json:"country"`
	Bytes   uint64 `json:"bytes"`
	Flows   uint64 `json:"flows"`
}

type FlowRow struct {
	TS          time.Time `json:"ts"`
	Source      string    `json:"source"`
	SrcIP       string    `json:"src_ip"`
	DstIP       string    `json:"dst_ip"`
	SrcPort     uint16    `json:"src_port"`
	DstPort     uint16    `json:"dst_port"`
	Protocol    uint8     `json:"protocol"`
	Bytes       uint64    `json:"bytes"`
	Packets     uint64    `json:"packets"`
	Application string    `json:"application_id"`
	SNI         string    `json:"sni"`
	HTTPHost    string    `json:"http_host"`
	SrcCountry  string    `json:"src_country"`
	DstCountry  string    `json:"dst_country"`
	SrcASNOrg   string    `json:"src_asn_org"`
	DstASNOrg   string    `json:"dst_asn_org"`
}

type AlertRow struct {
	TS        time.Time `json:"ts"`
	Source    string    `json:"source"`
	SrcIP     string    `json:"src_ip"`
	DstIP     string    `json:"dst_ip"`
	Signature string    `json:"signature"`
	Category  string    `json:"category"`
	Severity  uint8     `json:"severity"`
}

// FlowFilter holds the optional filters for the flow explorer. Zero-valued
// fields mean "no filter on that dimension".
type FlowFilter struct {
	Since   time.Time
	Source  string
	SrcIP   string
	DstIP   string
	App     string
	Country string
	Port    uint16
	Limit   int
	Offset  int
}

// Reader is the read side of the data store, consumed by the api package.
// *ClickHouseStore implements it; api tests use a fake.
type Reader interface {
	Overview(ctx context.Context, since time.Time, source string) (Overview, error)
	TopTalkers(ctx context.Context, since time.Time, source string, limit int) ([]Talker, error)
	TopApps(ctx context.Context, since time.Time, source string, limit int) ([]AppCount, error)
	Throughput(ctx context.Context, since time.Time, source string, buckets int) ([]ThroughputPoint, error)
	GeoByCountry(ctx context.Context, since time.Time, source string) ([]GeoCount, error)
	FlowsFiltered(ctx context.Context, f FlowFilter) (total uint64, items []FlowRow, err error)
	AlertsHistory(ctx context.Context, since time.Time, source string, limit, offset int) (total uint64, items []AlertRow, err error)
}
```

- [ ] **Step 2: Commit**

```bash
git add backend/internal/storage/reader.go
git commit -m "feat(api): add storage Reader interface and read DTOs"
```

---

## Task 6: ClickHouse read-query implementations

**Files:**
- Create: `backend/internal/storage/queries.go`

These run against live ClickHouse, so they are verified end-to-end (Task 14), not unit-tested. Implement `*ClickHouseStore` methods satisfying `Reader`.

- [ ] **Step 1: Implement the query methods**

Create `backend/internal/storage/queries.go`:

```go
package storage

import (
	"context"
	"fmt"
	"time"
)

// srcClause appends "AND source = ?" when source is non-empty, returning the
// clause fragment and the args slice to pass through.
func srcClause(source string) (string, []any) {
	if source == "" {
		return "", nil
	}
	return " AND source = ?", []any{source}
}

func (s *ClickHouseStore) Overview(ctx context.Context, since time.Time, source string) (Overview, error) {
	clause, srcArgs := srcClause(source)
	var o Overview
	args := append([]any{since}, srcArgs...)
	err := s.conn.QueryRow(ctx, `
		SELECT count(), sum(bytes), sum(packets), sum(is_alert)
		FROM network_flows WHERE timestamp >= ?`+clause, args...).
		Scan(&o.Flows, &o.Bytes, &o.Packets, &o.ActiveAlerts)
	if err != nil {
		return Overview{}, fmt.Errorf("clickhouse: overview: %w", err)
	}
	return o, nil
}

func (s *ClickHouseStore) TopTalkers(ctx context.Context, since time.Time, source string, limit int) ([]Talker, error) {
	clause, srcArgs := srcClause(source)
	args := append([]any{since}, srcArgs...)
	args = append(args, limit)
	rows, err := s.conn.Query(ctx, `
		SELECT toString(src_ip), any(src_hostname), sum(bytes), sum(packets), count()
		FROM network_flows WHERE timestamp >= ?`+clause+`
		GROUP BY src_ip ORDER BY sum(bytes) DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: top talkers: %w", err)
	}
	defer rows.Close()
	var out []Talker
	for rows.Next() {
		var t Talker
		if err := rows.Scan(&t.IP, &t.Hostname, &t.Bytes, &t.Packets, &t.Flows); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *ClickHouseStore) TopApps(ctx context.Context, since time.Time, source string, limit int) ([]AppCount, error) {
	clause, srcArgs := srcClause(source)
	args := append([]any{since}, srcArgs...)
	args = append(args, limit)
	rows, err := s.conn.Query(ctx, `
		SELECT application_id, sum(bytes), count()
		FROM network_flows WHERE timestamp >= ? AND application_id != ''`+clause+`
		GROUP BY application_id ORDER BY sum(bytes) DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: top apps: %w", err)
	}
	defer rows.Close()
	var out []AppCount
	for rows.Next() {
		var a AppCount
		if err := rows.Scan(&a.Application, &a.Bytes, &a.Flows); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *ClickHouseStore) Throughput(ctx context.Context, since time.Time, source string, buckets int) ([]ThroughputPoint, error) {
	if buckets <= 0 {
		buckets = 60
	}
	// Bucket width = window / buckets, computed from since to now.
	width := time.Since(since) / time.Duration(buckets)
	if width < time.Second {
		width = time.Second
	}
	clause, srcArgs := srcClause(source)
	args := append([]any{int64(width.Seconds()), since}, srcArgs...)
	rows, err := s.conn.Query(ctx, `
		SELECT toStartOfInterval(timestamp, toIntervalSecond(?)) AS bucket, sum(bytes), sum(packets)
		FROM network_flows WHERE timestamp >= ?`+clause+`
		GROUP BY bucket ORDER BY bucket`, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: throughput: %w", err)
	}
	defer rows.Close()
	var out []ThroughputPoint
	for rows.Next() {
		var p ThroughputPoint
		if err := rows.Scan(&p.TS, &p.Bytes, &p.Packets); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *ClickHouseStore) GeoByCountry(ctx context.Context, since time.Time, source string) ([]GeoCount, error) {
	clause, srcArgs := srcClause(source)
	args := append([]any{since}, srcArgs...)
	rows, err := s.conn.Query(ctx, `
		SELECT dst_country, sum(bytes), count()
		FROM network_flows WHERE timestamp >= ? AND dst_country != ''`+clause+`
		GROUP BY dst_country ORDER BY sum(bytes) DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: geo: %w", err)
	}
	defer rows.Close()
	var out []GeoCount
	for rows.Next() {
		var g GeoCount
		if err := rows.Scan(&g.Country, &g.Bytes, &g.Flows); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *ClickHouseStore) FlowsFiltered(ctx context.Context, f FlowFilter) (uint64, []FlowRow, error) {
	where := "timestamp >= ?"
	args := []any{f.Since}
	addEq := func(col, val string) {
		if val != "" {
			where += " AND " + col + " = ?"
			args = append(args, val)
		}
	}
	addEq("source", f.Source)
	addEq("toString(src_ip)", f.SrcIP)
	addEq("toString(dst_ip)", f.DstIP)
	addEq("application_id", f.App)
	addEq("dst_country", f.Country)
	if f.Port != 0 {
		where += " AND (src_port = ? OR dst_port = ?)"
		args = append(args, f.Port, f.Port)
	}

	var total uint64
	if err := s.conn.QueryRow(ctx, `SELECT count() FROM network_flows WHERE `+where, args...).Scan(&total); err != nil {
		return 0, nil, fmt.Errorf("clickhouse: flows count: %w", err)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	pageArgs := append(append([]any{}, args...), limit, f.Offset)
	rows, err := s.conn.Query(ctx, `
		SELECT timestamp, source, toString(src_ip), toString(dst_ip), src_port, dst_port, protocol,
		       bytes, packets, application_id, sni, http_host, src_country, dst_country, src_asn_org, dst_asn_org
		FROM network_flows WHERE `+where+`
		ORDER BY timestamp DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return 0, nil, fmt.Errorf("clickhouse: flows page: %w", err)
	}
	defer rows.Close()
	var out []FlowRow
	for rows.Next() {
		var r FlowRow
		if err := rows.Scan(&r.TS, &r.Source, &r.SrcIP, &r.DstIP, &r.SrcPort, &r.DstPort, &r.Protocol,
			&r.Bytes, &r.Packets, &r.Application, &r.SNI, &r.HTTPHost, &r.SrcCountry, &r.DstCountry,
			&r.SrcASNOrg, &r.DstASNOrg); err != nil {
			return 0, nil, err
		}
		out = append(out, r)
	}
	return total, out, rows.Err()
}

func (s *ClickHouseStore) AlertsHistory(ctx context.Context, since time.Time, source string, limit, offset int) (uint64, []AlertRow, error) {
	clause, srcArgs := srcClause(source)
	countArgs := append([]any{since}, srcArgs...)
	var total uint64
	if err := s.conn.QueryRow(ctx, `SELECT count() FROM suricata_alerts WHERE timestamp >= ?`+clause, countArgs...).Scan(&total); err != nil {
		return 0, nil, fmt.Errorf("clickhouse: alerts count: %w", err)
	}
	if limit <= 0 {
		limit = 50
	}
	pageArgs := append(append([]any{}, countArgs...), limit, offset)
	rows, err := s.conn.Query(ctx, `
		SELECT timestamp, source, toString(src_ip), toString(dst_ip), alert_signature, alert_category, alert_severity
		FROM suricata_alerts WHERE timestamp >= ?`+clause+`
		ORDER BY timestamp DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return 0, nil, fmt.Errorf("clickhouse: alerts page: %w", err)
	}
	defer rows.Close()
	var out []AlertRow
	for rows.Next() {
		var a AlertRow
		if err := rows.Scan(&a.TS, &a.Source, &a.SrcIP, &a.DstIP, &a.Signature, &a.Category, &a.Severity); err != nil {
			return 0, nil, err
		}
		out = append(out, a)
	}
	return total, out, rows.Err()
}
```

- [ ] **Step 2: Commit**

```bash
git add backend/internal/storage/queries.go
git commit -m "feat(api): implement ClickHouse read queries with optional source filter"
```

---

## Task 7: request param parsing

**Files:**
- Create: `backend/internal/api/params.go`, `backend/internal/api/params_test.go`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/api/params_test.go`:

```go
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
		"":    time.Hour, // default
	}
	for in, want := range cases {
		since, err := parseRange(in, now)
		if err != nil {
			t.Fatalf("parseRange(%q) error: %v", in, err)
		}
		got := now.Sub(since)
		if got != want {
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run "TestParseRange|TestClampLimit" -v`
Expected: FAIL (undefined: parseRange).

- [ ] **Step 3: Implement**

Create `backend/internal/api/params.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/api/ -run "TestParseRange|TestClampLimit" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/params.go backend/internal/api/params_test.go
git commit -m "feat(api): add range and limit parsing helpers"
```

---

## Task 8: metrics handlers

**Files:**
- Create: `backend/internal/api/metrics.go`, `backend/internal/api/metrics_test.go`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/api/metrics_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/storage"
)

// fakeReader implements storage.Reader with canned data and records the last
// source filter it was called with.
type fakeReader struct {
	lastSource string
	overview   storage.Overview
}

func (f *fakeReader) Overview(_ context.Context, _ time.Time, source string) (storage.Overview, error) {
	f.lastSource = source
	return f.overview, nil
}
func (f *fakeReader) TopTalkers(context.Context, time.Time, string, int) ([]storage.Talker, error) {
	return []storage.Talker{{IP: "10.0.0.1", Bytes: 100}}, nil
}
func (f *fakeReader) TopApps(context.Context, time.Time, string, int) ([]storage.AppCount, error) {
	return nil, nil
}
func (f *fakeReader) Throughput(context.Context, time.Time, string, int) ([]storage.ThroughputPoint, error) {
	return nil, nil
}
func (f *fakeReader) GeoByCountry(context.Context, time.Time, string) ([]storage.GeoCount, error) {
	return nil, nil
}
func (f *fakeReader) FlowsFiltered(context.Context, storage.FlowFilter) (uint64, []storage.FlowRow, error) {
	return 0, nil, nil
}
func (f *fakeReader) AlertsHistory(context.Context, time.Time, string, int, int) (uint64, []storage.AlertRow, error) {
	return 0, nil, nil
}

func TestOverviewHandler(t *testing.T) {
	fr := &fakeReader{overview: storage.Overview{Flows: 42, Bytes: 1000}}
	app := fiber.New()
	app.Get("/api/metrics/overview", overviewHandler(fr))

	resp, _ := app.Test(httptest.NewRequest("GET", "/api/metrics/overview?range=24h&source=10.0.0.1", nil))
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var got storage.Overview
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Flows != 42 {
		t.Errorf("expected 42 flows, got %d", got.Flows)
	}
	if fr.lastSource != "10.0.0.1" {
		t.Errorf("expected source filter to be passed through, got %q", fr.lastSource)
	}
}

func TestOverviewHandlerRejectsBadRange(t *testing.T) {
	app := fiber.New()
	app.Get("/api/metrics/overview", overviewHandler(&fakeReader{}))
	resp, _ := app.Test(httptest.NewRequest("GET", "/api/metrics/overview?range=bogus", nil))
	if resp.StatusCode != 400 {
		t.Fatalf("bad range should 400, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestOverview -v`
Expected: FAIL (undefined: overviewHandler).

- [ ] **Step 3: Implement**

Create `backend/internal/api/metrics.go`:

```go
package api

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/storage"
)

func badRange(c *fiber.Ctx, err error) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
}

func serverErr(c *fiber.Ctx) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "query failed"})
}

func overviewHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		o, err := r.Overview(c.Context(), since, c.Query("source"))
		if err != nil {
			return serverErr(c)
		}
		return c.JSON(o)
	}
}

func topTalkersHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		limit := clampLimit(c.QueryInt("limit", 10))
		items, err := r.TopTalkers(c.Context(), since, c.Query("source"), limit)
		if err != nil {
			return serverErr(c)
		}
		return c.JSON(items)
	}
}

func topAppsHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		limit := clampLimit(c.QueryInt("limit", 10))
		items, err := r.TopApps(c.Context(), since, c.Query("source"), limit)
		if err != nil {
			return serverErr(c)
		}
		return c.JSON(items)
	}
}

func throughputHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		buckets := c.QueryInt("buckets", 60)
		items, err := r.Throughput(c.Context(), since, c.Query("source"), buckets)
		if err != nil {
			return serverErr(c)
		}
		return c.JSON(items)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/api/ -run TestOverview -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/metrics.go backend/internal/api/metrics_test.go
git commit -m "feat(api): add dashboard metrics handlers"
```

---

## Task 9: geo, alerts, flows handlers

**Files:**
- Create: `backend/internal/api/geo.go`, `backend/internal/api/alerts.go`, `backend/internal/api/flows.go`
- Test: `backend/internal/api/flows_test.go`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/api/flows_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/storage"
)

type filterCapturingReader struct {
	fakeReader
	gotFilter storage.FlowFilter
}

func (f *filterCapturingReader) FlowsFiltered(_ context.Context, flt storage.FlowFilter) (uint64, []storage.FlowRow, error) {
	f.gotFilter = flt
	return 1, []storage.FlowRow{{SrcIP: "10.0.0.1"}}, nil
}

func TestFlowsHandlerParsesFilters(t *testing.T) {
	fr := &filterCapturingReader{}
	app := fiber.New()
	app.Get("/api/flows", flowsHandler(fr))

	resp, _ := app.Test(httptest.NewRequest("GET",
		"/api/flows?range=1h&src_ip=10.0.0.1&port=443&app=tls&country=US&limit=20&offset=40", nil))
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out struct {
		Total uint64           `json:"total"`
		Items []storage.FlowRow `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Total != 1 || len(out.Items) != 1 {
		t.Fatalf("unexpected payload: %+v", out)
	}
	f := fr.gotFilter
	if f.SrcIP != "10.0.0.1" || f.Port != 443 || f.App != "tls" || f.Country != "US" {
		t.Errorf("filters not parsed: %+v", f)
	}
	if f.Limit != 20 || f.Offset != 40 {
		t.Errorf("pagination not parsed: limit=%d offset=%d", f.Limit, f.Offset)
	}
	if time.Since(f.Since) < 59*time.Minute {
		t.Errorf("since should be ~1h ago, got %v", f.Since)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestFlowsHandler -v`
Expected: FAIL (undefined: flowsHandler).

- [ ] **Step 3: Implement the three handlers**

Create `backend/internal/api/geo.go`:

```go
package api

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/storage"
)

func geoHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		items, err := r.GeoByCountry(c.Context(), since, c.Query("source"))
		if err != nil {
			return serverErr(c)
		}
		return c.JSON(items)
	}
}
```

Create `backend/internal/api/alerts.go`:

```go
package api

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/storage"
)

func alertsHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		limit := clampLimit(c.QueryInt("limit", 50))
		offset := c.QueryInt("offset", 0)
		total, items, err := r.AlertsHistory(c.Context(), since, c.Query("source"), limit, offset)
		if err != nil {
			return serverErr(c)
		}
		if items == nil {
			items = []storage.AlertRow{}
		}
		return c.JSON(fiber.Map{"total": total, "items": items})
	}
}
```

Create `backend/internal/api/flows.go`:

```go
package api

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/storage"
)

func flowsHandler(r storage.Reader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		since, err := parseRange(c.Query("range"), time.Now())
		if err != nil {
			return badRange(c, err)
		}
		f := storage.FlowFilter{
			Since:   since,
			Source:  c.Query("source"),
			SrcIP:   c.Query("src_ip"),
			DstIP:   c.Query("dst_ip"),
			App:     c.Query("app"),
			Country: c.Query("country"),
			Port:    uint16(c.QueryInt("port", 0)),
			Limit:   clampLimit(c.QueryInt("limit", 50)),
			Offset:  c.QueryInt("offset", 0),
		}
		total, items, err := r.FlowsFiltered(c.Context(), f)
		if err != nil {
			return serverErr(c)
		}
		if items == nil {
			items = []storage.FlowRow{}
		}
		return c.JSON(fiber.Map{"total": total, "items": items})
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/api/ -run TestFlowsHandler -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/geo.go backend/internal/api/alerts.go backend/internal/api/flows.go backend/internal/api/flows_test.go
git commit -m "feat(api): add geo, alerts, and flow explorer handlers"
```

---

## Task 10: source handlers

**Files:**
- Create: `backend/internal/api/sources.go`, `backend/internal/api/sources_test.go`

These use the B1 `sources.Registry` (`List`, `Get`, `UpdateConfig`, `Refresh`) and `sources.Stats` (`Snapshot`). The handler computes `status` and `mismatch` (derived) per the spec.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/api/sources_test.go`:

```go
package api

import (
	"testing"
	"time"

	"fluxio-backend/internal/sources"
)

func TestSourceView_StatusAndMismatch(t *testing.T) {
	now := time.Now()
	stats := sources.NewStats()
	stats.Record("10.0.0.1", 500)
	stats.Roll() // 1 flow/s

	// active: enabled, seen recently
	active := sources.Source{Address: "10.0.0.1", Type: "netflow", Enabled: true, LastSeen: now}
	v := buildSourceView(active, stats)
	if v.Status != "active" {
		t.Errorf("expected active, got %q", v.Status)
	}
	if v.FlowsPerSec != 1 {
		t.Errorf("expected 1 flow/s, got %d", v.FlowsPerSec)
	}

	// silent: enabled but stale last_seen
	silent := sources.Source{Address: "x", Type: "netflow", Enabled: true, LastSeen: now.Add(-10 * time.Minute)}
	if buildSourceView(silent, stats).Status != "silent" {
		t.Errorf("stale source should be silent")
	}

	// disabled
	disabled := sources.Source{Address: "y", Type: "netflow", Enabled: false, LastSeen: now}
	if buildSourceView(disabled, stats).Status != "disabled" {
		t.Errorf("disabled source should report disabled")
	}

	// mismatch derived from expected_type != type
	mm := sources.Source{Address: "z", Type: "tzsp", ExpectedType: "netflow", Enabled: true, LastSeen: now}
	if !buildSourceView(mm, stats).Mismatch {
		t.Errorf("expected_type != type should set mismatch")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestSourceView -v`
Expected: FAIL (undefined: buildSourceView).

- [ ] **Step 3: Implement**

Create `backend/internal/api/sources.go`:

```go
package api

import (
	"database/sql"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/sources"
)

// silentAfter is how long without data before an enabled source is "silent".
const silentAfter = 5 * time.Minute

// sourceView is the API representation of a source: its stored config plus
// derived live status, rate, and mismatch flag.
type sourceView struct {
	sources.Source
	Status      string `json:"status"`
	Mismatch    bool   `json:"mismatch"`
	FlowsPerSec uint64 `json:"flows_per_sec"`
	TotalBytes  uint64 `json:"total_bytes"`
}

func buildSourceView(s sources.Source, stats *sources.Stats) sourceView {
	snap := stats.Snapshot(s.Address)
	status := "active"
	switch {
	case !s.Enabled:
		status = "disabled"
	case time.Since(s.LastSeen) > silentAfter:
		status = "silent"
	}
	return sourceView{
		Source:      s,
		Status:      status,
		Mismatch:    s.ExpectedType != "" && s.ExpectedType != s.Type,
		FlowsPerSec: snap.FlowsPerSec,
		TotalBytes:  snap.TotalBytes,
	}
}

func listSourcesHandler(reg *sources.Registry, repo *sources.Repository, stats *sources.Stats) fiber.Handler {
	return func(c *fiber.Ctx) error {
		list, err := repo.List(c.Context())
		if err != nil {
			return serverErr(c)
		}
		views := make([]sourceView, 0, len(list))
		for _, s := range list {
			views = append(views, buildSourceView(s, stats))
		}
		return c.JSON(views)
	}
}

func getSourceHandler(repo *sources.Repository, stats *sources.Stats) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := c.ParamsInt("id")
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
		}
		s, err := repo.Get(c.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "source not found"})
		}
		if err != nil {
			return serverErr(c)
		}
		return c.JSON(buildSourceView(s, stats))
	}
}

func patchSourceHandler(reg *sources.Registry, repo *sources.Repository, stats *sources.Stats) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := c.ParamsInt("id")
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
		}
		var patch sources.ConfigPatch
		if err := c.BodyParser(&patch); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
		}
		s, err := repo.UpdateConfig(c.Context(), id, patch)
		if errors.Is(err, sql.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "source not found"})
		}
		if err != nil {
			// validation errors from ConfigPatch.Validate surface as 400
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		reg.Refresh(s) // keep the hot-path cache in sync immediately
		return c.JSON(buildSourceView(s, stats))
	}
}
```

Note: `sources.ConfigPatch` uses `*string`/`*bool` fields; Fiber's `BodyParser` populates pointer fields from JSON, leaving absent keys nil — exactly the partial-update semantics we want.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/api/ -run TestSourceView -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/sources.go backend/internal/api/sources_test.go
git commit -m "feat(api): add source list/detail/patch handlers with derived status"
```

---

## Task 11: WebSocket hub

**Files:**
- Create: `backend/internal/api/hub.go`, `backend/internal/api/hub_test.go`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/api/hub_test.go`:

```go
package api

import (
	"testing"
	"time"
)

func TestHubBroadcastReachesClients(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Stop()

	c := h.Register(4)
	defer h.Unregister(c)

	h.Broadcast([]byte("hello"))

	select {
	case msg := <-c.send:
		if string(msg) != "hello" {
			t.Fatalf("expected hello, got %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not receive the broadcast")
	}
}

func TestHubDropsSlowClientWithoutBlocking(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Stop()

	// buffer of 1; never drain it, so it fills immediately
	c := h.Register(1)
	defer h.Unregister(c)

	// Many broadcasts must not block even though the client never reads.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			h.Broadcast([]byte("x"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked on a slow client")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestHub -race -v`
Expected: FAIL (undefined: NewHub).

- [ ] **Step 3: Implement**

Create `backend/internal/api/hub.go`:

```go
package api

// Client is one connected WebSocket subscriber. Messages are delivered on send;
// a full buffer means the client is too slow and will be dropped.
type Client struct {
	send chan []byte
}

// Hub fans out messages to all connected clients. A single goroutine (Run)
// owns the client set, so register/unregister/broadcast are race-free.
type Hub struct {
	clients    map[*Client]struct{}
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte
	stop       chan struct{}
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]struct{}),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan []byte, 64),
		stop:       make(chan struct{}),
	}
}

// Run owns the client set until Stop is called. Start it once in a goroutine.
func (h *Hub) Run() {
	for {
		select {
		case <-h.stop:
			return
		case c := <-h.register:
			h.clients[c] = struct{}{}
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
		case msg := <-h.broadcast:
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// Slow client: drop it rather than block the hub.
					delete(h.clients, c)
					close(c.send)
				}
			}
		}
	}
}

// Register adds a client with the given send-buffer size and returns it.
func (h *Hub) Register(buffer int) *Client {
	c := &Client{send: make(chan []byte, buffer)}
	h.register <- c
	return c
}

// Unregister removes a client (safe to call once; the Run loop closes send).
func (h *Hub) Unregister(c *Client) {
	select {
	case h.unregister <- c:
	case <-h.stop:
	}
}

// Broadcast sends msg to all connected clients. Never blocks on a slow client.
func (h *Hub) Broadcast(msg []byte) {
	select {
	case h.broadcast <- msg:
	case <-h.stop:
	}
}

// Stop shuts the hub down.
func (h *Hub) Stop() { close(h.stop) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/api/ -run TestHub -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/hub.go backend/internal/api/hub_test.go
git commit -m "feat(api): add WebSocket hub with slow-client drop"
```

---

## Task 12: stream handler + metrics broadcaster + envelopes

**Files:**
- Create: `backend/internal/api/stream.go`, `backend/internal/api/stream_test.go`

- [ ] **Step 1: Write the failing test (envelope marshaling)**

Create `backend/internal/api/stream_test.go`:

```go
package api

import (
	"encoding/json"
	"testing"

	"fluxio-backend/internal/storage"
)

func TestAlertEnvelope(t *testing.T) {
	msg := alertEnvelope(storage.AlertRow{SrcIP: "1.1.1.1", Signature: "ET X", Severity: 2})
	var env struct {
		Type string          `json:"type"`
		Data storage.AlertRow `json:"data"`
	}
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Type != "alert" {
		t.Errorf("expected type alert, got %q", env.Type)
	}
	if env.Data.Signature != "ET X" {
		t.Errorf("alert payload not preserved: %+v", env.Data)
	}
}

func TestMetricsEnvelope(t *testing.T) {
	msg := metricsEnvelope(metricsSnapshot{Overview: storage.Overview{Flows: 5}})
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Type != "metrics" {
		t.Errorf("expected type metrics, got %q", env.Type)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run "TestAlertEnvelope|TestMetricsEnvelope" -v`
Expected: FAIL (undefined: alertEnvelope).

- [ ] **Step 3: Implement**

Create `backend/internal/api/stream.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/gofiber/contrib/websocket"

	"fluxio-backend/internal/auth"
	"fluxio-backend/internal/storage"
)

type envelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// metricsSnapshot is the periodic dashboard push payload.
type metricsSnapshot struct {
	Overview        storage.Overview        `json:"overview"`
	TopTalkers      []storage.Talker        `json:"top_talkers"`
	TopApps         []storage.AppCount      `json:"top_apps"`
	ThroughputPoint *storage.ThroughputPoint `json:"throughput_point,omitempty"`
}

func marshalEnvelope(typ string, data any) []byte {
	b, err := json.Marshal(envelope{Type: typ, Data: data})
	if err != nil {
		log.Printf("api: marshal %s envelope: %v", typ, err)
		return nil
	}
	return b
}

func alertEnvelope(a storage.AlertRow) []byte    { return marshalEnvelope("alert", a) }
func metricsEnvelope(m metricsSnapshot) []byte   { return marshalEnvelope("metrics", m) }

// BroadcastAlert pushes a live alert to all WebSocket clients. Wire this as the
// alert-bridge callback from the Suricata correlator.
func BroadcastAlert(h *Hub, a storage.AlertRow) {
	if msg := alertEnvelope(a); msg != nil {
		h.Broadcast(msg)
	}
}

// RunMetricsBroadcaster pushes a metrics snapshot every 5s until ctx is done.
// It queries a rolling short window (5m) so the dashboard's "live" view updates.
func RunMetricsBroadcaster(ctx context.Context, h *Hub, r storage.Reader) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			since := time.Now().Add(-5 * time.Minute)
			snap := metricsSnapshot{}
			snap.Overview, _ = r.Overview(ctx, since, "")
			snap.TopTalkers, _ = r.TopTalkers(ctx, since, "", 10)
			snap.TopApps, _ = r.TopApps(ctx, since, "", 10)
			if pts, _ := r.Throughput(ctx, since, "", 1); len(pts) > 0 {
				snap.ThroughputPoint = &pts[len(pts)-1]
			}
			if msg := metricsEnvelope(snap); msg != nil {
				h.Broadcast(msg)
			}
		}
	}
}

// streamHandler upgrades to WebSocket after validating the token query param,
// then relays hub messages to the socket until it closes.
func streamHandler(h *Hub, signer *auth.JWT) fiber.Handler {
	return websocket.New(func(conn *websocket.Conn) {
		client := h.Register(16)
		defer h.Unregister(client)
		for msg := range client.send {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	})
}
```

Note: `streamHandler` references `fiber.Handler`; add the import `"github.com/gofiber/fiber/v2"`. The token is validated in the upgrade gate in `router.go` (next task), not here, because the gate runs before the upgrade. Keep the `signer` param for symmetry (used by the gate). If the compiler flags `signer` as unused, drop the parameter and validate solely in the gate.

This task adds a new dependency `github.com/gofiber/contrib/websocket`. The repo currently uses `github.com/gofiber/websocket/v2`. To avoid a second websocket dep, use the existing one: change the import to `"github.com/gofiber/websocket/v2"` and the call `websocket.New(...)` / `websocket.TextMessage` / `*websocket.Conn` accordingly (the API is identical). Use `github.com/gofiber/websocket/v2`.

- [ ] **Step 4: Fix the import to the existing websocket package**

Edit `stream.go` import to `"github.com/gofiber/websocket/v2"` and add `"github.com/gofiber/fiber/v2"`. Verify no `gofiber/contrib` import remains.

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./internal/api/ -run "TestAlertEnvelope|TestMetricsEnvelope" -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/api/stream.go backend/internal/api/stream_test.go
git commit -m "feat(api): add WS stream handler, metrics broadcaster, and alert bridge"
```

---

## Task 13: router — mount everything with auth

**Files:**
- Create: `backend/internal/api/router.go`

No standalone unit test (it is composition); covered by the end-to-end Task 15.

- [ ] **Step 1: Implement RegisterRoutes**

Create `backend/internal/api/router.go`:

```go
package api

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"

	"fluxio-backend/internal/auth"
	"fluxio-backend/internal/sources"
	"fluxio-backend/internal/storage"
)

// Deps bundles everything the routes need.
type Deps struct {
	Reader      storage.Reader
	Signer      *auth.JWT
	UserRepo    *auth.Repository
	Hub         *Hub
	SourceReg   *sources.Registry
	SourceRepo  *sources.Repository
	SourceStats *sources.Stats
}

// RegisterRoutes mounts the auth, read, source, and WebSocket routes on app.
// Public: GET /api/health, POST /api/auth/login, GET /ws (token-gated).
// Everything else under /api requires a valid JWT.
func RegisterRoutes(app *fiber.App, d Deps) {
	app.Get("/api/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	app.Post("/api/auth/login", loginHandler(d.UserRepo, d.Signer))

	// WebSocket: validate the token at the upgrade gate (query param), then upgrade.
	app.Use("/ws", func(c *fiber.Ctx) error {
		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}
		if !auth.ValidateToken(d.Signer, c.Query("token")) {
			return c.Status(fiber.StatusUnauthorized).SendString("invalid token")
		}
		return c.Next()
	})
	app.Get("/ws", streamHandler(d.Hub, d.Signer))

	// Authenticated API group.
	api := app.Group("/api", auth.Middleware(d.Signer))

	api.Get("/metrics/overview", overviewHandler(d.Reader))
	api.Get("/metrics/top-talkers", topTalkersHandler(d.Reader))
	api.Get("/metrics/top-apps", topAppsHandler(d.Reader))
	api.Get("/metrics/throughput", throughputHandler(d.Reader))
	api.Get("/geo/flows", geoHandler(d.Reader))
	api.Get("/alerts", alertsHandler(d.Reader))
	api.Get("/flows", flowsHandler(d.Reader))

	api.Get("/sources", listSourcesHandler(d.SourceReg, d.SourceRepo, d.SourceStats))
	api.Get("/sources/:id", getSourceHandler(d.SourceRepo, d.SourceStats))
	api.Patch("/sources/:id", patchSourceHandler(d.SourceReg, d.SourceRepo, d.SourceStats))
}

// loginHandler validates credentials against the user repo and issues a JWT.
func loginHandler(repo *auth.Repository, signer *auth.JWT) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.BodyParser(&body); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
		}
		u, err := repo.GetByUsername(context.Background(), body.Username)
		if err != nil || !auth.CheckPassword(u.PasswordHash, body.Password) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
		}
		tok, expires, err := signer.Issue(u.Username)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "could not issue token"})
		}
		return c.JSON(fiber.Map{"token": tok, "expires_at": expires})
	}
}
```

- [ ] **Step 2: Commit**

```bash
git add backend/internal/api/router.go
git commit -m "feat(api): add router wiring auth, read, source, and WS routes"
```

---

## Task 14: wire the API into main.go; remove stubs

**Files:**
- Modify: `backend/cmd/server/main.go`
- Modify: `backend/internal/collector/suricata_correlator.go`

- [ ] **Step 1: Add an alert-bridge hook to the correlator**

The correlator currently writes alerts to `alerts alertWriter`. Add an optional callback invoked for each alert so the hub can push it live. In `suricata_correlator.go`, find where `alerts.WriteAlert(alert)` is called (in `processEveLine`) and add a package-level optional hook:

```go
// AlertHook, if set, is called for every Suricata alert in addition to
// persistence — used to bridge alerts to the live WebSocket. Set once at startup.
var AlertHook func(processor.SuricataAlert)
```

Then where the alert is written:

```go
	if alert, ok := evt.ToAlert(); ok {
		alerts.WriteAlert(alert)
		if AlertHook != nil {
			AlertHook(alert)
		}
	}
```

- [ ] **Step 2: Remove the stub WS + mock login + old health, wire the API**

In `main.go`:

- Delete the stub websocket block: the `app.Use("/ws", ...)` upgrade gate and the `app.Get("/ws/alerts", websocket.New(...))` handler (lines ~28-53).
- Delete the inline `api.Get("/health", ...)` and `api.Post("/api/auth/login", ...)` stub handlers.
- Remove the `registerSettingsRoutes(api, settingsRepo, noopSwitcher{})` call and the `noopSwitcher` type, and delete `settings_routes.go` + its references in `main_test.go` (the global settings route is superseded by per-source config). Remove the `settings` import and `settingsRepo`.
- Remove the now-unused `github.com/gofiber/websocket/v2` and `github.com/gofiber/fiber/v2/middleware/cors` direct uses if they move into the api package — keep CORS in main (see below).

Add the construction and wiring after the source registry/stats are built and after `store`/`writer`:

```go
	// Auth: JWT signer + user repo + admin seed.
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "CHANGE_ME_INSECURE_DEFAULT"
		log.Println("WARNING: JWT_SECRET not set; using an insecure default. Set JWT_SECRET in .env.")
	}
	signer := auth.NewJWT(jwtSecret, 24*time.Hour)
	userRepo := auth.NewRepository(pgDB)
	if pw, err := auth.SeedAdmin(context.Background(), userRepo,
		envOr("ADMIN_USERNAME", "admin"), os.Getenv("ADMIN_PASSWORD")); err != nil {
		log.Printf("auth: admin seed failed: %v", err)
	} else if pw != "" {
		log.Printf("auth: created admin user %q with generated password: %s",
			envOr("ADMIN_USERNAME", "admin"), pw)
	}

	// WebSocket hub + live producers.
	hub := api.NewHub()
	go hub.Run()
	go api.RunMetricsBroadcaster(pipelineCtx, hub, store)
	collector.AlertHook = func(a processor.SuricataAlert) {
		api.BroadcastAlert(hub, storage.AlertRow{
			TS: a.Timestamp, Source: "127.0.0.1", SrcIP: a.SourceIP, DstIP: a.DestinationIP,
			Signature: a.Signature, Category: a.Category, Severity: a.Severity,
		})
	}

	api.RegisterRoutes(app, api.Deps{
		Reader: store, Signer: signer, UserRepo: userRepo, Hub: hub,
		SourceReg: sourceReg, SourceRepo: sourceRepo, SourceStats: sourceStats,
	})
```

Add a small helper near the top of `main.go`:

```go
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

Keep the CORS middleware and the SPA static/fallback routes as they are. Ensure the imports include `"fluxio-backend/internal/api"` and `"fluxio-backend/internal/auth"`, and that `storage` is imported (already is).

- [ ] **Step 3: Delete the superseded settings route + its test**

```bash
git rm backend/cmd/server/settings_routes.go
```

Edit `backend/cmd/server/main_test.go`: remove the `TestSettingsRoutes`-style test(s) that reference `registerSettingsRoutes`, `fakeModeStore`, `fakeModeSwitcher`, and the `var _ = settings.NewRepository` line. If that leaves the file empty of tests, delete it with `git rm backend/cmd/server/main_test.go`.

- [ ] **Step 4: Verify the build compiles via Docker**

Run: `docker compose build backend`
Expected: build succeeds (new deps `golang-jwt/jwt/v5`, `x/crypto` resolved by `GOFLAGS=-mod=mod`).

- [ ] **Step 5: Commit**

```bash
git add backend/cmd/server/main.go backend/internal/collector/suricata_correlator.go
git commit -m "feat(api): wire auth, read APIs, WS hub, and alert bridge; remove stubs"
```

---

## Task 15: env vars + end-to-end verification

**Files:**
- Modify: `.env.example`, `docker-compose.yml`

- [ ] **Step 1: Add the new env vars**

In `.env.example` add:

```env
# ── Auth ──────────────────────────────────────────────────────────────────────
# HS256 signing secret for JWTs. Generate with: openssl rand -base64 32
JWT_SECRET=
# Initial admin (created only on first boot when the users table is empty).
ADMIN_USERNAME=admin
# Leave blank to auto-generate a password (printed once in the backend logs).
ADMIN_PASSWORD=
```

In `docker-compose.yml`, under the backend service `environment:` list, add:

```yaml
      - JWT_SECRET=${JWT_SECRET:-}
      - ADMIN_USERNAME=${ADMIN_USERNAME:-admin}
      - ADMIN_PASSWORD=${ADMIN_PASSWORD:-}
```

- [ ] **Step 2: Bring up the stack**

Run: `docker compose up --build -d`
Expected: all containers running; backend log prints the generated admin password if `ADMIN_PASSWORD` was blank.

- [ ] **Step 3: Log in and capture a token**

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:${PORT:-80}/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<PASSWORD_FROM_LOGS>"}' | sed 's/.*"token":"\([^"]*\)".*/\1/')
echo "$TOKEN"
```
Expected: a non-empty JWT.

- [ ] **Step 4: Confirm auth is enforced and reads work**

```bash
# No token → 401
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:${PORT:-80}/api/metrics/overview   # expect 401
# With token → 200 + JSON
curl -s -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:${PORT:-80}/api/metrics/overview?range=1h"
curl -s -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:${PORT:-80}/api/sources"
```
Expected: 401 without token; JSON overview and a sources array (incl. the seeded `127.0.0.1/suricata`) with token.

- [ ] **Step 5: Confirm the WebSocket pushes**

Use a WS client (e.g. `websocat`) with the token:
```bash
websocat "ws://127.0.0.1:${PORT:-80}/ws?token=$TOKEN"
```
Expected: a `{"type":"metrics",...}` message within ~5s; `{"type":"alert",...}` when Suricata fires.

- [ ] **Step 6: Patch a source and confirm it persists**

```bash
SRC_ID=$(curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:${PORT:-80}/api/sources | sed 's/.*"id":\([0-9]*\).*/\1/')
curl -s -X PATCH -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"Name":"Edge Router","DPIMode":"suricata"}' \
  "http://127.0.0.1:${PORT:-80}/api/sources/$SRC_ID"
```
Expected: 200 with the updated source (name + dpi_mode changed).

- [ ] **Step 7: Tear down**

Run: `docker compose down`

- [ ] **Step 8: Commit**

```bash
git add .env.example docker-compose.yml
git commit -m "feat(api): add JWT/admin env vars and document them in compose + example"
```

---

## Notes for the executor

- **PATCH body field names:** `sources.ConfigPatch` uses exported Go field names (`Name`, `GroupTag`, `Enabled`, `DPIMode`, `ExpectedType`) with no JSON tags, so the PATCH body uses those exact keys. If the frontend (C) prefers snake_case, add JSON tags to `ConfigPatch` then — not in this plan.
- **Active alerts metric:** `Overview.ActiveAlerts` uses `sum(is_alert)` over the flow window as a proxy. If a distinct count from `suricata_alerts` is wanted later, add a second query — out of scope here.
- **Do not run `go mod tidy`** (upgrades deps to Go 1.23). The Dockerfile uses `GOFLAGS=-mod=mod`; new imports are resolved at build time.
- The metrics broadcaster always queries all sources (`source=""`) over a 5m window; per-source live views use REST. That matches the spec's hybrid model.
