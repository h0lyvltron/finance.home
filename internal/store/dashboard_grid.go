package store

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"finance.home/internal/models"
)

// Home dashboard cell grid: 12 columns, rows grow downward, 1-based coordinates.
const (
	GridCols   = 12
	GridMinW   = 2
	GridMinH   = 1
	GridMaxH   = 16
	gridMaxRow = 500
)

type gridRect struct {
	ID       int64
	Col, Row int
	W, H     int
}

func (a gridRect) bottom() int { return a.Row + a.H }

func (a gridRect) overlaps(b gridRect) bool {
	return a.Col < b.Col+b.W && b.Col < a.Col+a.W && a.Row < b.Row+b.H && b.Row < a.Row+a.H
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampRect(r gridRect) gridRect {
	r.W = clampInt(r.W, GridMinW, GridCols)
	r.H = clampInt(r.H, GridMinH, GridMaxH)
	r.Col = clampInt(r.Col, 1, GridCols-r.W+1)
	r.Row = clampInt(r.Row, 1, gridMaxRow)
	return r
}

func sortReading(rs []gridRect) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Row != rs[j].Row {
			return rs[i].Row < rs[j].Row
		}
		if rs[i].Col != rs[j].Col {
			return rs[i].Col < rs[j].Col
		}
		return rs[i].ID < rs[j].ID
	})
}

func collidesAny(r gridRect, placed []gridRect) bool {
	for _, p := range placed {
		if r.overlaps(p) {
			return true
		}
	}
	return false
}

// pushDown keeps fixedID where it is; every other rect, in reading order, slides
// straight down until it clears everything already placed. Gaps are left alone.
func pushDown(rs []gridRect, fixedID int64) []gridRect {
	sortReading(rs)
	out := make([]gridRect, 0, len(rs))
	for _, r := range rs {
		if r.ID == fixedID {
			out = append(out, r)
		}
	}
	for _, r := range rs {
		if r.ID == fixedID {
			continue
		}
		for moved := true; moved; {
			moved = false
			for _, p := range out {
				if r.overlaps(p) {
					r.Row = p.bottom()
					moved = true
					break
				}
			}
		}
		out = append(out, r)
	}
	sortReading(out)
	return out
}

// compactUp floats each rect (reading order) as high as it fits.
func compactUp(rs []gridRect) []gridRect {
	sortReading(rs)
	out := make([]gridRect, 0, len(rs))
	for _, r := range rs {
		for r.Row > 1 {
			up := r
			up.Row--
			if collidesAny(up, out) {
				break
			}
			r = up
		}
		out = append(out, r)
	}
	sortReading(out)
	return out
}

// packShelves lays rects out left to right in the given order, wrapping to a new
// shelf below the tallest item when a row is full (mirrors the old flow layout).
func packShelves(rs []gridRect, startRow int) {
	col, row, shelfH := 1, startRow, 0
	for i := range rs {
		if col+rs[i].W-1 > GridCols {
			row += shelfH
			col, shelfH = 1, 0
		}
		rs[i].Col, rs[i].Row = col, row
		col += rs[i].W
		if rs[i].H > shelfH {
			shelfH = rs[i].H
		}
	}
}

func spanForWidth(widgetType, width string) int {
	if widgetType == WidgetLeftToSpend {
		return GridCols
	}
	switch normalizeWidth(width) {
	case "s":
		return 4
	case "l":
		return GridCols
	default:
		return 6
	}
}

func widthForSpan(w int) string {
	switch {
	case w <= 4:
		return "s"
	case w <= 8:
		return "m"
	default:
		return "l"
	}
}

func defaultHeight(widgetType string) int {
	for _, c := range WidgetCatalog() {
		if c.Type == widgetType && c.DefaultH > 0 {
			return c.DefaultH
		}
	}
	return 3
}

func rectOf(w models.DashboardWidget) gridRect {
	return gridRect{ID: w.ID, Col: w.GridCol, Row: w.GridRow, W: w.GridW, H: w.GridH}
}

func gridBottom(rs []gridRect) int {
	b := 0
	for _, r := range rs {
		if r.bottom() > b {
			b = r.bottom()
		}
	}
	return b
}

func (s *Store) saveGrid(layoutID int64, rs []gridRect) error {
	sortReading(rs)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, r := range rs {
		if _, err := tx.Exec(`UPDATE dashboard_widgets SET grid_col=?, grid_row=?, grid_w=?, grid_h=?, sort_order=?, width=?
			WHERE id=? AND layout_id=?`, r.Col, r.Row, r.W, r.H, i, widthForSpan(r.W), r.ID, layoutID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ensureGrid places widgets saved before the cell grid existed (grid_w = 0),
// packing them in their old order below anything already placed.
func (s *Store) ensureGrid(layoutID int64) error {
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM dashboard_widgets WHERE layout_id=? AND grid_w<=0`, layoutID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	widgets, err := s.ListWidgets(layoutID)
	if err != nil {
		return err
	}
	var placed, pending []gridRect
	for _, w := range widgets {
		if w.GridW > 0 {
			placed = append(placed, clampRect(rectOf(w)))
			continue
		}
		pending = append(pending, gridRect{ID: w.ID, W: spanForWidth(w.WidgetType, w.Width), H: defaultHeight(w.WidgetType)})
	}
	packShelves(pending, gridBottom(placed)+1)
	return s.saveGrid(layoutID, pushDown(append(placed, pending...), 0))
}

func (s *Store) ownedWidgetRects(personID, widgetID int64) (int64, []gridRect, error) {
	w, err := s.GetWidget(widgetID)
	if err != nil {
		return 0, nil, err
	}
	if err := s.layoutOwnedBy(w.LayoutID, personID); err != nil {
		return 0, nil, err
	}
	if err := s.ensureGrid(w.LayoutID); err != nil {
		return 0, nil, err
	}
	widgets, err := s.ListWidgets(w.LayoutID)
	if err != nil {
		return 0, nil, err
	}
	rs := make([]gridRect, 0, len(widgets))
	for _, x := range widgets {
		rs = append(rs, rectOf(x))
	}
	return w.LayoutID, rs, nil
}

// PlaceWidget moves/resizes one widget to the given cells (clamped to the grid).
// The placed widget wins; anything it would overlap is pushed straight down.
// Returns the layout's widgets in reading order.
func (s *Store) PlaceWidget(personID, widgetID int64, col, row, w, h int) ([]models.DashboardWidget, error) {
	layoutID, rs, err := s.ownedWidgetRects(personID, widgetID)
	if err != nil {
		return nil, err
	}
	found := false
	for i := range rs {
		if rs[i].ID == widgetID {
			rs[i] = clampRect(gridRect{ID: widgetID, Col: col, Row: row, W: w, H: h})
			found = true
		}
	}
	if !found {
		return nil, sql.ErrNoRows
	}
	if err := s.saveGrid(layoutID, pushDown(rs, widgetID)); err != nil {
		return nil, err
	}
	s.touchLayout(layoutID)
	return s.ListWidgets(layoutID)
}

// CompactDashboard floats every widget on the person's layout upward to close gaps.
func (s *Store) CompactDashboard(personID int64) error {
	layout, err := s.EnsurePersonDashboard(personID)
	if err != nil {
		return err
	}
	widgets, err := s.ListWidgets(layout.ID)
	if err != nil {
		return err
	}
	rs := make([]gridRect, 0, len(widgets))
	for _, w := range widgets {
		rs = append(rs, clampRect(rectOf(w)))
	}
	if err := s.saveGrid(layout.ID, compactUp(pushDown(rs, 0))); err != nil {
		return err
	}
	s.touchLayout(layout.ID)
	return nil
}

// ResetDashboard replaces the person's widgets with a fresh copy of the household default.
func (s *Store) ResetDashboard(personID int64) error {
	layout, err := s.EnsurePersonDashboard(personID)
	if err != nil {
		return err
	}
	def, err := s.EnsureHouseholdDefault()
	if err != nil {
		return err
	}
	if def.ID == layout.ID {
		return fmt.Errorf("cannot reset the household default")
	}
	widgets, err := s.ListWidgets(def.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM dashboard_widgets WHERE layout_id=?`, layout.ID); err != nil {
		return err
	}
	if err := copyWidgets(tx, widgets, layout.ID, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.touchLayout(layout.ID)
	return nil
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func copyWidgets(db execer, widgets []models.DashboardWidget, layoutID int64, now string) error {
	for _, w := range widgets {
		cfg := w.ConfigJSON
		if cfg == "" {
			cfg = "{}"
		}
		if _, err := db.Exec(`INSERT INTO dashboard_widgets(layout_id, widget_type, width, sort_order, grid_col, grid_row, grid_w, grid_h, config_json, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, layoutID, w.WidgetType, w.Width, w.SortOrder, w.GridCol, w.GridRow, w.GridW, w.GridH, cfg, now); err != nil {
			return err
		}
	}
	return nil
}
