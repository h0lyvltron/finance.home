package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"finance.home/internal/models"
)

// Widget catalog types (placed on a 12-column cell grid; see dashboard_grid.go).
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
	Type         string
	Label        string
	DefaultWidth string
	DefaultH     int  // default row span on the cell grid
	NeedsFilter  bool // saved_filter requires picking a saved filter
}

// WidgetCatalog is the fixed set of personal dashboard widgets.
func WidgetCatalog() []CatalogEntry {
	return []CatalogEntry{
		{Type: WidgetLeftToSpend, Label: "Left to spend", DefaultWidth: "l", DefaultH: 3},
		{Type: WidgetNetWorth, Label: "Net worth", DefaultWidth: "m", DefaultH: 2},
		{Type: WidgetCashFlow, Label: "Cash flow", DefaultWidth: "l", DefaultH: 4},
		{Type: WidgetCategorySpend, Label: "Category spend", DefaultWidth: "l", DefaultH: 4},
		{Type: WidgetBudgetHealth, Label: "Budget health", DefaultWidth: "m", DefaultH: 3},
		{Type: WidgetGoals, Label: "Goals", DefaultWidth: "m", DefaultH: 4},
		{Type: WidgetAccounts, Label: "Accounts", DefaultWidth: "m", DefaultH: 4},
		{Type: WidgetRecent, Label: "Recent", DefaultWidth: "l", DefaultH: 8},
		{Type: WidgetInsights, Label: "Insights", DefaultWidth: "m", DefaultH: 4},
		{Type: WidgetBills, Label: "Bills", DefaultWidth: "m", DefaultH: 4},
		{Type: WidgetSavedFilter, Label: "Saved filter", DefaultWidth: "s", DefaultH: 2, NeedsFilter: true},
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
		return layout, s.ensureGrid(layout.ID)
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
		Type           string
		Col, Row, W, H int
	}{
		{WidgetLeftToSpend, 1, 1, 8, 3},
		{WidgetNetWorth, 9, 1, 4, 3},
		{WidgetInsights, 1, 4, 6, 4},
		{WidgetBudgetHealth, 7, 4, 6, 4},
		{WidgetBills, 1, 8, 6, 5},
		{WidgetRecent, 7, 8, 6, 8},
	}
	for i, d := range defaults {
		if _, err := s.DB.Exec(`INSERT INTO dashboard_widgets(layout_id, widget_type, width, sort_order, grid_col, grid_row, grid_w, grid_h, config_json, created_at)
			VALUES (?,?,?,?,?,?,?,?, '{}', ?)`, id, d.Type, widthForSpan(d.W), i, d.Col, d.Row, d.W, d.H, now); err != nil {
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
		return layout, s.ensureGrid(layout.ID)
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
	if err := copyWidgets(s.DB, widgets, newID, now); err != nil {
		return nil, err
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
	rows, err := s.DB.Query(`SELECT `+widgetCols+`
		FROM dashboard_widgets WHERE layout_id=? ORDER BY grid_row ASC, grid_col ASC, sort_order ASC, id ASC`, layoutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.DashboardWidget
	for rows.Next() {
		var w models.DashboardWidget
		if err := scanWidget(rows, &w); err != nil {
			return nil, err
		}
		w.Label = widgetLabel(w.WidgetType)
		out = append(out, w)
	}
	return out, rows.Err()
}

const widgetCols = `id, layout_id, widget_type, width, sort_order, grid_col, grid_row, grid_w, grid_h, config_json, created_at`

func scanWidget(row interface{ Scan(...any) error }, w *models.DashboardWidget) error {
	return row.Scan(&w.ID, &w.LayoutID, &w.WidgetType, &w.Width, &w.SortOrder,
		&w.GridCol, &w.GridRow, &w.GridW, &w.GridH, &w.ConfigJSON, &w.CreatedAt)
}

func (s *Store) GetWidget(id int64) (*models.DashboardWidget, error) {
	w := &models.DashboardWidget{}
	if err := scanWidget(s.DB.QueryRow(`SELECT `+widgetCols+` FROM dashboard_widgets WHERE id=?`, id), w); err != nil {
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
	var maxOrd, bottom int
	_ = s.DB.QueryRow(`SELECT COALESCE(MAX(sort_order), -1), COALESCE(MAX(grid_row + grid_h), 1)
		FROM dashboard_widgets WHERE layout_id=?`, layout.ID).Scan(&maxOrd, &bottom)
	r := clampRect(gridRect{Col: 1, Row: bottom, W: spanForWidth(in.WidgetType, width), H: defaultHeight(in.WidgetType)})
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.DB.Exec(`INSERT INTO dashboard_widgets(layout_id, widget_type, width, sort_order, grid_col, grid_row, grid_w, grid_h, config_json, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, layout.ID, in.WidgetType, width, maxOrd+1, r.Col, r.Row, r.W, r.H, string(cfgJSON), now)
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
	Layout      models.DashboardLayout
	Widgets     []WidgetView
	Catalog     []CatalogEntry
	Saved       []SavedFilter
	RangeFrom   string
	RangeTo     string
	YearMonth   string
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
		dash      *HomeDashboard
		accts     []models.Account
		txs       []models.Transaction
		goals     []models.GoalProgress
		cashFlow  []CashFlowPoint
		segments  []SegmentTotal
		segTotal  int64
		haveDash  bool
		haveAccts bool
		haveTxs   bool
		haveGoals bool
		haveCF    bool
		haveSeg   bool
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
				v.ChartJSON = CashFlowChart(cashFlow, false)
				v.ShowChart = len(cashFlow) > 0
			}
		case WidgetCategorySpend:
			if err := loadSeg(); err != nil {
				v.Error = err.Error()
			} else {
				v.Segments = segments
				v.SegmentTotal = segTotal
				v.ChartJSON = CategoryChart(segments, false)
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
