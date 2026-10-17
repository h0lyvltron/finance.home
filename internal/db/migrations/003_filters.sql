-- Slice 3: saved filters + indexes for shared query

CREATE TABLE IF NOT EXISTS saved_filters (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    person_id   INTEGER REFERENCES people(id) ON DELETE SET NULL,
    name        TEXT NOT NULL,
    query_json  TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_saved_filters_person ON saved_filters(person_id);
CREATE INDEX IF NOT EXISTS idx_tx_person ON transactions(person);
CREATE INDEX IF NOT EXISTS idx_tx_status ON transactions(status);
CREATE INDEX IF NOT EXISTS idx_tx_needs_review ON transactions(needs_review);
CREATE INDEX IF NOT EXISTS idx_tx_payee_name ON transactions(payee_name);
