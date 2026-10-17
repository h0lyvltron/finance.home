-- Slice 4: budgets, schedules/bills, household plan settings

PRAGMA foreign_keys = ON;

ALTER TABLE households ADD COLUMN expected_income_cents INTEGER NOT NULL DEFAULT 0;
ALTER TABLE households ADD COLUMN savings_target_cents INTEGER NOT NULL DEFAULT 0;

-- Parent budget mode: rollup of children OR independent cap (never both counting same spend).
ALTER TABLE categories ADD COLUMN budget_mode TEXT NOT NULL DEFAULT 'rollup';

-- Default monthly targets (copied into months; overrides live in budget_months).
CREATE TABLE IF NOT EXISTS budget_defaults (
    category_id   INTEGER PRIMARY KEY REFERENCES categories(id) ON DELETE CASCADE,
    amount_cents  INTEGER NOT NULL DEFAULT 0,
    rollover      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS budget_months (
    year_month    TEXT NOT NULL,
    category_id   INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    amount_cents  INTEGER NOT NULL DEFAULT 0,
    rollover      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (year_month, category_id)
);

CREATE INDEX IF NOT EXISTS idx_budget_months_ym ON budget_months(year_month);

CREATE TABLE IF NOT EXISTS schedule_templates (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    name                    TEXT NOT NULL,
    payee_name              TEXT,
    amount_cents            INTEGER NOT NULL,
    kind                    TEXT NOT NULL DEFAULT 'expense' CHECK (kind IN ('expense','income')),
    account_id              INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    category_id             INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    frequency               TEXT NOT NULL CHECK (frequency IN ('weekly','biweekly','monthly','yearly')),
    interval_n              INTEGER NOT NULL DEFAULT 1,
    next_due                TEXT NOT NULL,
    auto_match              INTEGER NOT NULL DEFAULT 1,
    amount_tolerance_cents  INTEGER NOT NULL DEFAULT 500,
    date_window_days        INTEGER NOT NULL DEFAULT 5,
    active                  INTEGER NOT NULL DEFAULT 1,
    created_at              TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at              TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_sched_tpl_active ON schedule_templates(active, next_due);

CREATE TABLE IF NOT EXISTS schedule_instances (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    template_id       INTEGER NOT NULL REFERENCES schedule_templates(id) ON DELETE CASCADE,
    due_date          TEXT NOT NULL,
    expected_amount   INTEGER NOT NULL,
    status            TEXT NOT NULL DEFAULT 'upcoming'
        CHECK (status IN ('upcoming','matched','skipped','overdue')),
    matched_tx_id     INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
    notes             TEXT,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(template_id, due_date)
);

CREATE INDEX IF NOT EXISTS idx_sched_inst_due ON schedule_instances(due_date, status);
CREATE INDEX IF NOT EXISTS idx_sched_inst_tpl ON schedule_instances(template_id);
CREATE INDEX IF NOT EXISTS idx_sched_inst_tx ON schedule_instances(matched_tx_id);
