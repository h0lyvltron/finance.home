package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"finance.home/internal/auth"
	"finance.home/internal/money"
	"finance.home/internal/store"
)

func (a *App) accountsList(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "accounts"
	a.withQuickAdd(&d)
	accts, err := a.Store.ListAccounts(false)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.Accounts = accts
	a.Render.RenderHTTP(w, "accounts", d)
}

func (a *App) accountNew(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Form = store.AccountInput{Type: "checking", Owner: "joint"}
	a.Render.RenderHTTP(w, "account-form", d)
}

func (a *App) accountCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	in, err := parseAccountForm(r)
	d := a.base(r, sess)
	d.Form = in
	d.FormBalance = r.FormValue("anchor_balance")
	if err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "account-form", d)
		return
	}
	if err := store.ValidateAccountInput(in); err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "account-form", d)
		return
	}
	id, err := a.Store.CreateAccount(in)
	if err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "account-form", d)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/accounts/%d", id), http.StatusSeeOther)
}

func (a *App) accountDetail(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	acct, err := a.Store.GetAccount(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d := a.base(r, sess)
	d.Nav = "accounts"
	d.Account = acct
	d.ShowRunning = true
	a.withQuickAdd(&d)
	txs, err := a.Store.ListTransactions(store.RegisterFilter{AccountID: id, Limit: 100})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = a.Store.AttachRunningBalances(id, txs)
	d.Transactions = txs
	a.Render.RenderHTTP(w, "account-detail", d)
}

func (a *App) accountEdit(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	acct, err := a.Store.GetAccount(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d := a.base(r, sess)
	d.Account = acct
	// Display liability anchors as positive owed for the form
	if acct.IsLiability() && acct.AnchorBalance < 0 {
		cp := *acct
		cp.AnchorBalance = -cp.AnchorBalance
		d.Account = &cp
	}
	n, _ := a.Store.CountTxBeforeAnchor(id, acct.AnchorDate)
	if n > 0 {
		d.Warn = fmt.Sprintf("%d transaction(s) exist before the anchor date — they are excluded from the balance.", n)
	}
	a.Render.RenderHTTP(w, "account-form", d)
}

func (a *App) accountUpdate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	in, err := parseAccountForm(r)
	acct, _ := a.Store.GetAccount(id)
	d := a.base(r, sess)
	d.Account = acct
	d.Form = in
	d.FormBalance = r.FormValue("anchor_balance")
	if err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "account-form", d)
		return
	}
	if err := store.ValidateAccountInput(in); err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "account-form", d)
		return
	}
	if err := a.Store.UpdateAccount(id, in); err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "account-form", d)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/accounts/%d", id), http.StatusSeeOther)
}

func parseAccountForm(r *http.Request) (store.AccountInput, error) {
	balRaw := strings.TrimSpace(r.FormValue("anchor_balance"))
	bal, err := money.ParseDecimal(balRaw)
	if err != nil {
		return store.AccountInput{}, fmt.Errorf("invalid balance")
	}
	typ := r.FormValue("type")
	// Liabilities: user enters positive owed → store negative
	if (typ == "credit" || typ == "loan") && bal > 0 {
		bal = -bal
	}
	return store.AccountInput{
		Name:          strings.TrimSpace(r.FormValue("name")),
		Type:          typ,
		Owner:         r.FormValue("owner"),
		Currency:      "USD",
		AnchorBalance: bal,
		AnchorDate:    r.FormValue("anchor_date"),
	}, nil
}

func (a *App) reconcileGet(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	acct, err := a.Store.GetAccount(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	items, _ := a.Store.UnclearedForAccount(id)
	d := a.base(r, sess)
	d.Account = acct
	d.Items = items
	a.Render.RenderHTTP(w, "reconcile", d)
}

func (a *App) reconcilePost(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	acct, err := a.Store.GetAccount(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	statementDate := r.FormValue("statement_date")
	endingRaw := r.FormValue("ending_balance")
	ending, err := money.ParseDecimal(endingRaw)
	d := a.base(r, sess)
	d.Account = acct
	d.StatementDate = statementDate
	d.EndingBalance = endingRaw
	items, _ := a.Store.UnclearedForAccount(id)
	d.Items = items
	if err != nil {
		d.Error = "Invalid ending balance"
		a.Render.RenderHTTP(w, "reconcile", d)
		return
	}
	if acct.IsLiability() && ending > 0 {
		ending = -ending
	}
	var clearIDs []int64
	for _, v := range r.Form["clear_id"] {
		cid, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			clearIDs = append(clearIDs, cid)
		}
	}
	diff, err := a.Store.Reconcile(id, statementDate, ending, clearIDs, sess.PersonID)
	if err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "reconcile", d)
		return
	}
	items, _ = a.Store.UnclearedForAccount(id)
	d.Items = items
	d.Result = &reconcileResult{Difference: diff}
	a.Render.RenderHTTP(w, "reconcile", d)
}
