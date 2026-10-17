package store_test

import (
	"testing"
	"time"

	"finance.home/internal/store"
)

func TestFilterAndReports(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catID := firstExpenseCat(t, s)
	h, err := s.Household()
	if err != nil {
		t.Fatal(err)
	}
	people, _ := s.People()
	var createdBy int64 = 1
	if len(people) > 0 {
		createdBy = people[0].ID
	}

	incomeCats, err := s.ListCategories("income", false)
	if err != nil || len(incomeCats) == 0 {
		t.Fatal("need income category")
	}
	incID := incomeCats[0].ID

	_, err = s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "Payroll", Amount: 300000,
		TransactionDate: "2024-06-01", Person: "shared", CategoryID: &incID, CreatedBy: createdBy,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "Coffee Shop", Amount: -525,
		TransactionDate: "2024-06-02", Person: "a", CategoryID: &catID, CreatedBy: createdBy,
		TagNames: []string{"cafe"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "Groceries", Amount: -4500,
		TransactionDate: "2024-06-03", Person: "shared", CategoryID: &catID, CreatedBy: createdBy,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Descendants expand
	cats, _ := s.ListCategories("expense", false)
	var parentID int64
	for _, c := range cats {
		if c.ParentID == nil && !c.Protected {
			parentID = c.ID
			break
		}
	}
	if parentID > 0 {
		expanded, err := s.ExpandCategories([]int64{parentID})
		if err != nil {
			t.Fatal(err)
		}
		if len(expanded) < 1 {
			t.Fatal("expected at least parent")
		}
	}

	f := store.QueryFilter{
		DatePreset: "custom", DateFrom: "2024-06-01", DateTo: "2024-06-30",
		People: []string{"a"}, Limit: 50,
	}
	txs, err := s.ListTransactions(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 || txs[0].PayeeName != "Coffee Shop" {
		t.Fatalf("person filter: %+v", txs)
	}

	f2 := store.QueryFilter{DatePreset: "custom", DateFrom: "2024-06-01", DateTo: "2024-06-30", Text: "coffee", Limit: 50}
	txs, err = s.ListTransactions(f2)
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 {
		t.Fatalf("text filter got %d", len(txs))
	}

	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	rangeF := store.QueryFilter{DatePreset: "custom", DateFrom: "2024-06-01", DateTo: "2024-06-30"}
	cf, err := s.CashFlow(rangeF, "month", h, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(cf) != 1 {
		t.Fatalf("cashflow periods: %+v", cf)
	}
	if cf[0].Income != 300000 || cf[0].Expense != 5025 {
		t.Fatalf("cashflow amounts: %+v", cf[0])
	}

	segs, grand, err := s.SpendingExplore(rangeF, "payee", 8, h, now)
	if err != nil {
		t.Fatal(err)
	}
	if grand != 5025 {
		t.Fatalf("grand %d", grand)
	}
	if len(segs) < 2 {
		t.Fatalf("segments %+v", segs)
	}

	pt, err := s.SegmentPivot(rangeF, "category", "month", h, now)
	if err != nil {
		t.Fatal(err)
	}
	if pt.Grand != 5025 {
		t.Fatalf("pivot grand %d", pt.Grand)
	}

	id, err := s.SaveFilter(createdBy, "June coffee", store.QueryFilter{
		DatePreset: "custom", DateFrom: "2024-06-01", DateTo: "2024-06-30", Payee: "Coffee",
	})
	if err != nil {
		t.Fatal(err)
	}
	sf, err := s.GetSavedFilter(id)
	if err != nil {
		t.Fatal(err)
	}
	got := store.FilterFromSaved(sf)
	if got.Payee != "Coffee" {
		t.Fatalf("saved %+v", got)
	}
}
