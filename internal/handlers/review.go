package handlers

import (
	"net/http"
	"strconv"

	"finance.home/internal/auth"
)

func (a *App) withReviewBadge(d *pageData) {
	n, _ := a.Store.NeedsReviewCount()
	d.ReviewCount = n
}

func (a *App) reviewList(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "transactions"
	a.withQuickAdd(&d)
	a.withReviewBadge(&d)
	items, err := a.Store.ListNeedsReview(100)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.Transactions = items
	a.Render.RenderHTTP(w, "review", d)
}

func (a *App) reviewConfirm(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	var catID *int64
	if v := r.FormValue("category_id"); v != "" {
		cid, err := strconv.ParseInt(v, 10, 64)
		if err == nil && cid > 0 {
			catID = &cid
		}
	}
	if err := a.Store.ConfirmReview(id, catID, r.FormValue("person"), r.FormValue("payee"), r.FormValue("notes")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/review", http.StatusSeeOther)
}

func (a *App) reviewConfirmBulk(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	var ids []int64
	for _, v := range r.Form["tx_id"] {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			ids = append(ids, id)
		}
	}
	_ = a.Store.ConfirmReviewAll(ids)
	http.Redirect(w, r, "/review", http.StatusSeeOther)
}
