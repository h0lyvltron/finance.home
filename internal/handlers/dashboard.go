package handlers

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"finance.home/internal/auth"
	"finance.home/internal/store"
)

func dashboardReturn(r *http.Request) string {
	ret := r.FormValue("return")
	if strings.HasPrefix(ret, "/?") || ret == "/" || strings.HasPrefix(ret, "/?edit") {
		return ret
	}
	if r.FormValue("edit") == "1" || strings.Contains(r.Referer(), "edit=1") {
		return "/?edit=1"
	}
	return "/"
}

func (a *App) dashboardSetRange(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	preset := r.FormValue("preset")
	if err := a.Store.SetDashboardRange(sess.PersonID, preset); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, dashboardReturn(r), http.StatusSeeOther)
}

func (a *App) dashboardAddWidget(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	in := store.AddWidgetInput{
		WidgetType: r.FormValue("widget_type"),
		Width:      r.FormValue("width"),
	}
	if v := r.FormValue("saved_filter_id"); v != "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		in.SavedFilterID = id
	}
	if _, err := a.Store.AddWidget(sess.PersonID, in); err != nil {
		http.Redirect(w, r, "/?edit=1&err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?edit=1", http.StatusSeeOther)
}

func (a *App) dashboardRemoveWidget(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = a.Store.RemoveWidget(sess.PersonID, id)
	http.Redirect(w, r, dashboardReturn(r), http.StatusSeeOther)
}

func (a *App) dashboardMoveWidget(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	_ = a.Store.MoveWidget(sess.PersonID, id, r.FormValue("dir"))
	http.Redirect(w, r, dashboardReturn(r), http.StatusSeeOther)
}

func (a *App) dashboardReorderWidgets(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	parts := strings.Split(r.FormValue("order"), ",")
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			if wantsFetch(r) {
				http.Error(w, "invalid widget order", http.StatusBadRequest)
				return
			}
			http.Redirect(w, r, "/?edit=1&err="+urlQueryEscape("invalid widget order"), http.StatusSeeOther)
			return
		}
		ids = append(ids, id)
	}
	if err := a.Store.ReorderWidgets(sess.PersonID, ids); err != nil {
		if wantsFetch(r) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/?edit=1&err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if wantsFetch(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/?edit=1", http.StatusSeeOther)
}

func wantsFetch(r *http.Request) bool {
	return r.Header.Get("X-Requested-With") == "fetch"
}

func (a *App) dashboardSetWidth(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	_ = a.Store.SetWidgetWidth(sess.PersonID, id, r.FormValue("width"))
	http.Redirect(w, r, dashboardReturn(r), http.StatusSeeOther)
}

// chartJS wraps widget chart JSON for safe template embedding.
func chartJS(s string) template.JS {
	if s == "" {
		return "null"
	}
	return template.JS(s)
}
