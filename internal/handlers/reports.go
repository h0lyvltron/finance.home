package handlers

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"finance.home/internal/auth"
	"finance.home/internal/models"
	"finance.home/internal/store"
)

// setUncatBanner fills the "not categorized yet" banner. Its link lists the
// same rows on Activity; the review queue may not hold them all, because a
// row can be marked reviewed while still uncategorized.
func (a *App) setUncatBanner(d *pageData, f store.QueryFilter, h *models.Household, now time.Time) {
	d.UncatCount, d.UncatSpend, _ = a.Store.UncategorizedSummary(f, h, now)
	uf := f.Clone()
	uf.Uncategorized = true
	uf.NeedsReview = nil
	d.UncatHref = "/transactions?" + uf.EncodeQuery().Encode()
}

func (a *App) withFilterOptions(d *pageData) {
	accts, _ := a.Store.ListAccounts(false)
	cats, _ := a.Store.ListCategories("", false)
	tags, _ := a.Store.ListTags()
	d.Accounts = accts
	d.Categories = cats
	d.Tags = tags
}

func compareMode(r *http.Request) string {
	if strings.TrimSpace(r.FormValue("compare")) == "ly" {
		return "ly"
	}
	return ""
}

// reportGrain returns the grain the user picked (kept in links) and the grain to
// draw with, which falls back to one sized for the date range.
func reportGrain(r *http.Request, f store.QueryFilter) (explicit, effective string) {
	explicit = strings.TrimSpace(r.FormValue("grain"))
	effective = explicit
	if effective == "" {
		effective = store.AutoGrain(f.DateFrom, f.DateTo)
	}
	return explicit, effective
}

func (a *App) trendsPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "trends"
	a.withQuickAdd(&d)
	a.withFilterOptions(&d)

	f, err := a.loadFilter(r, sess)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	explicit, grain := reportGrain(r, f)
	h := a.household()
	now := time.Now()

	points, err := a.Store.CashFlow(f, grain, h, now)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Start at the first period with data, so a range that reaches back
	// before the first import isn't padded (or its averages diluted) by
	// empty periods.
	first := len(points)
	for i, p := range points {
		if p.Income != 0 || p.Expense != 0 {
			first = i
			break
		}
	}
	points = points[first:]
	for _, p := range points {
		d.IncomeTotal += p.Income
		d.ExpenseTotal += p.Expense
	}
	d.PeriodCount = len(points)
	d.ReportTotal = d.IncomeTotal - d.ExpenseTotal
	if d.PeriodCount > 0 {
		d.AvgIncome = d.IncomeTotal / int64(d.PeriodCount)
		d.AvgExpense = d.ExpenseTotal / int64(d.PeriodCount)
	}

	segs, _, err := a.Store.SpendingExplore(f, "category", 8, h, now)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	pt, err := a.Store.SegmentPivot(f, "category", grain, h, now)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for len(pt.ColKeys) > 1 && pt.ColTotals[pt.ColKeys[0]] == 0 {
		pt.ColKeys, pt.ColLabels = pt.ColKeys[1:], pt.ColLabels[1:]
	}
	a.setUncatBanner(&d, f, h, now)

	d.Filter = a.filterViewFor(r, sess, f, "/trends", explicit, "")
	d.Grain = grain
	d.CashFlow = points
	d.Segments = segs
	d.ChartJSON = template.JS(store.IncomeSpendingChart(points))
	d.TrendChartJSON = template.JS(store.PivotChart(pt))
	d.CategoryChartJSON = template.JS(store.CategoryChart(segs, false))
	d.ShowChart = d.IncomeTotal != 0 || d.ExpenseTotal != 0
	a.Render.RenderHTTP(w, "trends", d)
}

func (a *App) explorePage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "trends"
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

	h := a.household()
	now := time.Now()
	segs, grand, err := a.Store.SpendingExplore(f, groupBy, topN, h, now)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	d.CompareMode = compareMode(r)
	if d.CompareMode == "ly" && f.DateFrom != "" && f.DateTo != "" {
		ly := store.ShiftFilterLastYear(f)
		lySegs, lyGrand, err := a.Store.SpendingExplore(ly, groupBy, 50, h, now)
		if err == nil {
			segs = store.MergeSegmentCompare(segs, lySegs)
			d.CompareTotal = lyGrand
			d.CompareSegs = lySegs
		}
	}

	fv := a.filterViewFor(r, sess, f, "/explore", "", groupBy)
	d.Filter = fv
	d.GroupBy = groupBy
	d.Segments = segs
	d.ReportTotal = grand
	a.setUncatBanner(&d, f, h, now)
	d.ChartJSON = template.JS(store.CategoryChart(segs, d.CompareMode == "ly"))
	d.DrillBase = "/transactions"

	a.Render.RenderHTTP(w, "explore", d)
}

func (a *App) cashflowPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "trends"
	a.withQuickAdd(&d)
	a.withFilterOptions(&d)

	f, err := a.loadFilter(r, sess)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	explicit, grain := reportGrain(r, f)
	h := a.household()
	now := time.Now()
	points, err := a.Store.CashFlow(f, grain, h, now)
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

	d.CompareMode = compareMode(r)
	if d.CompareMode == "ly" && f.DateFrom != "" && f.DateTo != "" {
		ly := store.ShiftFilterLastYear(f)
		lyPoints, err := a.Store.CashFlow(ly, grain, h, now)
		if err == nil {
			points = store.MergeCashFlowCompare(points, lyPoints, grain)
			d.CompareCashFlow = lyPoints
			for _, p := range lyPoints {
				d.CompareIncome += p.Income
				d.CompareExpense += p.Expense
				d.CompareTotal += p.Net
			}
		}
	}

	fv := a.filterViewFor(r, sess, f, "/reports/cashflow", explicit, "")
	d.Filter = fv
	d.Grain = grain
	d.CashFlow = points
	a.setUncatBanner(&d, f, h, now)
	d.IncomeTotal = income
	d.ExpenseTotal = expense
	d.ReportTotal = net
	d.ChartJSON = template.JS(store.CashFlowChart(points, d.CompareMode == "ly"))
	d.DrillBase = "/transactions"

	a.Render.RenderHTTP(w, "cashflow", d)
}

func (a *App) pivotPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "trends"
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
	explicit, grain := reportGrain(r, f)
	if grain == "day" {
		grain = "week"
	}
	pt, err := a.Store.SegmentPivot(f, groupBy, grain, a.household(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	fv := a.filterViewFor(r, sess, f, "/reports/pivot", explicit, groupBy)
	d.Filter = fv
	d.GroupBy = groupBy
	d.Grain = grain
	d.Pivot = pt
	d.ReportTotal = pt.Grand
	d.ChartJSON = template.JS(store.PivotChart(pt))
	d.ShowChart = len(pt.ColKeys) > 0 && len(pt.ColKeys) <= 18 && len(pt.RowKeys) > 0 && len(pt.RowKeys) <= 12
	d.DrillBase = "/transactions"

	a.Render.RenderHTTP(w, "pivot", d)
}

func (a *App) networthPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "trends"
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
	d.ChartJSON = template.JS(store.NetWorthChart(series))
	d.ShowChart = len(series.Points) > 0
	a.Render.RenderHTTP(w, "networth", d)
}

func (a *App) budgetReportPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "trends"
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
	d.ChartJSON = template.JS(store.BudgetChart(lines))
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
	d.Nav = "trends"
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
	d.ChartJSON = template.JS(store.SavingsRateChart(sr))
	d.ShowChart = true
	a.Render.RenderHTTP(w, "savings-rate", d)
}
