package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"finance.home/internal/models"
)

// Widget catalog types (ordered list; S/M/L width — no free-form grid).
const (
	WidgetLeftToSpend   = "left_to_spend"
	WidgetNetWorth      = "net_worth"
	WidgetCashFlow      = "cash_flow"
	WidgetCategorySpend = "category_spend"
	WidgetBudgetHealth  = "budget_health"
	WidgetGoals         = "goals"
	WidgetAccounts      = "accounts"
	WidgetRecent        = "recent"
	WidgetInsights      = "insights"
	WidgetBills         = "bills"
	WidgetSavedFilter   = "saved_filter"
)

// CatalogEntry describes an addable widget type.
type CatalogEntry struct {
	Type        string
	Label       string
	DefaultWidth string
	NeedsFilter bool // saved_filter requires picking a saved filter
}

// WidgetCatalog is the fixed set of personal dashboard widgets.
func WidgetCatalog() []CatalogEntry {
	return []CatalogEntry{
		{Type: WidgetLeftToSpend, Label: "Left to spend", DefaultWidth: "l"},
		{Type: WidgetNetWorth, Label: "Net worth", DefaultWidth: "m"},
		{Type: WidgetCashFlow, Label: "Cash flow", DefaultWidth: "l"},
		{Type: WidgetCategorySpend, Label: "Category spend", DefaultWidth: "l"},
		{Type: WidgetBudgetHealth, Label: "Budget health", DefaultWidth: "m"},
		{Type: WidgetGoals, Label: "Goals", DefaultWidth: "m"},
		{Type: WidgetAccounts, Label: "Accounts", DefaultWidth: "m"},
		{Type: WidgetRecent, Label: "Recent", DefaultWidth: "l"},
		{Type: WidgetInsights, Label: "Insights", DefaultWidth: "m"},
		{Type: WidgetBills, Label: "Bills", DefaultWidth: "m"},
		{Type: WidgetSavedFilter, Label: "Saved filter", DefaultWidth: "s", NeedsFilter: true},
	}
}

func widgetLabel(t string) string {
	for _, c := range WidgetCatalog() {
		if c.Type == t {
			return c.Label
		}
	}
	return t
}

func validWidgetType(t string) bool {
	for _, c := range WidgetCatalog() {
		if c.Type == t {
			return true
		}
	}
	return false
}

func normalizeWidth(w string) string {
	switch strings.ToLower(strings.TrimSpace(w)) {
	case "s", "m", "l":
		return strings.ToLower(w)
	default:
		return "m"
	}
}

func normalizeRangePreset(p string) string {
	switch p {
	case "this_month", "last_30", "ytd", "last_12":
		return p
	default:
		return "this_month"
	}
}

// EnsureHouseholdDefault creates the household default layout once (curated Home shape).
func (s *Store) EnsureHouseholdDefault() (*models.DashboardLayout, error) {
	layout, err := s.getLayoutByPerson(nil)
	if err == nil {
		return layout, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.DB.Exec(`INSERT INTO dashboard_layouts(person_id, range_preset, created_at, updated_at)
		VALUES (NULL, 'this_month', ?, ?)`, now, now)
	if err != nil {
		// Race: another request inserted default
		if layout, err2 := s.getLayoutByPerson(nil); err2 == nil {
			return layout, nil
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	defaults := []struct {
		Type  string
		Width string
	}{
		{WidgetLeftToSpend, "l"},
		{WidgetInsights, "m"},
		{WidgetBudgetHealth, "m"},
		{WidgetBills, "m"},
		{WidgetNetWorth, "s"},
		{WidgetRecent, "l"},
	}
	for i, d := range defaults {
		if _, err := s.DB.Exec(`INSERT INTO dashboard_widgets(layout_id, widget_type, width, sort_order, config_json, created_at)
			VALUES (?,?,?,?, '{}', ?)`, id, d.Type, d.Width, i, now); err != nil {
			return nil, err
		}
	}
	return s.GetLayout(id)
}

// EnsurePersonDashboard copies the household default once on first use, then returns the personal layout.
func (s *Store) EnsurePersonDashboard(personID int64) (*models.DashboardLayout, error) {
	if personID <= 0 {
		return nil, fmt.Errorf("person required")
	}
	def, err := s.EnsureHouseholdDefault()
	if err != nil {
		return nil, err
	}
	layout, err := s.getLayoutByPerson(&personID)
	if err == nil {
		return layout, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.DB.Exec(`INSERT INTO dashboard_layouts(person_id, range_preset, created_at, updated_at)
		VALUES (?,?,?,?)`, personID, def.RangePreset, now, now)
	if err != nil {
		if layout, err2 := s.getLayoutByPerson(&personID); err2 == nil {
			return layout, nil
		}
		return nil, err
	}
	newID, _ := res.LastInsertId()
	widgets, err := s.ListWidgets(def.ID)
	if err != nil {
		return nil, err
	}
	for _, w := range widgets {
		cfg := w.ConfigJSON
		if cfg == "" {
			cfg = "{}"
		}
		if _, err := s.DB.Exec(`INSERT INTO dashboard_widgets(layout_id, widget_type, width, sort_order, config_json, created_at)
			VALUES (?,?,?,?,?,?)`, newID, w.WidgetType, w.Width, w.SortOrder, cfg, now); err != nil {
			return nil, err
		}
	}
	return s.GetLayout(newID)
}

func (s *Store) GetLayout(id int64) (*models.DashboardLayout, error) {
	l := &models.DashboardLayout{}
	var pid sql.NullInt64
	err := s.DB.QueryRow(`SELECT id, person_id, range_preset, created_at, updated_at FROM dashboard_layouts WHERE id=?`, id).
		Scan(&l.ID, &pid, &l.RangePreset, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if pid.Valid {
		v := pid.Int64
		l.PersonID = &v
	}
	return l, nil
}

func (s *Store) getLayoutByPerson(personID *int64) (*models.DashboardLayout, error) {
	l := &models.DashboardLayout{}
	var pid sql.NullInt64
	var err error
	if personID == nil {
		err = s.DB.QueryRow(`SELECT id, person_id, range_preset, created_at, updated_at
			FROM dashboard_layouts WHERE person_id IS NULL`).
			Scan(&l.ID, &pid, &l.RangePreset, &l.CreatedAt, &l.UpdatedAt)
	} else {
		err = s.DB.QueryRow(`SELECT id, person_id, range_preset, created_at, updated_at
			FROM dashboard_layouts WHERE person_id=?`, *personID).
			Scan(&l.ID, &pid, &l.RangePreset, &l.CreatedAt, &l.UpdatedAt)
	}
	if err != nil {
		return nil, err
	}
	if pid.Valid {
		v := pid.Int64
		l.PersonID = &v
	}
	return l, nil
}

func (s *Store) ListWidgets(layoutID int64) ([]models.DashboardWidget, error) {
	rows, err := s.DB.Query(`SELECT id, layout_id, widget_type, width, sort_order, config_json, created_at
		FROM dashboard_widgets WHERE layout_id=? ORDER BY sort_order ASC, id ASC`, layoutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.DashboardWidget
	for rows.Next() {
		var w models.DashboardWidget
		if err := rows.Scan(&w.ID, &w.LayoutID, &w.WidgetType, &w.Width, &w.SortOrder, &w.ConfigJSON, &w.CreatedAt); err != nil {
			return nil, err
		}
		w.Label = widgetLabel(w.WidgetType)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) GetWidget(id int64) (*models.DashboardWidget, error) {
	w := &models.DashboardWidget{}
	err := s.DB.QueryRow(`SELECT id, layout_id, widget_type, width, sort_order, config_json, created_at
		FROM dashboard_widgets WHERE id=?`, id).
		Scan(&w.ID, &w.LayoutID, &w.WidgetType, &w.Width, &w.SortOrder, &w.ConfigJSON, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	w.Label = widgetLabel(w.WidgetType)
	return w, nil
}

func (s *Store) layoutOwnedBy(layoutID, personID int64) error {
	var pid sql.NullInt64
	err := s.DB.QueryRow(`SELECT person_id FROM dashboard_layouts WHERE id=?`, layoutID).Scan(&pid)
	if err != nil {
		return err
	}
	if !pid.Valid || pid.Int64 != personID {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) touchLayout(layoutID int64) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.DB.Exec(`UPDATE dashboard_layouts SET updated_at=? WHERE id=?`, now, layoutID)
}

func (s *Store) SetDashboardRange(personID int64, preset string) error {
	layout, err := s.EnsurePersonDashboard(personID)
	if err != nil {
		return err
	}
	preset = normalizeRangePreset(preset)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.DB.Exec(`UPDATE dashboard_layouts SET range_preset=?, updated_at=? WHERE id=? AND person_id=?`,
		preset, now, layout.ID, personID)
	return err
}

type AddWidgetInput struct {
	WidgetType    string
	Width         string
	SavedFilterID int64 // for saved_filter
}

func (s *Store) AddWidget(personID int64, in AddWidgetInput) (int64, error) {
	if !validWidgetType(in.WidgetType) {
		return 0, fmt.Errorf("unknown widget type")
	}
	layout, err := s.EnsurePersonDashboard(personID)
	if err != nil {
		return 0, err
	}
	width := strings.TrimSpace(in.Width)
	if width == "" {
		width = "m"
		for _, c := range WidgetCatalog() {
			if c.Type == in.WidgetType {
				width = c.DefaultWidth
				break
			}
		}
	} else {
		width = normalizeWidth(width)
	}
	cfg := map[string]any{}
	if in.WidgetType == WidgetSavedFilter {
		if in.SavedFilterID <= 0 {
			return 0, fmt.Errorf("pick a saved filter")
		}
		cfg["saved_filter_id"] = in.SavedFilterID
	}
	cfgJSON, _ := json.Marshal(cfg)
	var maxOrd int
	_ = s.DB.QueryRow(`SELECT COALESCE(MAX(sort_order), -1) FROM dashboard_widgets WHERE layout_id=?`, layout.ID).Scan(&maxOrd)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.DB.Exec(`INSERT INTO dashboard_widgets(layout_id, widget_type, width, sort_order, config_json, created_at)
		VALUES (?,?,?,?,?,?)`, layout.ID, in.WidgetType, width, maxOrd+1, string(cfgJSON), now)
	if err != nil {
		return 0, err
	}
	s.touchLayout(layout.ID)
	return res.LastInsertId()
}

func (s *Store) RemoveWidget(personID, widgetID int64) error {
	w, err := s.GetWidget(widgetID)
	if err != nil {
		return err
	}
	if err := s.layoutOwnedBy(w.LayoutID, personID); err != nil {
		return err
	}
	_, err = s.DB.Exec(`DELETE FROM dashboard_widgets WHERE id=?`, widgetID)
	if err != nil {
		return err
	}
	s.touchLayout(w.LayoutID)
	return nil
}

func (s *Store) SetWidgetWidth(personID, widgetID int64, width string) error {
	w, err := s.GetWidget(widgetID)
	if err != nil {
		return err
	}
	if err := s.layoutOwnedBy(w.LayoutID, personID); err != nil {
		return err
	}
	width = normalizeWidth(width)
	_, err = s.DB.Exec(`UPDATE dashboard_widgets SET width=? WHERE id=?`, width, widgetID)
	if err != nil {
		return err
	}
	s.touchLayout(w.LayoutID)
	return nil
}

func (s *Store) MoveWidget(personID, widgetID int64, dir string) error {
	w, err := s.GetWidget(widgetID)
	if err != nil {
		return err
	}
	if err := s.layoutOwnedBy(w.LayoutID, personID); err != nil {
		return err
	}
	widgets, err := s.ListWidgets(w.LayoutID)
	if err != nil {
		return err
	}
	idx := -1
	for i, x := range widgets {
		if x.ID == widgetID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return sql.ErrNoRows
	}
	swap := idx
	if dir == "up" && idx > 0 {
		swap = idx - 1
	} else if dir == "down" && idx < len(widgets)-1 {
		swap = idx + 1
	} else {
		return nil
	}
	a, b := widgets[idx], widgets[swap]
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE dashboard_widgets SET sort_order=? WHERE id=?`, b.SortOrder, a.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE dashboard_widgets SET sort_order=? WHERE id=?`, a.SortOrder, b.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.touchLayout(w.LayoutID)
	return nil
}

// WidgetView is a populated widget for rendering Home.
type WidgetView struct {
	models.DashboardWidget
	// Shared payload slices (only relevant fields filled per type)
	LeftToSpend  models.LeftToSpend
	BudgetHealth models.BudgetHealth
	Bills        []models.ScheduleInstance
	Insights     []models.CoachingInsight
	NetWorth     int64
	Accounts     []models.Account
	Transactions []models.Transaction
	Goals        []models.GoalProgress
	CashFlow     []CashFlowPoint
	Segments     []SegmentTotal
	SegmentTotal int64
	SavedFilter  *SavedFilter
	SavedHref    string
	ChartJSON    string
	ShowChart    bool
	Error        string
}

// PersonalDashboard is the assembled Home for one person.
type PersonalDashboard struct {
	Layout    models.DashboardLayout
	Widgets   []WidgetView
	Catalog   []CatalogEntry
	Saved     []SavedFilter
	RangeFrom string
	RangeTo   string
	YearMonth string
	ReviewCount int
}

// BuildPersonalDashboard ensures a layout exists and fills each widget for the range.
func (s *Store) BuildPersonalDashboard(personID int64, h *models.Household, now time.Time) (PersonalDashboard, error) {
	var out PersonalDashboard
	layout, err := s.EnsurePersonDashboard(personID)
	if err != nil {
		return out, err
	}
	out.Layout = *layout
	out.Catalog = WidgetCatalog()
	out.Saved, _ = s.ListSavedFilters(personID)
	out.YearMonth = YearMonth(h, now)
	out.ReviewCount, _ = s.NeedsReviewCount()

	f := QueryFilter{DatePreset: normalizeRangePreset(layout.RangePreset)}
	f.ResolveDates(h, now)
	out.RangeFrom, out.RangeTo = f.DateFrom, f.DateTo

	widgets, err := s.ListWidgets(layout.ID)
	if err != nil {
		return out, err
	}

	// Shared fetches (lazy-filled once)
	var (
		dash        *HomeDashboard
		accts       []models.Account
		txs         []models.Transaction
		goals       []models.GoalProgress
		cashFlow    []CashFlowPoint
		segments    []SegmentTotal
		segTotal    int64
		haveDash    bool
		haveAccts   bool
		haveTxs     bool
		haveGoals   bool
		haveCF      bool
		haveSeg     bool
	)

	loadDash := func() error {
		if haveDash {
			return nil
		}
		d, err := s.BuildHomeDashboardFor(h, now, personID)
		if err != nil {
			return err
		}
		dash = &d
		haveDash = true
		return nil
	}
	loadAccts := func() error {
		if haveAccts {
			return nil
		}
		a, err := s.ListAccounts(false)
		if err != nil {
			return err
		}
		accts = a
		haveAccts = true
		return nil
	}
	loadTxs := func() error {
		if haveTxs {
			return nil
		}
		tf := f
		tf.Limit = 8
		tf.ExcludeTransfers = false
		list, err := s.ListTransactions(tf)
		if err != nil {
			return err
		}
		txs = list
		haveTxs = true
		return nil
	}
	loadGoals := func() error {
		if haveGoals {
			return nil
		}
		g, err := s.ListGoalProgress(now)
		if err != nil {
			return err
		}
		goals = g
		haveGoals = true
		return nil
	}
	loadCF := func() error {
		if haveCF {
			return nil
		}
		grain := grainForDashboardRange(layout.RangePreset)
		cf, err := s.CashFlow(f, grain, h, now)
		if err != nil {
			return err
		}
		cashFlow = cf
		haveCF = true
		return nil
	}
	loadSeg := func() error {
		if haveSeg {
			return nil
		}
		segs, total, err := s.SpendingExplore(f, "category", 8, h, now)
		if err != nil {
			return err
		}
		segments = segs
		segTotal = total
		haveSeg = true
		return nil
	}

	out.Widgets = make([]WidgetView, 0, len(widgets))
	for _, w := range widgets {
		v := WidgetView{DashboardWidget: w}
		switch w.WidgetType {
		case WidgetLeftToSpend:
			if err := loadDash(); err != nil {
				v.Error = err.Error()
			} else {
				v.LeftToSpend = dash.LeftToSpend
			}
		case WidgetBudgetHealth:
			if err := loadDash(); err != nil {
				v.Error = err.Error()
			} else {
				v.BudgetHealth = dash.BudgetHealth
			}
		case WidgetBills:
			if err := loadDash(); err != nil {
				v.Error = err.Error()
			} else {
				v.Bills = dash.Bills
			}
		case WidgetInsights:
			if err := loadDash(); err != nil {
				v.Error = err.Error()
			} else {
				v.Insights = dash.Insights
			}
		case WidgetNetWorth:
			if err := loadDash(); err != nil {
				v.Error = err.Error()
			} else {
				v.NetWorth = dash.NetWorth
			}
			_ = loadAccts()
			v.Accounts = accts
		case WidgetAccounts:
			if err := loadAccts(); err != nil {
				v.Error = err.Error()
			} else {
				v.Accounts = accts
			}
		case WidgetRecent:
			if err := loadTxs(); err != nil {
				v.Error = err.Error()
			} else {
				v.Transactions = txs
			}
		case WidgetGoals:
			if err := loadGoals(); err != nil {
				v.Error = err.Error()
			} else {
				v.Goals = goals
			}
		case WidgetCashFlow:
			if err := loadCF(); err != nil {
				v.Error = err.Error()
			} else {
				v.CashFlow = cashFlow
				v.ChartJSON = dashboardCashFlowChart(cashFlow)
				v.ShowChart = len(cashFlow) > 0
			}
		case WidgetCategorySpend:
			if err := loadSeg(); err != nil {
				v.Error = err.Error()
			} else {
				v.Segments = segments
				v.SegmentTotal = segTotal
				v.ChartJSON = dashboardCategoryChart(segments)
				v.ShowChart = len(segments) > 0
			}
		case WidgetSavedFilter:
			sfID := savedFilterIDFromConfig(w.ConfigJSON)
			if sfID <= 0 {
				v.Error = "No saved filter linked"
			} else if sf, err := s.GetSavedFilter(sfID); err != nil {
				v.Error = "Saved filter missing"
			} else {
				v.SavedFilter = sf
				qf := FilterFromSaved(sf)
				v.SavedHref = "/transactions?" + qf.EncodeQuery().Encode()
			}
		}
		out.Widgets = append(out.Widgets, v)
	}
	return out, nil
}

func grainForDashboardRange(preset string) string {
	switch normalizeRangePreset(preset) {
	case "this_month", "last_30":
		return "week"
	case "ytd", "last_12":
		return "month"
	default:
		return "week"
	}
}

func savedFilterIDFromConfig(cfg string) int64 {
	if cfg == "" {
		return 0
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(cfg), &m); err != nil {
		return 0
	}
	return int64Val(m["saved_filter_id"])
}

func dashboardCashFlowChart(points []CashFlowPoint) string {
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
			"responsive":          true,
			"maintainAspectRatio": false,
			"scales": map[string]any{
				"y": map[string]any{"beginAtZero": true},
			},
		},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

func dashboardCategoryChart(segs []SegmentTotal) string {
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
			"indexAxis":           "y",
			"responsive":          true,
			"maintainAspectRatio": false,
			"plugins":             map[string]any{"legend": map[string]any{"display": false}},
			"scales":              map[string]any{"x": map[string]any{"beginAtZero": true}},
		},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}
