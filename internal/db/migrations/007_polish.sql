-- Slice 7: polish — month-start day, coaching density, recycle retention, drafts

PRAGMA foreign_keys = ON;

ALTER TABLE households ADD COLUMN month_start_day INTEGER NOT NULL DEFAULT 1;
ALTER TABLE households ADD COLUMN fewer_nudges INTEGER NOT NULL DEFAULT 0;

-- Per-person coaching snooze / dismiss (rule kind = insight Kind).
CREATE TABLE IF NOT EXISTS coaching_prefs (
    person_id       INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,
    snoozed_until   TEXT, -- YYYY-MM-DD inclusive; null when dismissed forever
    dismissed       INTEGER NOT NULL DEFAULT 0,
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (person_id, kind)
);

CREATE INDEX IF NOT EXISTS idx_coaching_prefs_person ON coaching_prefs(person_id);

-- Durable quick-add drafts (offline queue syncs into these, or Save draft).
CREATE TABLE IF NOT EXISTS draft_transactions (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    person_id        INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    account_id       INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    category_id      INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    payee_name       TEXT,
    notes            TEXT,
    amount           INTEGER NOT NULL DEFAULT 0, -- signed cents
    transaction_date TEXT, -- YYYY-MM-DD
    person           TEXT NOT NULL DEFAULT 'shared' CHECK (person IN ('shared','a','b')),
    kind             TEXT NOT NULL DEFAULT 'expense' CHECK (kind IN ('expense','income')),
    client_key       TEXT, -- idempotent offline sync key
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(person_id, client_key)
);

CREATE INDEX IF NOT EXISTS idx_drafts_person ON draft_transactions(person_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_tx_deleted ON transactions(deleted_at) WHERE deleted_at IS NOT NULL;
