-- Slice 2: import, mapping profiles, payee rules, review

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS csv_mapping_profiles (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    account_id      INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    date_column     TEXT NOT NULL,
    date_format     TEXT NOT NULL DEFAULT 'auto',
    amount_mode     TEXT NOT NULL DEFAULT 'signed'
                    CHECK (amount_mode IN ('signed','debit_credit')),
    amount_column   TEXT,
    debit_column    TEXT,
    credit_column   TEXT,
    payee_column    TEXT,
    notes_column    TEXT,
    invert_sign     INTEGER NOT NULL DEFAULT 0,
    has_header      INTEGER NOT NULL DEFAULT 1,
    delimiter       TEXT NOT NULL DEFAULT ',',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS payee_rules (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    sort_order        INTEGER NOT NULL DEFAULT 0,
    name              TEXT NOT NULL,
    enabled           INTEGER NOT NULL DEFAULT 1,
    payee_match       TEXT NOT NULL CHECK (payee_match IN ('contains','equals','starts_with')),
    payee_value       TEXT NOT NULL,
    amount_min        INTEGER,
    amount_max        INTEGER,
    account_id        INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    notes_contains    TEXT,
    set_category_id   INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    set_person        TEXT CHECK (set_person IN ('shared','a','b') OR set_person IS NULL),
    rename_payee      TEXT,
    set_tags          TEXT,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_payee_rules_order ON payee_rules(sort_order, id);

CREATE TABLE IF NOT EXISTS import_batches (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      INTEGER REFERENCES accounts(id),
    profile_id      INTEGER REFERENCES csv_mapping_profiles(id) ON DELETE SET NULL,
    filename        TEXT,
    status          TEXT NOT NULL DEFAULT 'uploaded'
                    CHECK (status IN ('uploaded','mapped','committed','undone')),
    raw_csv         TEXT,
    headers_json    TEXT,
    row_count       INTEGER NOT NULL DEFAULT 0,
    imported_count  INTEGER NOT NULL DEFAULT 0,
    created_by      INTEGER REFERENCES people(id),
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    committed_at    TEXT,
    undone_at       TEXT
);

CREATE TABLE IF NOT EXISTS import_staging_rows (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    batch_id            INTEGER NOT NULL REFERENCES import_batches(id) ON DELETE CASCADE,
    row_index           INTEGER NOT NULL,
    raw_json            TEXT NOT NULL,
    transaction_date    TEXT,
    amount              INTEGER,
    payee_name          TEXT,
    notes               TEXT,
    fingerprint         TEXT,
    action              TEXT NOT NULL DEFAULT 'import'
                        CHECK (action IN ('import','skip','merge','transfer_pair')),
    match_tx_id         INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
    transfer_tx_id      INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
    matched_rule_id     INTEGER REFERENCES payee_rules(id) ON DELETE SET NULL,
    matched_rule_name   TEXT,
    category_id         INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    person              TEXT NOT NULL DEFAULT 'shared'
                        CHECK (person IN ('shared','a','b')),
    rename_payee        TEXT,
    tag_names           TEXT,
    exception           TEXT,
    duplicate_tx_id     INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
    needs_review        INTEGER NOT NULL DEFAULT 1,
    UNIQUE(batch_id, row_index)
);

CREATE INDEX IF NOT EXISTS idx_staging_batch ON import_staging_rows(batch_id, row_index);
CREATE INDEX IF NOT EXISTS idx_staging_fingerprint ON import_staging_rows(fingerprint);

ALTER TABLE transactions ADD COLUMN import_batch_id INTEGER REFERENCES import_batches(id) ON DELETE SET NULL;
ALTER TABLE transactions ADD COLUMN import_fingerprint TEXT;

CREATE INDEX IF NOT EXISTS idx_tx_import_batch ON transactions(import_batch_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tx_fingerprint ON transactions(import_fingerprint) WHERE deleted_at IS NULL AND import_fingerprint IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tx_needs_review ON transactions(needs_review) WHERE deleted_at IS NULL AND needs_review = 1;
