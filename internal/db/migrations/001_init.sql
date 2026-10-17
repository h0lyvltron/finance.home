-- Household finance schema (slice 0–1)

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS households (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    name          TEXT NOT NULL,
    currency      TEXT NOT NULL DEFAULT 'USD',
    timezone      TEXT NOT NULL DEFAULT 'America/New_York',
    week_starts   INTEGER NOT NULL DEFAULT 1, -- 0=Sun … 6=Sat; 1=Mon
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS people (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    slot          TEXT NOT NULL UNIQUE CHECK (slot IN ('a', 'b')),
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT PRIMARY KEY,
    person_id   INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    csrf_token  TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_sessions_person ON sessions(person_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS accounts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    type            TEXT NOT NULL CHECK (type IN ('checking','savings','cash','credit','loan')),
    owner           TEXT NOT NULL CHECK (owner IN ('a','b','joint')),
    currency        TEXT NOT NULL DEFAULT 'USD',
    anchor_balance  INTEGER NOT NULL DEFAULT 0, -- cents, NW-oriented (liabilities typically ≤0)
    anchor_date     TEXT NOT NULL,              -- YYYY-MM-DD
    display_order   INTEGER NOT NULL DEFAULT 0,
    archived        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS categories (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('expense','income','transfer','starting_balance')),
    parent_id       INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    fixed_flexible  TEXT CHECK (fixed_flexible IN ('fixed','flexible') OR fixed_flexible IS NULL),
    color           TEXT,
    exclude_budget  INTEGER NOT NULL DEFAULT 0,
    protected       INTEGER NOT NULL DEFAULT 0,
    archived        INTEGER NOT NULL DEFAULT 0,
    display_order   INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_categories_parent ON categories(parent_id);
CREATE INDEX IF NOT EXISTS idx_categories_kind ON categories(kind);

CREATE TABLE IF NOT EXISTS tags (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL COLLATE NOCASE,
    archived   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(name)
);

CREATE TABLE IF NOT EXISTS payees (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL COLLATE NOCASE,
    last_used_at  TEXT,
    default_category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    default_account_id  INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    archived      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(name)
);

CREATE TABLE IF NOT EXISTS transactions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      INTEGER NOT NULL REFERENCES accounts(id),
    payee_id        INTEGER REFERENCES payees(id) ON DELETE SET NULL,
    payee_name      TEXT, -- denormalized for display / free-text
    notes           TEXT,
    amount          INTEGER NOT NULL, -- cents; signed (expense negative, income positive)
    transaction_date TEXT NOT NULL,  -- YYYY-MM-DD
    person          TEXT NOT NULL DEFAULT 'shared' CHECK (person IN ('shared','a','b')),
    status          TEXT NOT NULL DEFAULT 'cleared' CHECK (status IN ('pending','cleared','reconciled')),
    needs_review    INTEGER NOT NULL DEFAULT 0,
    transfer_id     INTEGER, -- self-FK via transfers table
    category_id     INTEGER REFERENCES categories(id) ON DELETE SET NULL, -- null when split
    is_split        INTEGER NOT NULL DEFAULT 0,
    deleted_at      TEXT,
    created_by      INTEGER REFERENCES people(id),
    updated_by      INTEGER REFERENCES people(id),
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_tx_account_date ON transactions(account_id, transaction_date, id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tx_date ON transactions(transaction_date, id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tx_payee ON transactions(payee_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS transaction_splits (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    transaction_id  INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    category_id     INTEGER NOT NULL REFERENCES categories(id),
    person          TEXT NOT NULL DEFAULT 'shared' CHECK (person IN ('shared','a','b')),
    amount          INTEGER NOT NULL,
    notes           TEXT,
    display_order   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_splits_tx ON transaction_splits(transaction_id);
CREATE INDEX IF NOT EXISTS idx_splits_cat ON transaction_splits(category_id);

CREATE TABLE IF NOT EXISTS transaction_tags (
    transaction_id INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    tag_id         INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (transaction_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_tt_tag ON transaction_tags(tag_id, transaction_id);

CREATE TABLE IF NOT EXISTS transfers (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    from_tx_id        INTEGER NOT NULL UNIQUE REFERENCES transactions(id),
    to_tx_id          INTEGER NOT NULL UNIQUE REFERENCES transactions(id),
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS reconciliations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      INTEGER NOT NULL REFERENCES accounts(id),
    statement_date  TEXT NOT NULL,
    ending_balance  INTEGER NOT NULL,
    difference      INTEGER NOT NULL DEFAULT 0,
    completed_at    TEXT,
    created_by      INTEGER REFERENCES people(id),
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS undo_ops (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL, -- soft_delete_tx, etc.
    payload      TEXT NOT NULL, -- JSON
    expires_at   TEXT NOT NULL,
    created_by   INTEGER REFERENCES people(id),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
