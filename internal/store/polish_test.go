package store_test

import (
	"testing"
	"time"

	"finance.home/internal/models"
	"finance.home/internal/store"
)

func TestFiscalMonthBoundsAndYearMonth(t *testing.T) {
	h := &models.Household{Timezone: "UTC", MonthStartDay: 15}
	now := time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC)
	ym := store.YearMonth(h, now)
	if ym != "2024-05" {
		t.Fatalf("expected May fiscal month, got %s", ym)
	}
	from, to, err := store.FiscalMonthBounds(h, "2024-05")
	if err != nil {
		t.Fatal(err)
	}
	if from != "2024-05-15" || to != "2024-06-14" {
		t.Fatalf("bounds %s..%s", from, to)
	}
	now2 := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	if store.YearMonth(h, now2) != "2024-06" {
		t.Fatalf("expected June on the 15th")
	}
}

func TestResolveDatesMonthStartDay(t *testing.T) {
	f := store.QueryFilter{DatePreset: "this_month"}
	h := &models.Household{Timezone: "UTC", MonthStartDay: 15}
	now := time.Date(2024, 6, 20, 12, 0, 0, 0, time.UTC)
	f.ResolveDates(h, now)
	if f.DateFrom != "2024-06-15" || f.DateTo != "2024-06-20" {
		t.Fatalf("got %s..%s", f.DateFrom, f.DateTo)
	}
}

func TestShiftFilterLastYear(t *testing.T) {
	f := store.QueryFilter{DatePreset: "custom", DateFrom: "2024-06-01", DateTo: "2024-06-30"}
	ly := store.ShiftFilterLastYear(f)
	if ly.DateFrom != "2023-06-01" || ly.DateTo != "2023-06-30" || ly.DatePreset != "custom" {
		t.Fatalf("%+v", ly)
	}
}

func TestMergeCashFlowCompare(t *testing.T) {
	cur := []store.CashFlowPoint{{Period: "2024-06", Income: 100, Expense: 40, Net: 60}}
	ly := []store.CashFlowPoint{{Period: "2023-06", Income: 80, Expense: 50, Net: 30}}
	out := store.MergeCashFlowCompare(cur, ly, "month")
	if !out[0].HasCompare || out[0].CompareIncome != 80 || out[0].CompareExpense != 50 {
		t.Fatalf("%+v", out[0])
	}
}

func TestRecycleBinRestore(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catID := firstExpenseCat(t, s)
	people, _ := s.People()
	createdBy := people[0].ID
	txID, err := s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "Trash Me", Amount: -1200,
		TransactionDate: "2024-06-05", Person: "shared", CategoryID: &catID, CreatedBy: createdBy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDeleteTransaction(txID, createdBy); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListSoftDeleted(10)
	if err != nil || len(list) != 1 {
		t.Fatalf("bin: %v %d", err, len(list))
	}
	if err := s.RestoreTransaction(txID); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListSoftDeleted(10)
	if len(list) != 0 {
		t.Fatal("expected empty bin")
	}
}

func TestCoachingSnoozeAndFewerNudges(t *testing.T) {
	s := setupStore(t)
	people, _ := s.People()
	pid := people[0].ID
	if err := s.SnoozeCoaching(pid, "review", "2099-01-01"); err != nil {
		t.Fatal(err)
	}
	prefs, err := s.ListCoachingPrefs(pid)
	if err != nil || len(prefs) != 1 || prefs[0].Kind != "review" {
		t.Fatalf("%v %+v", err, prefs)
	}
	if err := s.UpdateHouseholdSettings(1, true); err != nil {
		t.Fatal(err)
	}
	h, _ := s.Household()
	if !h.FewerNudges || h.MonthStartDay != 1 {
		t.Fatalf("%+v", h)
	}
	insights, err := s.HomeInsightsFor(h, time.Now(), 3, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) > 1 {
		t.Fatalf("fewer nudges should cap at 1, got %d", len(insights))
	}
}

func TestDraftUpsertAndPromote(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catID := firstExpenseCat(t, s)
	people, _ := s.People()
	pid := people[0].ID
	aid, cid := acctID, catID
	id, err := s.UpsertDraft(models.DraftTransaction{
		PersonID: pid, AccountID: &aid, CategoryID: &cid,
		PayeeName: "Draft Coffee", Amount: -350, TransactionDate: "2024-06-08",
		Person: "shared", Kind: "expense", ClientKey: "test-key-1",
	})
	if err != nil || id == 0 {
		t.Fatalf("%v %d", err, id)
	}
	id2, err := s.UpsertDraft(models.DraftTransaction{
		PersonID: pid, AccountID: &aid, CategoryID: &cid,
		PayeeName: "Draft Coffee", Amount: -400, TransactionDate: "2024-06-08",
		Person: "shared", Kind: "expense", ClientKey: "test-key-1",
	})
	if err != nil || id2 != id {
		t.Fatalf("idempotent upsert: %v %d vs %d", err, id2, id)
	}
	txID, err := s.PromoteDraft(id, pid)
	if err != nil || txID == 0 {
		t.Fatalf("%v %d", err, txID)
	}
	drafts, _ := s.ListDrafts(pid)
	if len(drafts) != 0 {
		t.Fatal("draft should be gone after promote")
	}
}
