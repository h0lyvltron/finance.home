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

	edit := r.URL.Query().Get("edit") == "1"
	d.DashEdit = edit
	if errMsg := r.URL.Query().Get("err"); errMsg != "" {
		d.Error = errMsg
	}

	dash, err := a.Store.BuildPersonalDashboard(sess.PersonID, d.Household, time.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.PersonalDash = dash
	d.YearMonth = dash.YearMonth
	d.ReviewCount = dash.ReviewCount

	// Keep legacy fields populated for any shared partials
	for _, wgt := range dash.Widgets {
		if wgt.WidgetType == store.WidgetNetWorth {
			d.NetWorth = wgt.NetWorth
		}
		if wgt.WidgetType == store.WidgetLeftToSpend {
			d.LeftToSpend = wgt.LeftToSpend
		}
	}

	a.Render.RenderHTTP(w, "home", d)
}

func (a *App) more(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	a.Render.RenderHTTP(w, "more", d)
}
