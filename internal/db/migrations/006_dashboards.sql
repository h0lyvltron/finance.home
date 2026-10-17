-- Slice 6: per-person dashboard layouts (ordered widget list)

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS dashboard_layouts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    person_id     INTEGER UNIQUE REFERENCES people(id) ON DELETE CASCADE,
    -- NULL person_id = household default template (copied once per profile)
    range_preset  TEXT NOT NULL DEFAULT 'this_month'
        CHECK (range_preset IN ('this_month', 'last_30', 'ytd', 'last_12')),
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- At most one household default (person_id NULL)
CREATE UNIQUE INDEX IF NOT EXISTS idx_dashboard_layouts_default
    ON dashboard_layouts((1))
    WHERE person_id IS NULL;

CREATE TABLE IF NOT EXISTS dashboard_widgets (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    layout_id     INTEGER NOT NULL REFERENCES dashboard_layouts(id) ON DELETE CASCADE,
    widget_type   TEXT NOT NULL
        CHECK (widget_type IN (
            'left_to_spend', 'net_worth', 'cash_flow', 'category_spend',
            'budget_health', 'goals', 'accounts', 'recent', 'insights',
            'bills', 'saved_filter'
        )),
    width         TEXT NOT NULL DEFAULT 'm' CHECK (width IN ('s', 'm', 'l')),
    sort_order    INTEGER NOT NULL DEFAULT 0,
    config_json   TEXT NOT NULL DEFAULT '{}',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_dashboard_widgets_layout
    ON dashboard_widgets(layout_id, sort_order, id);
