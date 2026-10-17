package store_test

import (
	"testing"
	"time"

	"finance.home/internal/models"
	"finance.home/internal/store"
)

func TestPersonalDashboardCopyOnceThenDiverge(t *testing.T) {
	s := setupStore(t)
	people, err := s.People()
	if err != nil || len(people) < 2 {
		t.Fatal("need two people")
	}
	a, b := people[0].ID, people[1].ID

	la, err := s.EnsurePersonDashboard(a)
	if err != nil {
		t.Fatal(err)
	}
	lb, err := s.EnsurePersonDashboard(b)
	if err != nil {
		t.Fatal(err)
	}
	if la.ID == lb.ID {
		t.Fatal("profiles should have separate layouts")
	}
	wa, err := s.ListWidgets(la.ID)
	if err != nil {
		t.Fatal(err)
	}
	wb, err := s.ListWidgets(lb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wa) < 4 || len(wb) != len(wa) {
		t.Fatalf("expected copied curated defaults; a=%d b=%d", len(wa), len(wb))
	}

	// Diverge: remove one from A, add cash flow
	if err := s.RemoveWidget(a, wa[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWidget(a, store.AddWidgetInput{WidgetType: store.WidgetCashFlow}); err != nil {
		t.Fatal(err)
	}
	wa2, _ := s.ListWidgets(la.ID)
	wb2, _ := s.ListWidgets(lb.ID)
	if len(wa2) == len(wb2) && wa2[0].WidgetType == wb2[0].WidgetType {
		// lengths may coincide; ensure B unchanged count from original
		if len(wb2) != len(wb) {
			t.Fatalf("B should be unchanged; was %d now %d", len(wb), len(wb2))
		}
	}
	if len(wb2) != len(wb) {
		t.Fatalf("B layout mutated; was %d now %d", len(wb), len(wb2))
	}

	// Second ensure does not re-copy
	la2, err := s.EnsurePersonDashboard(a)
	if err != nil {
		t.Fatal(err)
	}
	if la2.ID != la.ID {
		t.Fatal("re-ensure created new layout")
	}
	wa3, _ := s.ListWidgets(la.ID)
	if len(wa3) != len(wa2) {
		t.Fatalf("re-ensure re-copied; %d vs %d", len(wa3), len(wa2))
	}
}

func TestDashboardRangeAndBuild(t *testing.T) {
	s := setupStore(t)
	people, _ := s.People()
	pid := people[0].ID
	layout, err := s.EnsurePersonDashboard(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDashboardRange(pid, "last_12"); err != nil {
		t.Fatal(err)
	}
	layout, _ = s.GetLayout(layout.ID)
	if layout.RangePreset != "last_12" {
		t.Fatalf("range=%s", layout.RangePreset)
	}

	h, _ := s.Household()
	now := time.Date(2024, 10, 15, 12, 0, 0, 0, time.UTC)
	dash, err := s.BuildPersonalDashboard(pid, h, now)
	if err != nil {
		t.Fatal(err)
	}
	if dash.Layout.RangePreset != "last_12" {
		t.Fatalf("build range=%s", dash.Layout.RangePreset)
	}
	if dash.RangeFrom == "" || dash.RangeTo == "" {
		t.Fatal("expected resolved range")
	}
	if len(dash.Widgets) == 0 {
		t.Fatal("no widgets rendered")
	}
}

type cell struct{ col, row, w, h int }

func cellOf(w models.DashboardWidget) cell { return cell{w.GridCol, w.GridRow, w.GridW, w.GridH} }

func assertValidGrid(t *testing.T, ws []models.DashboardWidget) {
	t.Helper()
	for i, a := range ws {
		if a.GridW < store.GridMinW || a.GridH < store.GridMinH || a.GridCol < 1 || a.GridRow < 1 ||
			a.GridCol+a.GridW-1 > store.GridCols {
			t.Fatalf("widget %d out of bounds: %+v", a.ID, cellOf(a))
		}
		for _, b := range ws[i+1:] {
			if a.GridCol < b.GridCol+b.GridW && b.GridCol < a.GridCol+a.GridW &&
				a.GridRow < b.GridRow+b.GridH && b.GridRow < a.GridRow+a.GridH {
				t.Fatalf("widgets %d %+v and %d %+v overlap", a.ID, cellOf(a), b.ID, cellOf(b))
			}
		}
	}
}

func byID(ws []models.DashboardWidget) map[int64]models.DashboardWidget {
	m := make(map[int64]models.DashboardWidget, len(ws))
	for _, w := range ws {
		m[w.ID] = w
	}
	return m
}

func TestDashboardGridDefaultsAndAdd(t *testing.T) {
	s := setupStore(t)
	people, _ := s.People()
	pid := people[0].ID
	layout, err := s.EnsurePersonDashboard(pid)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := s.ListWidgets(layout.ID)
	assertValidGrid(t, ws)
	if ws[0].WidgetType != store.WidgetLeftToSpend || cellOf(ws[0]) != (cell{1, 1, 8, 3}) {
		t.Fatalf("first default = %s %+v", ws[0].WidgetType, cellOf(ws[0]))
	}
	bottom := 0
	for _, w := range ws {
		if w.GridRow+w.GridH > bottom {
			bottom = w.GridRow + w.GridH
		}
	}
	id, err := s.AddWidget(pid, store.AddWidgetInput{WidgetType: store.WidgetGoals, Width: "s"})
	if err != nil {
		t.Fatal(err)
	}
	w, _ := s.GetWidget(id)
	if cellOf(*w) != (cell{1, bottom, 4, 4}) {
		t.Fatalf("added widget at %+v, want below bottom row %d", cellOf(*w), bottom)
	}
}

func TestPlaceWidgetPushesOverlapsDown(t *testing.T) {
	s := setupStore(t)
	people, _ := s.People()
	pid := people[0].ID
	layout, _ := s.EnsurePersonDashboard(pid)
	before := byID(mustWidgets(t, s, layout.ID))

	var lts, nw, insights models.DashboardWidget
	for _, w := range before {
		switch w.WidgetType {
		case store.WidgetLeftToSpend:
			lts = w
		case store.WidgetNetWorth:
			nw = w
		case store.WidgetInsights:
			insights = w
		}
	}
	// Drop net worth (4x3) onto the top-left corner of left-to-spend.
	got, err := s.PlaceWidget(pid, nw.ID, 1, 1, 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertValidGrid(t, got)
	after := byID(got)
	if cellOf(after[nw.ID]) != (cell{1, 1, 4, 3}) {
		t.Fatalf("placed widget moved: %+v", cellOf(after[nw.ID]))
	}
	if c := cellOf(after[lts.ID]); c.col != 1 || c.row != 4 || c.w != 8 {
		t.Fatalf("left to spend should slide straight down to row 4, got %+v", c)
	}
	if c := cellOf(after[insights.ID]); c.row != 7 || c.col != 1 {
		t.Fatalf("insights should cascade below left to spend, got %+v", c)
	}
	if got[0].ID != nw.ID || got[0].SortOrder != 0 {
		t.Fatalf("reading order should start with placed widget, got %d", got[0].ID)
	}

	// Moving into empty space leaves everything else alone.
	snapshot := byID(got)
	got, err = s.PlaceWidget(pid, nw.ID, 9, 40, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertValidGrid(t, got)
	for id, w := range byID(got) {
		if id != nw.ID && cellOf(w) != cellOf(snapshot[id]) {
			t.Fatalf("widget %d moved from %+v to %+v", id, cellOf(snapshot[id]), cellOf(w))
		}
	}

	// Growing taller pushes the widget it now covers.
	got, err = s.PlaceWidget(pid, lts.ID, 1, 4, 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	assertValidGrid(t, got)
	if c := cellOf(byID(got)[insights.ID]); c.row != 8 {
		t.Fatalf("insights should be pushed to row 8 by the resize, got %+v", c)
	}
}

func TestPlaceWidgetClampsAndOwnership(t *testing.T) {
	s := setupStore(t)
	people, _ := s.People()
	a, b := people[0].ID, people[1].ID
	layout, _ := s.EnsurePersonDashboard(a)
	_, _ = s.EnsurePersonDashboard(b)
	ws := mustWidgets(t, s, layout.ID)
	id := ws[len(ws)-1].ID

	got, err := s.PlaceWidget(a, id, 11, 0, 6, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c := cellOf(byID(got)[id]); c != (cell{7, 1, 6, store.GridMinH}) {
		t.Fatalf("clamp = %+v", c)
	}
	got, _ = s.PlaceWidget(a, id, -3, 2, 99, 99)
	if c := cellOf(byID(got)[id]); c != (cell{1, 2, store.GridCols, store.GridMaxH}) {
		t.Fatalf("clamp wide = %+v", c)
	}
	assertValidGrid(t, got)

	if _, err := s.PlaceWidget(b, id, 1, 1, 4, 4); err == nil {
		t.Fatal("other profile should not be able to move this widget")
	}
}

func TestLegacyLayoutBackfill(t *testing.T) {
	s := setupStore(t)
	people, _ := s.People()
	pid := people[0].ID
	layout, _ := s.EnsurePersonDashboard(pid)
	if _, err := s.DB.Exec(`DELETE FROM dashboard_widgets WHERE layout_id=?`, layout.ID); err != nil {
		t.Fatal(err)
	}
	legacy := []struct{ typ, width string }{
		{store.WidgetLeftToSpend, "l"},
		{store.WidgetInsights, "m"},
		{store.WidgetBudgetHealth, "m"},
		{store.WidgetBills, "m"},
		{store.WidgetNetWorth, "s"},
		{store.WidgetRecent, "l"},
	}
	for i, l := range legacy {
		if _, err := s.DB.Exec(`INSERT INTO dashboard_widgets(layout_id, widget_type, width, sort_order, config_json)
			VALUES (?,?,?,?, '{}')`, layout.ID, l.typ, l.width, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.EnsurePersonDashboard(pid); err != nil {
		t.Fatal(err)
	}
	ws := mustWidgets(t, s, layout.ID)
	assertValidGrid(t, ws)
	want := []cell{{1, 1, 12, 3}, {1, 4, 6, 4}, {7, 4, 6, 3}, {1, 8, 6, 4}, {7, 8, 4, 2}, {1, 12, 12, 8}}
	for i, w := range ws {
		if w.WidgetType != legacy[i].typ || cellOf(w) != want[i] {
			t.Fatalf("legacy[%d] %s at %+v, want %s at %+v", i, w.WidgetType, cellOf(w), legacy[i].typ, want[i])
		}
	}
}

func TestCompactAndResetDashboard(t *testing.T) {
	s := setupStore(t)
	people, _ := s.People()
	pid := people[0].ID
	layout, _ := s.EnsurePersonDashboard(pid)
	orig := mustWidgets(t, s, layout.ID)

	first := orig[0]
	if _, err := s.PlaceWidget(pid, first.ID, first.GridCol, 50, first.GridW, first.GridH); err != nil {
		t.Fatal(err)
	}
	if err := s.CompactDashboard(pid); err != nil {
		t.Fatal(err)
	}
	ws := mustWidgets(t, s, layout.ID)
	assertValidGrid(t, ws)
	minRow := 999
	for _, w := range ws {
		if w.GridRow < minRow {
			minRow = w.GridRow
		}
		if w.ID == first.ID && w.GridRow >= 50 {
			t.Fatalf("compact left widget at row %d", w.GridRow)
		}
	}
	if minRow != 1 {
		t.Fatalf("compact should start at row 1, got %d", minRow)
	}

	if _, err := s.AddWidget(pid, store.AddWidgetInput{WidgetType: store.WidgetAccounts}); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetDashboard(pid); err != nil {
		t.Fatal(err)
	}
	reset := mustWidgets(t, s, layout.ID)
	if len(reset) != len(orig) {
		t.Fatalf("reset count %d want %d", len(reset), len(orig))
	}
	for i := range reset {
		if reset[i].WidgetType != orig[i].WidgetType || cellOf(reset[i]) != cellOf(orig[i]) {
			t.Fatalf("reset[%d] = %s %+v want %s %+v", i, reset[i].WidgetType, cellOf(reset[i]), orig[i].WidgetType, cellOf(orig[i]))
		}
	}
}

func mustWidgets(t *testing.T, s *store.Store, layoutID int64) []models.DashboardWidget {
	t.Helper()
	ws, err := s.ListWidgets(layoutID)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestResolveDatesLast12(t *testing.T) {
	s := setupStore(t)
	hh, err := s.Household()
	if err != nil {
		t.Fatal(err)
	}
	f := store.QueryFilter{DatePreset: "last_12"}
	now := time.Date(2024, 10, 17, 12, 0, 0, 0, time.UTC)
	f.ResolveDates(hh, now)
	if f.DateFrom != "2023-10-18" || f.DateTo != "2024-10-17" {
		t.Fatalf("from=%s to=%s", f.DateFrom, f.DateTo)
	}
}
