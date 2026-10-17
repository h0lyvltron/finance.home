package handlers

import (
	"encoding/json"
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

type widgetCell struct {
	ID  int64 `json:"id"`
	Col int   `json:"col"`
	Row int   `json:"row"`
	W   int   `json:"w"`
	H   int   `json:"h"`
}

// dashboardPlaceWidget moves/resizes a widget on the cell grid; fetch callers get
// every widget's resolved cells back since others may have been pushed down.
func (a *App) dashboardPlaceWidget(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	num := func(k string) int {
		n, _ := strconv.Atoi(strings.TrimSpace(r.FormValue(k)))
		return n
	}
	widgets, err := a.Store.PlaceWidget(sess.PersonID, id, num("col"), num("row"), num("w"), num("h"))
	if err != nil {
		if wantsFetch(r) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/?edit=1&err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if !wantsFetch(r) {
		http.Redirect(w, r, "/?edit=1", http.StatusSeeOther)
		return
	}
	cells := make([]widgetCell, 0, len(widgets))
	for _, x := range widgets {
		cells = append(cells, widgetCell{ID: x.ID, Col: x.GridCol, Row: x.GridRow, W: x.GridW, H: x.GridH})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"widgets": cells})
}

func (a *App) dashboardCompact(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := a.Store.CompactDashboard(sess.PersonID); err != nil {
		http.Redirect(w, r, "/?edit=1&err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?edit=1", http.StatusSeeOther)
}

func (a *App) dashboardReset(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := a.Store.ResetDashboard(sess.PersonID); err != nil {
		http.Redirect(w, r, "/?edit=1&err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?edit=1", http.StatusSeeOther)
}

func wantsFetch(r *http.Request) bool {
	return r.Header.Get("X-Requested-With") == "fetch"
}

// chartJS wraps widget chart JSON for safe template embedding.
func chartJS(s string) template.JS {
	if s == "" {
		return "null"
	}
	return template.JS(s)
}
