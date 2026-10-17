-- Slice 5: savings/debt goals + linked accounts

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS goals (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    kind               TEXT NOT NULL CHECK (kind IN ('savings', 'debt')),
    name               TEXT NOT NULL,
    target_cents       INTEGER NOT NULL DEFAULT 0,
    target_date        TEXT, -- YYYY-MM-DD optional
    pace_window_days   INTEGER NOT NULL DEFAULT 90,
    archived           INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_goals_active ON goals(archived, kind);

CREATE TABLE IF NOT EXISTS goal_accounts (
    goal_id    INTEGER NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    PRIMARY KEY (goal_id, account_id)
);

CREATE INDEX IF NOT EXISTS idx_goal_accounts_acct ON goal_accounts(account_id);
