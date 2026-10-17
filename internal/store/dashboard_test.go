package store_test

import (
	"testing"
	"time"

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

func TestMoveWidgetAndRange(t *testing.T) {
	s := setupStore(t)
	people, _ := s.People()
	pid := people[0].ID
	layout, err := s.EnsurePersonDashboard(pid)
	if err != nil {
		t.Fatal(err)
	}
	widgets, _ := s.ListWidgets(layout.ID)
	if len(widgets) < 2 {
		t.Fatal("need widgets")
	}
	first, second := widgets[0], widgets[1]
	if err := s.MoveWidget(pid, second.ID, "up"); err != nil {
		t.Fatal(err)
	}
	after, _ := s.ListWidgets(layout.ID)
	if after[0].ID != second.ID || after[1].ID != first.ID {
		t.Fatalf("move up failed: got %d,%d want %d,%d", after[0].ID, after[1].ID, second.ID, first.ID)
	}
	if err := s.SetWidgetWidth(pid, after[0].ID, "s"); err != nil {
		t.Fatal(err)
	}
	w, _ := s.GetWidget(after[0].ID)
	if w.Width != "s" {
		t.Fatalf("width=%s", w.Width)
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
