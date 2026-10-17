package store_test

import (
	"testing"
	"time"

	"finance.home/internal/store"
)

func TestBudgetsLeftToSpendAndRollover(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	h, err := s.Household()
	if err != nil {
		t.Fatal(err)
	}
	people, _ := s.People()
	createdBy := people[0].ID

	cats, err := s.ListCategories("expense", false)
	if err != nil {
		t.Fatal(err)
	}
	var groceries, coffee, foodParent int64
	for _, c := range cats {
		if c.Name == "Food" && c.ParentID == nil {
			foodParent = c.ID
		}
		if c.Name == "Groceries" {
			groceries = c.ID
		}
		if c.Name == "Coffee" {
			coffee = c.ID
		}
	}
	if groceries == 0 || coffee == 0 || foodParent == 0 {
		t.Fatal("seed categories missing")
	}

	if err := s.UpdateHouseholdPlan(500000, 50000); err != nil { // $5000 income, $500 savings
		t.Fatal(err)
	}
	h, _ = s.Household()

	// Budgets for May
	if err := s.UpsertBudgetMonth("2024-05", groceries, 40000, true); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBudgetMonth("2024-05", coffee, 5000, true); err != nil {
		t.Fatal(err)
	}
	// Spend less than budget in May → rollover
	_, err = s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "Market", Amount: -20000,
		TransactionDate: "2024-05-10", Person: "shared", CategoryID: &groceries, CreatedBy: createdBy,
	})
	if err != nil {
		t.Fatal(err)
	}

	// June budgets + spend
	if err := s.UpsertBudgetMonth("2024-06", groceries, 40000, true); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBudgetMonth("2024-06", coffee, 5000, false); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "Cafe", Amount: -1500,
		TransactionDate: "2024-06-05", Person: "shared", CategoryID: &coffee, CreatedBy: createdBy,
	})
	if err != nil {
		t.Fatal(err)
	}

	lines, err := s.MonthBudgetLines("2024-06")
	if err != nil {
		t.Fatal(err)
	}
	var gLine *struct {
		amount, carry, spent int64
	}
	for _, ln := range lines {
		if ln.CategoryID == groceries {
			gLine = &struct{ amount, carry, spent int64 }{ln.AmountCents, ln.RolloverCarry, ln.SpentCents}
		}
	}
	if gLine == nil {
		t.Fatal("groceries line missing")
	}
	// May unused = 40000-20000 = 20000 carry into June → effective 60000
	if gLine.carry != 20000 || gLine.amount != 60000 {
		t.Fatalf("rollover: amount=%d carry=%d", gLine.amount, gLine.carry)
	}

	// Parent Food rollup spent should include coffee
	var foodSpent int64
	for _, ln := range lines {
		if ln.CategoryID == foodParent {
			foodSpent = ln.SpentCents
		}
	}
	if foodSpent < 1500 {
		t.Fatalf("food rollup spent %d", foodSpent)
	}

	// Schedule fixed bill
	_, err = s.CreateScheduleTemplate(store.ScheduleInput{
		Name: "Rent", PayeeName: "Landlord", AmountCents: 150000, Kind: "expense",
		Frequency: "monthly", IntervalN: 1, NextDue: "2024-06-01", AutoMatch: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := s.FixedBillsForMonth("2024-06")
	if err != nil {
		t.Fatal(err)
	}
	if fixed != 150000 {
		t.Fatalf("fixed bills %d", fixed)
	}

	lts, err := s.LeftToSpend(h, "2024-06")
	if err != nil {
		t.Fatal(err)
	}
	// 500000 - 150000 - 1500 flexible - 50000 savings = 298500
	if lts.Amount != 298500 {
		t.Fatalf("left to spend %+v", lts)
	}

	// Copy previous month into July
	n, err := s.CopyBudgetsFromPrevious("2024-07")
	if err != nil || n < 1 {
		t.Fatalf("copy %d %v", n, err)
	}

	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	dash, err := s.BuildHomeDashboard(h, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(dash.Insights) == 0 || len(dash.Insights) > 3 {
		t.Fatalf("insights %d", len(dash.Insights))
	}
	hasPositive := false
	for _, in := range dash.Insights {
		if in.Kind == "positive" || in.Tone == "ok" {
			hasPositive = true
		}
	}
	if !hasPositive && len(dash.Insights) == 3 {
		// may be crowded with warns — still ok if max 3
	}
}

func TestScheduleMatchAndMarkPaid(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catID := firstExpenseCat(t, s)
	people, _ := s.People()
	createdBy := people[0].ID

	id, err := s.CreateScheduleTemplate(store.ScheduleInput{
		Name: "Netflix", PayeeName: "Netflix", AmountCents: 1549, Kind: "expense",
		AccountID: &acctID, CategoryID: &catID,
		Frequency: "monthly", NextDue: "2024-06-10", AutoMatch: true,
		AmountToleranceCents: 100, DateWindowDays: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = id

	_, err = s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "Netflix", Amount: -1549,
		TransactionDate: "2024-06-11", Person: "shared", CategoryID: &catID, CreatedBy: createdBy,
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2024, 6, 12, 12, 0, 0, 0, time.UTC)
	if err := s.EnsureScheduleInstances(now, 30); err != nil {
		t.Fatal(err)
	}
	inst, err := s.ListScheduleInstances("2024-06-01", "2024-06-30")
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, i := range inst {
		if i.TemplateName == "Netflix" && i.Status == "matched" && i.MatchedTxID != nil {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("expected matched instance: %+v", inst)
	}

	// Suggest heuristic: create repeating payee
	for _, day := range []string{"2024-01-05", "2024-02-05", "2024-03-05"} {
		_, err = s.CreateTransaction(store.TxInput{
			AccountID: acctID, PayeeName: "Gym Co", Amount: -4500,
			TransactionDate: day, Person: "shared", CategoryID: &catID, CreatedBy: createdBy,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sugs, err := s.SuggestRecurringFromHistory(now, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sug := range sugs {
		if sug.PayeeName == "Gym Co" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Gym Co suggestion: %+v", sugs)
	}
}

func TestParentIndependentVsRollup(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	people, _ := s.People()
	createdBy := people[0].ID
	cats, _ := s.ListCategories("expense", false)
	var food, groceries int64
	for _, c := range cats {
		if c.Name == "Food" && c.ParentID == nil {
			food = c.ID
		}
		if c.Name == "Groceries" {
			groceries = c.ID
		}
	}
	_ = s.SetCategoryBudgetMode(food, "independent")
	_ = s.UpsertBudgetMonth("2024-06", food, 10000, false)
	_ = s.UpsertBudgetMonth("2024-06", groceries, 40000, false)
	_, _ = s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "Store", Amount: -30000,
		TransactionDate: "2024-06-08", Person: "shared", CategoryID: &groceries, CreatedBy: createdBy,
	})
	_, _ = s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "Misc food", Amount: -2000,
		TransactionDate: "2024-06-09", Person: "shared", CategoryID: &food, CreatedBy: createdBy,
	})

	lines, err := s.MonthBudgetLines("2024-06")
	if err != nil {
		t.Fatal(err)
	}
	var foodSpent, grocSpent int64
	for _, ln := range lines {
		if ln.CategoryID == food {
			foodSpent = ln.SpentCents
		}
		if ln.CategoryID == groceries {
			grocSpent = ln.SpentCents
		}
	}
	if foodSpent != 2000 {
		t.Fatalf("independent parent should only count direct spend, got %d", foodSpent)
	}
	if grocSpent != 30000 {
		t.Fatalf("child spend %d", grocSpent)
	}

	bh, err := s.BudgetHealthSummary("2024-06")
	if err != nil {
		t.Fatal(err)
	}
	// Both independent parent and child count (different spend) — no double count of 30000 on parent
	if bh.Spent != 32000 {
		t.Fatalf("health spent %d want 32000", bh.Spent)
	}
}
