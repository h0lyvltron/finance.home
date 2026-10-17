package handlers

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"finance.home/internal/auth"
	"finance.home/internal/models"
	"finance.home/internal/money"
	"finance.home/internal/store"
)

func (a *App) goalsPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	accts, _ := a.Store.ListAccounts(false)
	d.Accounts = accts

	if errMsg := r.FormValue("err"); errMsg != "" {
		d.Error = errMsg
	}

	progress, err := a.Store.ListGoalProgress(time.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.GoalProgress = progress

	// Debt chart for first debt goal if present
	for _, p := range progress {
		if p.Goal.Kind != "debt" {
			continue
		}
		h := d.Household
		f := store.QueryFilter{DatePreset: "last_90"}
		f.ResolveDates(h, time.Now())
		from, to := f.DateFrom, f.DateTo
		if from == "" || to == "" {
			now := time.Now()
			to = now.Format("2006-01-02")
			from = now.AddDate(0, 0, -90).Format("2006-01-02")
		}
		pts, err := a.Store.DebtBalanceSeries(p.Goal, from, to)
		if err == nil && len(pts) > 0 {
			d.DebtSeries = pts
			d.DebtGoalName = p.Goal.Name
			d.ChartJSON = template.JS(chartJSONLinePoints(pts, "Balance owed", "var:expense"))
			d.ShowChart = true
		}
		break
	}

	a.Render.RenderHTTP(w, "goals", d)
}

func (a *App) goalCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	in := parseGoalForm(r)
	if err := in.Validate(); err != nil {
		http.Redirect(w, r, "/goals?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if _, err := a.Store.CreateGoal(in); err != nil {
		http.Redirect(w, r, "/goals?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/goals", http.StatusSeeOther)
}

func (a *App) goalUpdate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	in := parseGoalForm(r)
	if err := in.Validate(); err != nil {
		http.Redirect(w, r, "/goals?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if err := a.Store.UpdateGoal(id, in); err != nil {
		http.Redirect(w, r, "/goals?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/goals", http.StatusSeeOther)
}

func (a *App) goalArchive(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		http.NotFound(w, r)
		return
	}
	_ = a.Store.ArchiveGoal(id)
	http.Redirect(w, r, "/goals", http.StatusSeeOther)
}

func parseGoalForm(r *http.Request) store.GoalInput {
	target, err := money.ParseDecimal(r.FormValue("target"))
	if err != nil {
		target = 0
	}
	if target < 0 {
		target = -target
	}
	pace, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("pace_window")))
	if pace <= 0 {
		pace = 90
	}
	var accountIDs []int64
	for _, v := range r.Form["account_id"] {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil && id > 0 {
			accountIDs = append(accountIDs, id)
		}
	}
	return store.GoalInput{
		Kind:           r.FormValue("kind"),
		Name:           r.FormValue("name"),
		TargetCents:    target,
		TargetDate:     strings.TrimSpace(r.FormValue("target_date")),
		PaceWindowDays: pace,
		AccountIDs:     accountIDs,
	}
}

func urlQueryEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, " ", "+"), "&", "%26")
}

func chartJSONLinePoints(pts []models.BalancePoint, label, color string) string {
	labels := make([]string, len(pts))
	data := make([]float64, len(pts))
	for i, p := range pts {
		labels[i] = p.Label
		data[i] = float64(p.Amount) / 100
	}
	cfg := map[string]any{
		"type": "line",
		"data": map[string]any{
			"labels": labels,
			"datasets": []map[string]any{{
				"label":           label,
				"data":            data,
				"borderColor":     color,
				"backgroundColor": "var:net-fill",
				"fill":            true,
				"tension":         0.25,
				"pointRadius":     0,
				"borderWidth":     2,
			}},
		},
		"options": map[string]any{
			"responsive":          true,
			"maintainAspectRatio": false,
			"plugins": map[string]any{
				"legend": map[string]any{"display": true},
			},
			"scales": map[string]any{
				"y": map[string]any{"beginAtZero": false},
			},
		},
		"finance": map[string]any{"format": "currency"},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}
