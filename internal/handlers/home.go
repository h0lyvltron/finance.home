package handlers

import (
	"net/http"
	"time"

	"finance.home/internal/auth"
	"finance.home/internal/store"
)

func (a *App) home(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "home"
	a.withQuickAdd(&d)

	dash, err := a.Store.BuildHomeDashboard(d.Household, time.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.NetWorth = dash.NetWorth
	d.LeftToSpend = dash.LeftToSpend
	d.BudgetHealth = dash.BudgetHealth
	d.Bills = dash.Bills
	d.Insights = dash.Insights
	d.YearMonth = dash.YearMonth
	d.ReviewCount = dash.ReviewCount

	txs, _ := a.Store.ListTransactions(store.RegisterFilter{Limit: 8})
	d.Transactions = txs

	accts, _ := a.Store.ListAccounts(false)
	d.Accounts = accts

	a.Render.RenderHTTP(w, "home", d)
}

func (a *App) more(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	a.Render.RenderHTTP(w, "more", d)
}
