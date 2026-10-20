package store_test

import (
	"strings"
	"testing"
	"time"

	"finance.home/internal/models"
	"finance.home/internal/store"
)

func TestFillCashFlowGapsSkipsFutureAndFillsHoles(t *testing.T) {
	now := time.Date(2024, 6, 12, 15, 0, 0, 0, time.UTC)
	h := &models.Household{Timezone: "UTC"}
	in := []store.CashFlowPoint{{
		Period:  "2024-06-03",
		Label:   "Week of 2024-06-03",
		Income:  5000,
		Expense: 1200,
		Net:     3800,
	}}
	out := store.FillCashFlowGaps(in, "week", "2024-06-01", "2024-06-30", now, h)
	var periods []string
	for _, p := range out {
		periods = append(periods, p.Period)
		if p.Period >= "2024-06-17" {
			t.Fatalf("future week present: %s", p.Period)
		}
	}
	if strings.Join(periods, ",") != "2024-05-27,2024-06-03,2024-06-10" {
		t.Fatalf("periods %v", periods)
	}
	if out[0].Income != 0 || out[0].Expense != 0 {
		t.Fatalf("leading week should be zero: %+v", out[0])
	}
	if out[1].Income != 5000 || out[1].Expense != 1200 {
		t.Fatalf("data week changed: %+v", out[1])
	}
	if out[2].Income != 0 || !out[2].InProgress {
		t.Fatalf("current empty week: %+v", out[2])
	}

	withFuture := append([]store.CashFlowPoint{}, in...)
	withFuture = append(withFuture, store.CashFlowPoint{Period: "2024-06-24", Income: 100, Expense: 0, Net: 100})
	kept := store.FillCashFlowGaps(withFuture, "week", "2024-06-01", "2024-06-30", now, h)
	if kept[len(kept)-1].Period != "2024-06-24" || kept[len(kept)-1].Income != 100 {
		t.Fatalf("real future point dropped: %+v", kept)
	}
	for _, p := range kept[:len(kept)-1] {
		if p.Period >= "2024-06-17" {
			t.Fatalf("invented future week %s", p.Period)
		}
	}
}

func TestAccountBalanceBeforeAnchorWalksBack(t *testing.T) {
	s := setupStore(t)
	id, err := s.CreateAccount(store.AccountInput{
		Name: "Later", Type: "checking", Owner: "joint",
		AnchorBalance: 250000, AnchorDate: "2024-06-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.AccountBalanceAsOf(id, "2024-05-31")
	if err != nil {
		t.Fatal(err)
	}
	if before != 250000 {
		t.Fatalf("before anchor with no history = %d", before)
	}
	exp := firstExpenseCat(t, s)
	people, _ := s.People()
	for _, tx := range []store.TxInput{
		{AccountID: id, PayeeName: "Old", Amount: -10000, TransactionDate: "2024-05-10", CategoryID: &exp, CreatedBy: people[0].ID},
		{AccountID: id, PayeeName: "Recent", Amount: -2500, TransactionDate: "2024-05-31", CategoryID: &exp, CreatedBy: people[0].ID},
	} {
		if _, err := s.CreateTransaction(tx); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		date string
		want int64
	}{
		{"2024-05-31", 250000},
		{"2024-05-30", 252500},
		{"2024-05-09", 262500},
	} {
		got, err := s.AccountBalanceAsOf(id, c.date)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Fatalf("as of %s = %d, want %d", c.date, got, c.want)
		}
	}
	on, err := s.AccountBalanceAsOf(id, "2024-06-01")
	if err != nil {
		t.Fatal(err)
	}
	if on != 250000 {
		t.Fatalf("on anchor = %d", on)
	}
	cur, err := s.AccountBalance(id)
	if err != nil {
		t.Fatal(err)
	}
	if cur != 250000 {
		t.Fatalf("current balance = %d", cur)
	}
}

func TestNetWorthSeriesClosesOnRangeEnd(t *testing.T) {
	s := setupStore(t)
	if _, err := s.CreateAccount(store.AccountInput{
		Name: "Checking", Type: "checking", Owner: "joint",
		AnchorBalance: 100000, AnchorDate: "2024-01-01",
	}); err != nil {
		t.Fatal(err)
	}
	series, err := s.NetWorthSeries("2024-01-01", "2024-08-01", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Points) < 2 {
		t.Fatalf("points %d", len(series.Points))
	}
	if series.Points[0].Date != "2024-01-01" || series.Points[len(series.Points)-1].Date != "2024-08-01" {
		t.Fatalf("ends %+v %+v", series.Points[0], series.Points[len(series.Points)-1])
	}
	prev, _ := time.Parse("2006-01-02", series.Points[len(series.Points)-2].Date)
	last, _ := time.Parse("2006-01-02", series.Points[len(series.Points)-1].Date)
	gap := last.Sub(prev).Hours() / 24
	if gap < 7 {
		t.Fatalf("last segment is a short kink: %.0f days (%s to %s)", gap, prev.Format("2006-01-02"), last.Format("2006-01-02"))
	}
}

func TestSavingsRatePointsMatchCashFlow(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	inc := firstIncomeCat(t, s)
	exp := firstExpenseCat(t, s)
	people, _ := s.People()
	createdBy := people[0].ID
	txns := []store.TxInput{
		{AccountID: acctID, PayeeName: "May pay", Amount: 200000, TransactionDate: "2024-05-03", Person: "shared", CategoryID: &inc, CreatedBy: createdBy},
		{AccountID: acctID, PayeeName: "May store", Amount: -50000, TransactionDate: "2024-05-10", Person: "shared", CategoryID: &exp, CreatedBy: createdBy},
		{AccountID: acctID, PayeeName: "July pay", Amount: 210000, TransactionDate: "2024-07-02", Person: "shared", CategoryID: &inc, CreatedBy: createdBy},
	}
	for _, in := range txns {
		if _, err := s.CreateTransaction(in); err != nil {
			t.Fatal(err)
		}
	}
	h, err := s.Household()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2024, 7, 20, 12, 0, 0, 0, time.UTC)
	f := store.QueryFilter{DatePreset: "custom", DateFrom: "2024-05-01", DateTo: "2024-07-31"}
	cf, err := s.CashFlow(f, "month", h, now)
	if err != nil {
		t.Fatal(err)
	}
	sr, err := s.SavingsRate(f, h, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(sr.Points) != len(cf) || len(cf) != 3 {
		t.Fatalf("points sr=%d cf=%d", len(sr.Points), len(cf))
	}
	if cf[1].Period != "2024-06" || cf[1].Income != 0 || cf[1].Expense != 0 {
		t.Fatalf("june should be a zero bucket: %+v", cf[1])
	}
	var income, expense int64
	for i := range cf {
		if sr.Points[i].Period != cf[i].Period || sr.Points[i].Income != cf[i].Income || sr.Points[i].Expense != cf[i].Expense {
			t.Fatalf("point %d sr=%+v cf=%+v", i, sr.Points[i], cf[i])
		}
		if sr.Points[i].Saved != cf[i].Income-cf[i].Expense {
			t.Fatalf("saved %d", sr.Points[i].Saved)
		}
		income += cf[i].Income
		expense += cf[i].Expense
	}
	if sr.Income != income || sr.Expense != expense || sr.Saved != income-expense {
		t.Fatalf("totals sr=%+v income=%d expense=%d", sr, income, expense)
	}
	if !sr.Points[0].HasIncome || sr.Points[1].HasIncome || !sr.Points[2].HasIncome {
		t.Fatalf("has income flags %+v", sr.Points)
	}
}

func TestCashFlowChartDiverges(t *testing.T) {
	raw := store.CashFlowChart([]store.CashFlowPoint{{
		Period: "2024-06", Label: "Jun 2024", Income: 10000, Expense: 4000, Net: 6000,
	}}, false)
	if !strings.Contains(raw, `"Net"`) || !strings.Contains(raw, "-40") {
		t.Fatalf("chart %s", raw)
	}
}
