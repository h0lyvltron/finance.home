package handlers

import (
	"net/http"

	"finance.home/internal/auth"
	"finance.home/internal/store"
)

func (a *App) home(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "home"
	a.withQuickAdd(&d)
	accts, _ := a.Store.ListAccounts(false)
	d.Accounts = accts
	var net int64
	for _, ac := range accts {
		net += ac.Balance
	}
	d.NetWorth = net
	txs, _ := a.Store.ListTransactions(store.RegisterFilter{Limit: 8})
	d.Transactions = txs
	a.Render.RenderHTTP(w, "home", d)
}

func (a *App) more(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	a.Render.RenderHTTP(w, "more", d)
}
