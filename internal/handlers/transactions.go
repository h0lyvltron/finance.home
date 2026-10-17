package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"finance.home/internal/auth"
	"finance.home/internal/money"
	"finance.home/internal/store"
	"github.com/starfederation/datastar-go/datastar"
)

func (a *App) transactionsList(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "transactions"
	a.withQuickAdd(&d)
	a.withFilterOptions(&d)

	f, err := a.loadFilter(r, sess)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Activity without date intent: show recent across all time
	if r.FormValue("preset") == "" && r.FormValue("from") == "" && r.FormValue("to") == "" &&
		r.FormValue("rolling") == "" && r.FormValue("saved") == "" {
		f.DatePreset = "all"
		f.DateFrom, f.DateTo = "", ""
	}
	f.Limit = 75
	txs, err := a.Store.ListTransactions(f)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.Transactions = txs
	d.Filter = a.filterViewFor(r, sess, f, "/transactions", "", "")
	d.QueryString = d.Filter.QueryString
	a.Render.RenderHTTP(w, "transactions", d)
}

func (a *App) transactionNew(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	a.withQuickAdd(&d)
	a.Render.RenderHTTP(w, "transaction-form", d)
}

func (a *App) transactionCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	d := a.base(r, sess)
	a.withQuickAdd(&d)

	in, err := a.parseTxForm(r, sess.PersonID)
	if err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "transaction-form", d)
		return
	}
	if _, err := a.Store.CreateTransaction(in); err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "transaction-form", d)
		return
	}
	http.Redirect(w, r, "/transactions", http.StatusSeeOther)
}

func (a *App) quickAdd(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	kind := r.FormValue("kind")
	if kind == "" {
		kind = "expense"
	}
	amount, err := money.ParseDecimal(r.FormValue("amount"))
	if err != nil || amount == 0 {
		a.qaError(w, r, "Enter an amount.")
		return
	}
	if amount < 0 {
		amount = -amount
	}
	signed := amount
	if kind == "expense" {
		signed = -amount
	}
	acctID, _ := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	catID, _ := strconv.ParseInt(r.FormValue("category_id"), 10, 64)
	if acctID == 0 || catID == 0 {
		a.qaError(w, r, "Account and category are required.")
		return
	}
	date := r.FormValue("date")
	if date == "" {
		a.qaError(w, r, "Date is required.")
		return
	}
	person := r.FormValue("person")
	if person == "" {
		person = "shared"
	}
	in := store.TxInput{
		AccountID:       acctID,
		PayeeName:       r.FormValue("payee"),
		Notes:           r.FormValue("notes"),
		Amount:          signed,
		TransactionDate: date,
		Person:          person,
		Status:          "cleared",
		CategoryID:      &catID,
		CreatedBy:       sess.PersonID,
	}
	if _, err := a.Store.CreateTransaction(in); err != nil {
		a.qaError(w, r, err.Error())
		return
	}

	andAnother := r.FormValue("and_another") == "1"
	if wantsDatastar(r) {
		sse := datastar.NewSSE(w, r)
		if andAnother {
			_ = sse.PatchElements(`<div id="qa-error" class="alert alert-ok">Saved. Add another.</div>`)
			_ = sse.ExecuteScript(`document.getElementById('qa-amount') && (document.getElementById('qa-amount').value=''); document.getElementById('qa-payee') && (document.getElementById('qa-payee').value=''); document.getElementById('qa-notes') && (document.getElementById('qa-notes').value=''); document.getElementById('qa-amount')?.focus();`)
			return
		}
		_ = sse.PatchElements(`<div id="qa-error"></div>`)
		_ = sse.ExecuteScript(`window.location.href='/transactions';`)
		return
	}
	if andAnother {
		http.Redirect(w, r, "/?qa=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/transactions", http.StatusSeeOther)
}

func (a *App) qaError(w http.ResponseWriter, r *http.Request, msg string) {
	if wantsDatastar(r) {
		sse := datastar.NewSSE(w, r)
		_ = sse.PatchElements(fmt.Sprintf(`<div id="qa-error" class="alert alert-error">%s</div>`, templateEscape(msg)))
		return
	}
	http.Error(w, msg, 400)
}

func templateEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func (a *App) transactionDelete(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	undoID, err := a.Store.SoftDeleteTransaction(id, sess.PersonID)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if wantsDatastar(r) {
		sse := datastar.NewSSE(w, r)
		html, _ := a.Render.RenderString("toast", pageData{
			CSRF:    sess.CSRFToken,
			UndoID:  undoID,
			Message: "Deleted · undo 30m · recycle bin 30d",
		})
		_ = sse.PatchElements(`<div id="toast-region" class="toast-region">`+html+`</div>`)
		// refresh list simply by redirect script
		_ = sse.ExecuteScript(`setTimeout(() => location.reload(), 50)`)
		return
	}
	http.Redirect(w, r, "/transactions?undo="+undoID, http.StatusSeeOther)
}

func (a *App) undo(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	undoID := r.FormValue("undo_id")
	if err := a.Store.Undo(undoID); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if wantsDatastar(r) {
		sse := datastar.NewSSE(w, r)
		_ = sse.PatchElements(`<div id="toast-region" class="toast-region"></div>`)
		_ = sse.ExecuteScript(`location.reload()`)
		return
	}
	http.Redirect(w, r, "/transactions", http.StatusSeeOther)
}

func (a *App) transferNew(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	a.withQuickAdd(&d)
	a.Render.RenderHTTP(w, "transfer-form", d)
}

func (a *App) transferCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	d := a.base(r, sess)
	a.withQuickAdd(&d)
	fromID, _ := strconv.ParseInt(r.FormValue("from_account_id"), 10, 64)
	toID, _ := strconv.ParseInt(r.FormValue("to_account_id"), 10, 64)
	amt, err := money.ParseDecimal(r.FormValue("amount"))
	if err != nil || amt <= 0 {
		d.Error = "Enter a positive amount."
		a.Render.RenderHTTP(w, "transfer-form", d)
		return
	}
	_, err = a.Store.CreateTransfer(store.TransferInput{
		FromAccountID:   fromID,
		ToAccountID:     toID,
		Amount:          amt,
		TransactionDate: r.FormValue("date"),
		Notes:           r.FormValue("notes"),
		Person:          "shared",
		CreatedBy:       sess.PersonID,
	})
	if err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "transfer-form", d)
		return
	}
	http.Redirect(w, r, "/transactions", http.StatusSeeOther)
}

func (a *App) parseTxForm(r *http.Request, personID int64) (store.TxInput, error) {
	amount, err := money.ParseDecimal(r.FormValue("amount"))
	if err != nil {
		return store.TxInput{}, fmt.Errorf("invalid amount")
	}
	kind := r.FormValue("kind")
	switch kind {
	case "expense":
		if amount < 0 {
			amount = -amount
		}
		amount = -amount
	case "income":
		if amount < 0 {
			amount = -amount
		}
	}
	acctID, _ := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if acctID == 0 {
		return store.TxInput{}, fmt.Errorf("account required")
	}
	var catID *int64
	if v := r.FormValue("category_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil && id > 0 {
			catID = &id
		}
	}
	var splits []store.SplitInput
	for i := 0; i < 3; i++ {
		cs := r.FormValue(fmt.Sprintf("split_category_%d", i))
		as := r.FormValue(fmt.Sprintf("split_amount_%d", i))
		if cs == "" || strings.TrimSpace(as) == "" {
			continue
		}
		cid, err := strconv.ParseInt(cs, 10, 64)
		if err != nil {
			continue
		}
		sa, err := money.ParseDecimal(as)
		if err != nil {
			return store.TxInput{}, fmt.Errorf("invalid split amount")
		}
		// Align split signs with transaction direction for expense/income kinds
		if kind == "expense" && sa > 0 {
			sa = -sa
		}
		if kind == "income" && sa < 0 {
			sa = -sa
		}
		splits = append(splits, store.SplitInput{
			CategoryID: cid,
			Person:     r.FormValue(fmt.Sprintf("split_person_%d", i)),
			Amount:     sa,
		})
	}
	if len(splits) == 0 && catID == nil {
		return store.TxInput{}, fmt.Errorf("category or splits required")
	}
	var tags []string
	for _, t := range strings.Split(r.FormValue("tags"), ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tags = append(tags, t)
		}
	}
	person := r.FormValue("person")
	if person == "" {
		person = "shared"
	}
	status := r.FormValue("status")
	if status == "" {
		status = "cleared"
	}
	return store.TxInput{
		AccountID:       acctID,
		PayeeName:       r.FormValue("payee"),
		Notes:           r.FormValue("notes"),
		Amount:          amount,
		TransactionDate: r.FormValue("date"),
		Person:          person,
		Status:          status,
		CategoryID:      catID,
		TagNames:        tags,
		Splits:          splits,
		CreatedBy:       personID,
	}, nil
}
