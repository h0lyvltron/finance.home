package store_test

import (
	"sort"
	"testing"
	"time"

	"finance.home/internal/store"
)

func TestDashboardScopeFiltersAccountWidgets(t *testing.T) {
	s := setupStore(t)
	people, err := s.People()
	if err != nil || len(people) < 2 {
		t.Fatal("need two people")
	}
	var mama int64
	for _, p := range people {
		if p.Slot == "a" {
			mama = p.ID
		}
	}
	h, err := s.Household()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	today := now.Format("2006-01-02")

	mk := func(name, typ, owner string, anchor int64) int64 {
		id, err := s.CreateAccount(store.AccountInput{Name: name, Type: typ, Owner: owner, AnchorBalance: anchor, AnchorDate: "2020-01-01"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	joint := mk("STCU", "checking", "joint", 100000)
	mom := mk("Capital One (Mom)", "credit", "a", -20000)
	dad := mk("Global CU", "checking", "b", 500000)

	groceries := catByName(t, s, "Groceries")
	for _, acct := range []int64{joint, mom, dad} {
		if _, err := s.CreateTransaction(store.TxInput{AccountID: acct, PayeeName: "Store", Amount: -1000,
			TransactionDate: today, CategoryID: &groceries, CreatedBy: mama}); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []store.GoalInput{
		{Kind: "savings", Name: "Dad truck", TargetCents: 100000, AccountIDs: []int64{dad}},
		{Kind: "debt", Name: "Mom card", TargetCents: 20000, AccountIDs: []int64{mom}},
		{Kind: "savings", Name: "Vacation", TargetCents: 50000, AccountIDs: []int64{joint}},
	} {
		if _, err := s.CreateGoal(g); err != nil {
			t.Fatal(err)
		}
	}
	due := now.AddDate(0, 0, 3).Format("2006-01-02")
	for _, sc := range []store.ScheduleInput{
		{Name: "Dad phone", AmountCents: 5000, Kind: "expense", AccountID: &dad, CategoryID: &groceries, Frequency: "monthly", IntervalN: 1, NextDue: due},
		{Name: "Internet", AmountCents: 7000, Kind: "expense", CategoryID: &groceries, Frequency: "monthly", IntervalN: 1, NextDue: due},
	} {
		if _, err := s.CreateScheduleTemplate(sc); err != nil {
			t.Fatal(err)
		}
	}

	for _, w := range []string{store.WidgetAccounts, store.WidgetGoals} {
		if _, err := s.AddWidget(mama, store.AddWidgetInput{WidgetType: w}); err != nil {
			t.Fatal(err)
		}
	}

	ids, err := s.ScopeAccountIDs("a")
	if err != nil || len(ids) != 2 || !ids[joint] || !ids[mom] {
		t.Fatalf("scope a accounts %v err=%v", ids, err)
	}
	if ids, _ := s.ScopeAccountIDs("household"); ids != nil {
		t.Fatalf("household scope should mean all accounts, got %v", ids)
	}

	if err := s.SetDashboardScope(mama, "a"); err != nil {
		t.Fatal(err)
	}
	dash, err := s.BuildPersonalDashboard(mama, h, now)
	if err != nil {
		t.Fatal(err)
	}
	if dash.Layout.ViewScope != "a" || len(dash.ScopeAccounts) != 2 {
		t.Fatalf("layout scope %q accounts %v", dash.Layout.ViewScope, dash.ScopeAccounts)
	}
	seen := map[string]bool{}
	for _, w := range dash.Widgets {
		seen[w.WidgetType] = true
		switch w.WidgetType {
		case store.WidgetAccounts:
			var names []string
			for _, a := range w.Accounts {
				names = append(names, a.Name)
			}
			sort.Strings(names)
			if len(names) != 2 || names[0] != "Capital One (Mom)" || names[1] != "STCU" {
				t.Errorf("accounts widget %v", names)
			}
		case store.WidgetNetWorth:
			if want := int64(100000 - 1000 - 20000 - 1000); w.NetWorth != want {
				t.Errorf("net worth %d, want %d", w.NetWorth, want)
			}
		case store.WidgetRecent:
			for _, tx := range w.Transactions {
				if tx.AccountID == dad {
					t.Errorf("recent shows Dad's account")
				}
			}
			if len(w.Transactions) != 2 {
				t.Errorf("recent has %d rows, want 2", len(w.Transactions))
			}
		case store.WidgetGoals:
			var names []string
			for _, g := range w.Goals {
				names = append(names, g.Goal.Name)
			}
			sort.Strings(names)
			if len(names) != 2 || names[0] != "Mom card" || names[1] != "Vacation" {
				t.Errorf("goals widget %v", names)
			}
		case store.WidgetBills:
			for _, b := range w.Bills {
				if b.TemplateName == "Dad phone" {
					t.Errorf("bills show Dad's scheduled bill")
				}
			}
			if len(w.Bills) == 0 {
				t.Errorf("unassigned bill should stay visible")
			}
		case store.WidgetLeftToSpend, store.WidgetBudgetHealth, store.WidgetInsights:
			if !w.HouseholdOnly {
				t.Errorf("%s should carry the Household badge", w.WidgetType)
			}
		}
	}
	for _, w := range []string{store.WidgetAccounts, store.WidgetNetWorth, store.WidgetRecent, store.WidgetGoals, store.WidgetBills} {
		if !seen[w] {
			t.Errorf("widget %s missing from layout", w)
		}
	}

	if err := s.SetDashboardScope(mama, "household"); err != nil {
		t.Fatal(err)
	}
	dash, err = s.BuildPersonalDashboard(mama, h, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range dash.Widgets {
		if w.WidgetType == store.WidgetAccounts && len(w.Accounts) != 3 {
			t.Errorf("household accounts widget has %d", len(w.Accounts))
		}
		if w.HouseholdOnly {
			t.Errorf("%s shouldn't be badged in household view", w.WidgetType)
		}
	}
}
