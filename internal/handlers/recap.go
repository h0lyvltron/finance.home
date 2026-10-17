package handlers

import (
	"net/http"
	"time"

	"finance.home/internal/auth"
	"finance.home/internal/store"
)

func (a *App) recapPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)

	h := d.Household
	now := time.Now()
	ym := store.ParseYearMonth(r.FormValue("month"), h, now)
	d.YearMonth = ym

	recap, err := a.Store.BuildMonthlyRecap(h, ym, now)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.MonthlyRecap = recap
	a.Render.RenderHTTP(w, "recap", d)
}
