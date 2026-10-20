package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"finance.home/internal/auth"
	"finance.home/internal/money"
	"finance.home/internal/store"
)

// oneSidedGroup bundles one-sided transfers from the same account that point
// at the same likely counterpart, so they can be fixed together.
type oneSidedGroup struct {
	AccountName        string
	SuggestedAccountID int64
	Total              int64
	Rows               []store.OneSidedTx
}

func groupOneSided(rows []store.OneSidedTx) []oneSidedGroup {
	var out []oneSidedGroup
	idx := map[[2]int64]int{}
	for _, r := range rows {
		k := [2]int64{r.AccountID, r.SuggestedAccountID}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, oneSidedGroup{AccountName: r.AccountName, SuggestedAccountID: r.SuggestedAccountID})
		}
		out[i].Rows = append(out[i].Rows, r)
		out[i].Total += r.Amount
	}
	return out
}

func (a *App) transferAudit(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	audit, err := a.Store.BuildTransferAudit()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.Audit = &audit
	d.OneSidedGroups = groupOneSided(audit.OneSided)
	d.UndoID = r.URL.Query().Get("undo")
	d.Message = r.URL.Query().Get("msg")
	if e := r.URL.Query().Get("err"); e != "" {
		d.Flash, d.FlashType = e, "error"
	}
	a.Render.RenderHTTP(w, "transfer-audit", d)
}

func auditDone(w http.ResponseWriter, r *http.Request, undoID, msg string, err error) {
	q := url.Values{}
	if err != nil {
		q.Set("err", err.Error())
	} else {
		if undoID != "" {
			q.Set("undo", undoID)
		}
		if msg != "" {
			q.Set("msg", msg)
		}
	}
	http.Redirect(w, r, "/transfers/audit?"+q.Encode(), http.StatusSeeOther)
}

func formID(r *http.Request, name string) int64 {
	id, _ := strconv.ParseInt(r.FormValue(name), 10, 64)
	return id
}

func formIDs(r *http.Request, name string) []int64 {
	var ids []int64
	for _, v := range r.Form[name] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func (a *App) transferAuditLink(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	undoID, err := a.Store.LinkTransfers([][2]int64{{formID(r, "out_id"), formID(r, "in_id")}}, sess.PersonID)
	auditDone(w, r, undoID, "Linked as a transfer.", err)
}

func (a *App) transferAuditLinkAll(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	undoID, n, err := a.Store.LinkHighConfidenceTransfers(sess.PersonID)
	auditDone(w, r, undoID, "Linked "+plural(n, "transfer")+".", err)
}

func (a *App) transferAuditUnlink(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	undoID, err := a.Store.UnlinkTransfer(formID(r, "transfer_id"), sess.PersonID)
	auditDone(w, r, undoID, "Unlinked. Both sides are in Needs review.", err)
}

func (a *App) transferAuditRePair(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	undoID, err := a.Store.RePairTransfer(formID(r, "a_id"), formID(r, "b_id"), sess.PersonID)
	auditDone(w, r, undoID, "Re-paired. Anything left without a partner is in Needs review.", err)
}

func (a *App) transferAuditCounterparts(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	res, err := a.Store.CreateCounterparts(formIDs(r, "tx_id"), formID(r, "account_id"), sess.PersonID)
	msg := fmt.Sprintf("Added %s on %s.", plural(res.Created, "matching transaction"), res.TargetName)
	if res.ChangedAnchor > 0 {
		msg += " Its current balance moved; reconcile it if it no longer matches the bank."
	}
	auditDone(w, r, res.UndoID, msg, err)
}

func (a *App) transferAuditNotTransfer(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	ids := formIDs(r, "tx_id")
	undoID, err := a.Store.MarkNotTransfer(ids, sess.PersonID)
	auditDone(w, r, undoID, plural(len(ids), "transaction")+" sent to Needs review to categorize.", err)
}

func (a *App) transferAuditLoan(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	interest, err := money.ParseDecimal(r.FormValue("interest"))
	if err != nil {
		auditDone(w, r, "", "", fmt.Errorf("enter the interest amount"))
		return
	}
	if interest < 0 {
		interest = -interest
	}
	undoID, err := a.Store.PairLoanPaymentWithInterest(formID(r, "out_id"), formID(r, "loan_id"), interest, sess.PersonID)
	auditDone(w, r, undoID, "Loan payment linked; interest recorded on the loan.", err)
}

func (a *App) transferAuditLoanAll(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	undoID, n, err := a.Store.PairExactLoanPayments(sess.PersonID)
	auditDone(w, r, undoID, "Fixed "+plural(n, "loan payment")+".", err)
}

func (a *App) transferAuditNormalize(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	n, err := a.Store.NormalizeTransferSplits()
	auditDone(w, r, "", "Cleaned up "+plural(n, "transfer")+" with stale categories.", err)
}
