package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"finance.home/internal/auth"
	"finance.home/internal/store"
)

func (a *App) settingsPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	if d.Household != nil {
		d.MonthStartDay = d.Household.MonthStartDay
		d.FewerNudges = d.Household.FewerNudges
	}
	if d.MonthStartDay < 1 {
		d.MonthStartDay = 1
	}
	d.RetentionDays = store.SoftDeleteRetentionDays
	prefs, _ := a.Store.ListCoachingPrefs(sess.PersonID)
	d.CoachingPrefs = prefs
	d.Flash = r.URL.Query().Get("ok")
	a.Render.RenderHTTP(w, "settings", d)
}

func (a *App) settingsPost(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	day, _ := strconv.Atoi(r.FormValue("month_start_day"))
	fewer := r.FormValue("fewer_nudges") == "1" || r.FormValue("fewer_nudges") == "on"
	if err := a.Store.UpdateHouseholdSettings(day, fewer); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/settings?ok=saved", http.StatusSeeOther)
}

func (a *App) settingsBackup(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	dir := filepath.Join(a.Cfg.DataDir, "backups")
	path, err := a.Store.CreateBackupFile(dir)
	if err != nil {
		http.Error(w, "backup failed: "+err.Error(), 500)
		return
	}
	defer os.Remove(path)

	f, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer f.Close()

	stat, _ := f.Stat()
	name := filepath.Base(path)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	if stat != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	}
	http.ServeContent(w, r, name, time.Now(), f)
}

func (a *App) recyclePage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	d.RetentionDays = store.SoftDeleteRetentionDays
	items, err := a.Store.ListSoftDeleted(100)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.SoftDeleted = items
	d.Flash = r.URL.Query().Get("ok")
	a.Render.RenderHTTP(w, "recycle", d)
}

func (a *App) recycleRestore(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := a.Store.RestoreTransaction(id); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/recycle?ok=restored", http.StatusSeeOther)
}

func (a *App) coachingSnooze(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	kind := r.FormValue("kind")
	days, _ := strconv.Atoi(r.FormValue("days"))
	if days <= 0 {
		days = 7
	}
	h := a.household()
	tz := ""
	if h != nil {
		tz = h.Timezone
	}
	until := store.SnoozeUntilDays(tz, days, time.Now())
	if err := a.Store.SnoozeCoaching(sess.PersonID, kind, until); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ret := r.FormValue("return")
	if ret == "" {
		ret = "/"
	}
	http.Redirect(w, r, ret, http.StatusSeeOther)
}

func (a *App) coachingDismiss(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	kind := r.FormValue("kind")
	var err error
	if kind == "split" && r.FormValue("payee_key") != "" {
		catID, _ := strconv.ParseInt(r.FormValue("category_id"), 10, 64)
		err = a.Store.DismissSplitPair(sess.PersonID, catID, r.FormValue("payee_key"))
	} else {
		err = a.Store.DismissCoaching(sess.PersonID, kind)
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ret := r.FormValue("return")
	if ret == "" {
		ret = "/"
	}
	http.Redirect(w, r, ret, http.StatusSeeOther)
}

func (a *App) coachingClear(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	kind := r.FormValue("kind")
	_ = a.Store.ClearCoachingPref(sess.PersonID, kind)
	http.Redirect(w, r, "/settings?ok=coaching", http.StatusSeeOther)
}
