CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO settings (key, value)
VALUES ('dpi_mode', 'none')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE IF NOT EXISTS sources (
    id            SERIAL PRIMARY KEY,
    address       TEXT NOT NULL,
    type          TEXT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    group_tag     TEXT NOT NULL DEFAULT '',
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    dpi_mode      TEXT NOT NULL DEFAULT 'auto',
    expected_type TEXT NOT NULL DEFAULT '',
    first_seen    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (address, type)
);

CREATE TABLE IF NOT EXISTS users (
    id            SERIAL PRIMARY KEY,
    username      TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
