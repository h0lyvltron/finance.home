package handlers

import (
	"strings"
	"testing"

	"finance.home/internal/auth"
	"finance.home/internal/models"
	"finance.home/internal/store"
	"finance.home/internal/web"
)

func TestHomeTemplateRendersScoped(t *testing.T) {
	r, err := web.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	var widgets []store.WidgetView
	for _, c := range store.WidgetCatalog() {
		widgets = append(widgets, store.WidgetView{
			DashboardWidget: models.DashboardWidget{WidgetType: c.Type, Label: c.Label},
			HouseholdOnly:   c.Type == store.WidgetLeftToSpend || c.Type == store.WidgetBudgetHealth || c.Type == store.WidgetInsights,
		})
	}
	d := pageData{
		Household: &models.Household{Name: "Home"},
		Session:   &auth.Session{PersonName: "Mama"},
		Currency:  "USD",
		NameA:     "Mama",
		NameB:     "Papa",
		PersonalDash: store.PersonalDashboard{
			Layout:        models.DashboardLayout{RangePreset: "this_month", ViewScope: "a"},
			Widgets:       widgets,
			ScopeAccounts: []int64{1, 4},
		},
	}
	html, err := r.RenderString("home", d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`/transactions?account=1&account=4`, `aria-pressed="true">Mama<`, `>Household</span>`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestTransferAuditTemplateRenders(t *testing.T) {
	r, err := web.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	out := store.AuditTx{ID: 1, AccountID: 5, AccountName: "GCU", Date: "2026-10-03", Amount: -73700, Payee: "To Loan 02", CategoryName: "Loan payment"}
	loan := store.AuditTx{ID: 2, AccountID: 11, AccountName: "Auto Loan", Date: "2026-10-03", Amount: 63776, CategoryName: "Transfer"}
	xfer := int64(9)
	audit := store.TransferAudit{
		Missed:      []store.MissedPair{{Out: out, In: loan, Score: 60, HighConfidence: true}},
		Suspicious:  []store.SuspiciousPair{{TransferID: xfer, From: out, To: loan, Reason: "no shared wording", Suggest: &store.RepairSuggestion{A: out, B: loan, Breaks: []int64{xfer}}}},
		Loans:       []store.LoanPaymentFix{{Out: out, Loan: loan, Interest: 9924, Exact: true}, {Out: out, Loan: loan, Interest: 100, Note: "off by $99"}},
		OneSided:    []store.OneSidedTx{{AuditTx: out, SuggestedAccountID: 11}},
		StaleSplits: 3,
	}
	d := pageData{
		Household:      &models.Household{Name: "Home"},
		Currency:       "USD",
		CSRF:           "tok",
		Accounts:       []models.Account{{ID: 11, Name: "Auto Loan"}},
		Audit:          &audit,
		OneSidedGroups: groupOneSided(audit.OneSided),
		UndoID:         "u1",
		Message:        "Linked.",
	}
	html, err := r.RenderString("transfer-audit", d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Link 1 confident", "Re-pair as suggested", "Fix 1 exact", "Clean up 3",
		`<option value="11" selected>Auto Loan</option>`, `name="undo_id" value="u1"`, "Fix with this interest"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
}
