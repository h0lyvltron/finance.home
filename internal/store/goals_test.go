package store_test

import (
	"testing"
	"time"

	"finance.home/internal/store"
)

func TestSavingsGoalPaceAndCatchUp(t *testing.T) {
	s := setupStore(t)
	savID, err := s.CreateAccount(store.AccountInput{
		Name: "Emergency", Type: "savings", Owner: "joint",
		AnchorBalance: 200000, AnchorDate: "2024-01-01", // $2000
	})
	if err != nil {
		t.Fatal(err)
	}
	people, _ := s.People()
	createdBy := people[0].ID
	incCat := firstIncomeCat(t, s)

	// Deposit $500 on 2024-06-01
	_, err = s.CreateTransaction(store.TxInput{
		AccountID: savID, PayeeName: "Transfer in", Amount: 50000,
		TransactionDate: "2024-06-01", Person: "shared", CategoryID: &incCat, CreatedBy: createdBy,
	})
	if err != nil {
		t.Fatal(err)
	}

	goalID, err := s.CreateGoal(store.GoalInput{
		Kind: "savings", Name: "Emergency fund", TargetCents: 1000000, // $10k
		TargetDate: "2024-12-01", PaceWindowDays: 90, AccountIDs: []int64{savID},
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.GetGoal(goalID)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2024, 7, 1, 12, 0, 0, 0, time.UTC)
	prog, err := s.GoalProgress(*g, now)
	if err != nil {
		t.Fatal(err)
	}
	if prog.CurrentCents != 250000 {
		t.Fatalf("current=%d want 250000", prog.CurrentCents)
	}
	if prog.Pct < 24 || prog.Pct > 26 {
		t.Fatalf("pct=%.1f want ~25", prog.Pct)
	}
	// Still far from $10k by Dec → behind with weekly catch-up
	if !prog.Behind || prog.WeeklyCatchUp <= 0 {
		t.Fatalf("expected behind with catch-up; behind=%v catch=%d needed=%d recent=%d",
			prog.Behind, prog.WeeklyCatchUp, prog.NeededPerWeek, prog.RecentPerWeek)
	}
}

func TestDebtBalanceSeries(t *testing.T) {
	s := setupStore(t)
	cardID, err := s.CreateAccount(store.AccountInput{
		Name: "Visa", Type: "credit", Owner: "joint",
		AnchorBalance: -50000, AnchorDate: "2024-01-01", // $500 owed
	})
	if err != nil {
		t.Fatal(err)
	}
	people, _ := s.People()
	payCat := firstExpenseCat(t, s)
	_, err = s.CreateTransaction(store.TxInput{
		AccountID: cardID, PayeeName: "Payment", Amount: 20000, // pay down
		TransactionDate: "2024-03-15", Person: "shared", CategoryID: &payCat, CreatedBy: people[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	goalID, err := s.CreateGoal(store.GoalInput{
		Kind: "debt", Name: "Pay Visa", TargetCents: 0,
		PaceWindowDays: 90, AccountIDs: []int64{cardID},
	})
	if err != nil {
		t.Fatal(err)
	}
	g, _ := s.GetGoal(goalID)
	pts, err := s.DebtBalanceSeries(*g, "2024-01-01", "2024-04-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) < 2 {
		t.Fatalf("expected series, got %d", len(pts))
	}
	if pts[0].Amount != 50000 {
		t.Fatalf("start owed=%d", pts[0].Amount)
	}
	last := pts[len(pts)-1].Amount
	if last != 30000 {
		t.Fatalf("end owed=%d want 30000", last)
	}
}

func TestNetWorthAndSavingsRateAndRecap(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	people, _ := s.People()
	createdBy := people[0].ID
	incomeCat := firstIncomeCat(t, s)
	expCat := firstExpenseCat(t, s)

	_, err := s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "Job", Amount: 500000,
		TransactionDate: "2024-06-05", Person: "shared", CategoryID: &incomeCat, CreatedBy: createdBy,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "Store", Amount: -100000,
		TransactionDate: "2024-06-10", Person: "shared", CategoryID: &expCat, CreatedBy: createdBy,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateHouseholdPlan(500000, 100000); err != nil {
		t.Fatal(err)
	}
	h, _ := s.Household()

	nw, err := s.NetWorthSeries("2024-06-01", "2024-06-30", true)
	if err != nil {
		t.Fatal(err)
	}
	if nw.End <= nw.Start {
		t.Fatalf("expected NW up: start=%d end=%d", nw.Start, nw.End)
	}

	f := store.QueryFilter{DatePreset: "custom", DateFrom: "2024-06-01", DateTo: "2024-06-30"}
	sr, err := s.SavingsRate(f, h, time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if sr.Saved != 400000 {
		t.Fatalf("saved=%d", sr.Saved)
	}
	if !sr.HasTarget || sr.TargetCents != 100000 {
		t.Fatalf("target=%d has=%v", sr.TargetCents, sr.HasTarget)
	}

	recap, err := s.BuildMonthlyRecap(h, "2024-06", time.Date(2024, 6, 28, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if recap.SavingsRate.Saved != 400000 {
		t.Fatalf("recap saved=%d", recap.SavingsRate.Saved)
	}
	if recap.Watch == nil && len(recap.Wins) == 0 {
		t.Fatal("expected wins or watch on recap")
	}
}

func TestGoalCatchUpInsight(t *testing.T) {
	s := setupStore(t)
	savID, err := s.CreateAccount(store.AccountInput{
		Name: "Vacation", Type: "savings", Owner: "joint",
		AnchorBalance: 10000, AnchorDate: "2024-01-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateGoal(store.GoalInput{
		Kind: "savings", Name: "Trip", TargetCents: 500000,
		TargetDate: "2024-08-01", PaceWindowDays: 60, AccountIDs: []int64{savID},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	ins := s.GoalBehindInsight(now)
	if ins == nil || ins.Kind != "goal" || ins.Amount <= 0 {
		t.Fatalf("expected goal catch-up insight, got %+v", ins)
	}
	h, _ := s.Household()
	home, err := s.HomeInsights(h, now, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(home) == 0 || len(home) > 3 {
		t.Fatalf("insights=%d", len(home))
	}
	// last should be positive tone
	if home[len(home)-1].Tone != "ok" {
		t.Fatalf("last insight tone=%s", home[len(home)-1].Tone)
	}
}
