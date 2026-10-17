-- Slice 8: dashboard cell grid (12 columns, 1-based col/row, spans in cells).
-- grid_w = 0 marks a widget not yet placed; the store packs those from
-- sort_order + S/M/L width on first load so existing layouts keep their shape.

PRAGMA foreign_keys = ON;

ALTER TABLE dashboard_widgets ADD COLUMN grid_col INTEGER NOT NULL DEFAULT 0;
ALTER TABLE dashboard_widgets ADD COLUMN grid_row INTEGER NOT NULL DEFAULT 0;
ALTER TABLE dashboard_widgets ADD COLUMN grid_w   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE dashboard_widgets ADD COLUMN grid_h   INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_dashboard_widgets_grid
    ON dashboard_widgets(layout_id, grid_row, grid_col, id);
