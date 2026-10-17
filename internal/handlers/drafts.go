package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"finance.home/internal/auth"
	"finance.home/internal/models"
	"finance.home/internal/money"
	"finance.home/internal/store"
	"github.com/starfederation/datastar-go/datastar"
)

func (a *App) draftsPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	drafts, err := a.Store.ListDrafts(sess.PersonID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.Drafts = drafts
	d.Flash = r.URL.Query().Get("ok")
	a.Render.RenderHTTP(w, "drafts", d)
}

func (a *App) parseDraftForm(r *http.Request, personID int64) (models.DraftTransaction, error) {
	kind := r.FormValue("kind")
	if kind == "" {
		kind = "expense"
	}
	amount, err := money.ParseDecimal(r.FormValue("amount"))
	if err != nil {
		amount = 0
	}
	if amount < 0 {
		amount = -amount
	}
	signed := amount
	if kind == "expense" {
		signed = -amount
	}
	var acctID, catID *int64
	if v, err := strconv.ParseInt(r.FormValue("account_id"), 10, 64); err == nil && v > 0 {
		acctID = &v
	}
	if v, err := strconv.ParseInt(r.FormValue("category_id"), 10, 64); err == nil && v > 0 {
		catID = &v
	}
	person := r.FormValue("person")
	if person == "" {
		person = "shared"
	}
	return models.DraftTransaction{
		PersonID:        personID,
		AccountID:       acctID,
		CategoryID:      catID,
		PayeeName:       strings.TrimSpace(r.FormValue("payee")),
		Notes:           strings.TrimSpace(r.FormValue("notes")),
		Amount:          signed,
		TransactionDate: r.FormValue("date"),
		Person:          person,
		Kind:            kind,
		ClientKey:       strings.TrimSpace(r.FormValue("client_key")),
	}, nil
}

func (a *App) draftSave(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	in, err := a.parseDraftForm(r, sess.PersonID)
	if err != nil {
		a.qaError(w, r, err.Error())
		return
	}
	if in.Amount == 0 {
		a.qaError(w, r, "Enter an amount to save a draft.")
		return
	}
	id, err := a.Store.UpsertDraft(in)
	if err != nil {
		a.qaError(w, r, err.Error())
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"ok":true,"draft_id":%d}`, id)
		return
	}
	if wantsDatastar(r) {
		sse := datastar.NewSSE(w, r)
		_ = sse.PatchElements(fmt.Sprintf(
			`<div id="qa-error" class="alert alert-ok">Draft saved (#%d). <a href="/drafts">View drafts</a></div>`, id))
		return
	}
	http.Redirect(w, r, "/drafts?ok=saved", http.StatusSeeOther)
}

// draftSync accepts offline-queued quick-add payloads (idempotent via client_key).
func (a *App) draftSync(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	in, err := a.parseDraftForm(r, sess.PersonID)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if in.ClientKey == "" {
		http.Error(w, "client_key required", 400)
		return
	}
	if in.Amount == 0 {
		http.Error(w, "amount required", 400)
		return
	}
	// Prefer promoting straight to a transaction when complete; else keep as draft.
	if in.AccountID != nil && in.CategoryID != nil && in.TransactionDate != "" {
		txID, err := a.Store.CreateTransaction(store.TxInput{
			AccountID:       *in.AccountID,
			PayeeName:       in.PayeeName,
			Notes:           in.Notes,
			Amount:          in.Amount,
			TransactionDate: in.TransactionDate,
			Person:          in.Person,
			Status:          "cleared",
			CategoryID:      in.CategoryID,
			CreatedBy:       sess.PersonID,
		})
		if err != nil {
			// Fall back to draft so the offline item is not lost.
			if _, dErr := a.Store.UpsertDraft(in); dErr != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"draft":true}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"ok":true,"transaction_id":%d}`, txID)
		return
	}
	id, err := a.Store.UpsertDraft(in)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"ok":true,"draft_id":%d}`, id)
}

func (a *App) draftPromote(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if _, err := a.Store.PromoteDraft(id, sess.PersonID); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/drafts?ok=posted", http.StatusSeeOther)
}

func (a *App) draftDelete(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = a.Store.DeleteDraft(id, sess.PersonID)
	http.Redirect(w, r, "/drafts?ok=deleted", http.StatusSeeOther)
}
