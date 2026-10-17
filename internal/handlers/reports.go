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
	"finance.home/internal/store"
)

func (a *App) withFilterOptions(d *pageData) {
	accts, _ := a.Store.ListAccounts(false)
	cats, _ := a.Store.ListCategories("", false)
	tags, _ := a.Store.ListTags()
	d.Accounts = accts
	d.Categories = cats
	d.Tags = tags
}

func (a *App) explorePage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "explore"
	a.withQuickAdd(&d)
	a.withFilterOptions(&d)

	f, err := a.loadFilter(r, sess)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	groupBy := strings.TrimSpace(r.FormValue("group"))
	if groupBy == "" {
		groupBy = "category"
	}
	topN := 8
	if r.FormValue("top") != "" {
		if n, err := strconv.Atoi(r.FormValue("top")); err == nil && n > 0 {
			topN = n
		}
	}

	segs, grand, err := a.Store.SpendingExplore(f, groupBy, topN, a.household(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	fv := a.filterViewFor(r, sess, f, "/explore", "", groupBy)
	d.Filter = fv
	d.GroupBy = groupBy
	d.Segments = segs
	d.ReportTotal = grand
	d.ChartJSON = template.JS(chartJSONHorizontalBar(segs))
	d.DrillBase = "/transactions"

	a.Render.RenderHTTP(w, "explore", d)
}

func (a *App) cashflowPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "explore"
	a.withQuickAdd(&d)
	a.withFilterOptions(&d)

	f, err := a.loadFilter(r, sess)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	grain := strings.TrimSpace(r.FormValue("grain"))
	if grain == "" {
		grain = "month"
	}
	points, err := a.Store.CashFlow(f, grain, a.household(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var income, expense, net int64
	for _, p := range points {
		income += p.Income
		expense += p.Expense
		net += p.Net
	}
	fv := a.filterViewFor(r, sess, f, "/reports/cashflow", grain, "")
	d.Filter = fv
	d.Grain = grain
	d.CashFlow = points
	d.IncomeTotal = income
	d.ExpenseTotal = expense
	d.ReportTotal = net
	d.ChartJSON = template.JS(chartJSONCashFlow(points))
	d.DrillBase = "/transactions"

	a.Render.RenderHTTP(w, "cashflow", d)
}

func (a *App) pivotPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "explore"
	a.withQuickAdd(&d)
	a.withFilterOptions(&d)

	f, err := a.loadFilter(r, sess)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	groupBy := strings.TrimSpace(r.FormValue("group"))
	if groupBy == "" {
		groupBy = "category"
	}
	grain := strings.TrimSpace(r.FormValue("grain"))
	if grain == "" {
		grain = "month"
	}
	pt, err := a.Store.SegmentPivot(f, groupBy, grain, a.household(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	fv := a.filterViewFor(r, sess, f, "/reports/pivot", grain, groupBy)
	d.Filter = fv
	d.GroupBy = groupBy
	d.Grain = grain
	d.Pivot = pt
	d.ReportTotal = pt.Grand
	d.ChartJSON = template.JS(chartJSONPivotStacked(pt))
	d.ShowChart = len(pt.ColKeys) > 0 && len(pt.ColKeys) <= 18 && len(pt.RowKeys) > 0 && len(pt.RowKeys) <= 12
	d.DrillBase = "/transactions"

	a.Render.RenderHTTP(w, "pivot", d)
}

func chartJSONHorizontalBar(segs []store.SegmentTotal) string {
	labels := make([]string, len(segs))
	data := make([]float64, len(segs))
	for i, s := range segs {
		labels[i] = s.Label
		data[i] = float64(s.Amount) / 100
	}
	cfg := map[string]any{
		"type": "bar",
		"data": map[string]any{
			"labels": labels,
			"datasets": []map[string]any{{
				"label":           "Spend",
				"data":            data,
				"backgroundColor": "rgba(47, 93, 80, 0.75)",
				"borderRadius":    6,
			}},
		},
		"options": map[string]any{
			"indexAxis": "y",
			"responsive": true,
			"maintainAspectRatio": false,
			"plugins": map[string]any{
				"legend": map[string]any{"display": false},
			},
			"scales": map[string]any{
				"x": map[string]any{"beginAtZero": true},
			},
		},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

func chartJSONCashFlow(points []store.CashFlowPoint) string {
	labels := make([]string, len(points))
	income := make([]float64, len(points))
	expense := make([]float64, len(points))
	for i, p := range points {
		labels[i] = p.Label
		if p.InProgress {
			labels[i] += " *"
		}
		income[i] = float64(p.Income) / 100
		expense[i] = float64(p.Expense) / 100
	}
	cfg := map[string]any{
		"type": "bar",
		"data": map[string]any{
			"labels": labels,
			"datasets": []map[string]any{
				{"label": "Income", "data": income, "backgroundColor": "rgba(47, 93, 80, 0.8)"},
				{"label": "Expense", "data": expense, "backgroundColor": "rgba(139, 58, 58, 0.75)"},
			},
		},
		"options": map[string]any{
			"responsive": true,
			"maintainAspectRatio": false,
			"scales": map[string]any{
				"x": map[string]any{"stacked": false},
				"y": map[string]any{"beginAtZero": true},
			},
		},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

func chartJSONPivotStacked(pt *store.PivotTable) string {
	if pt == nil {
		return "null"
	}
	// Limit series for readability
	rows := pt.RowKeys
	labels := pt.RowLabels
	if len(rows) > 8 {
		rows = rows[:8]
		labels = labels[:8]
	}
	palette := []string{
		"rgba(47, 93, 80, 0.85)",
		"rgba(139, 58, 58, 0.75)",
		"rgba(138, 106, 47, 0.8)",
		"rgba(60, 90, 140, 0.8)",
		"rgba(110, 70, 120, 0.8)",
		"rgba(70, 120, 100, 0.8)",
		"rgba(160, 90, 60, 0.8)",
		"rgba(90, 90, 90, 0.7)",
	}
	datasets := make([]map[string]any, 0, len(rows))
	for i, rk := range rows {
		data := make([]float64, len(pt.ColKeys))
		for j, ck := range pt.ColKeys {
			if cell, ok := pt.Cells[rk][ck]; ok {
				data[j] = float64(cell.Amount) / 100
			}
		}
		datasets = append(datasets, map[string]any{
			"label":           labels[i],
			"data":            data,
			"backgroundColor": palette[i%len(palette)],
			"stack":           "spend",
		})
	}
	colLabels := make([]string, len(pt.ColKeys))
	for i, ck := range pt.ColKeys {
		colLabels[i] = pt.ColLabels[i]
		if ck == pt.InProgCol {
			colLabels[i] += " *"
		}
	}
	cfg := map[string]any{
		"type": "bar",
		"data": map[string]any{
			"labels":   colLabels,
			"datasets": datasets,
		},
		"options": map[string]any{
			"responsive": true,
			"maintainAspectRatio": false,
			"scales": map[string]any{
				"x": map[string]any{"stacked": true},
				"y": map[string]any{"stacked": true, "beginAtZero": true},
			},
		},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

func (a *App) networthPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "explore"
	a.withQuickAdd(&d)
	a.withFilterOptions(&d)

	f, err := a.loadFilter(r, sess)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if f.DateFrom == "" || f.DateTo == "" {
		f.DatePreset = "last_90"
		f.ResolveDates(a.household(), time.Now())
	}
	series, err := a.Store.NetWorthSeries(f.DateFrom, f.DateTo, true)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	fv := a.filterViewFor(r, sess, f, "/reports/networth", "", "")
	d.Filter = fv
	d.NetWorthSeries = series
	d.NetWorth = series.End
	d.ReportTotal = series.Change
	d.ChartJSON = template.JS(chartJSONNetWorth(series))
	d.ShowChart = len(series.Points) > 0
	a.Render.RenderHTTP(w, "networth", d)
}

func (a *App) budgetReportPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "explore"
	a.withQuickAdd(&d)
	a.withFilterOptions(&d)

	h := a.household()
	ym := store.ParseYearMonth(r.FormValue("month"), h, time.Now())
	d.YearMonth = ym

	lines, err := a.Store.MonthBudgetLines(ym)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.BudgetLines = lines
	bh, _ := a.Store.BudgetHealthSummary(ym)
	d.BudgetHealth = bh
	d.ChartJSON = template.JS(chartJSONBudgetVsActual(lines))
	d.ShowChart = true

	f := store.QueryFilter{DatePreset: "custom"}
	from, to, _ := monthBoundsYM(ym)
	f.DateFrom, f.DateTo = from, to
	fv := a.filterViewFor(r, sess, f, "/reports/budget", "", "")
	d.Filter = fv

	a.Render.RenderHTTP(w, "budget-report", d)
}

func monthBoundsYM(ym string) (from, to string, err error) {
	t, err := time.Parse("2006-01", ym)
	if err != nil {
		return "", "", err
	}
	from = t.Format("2006-01-02")
	to = t.AddDate(0, 1, -1).Format("2006-01-02")
	return from, to, nil
}

func (a *App) savingsRatePage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "explore"
	a.withQuickAdd(&d)
	a.withFilterOptions(&d)

	f, err := a.loadFilter(r, sess)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	sr, err := a.Store.SavingsRate(f, a.household(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	fv := a.filterViewFor(r, sess, f, "/reports/savings", "", "")
	d.Filter = fv
	d.SavingsRate = sr
	d.IncomeTotal = sr.Income
	d.ExpenseTotal = sr.Expense
	d.ReportTotal = sr.Saved
	d.ChartJSON = template.JS(chartJSONSavingsRate(sr))
	d.ShowChart = true
	a.Render.RenderHTTP(w, "savings-rate", d)
}

func chartJSONNetWorth(series models.NetWorthSeries) string {
	labels := make([]string, len(series.Points))
	data := make([]float64, len(series.Points))
	for i, p := range series.Points {
		labels[i] = p.Label
		data[i] = float64(p.Amount) / 100
	}
	datasets := []map[string]any{{
		"label":           "Net worth",
		"data":            data,
		"borderColor":     "rgba(47, 93, 80, 0.9)",
		"backgroundColor": "rgba(47, 93, 80, 0.12)",
		"fill":            true,
		"tension":         0.25,
		"pointRadius":     0,
	}}
	palette := []string{
		"rgba(60, 90, 140, 0.75)",
		"rgba(138, 106, 47, 0.75)",
		"rgba(139, 58, 58, 0.7)",
		"rgba(110, 70, 120, 0.7)",
		"rgba(70, 120, 100, 0.7)",
	}
	for i, acct := range series.Accounts {
		if i >= 5 {
			break
		}
		ad := make([]float64, len(acct.Points))
		for j, p := range acct.Points {
			ad[j] = float64(p.Amount) / 100
		}
		datasets = append(datasets, map[string]any{
			"label":           acct.AccountName,
			"data":            ad,
			"borderColor":     palette[i%len(palette)],
			"backgroundColor": "transparent",
			"fill":            false,
			"tension":         0.2,
			"pointRadius":     0,
			"borderWidth":     1.5,
		})
	}
	cfg := map[string]any{
		"type": "line",
		"data": map[string]any{"labels": labels, "datasets": datasets},
		"options": map[string]any{
			"responsive": true, "maintainAspectRatio": false,
			"scales": map[string]any{"y": map[string]any{"beginAtZero": false}},
		},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

func chartJSONBudgetVsActual(lines []models.BudgetLine) string {
	var labels []string
	var budgeted, spent []float64
	for _, ln := range lines {
		if ln.HasChildren || (ln.AmountCents <= 0 && ln.SpentCents <= 0) {
			continue
		}
		if len(labels) >= 12 {
			break
		}
		labels = append(labels, ln.CategoryName)
		budgeted = append(budgeted, float64(ln.AmountCents)/100)
		spent = append(spent, float64(ln.SpentCents)/100)
	}
	cfg := map[string]any{
		"type": "bar",
		"data": map[string]any{
			"labels": labels,
			"datasets": []map[string]any{
				{"label": "Budget", "data": budgeted, "backgroundColor": "rgba(47, 93, 80, 0.55)"},
				{"label": "Actual", "data": spent, "backgroundColor": "rgba(139, 58, 58, 0.7)"},
			},
		},
		"options": map[string]any{
			"indexAxis": "y",
			"responsive": true, "maintainAspectRatio": false,
			"scales": map[string]any{"x": map[string]any{"beginAtZero": true}},
		},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

func chartJSONSavingsRate(sr models.SavingsRateReport) string {
	labels := []string{"Income", "Expense", "Saved"}
	data := []float64{float64(sr.Income) / 100, float64(sr.Expense) / 100, float64(sr.Saved) / 100}
	colors := []string{"rgba(47, 93, 80, 0.8)", "rgba(139, 58, 58, 0.75)", "rgba(60, 90, 140, 0.8)"}
	if sr.HasTarget {
		labels = append(labels, "Target")
		data = append(data, float64(sr.TargetCents)/100)
		colors = append(colors, "rgba(138, 106, 47, 0.75)")
	}
	cfg := map[string]any{
		"type": "bar",
		"data": map[string]any{
			"labels": labels,
			"datasets": []map[string]any{{
				"label": "Amount", "data": data, "backgroundColor": colors, "borderRadius": 6,
			}},
		},
		"options": map[string]any{
			"responsive": true, "maintainAspectRatio": false,
			"plugins": map[string]any{"legend": map[string]any{"display": false}},
			"scales":  map[string]any{"y": map[string]any{"beginAtZero": true}},
		},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}
