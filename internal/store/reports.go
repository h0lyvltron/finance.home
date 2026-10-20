package store

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"finance.home/internal/models"
)

type CashFlowPoint struct {
	Period         string
	Label          string
	Income         int64 // positive cents
	Expense        int64 // positive cents (magnitude of spend)
	Net            int64 // income - expense (signed)
	InProgress     bool
	CompareIncome  int64
	CompareExpense int64
	CompareNet     int64
	HasCompare     bool
}

type SegmentTotal struct {
	Key           string
	Label         string
	Amount        int64 // signed (expense negative) or spend magnitude depending on caller
	Count         int
	DrillQuery    string // query string fragment for /transactions
	Color         string
	CompareAmount int64
	HasCompare    bool
	Delta         int64   // Amount - CompareAmount
	SharePct      float64 // share of the grand total, 0–100
}

// MergeCashFlowCompare attaches same-period-last-year values onto current points.
// comparePoints should already be computed for the shifted date range.
func MergeCashFlowCompare(current, compare []CashFlowPoint, grain string) []CashFlowPoint {
	byPeriod := map[string]CashFlowPoint{}
	for _, p := range compare {
		byPeriod[p.Period] = p
	}
	out := make([]CashFlowPoint, len(current))
	for i, p := range current {
		out[i] = p
		lyKey := shiftPeriodKey(p.Period, grain, -1)
		if ly, ok := byPeriod[lyKey]; ok {
			out[i].CompareIncome = ly.Income
			out[i].CompareExpense = ly.Expense
			out[i].CompareNet = ly.Net
			out[i].HasCompare = true
		}
	}
	return out
}

// MergeSegmentCompare attaches last-year amounts by segment key.
func MergeSegmentCompare(current, compare []SegmentTotal) []SegmentTotal {
	byKey := map[string]int64{}
	for _, s := range compare {
		byKey[s.Key] = s.Amount
	}
	out := make([]SegmentTotal, len(current))
	for i, s := range current {
		out[i] = s
		if amt, ok := byKey[s.Key]; ok {
			out[i].CompareAmount = amt
			out[i].HasCompare = true
			out[i].Delta = s.Amount - amt
		}
	}
	return out
}

func shiftPeriodKey(key, grain string, years int) string {
	switch grain {
	case "day", "week":
		t, err := time.Parse("2006-01-02", key)
		if err != nil {
			return key
		}
		return t.AddDate(years, 0, 0).Format("2006-01-02")
	case "quarter":
		// YYYY-Qn
		var y, q int
		if _, err := fmt.Sscanf(key, "%d-Q%d", &y, &q); err != nil {
			return key
		}
		return fmt.Sprintf("%04d-Q%d", y+years, q)
	case "year":
		var y int
		if _, err := fmt.Sscanf(key, "%d", &y); err != nil {
			return key
		}
		return fmt.Sprintf("%04d", y+years)
	default: // month YYYY-MM
		t, err := time.Parse("2006-01", key)
		if err != nil {
			return key
		}
		return t.AddDate(years, 0, 0).Format("2006-01")
	}
}

type PivotCell struct {
	Amount int64
	Count  int
}

type PivotTable struct {
	RowKeys   []string
	RowLabels []string
	ColKeys   []string
	ColLabels []string
	Cells     map[string]map[string]PivotCell // row -> col
	RowTotals map[string]int64
	ColTotals map[string]int64
	Grand     int64
	InProgCol string            // column key that is in-progress, if any
	RowColors map[string]string // row key -> category color, when rows are categories
}

// categoryColors maps category id (as a string key) to its #RRGGBB color.
func (s *Store) categoryColors() map[string]string {
	out := map[string]string{}
	rows, err := s.DB.Query(`SELECT id, UPPER(COALESCE(color,'')) FROM categories WHERE COALESCE(color,'') != ''`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var c string
		if rows.Scan(&id, &c) == nil && hexColor.MatchString(c) {
			out[strconv.FormatInt(id, 10)] = c
		}
	}
	return out
}

func grainExpr(grain string) string {
	switch grain {
	case "day":
		return `t.transaction_date`
	case "week":
		// ISO-ish week start Monday via SQLite strftime
		return `date(t.transaction_date, '-' || ((CAST(strftime('%w', t.transaction_date) AS INTEGER) + 6) % 7) || ' days')`
	case "quarter":
		return `printf('%04d-Q%d', CAST(strftime('%Y', t.transaction_date) AS INTEGER),
			(CAST(strftime('%m', t.transaction_date) AS INTEGER) + 2) / 3)`
	case "year":
		return `strftime('%Y', t.transaction_date)`
	default: // month
		return `strftime('%Y-%m', t.transaction_date)`
	}
}

// reportLines yields one row per split plus one row for every transaction that
// has no splits (uncategorized imports awaiting review), so report totals
// include money that hasn't been categorized yet. Alias it as s and LEFT JOIN
// categories c ON c.id = s.category_id; use lineKind for the effective kind.
const reportLines = `(SELECT sp.transaction_id, sp.category_id, sp.person, sp.amount FROM transaction_splits sp
		UNION ALL
		SELECT t0.id, t0.category_id, t0.person, t0.amount FROM transactions t0
		WHERE NOT EXISTS (SELECT 1 FROM transaction_splits sp0 WHERE sp0.transaction_id = t0.id))`

// lineKind treats uncategorized outflows as expense and inflows as income.
const lineKind = `(CASE WHEN c.id IS NOT NULL THEN c.kind WHEN s.amount < 0 THEN 'expense' ELSE 'income' END)`

// UncategorizedLabel names the bucket for transactions without a category.
const UncategorizedLabel = "Uncategorized"

// AutoGrain picks a readable bucket size for a date range.
func AutoGrain(from, to string) string {
	a, err1 := time.Parse("2006-01-02", from)
	b, err2 := time.Parse("2006-01-02", to)
	if err1 != nil || err2 != nil {
		return "month"
	}
	days := b.Sub(a).Hours() / 24
	switch {
	case days <= 45:
		return "week"
	case days <= 731:
		return "month"
	default:
		return "quarter"
	}
}

func defaultGrain(grain string) string {
	switch grain {
	case "day", "week", "month", "quarter", "year":
		return grain
	default:
		return "month"
	}
}

func (s *Store) CashFlow(f QueryFilter, grain string, h *models.Household, now time.Time) ([]CashFlowPoint, error) {
	grain = defaultGrain(grain)
	f = f.Clone()
	f.ExcludeTransfers = true
	f.ExcludeStarting = true
	f.ResolveDates(h, now)

	frag, err := s.buildTxFilter(f, true)
	if err != nil {
		return nil, err
	}
	g := grainExpr(grain)
	q := `SELECT ` + g + ` AS period,
		COALESCE(SUM(CASE WHEN ` + lineKind + ` = 'income' THEN s.amount ELSE 0 END), 0) AS income,
		COALESCE(SUM(CASE WHEN ` + lineKind + ` = 'expense' THEN s.amount ELSE 0 END), 0) AS expense
		FROM ` + reportLines + ` s
		JOIN transactions t ON t.id = s.transaction_id
		LEFT JOIN categories c ON c.id = s.category_id` + frag.joins + `
		WHERE` + frag.where + ` AND ` + lineKind + ` IN ('income','expense')
		GROUP BY period
		ORDER BY period`
	rows, err := s.DB.Query(q, frag.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CashFlowPoint
	currentKey := currentPeriodKey(grain, now, h)
	for rows.Next() {
		var p CashFlowPoint
		var income, expense int64
		if err := rows.Scan(&p.Period, &income, &expense); err != nil {
			return nil, err
		}
		p.Income = income
		if expense < 0 {
			p.Expense = -expense
		} else {
			p.Expense = expense
		}
		p.Net = income + expense // expense usually negative
		p.Label = formatPeriodLabel(grain, p.Period)
		p.InProgress = p.Period == currentKey
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return FillCashFlowGaps(out, grain, f.DateFrom, f.DateTo, now, h), nil
}

func currentPeriodKey(grain string, now time.Time, h *models.Household) string {
	if h != nil && h.Timezone != "" {
		if loc, err := time.LoadLocation(h.Timezone); err == nil {
			now = now.In(loc)
		}
	}
	switch grain {
	case "day":
		return now.Format("2006-01-02")
	case "week":
		// Monday of this week
		wd := int(now.Weekday())
		if wd == 0 {
			wd = 7
		}
		mon := now.AddDate(0, 0, -(wd - 1))
		return time.Date(mon.Year(), mon.Month(), mon.Day(), 0, 0, 0, 0, mon.Location()).Format("2006-01-02")
	case "quarter":
		q := (int(now.Month()) + 2) / 3
		return fmt.Sprintf("%04d-Q%d", now.Year(), q)
	case "year":
		return fmt.Sprintf("%04d", now.Year())
	default:
		return now.Format("2006-01")
	}
}

func formatPeriodLabel(grain, key string) string {
	switch grain {
	case "day":
		return key
	case "week":
		return "Week of " + key
	case "quarter":
		return key
	case "year":
		return key
	default:
		if len(key) == 7 {
			t, err := time.Parse("2006-01", key)
			if err == nil {
				return t.Format("Jan 2006")
			}
		}
		return key
	}
}

// SpendingExplore groups expense (and optional income) by a dimension.
// Amounts are spend magnitude (positive) for expense groups.
func (s *Store) SpendingExplore(f QueryFilter, groupBy string, topN int, h *models.Household, now time.Time) ([]SegmentTotal, int64, error) {
	groupBy = strings.ToLower(groupBy)
	switch groupBy {
	case "category", "payee", "tag", "person", "account":
	default:
		groupBy = "category"
	}
	if topN <= 0 {
		topN = 8
	}
	f = f.Clone()
	f.ExcludeTransfers = true
	f.ExcludeStarting = true
	f.ResolveDates(h, now)

	frag, err := s.buildTxFilter(f, true)
	if err != nil {
		return nil, 0, err
	}

	var selectKey, selectLabel, groupClause, extraJoin string
	switch groupBy {
	case "payee":
		selectKey = `COALESCE(t.payee_name,'(no payee)')`
		selectLabel = selectKey
		groupClause = selectKey
	case "person":
		selectKey = `s.person`
		selectLabel = `s.person`
		groupClause = `s.person`
	case "account":
		selectKey = `CAST(t.account_id AS TEXT)`
		selectLabel = `a.name`
		extraJoin = ` JOIN accounts a ON a.id = t.account_id`
		groupClause = `t.account_id, a.name`
	case "tag":
		// explode tags; untagged as (untagged)
		selectKey = `CAST(COALESCE(tg.id, 0) AS TEXT)`
		selectLabel = `COALESCE(tg.name,'(untagged)')`
		extraJoin = ` LEFT JOIN transaction_tags tt ON tt.transaction_id = t.id
			LEFT JOIN tags tg ON tg.id = tt.tag_id`
		groupClause = `tg.id, tg.name`
	default: // category
		selectKey = `CAST(COALESCE(c.id, 0) AS TEXT)`
		selectLabel = `COALESCE(c.name, '` + UncategorizedLabel + `')`
		groupClause = `c.id, c.name`
	}

	q := `SELECT ` + selectKey + ` AS k, ` + selectLabel + ` AS lbl,
		COALESCE(SUM(s.amount), 0) AS amt, COUNT(DISTINCT t.id) AS cnt
		FROM ` + reportLines + ` s
		JOIN transactions t ON t.id = s.transaction_id
		LEFT JOIN categories c ON c.id = s.category_id` + frag.joins + extraJoin + `
		WHERE` + frag.where + ` AND ` + lineKind + ` = 'expense'
		GROUP BY ` + groupClause + `
		ORDER BY amt ASC` // most negative spend first

	rows, err := s.DB.Query(q, frag.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var raw []SegmentTotal
	var grand int64
	for rows.Next() {
		var seg SegmentTotal
		var amt int64
		if err := rows.Scan(&seg.Key, &seg.Label, &amt, &seg.Count); err != nil {
			return nil, 0, err
		}
		spend := -amt
		if spend < 0 {
			spend = amt // unexpected positive expense; keep as-is magnitude
			if spend < 0 {
				spend = -spend
			}
		}
		seg.Amount = spend
		grand += spend
		seg.DrillQuery = exploreDrillQuery(f, groupBy, seg.Key, seg.Label)
		raw = append(raw, seg)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	if groupBy == "category" {
		colors := s.categoryColors()
		for i := range raw {
			raw[i].Color = colors[raw[i].Key]
		}
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i].Amount > raw[j].Amount })
	if grand > 0 {
		for i := range raw {
			raw[i].SharePct = float64(raw[i].Amount) * 100 / float64(grand)
		}
	}

	if len(raw) <= topN {
		return raw, grand, nil
	}
	out := make([]SegmentTotal, 0, topN+1)
	out = append(out, raw[:topN]...)
	var otherAmt int64
	var otherCnt int
	var otherPct float64
	for _, seg := range raw[topN:] {
		otherAmt += seg.Amount
		otherCnt += seg.Count
		otherPct += seg.SharePct
	}
	out = append(out, SegmentTotal{
		Key:      "__other__",
		Label:    "Other",
		Amount:   otherAmt,
		Count:    otherCnt,
		SharePct: otherPct,
	})
	return out, grand, nil
}

// UncategorizedSummary counts non-transfer transactions in the filter range that
// have no category, and their total outflow (positive cents).
func (s *Store) UncategorizedSummary(f QueryFilter, h *models.Household, now time.Time) (int, int64, error) {
	f = f.Clone()
	f.ExcludeTransfers = true
	f.ExcludeStarting = true
	f.ResolveDates(h, now)
	frag, err := s.buildTxFilter(f, true)
	if err != nil {
		return 0, 0, err
	}
	var n int
	var spend int64
	err = s.DB.QueryRow(`SELECT COUNT(1), COALESCE(SUM(CASE WHEN t.amount < 0 THEN -t.amount ELSE 0 END), 0)
		FROM transactions t`+frag.joins+`
		WHERE`+frag.where+` AND t.category_id IS NULL
		AND NOT EXISTS (SELECT 1 FROM transaction_splits sp WHERE sp.transaction_id = t.id)`,
		frag.args...).Scan(&n, &spend)
	return n, spend, err
}

func exploreDrillQuery(f QueryFilter, groupBy, key, label string) string {
	base := f.Clone()
	// clear the dimension we'll set
	switch groupBy {
	case "category":
		id, _ := parseInt64(key)
		if id > 0 {
			base.CategoryIDs = []int64{id}
		} else {
			base.Uncategorized = true
		}
	case "account":
		id, _ := parseInt64(key)
		if id > 0 {
			base.AccountIDs = []int64{id}
			base.AccountID = id
		}
	case "person":
		base.People = []string{key}
	case "payee":
		if label != "(no payee)" {
			base.Payee = label
		}
	case "tag":
		id, _ := parseInt64(key)
		if id > 0 {
			base.TagIDs = []int64{id}
		}
	}
	return base.EncodeQuery().Encode()
}

func parseInt64(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscan(s, &n)
	return n, err
}

func (s *Store) SegmentPivot(f QueryFilter, groupBy, grain string, h *models.Household, now time.Time) (*PivotTable, error) {
	groupBy = strings.ToLower(groupBy)
	switch groupBy {
	case "category", "payee", "tag", "person", "account":
	default:
		groupBy = "category"
	}
	grain = defaultGrain(grain)
	f = f.Clone()
	f.ExcludeTransfers = true
	f.ExcludeStarting = true
	f.ResolveDates(h, now)

	frag, err := s.buildTxFilter(f, true)
	if err != nil {
		return nil, err
	}
	g := grainExpr(grain)

	var selectKey, selectLabel, groupExtra, extraJoin string
	switch groupBy {
	case "payee":
		selectKey = `COALESCE(t.payee_name,'(no payee)')`
		selectLabel = selectKey
		groupExtra = selectKey
	case "person":
		selectKey = `s.person`
		selectLabel = `s.person`
		groupExtra = `s.person`
	case "account":
		selectKey = `CAST(t.account_id AS TEXT)`
		selectLabel = `a.name`
		extraJoin = ` JOIN accounts a ON a.id = t.account_id`
		groupExtra = `t.account_id, a.name`
	case "tag":
		selectKey = `CAST(COALESCE(tg.id, 0) AS TEXT)`
		selectLabel = `COALESCE(tg.name,'(untagged)')`
		extraJoin = ` LEFT JOIN transaction_tags tt ON tt.transaction_id = t.id
			LEFT JOIN tags tg ON tg.id = tt.tag_id`
		groupExtra = `tg.id, tg.name`
	default:
		selectKey = `CAST(COALESCE(c.id, 0) AS TEXT)`
		selectLabel = `COALESCE(c.name, '` + UncategorizedLabel + `')`
		groupExtra = `c.id, c.name`
	}

	q := `SELECT ` + selectKey + ` AS rk, ` + selectLabel + ` AS rl, ` + g + ` AS ck,
		COALESCE(SUM(s.amount), 0) AS amt, COUNT(DISTINCT t.id) AS cnt
		FROM ` + reportLines + ` s
		JOIN transactions t ON t.id = s.transaction_id
		LEFT JOIN categories c ON c.id = s.category_id` + frag.joins + extraJoin + `
		WHERE` + frag.where + ` AND ` + lineKind + ` = 'expense'
		GROUP BY ` + groupExtra + `, ck`

	rows, err := s.DB.Query(q, frag.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pt := &PivotTable{
		Cells:     map[string]map[string]PivotCell{},
		RowTotals: map[string]int64{},
		ColTotals: map[string]int64{},
	}
	rowLabel := map[string]string{}
	colSeen := map[string]bool{}
	rowSeen := map[string]bool{}

	for rows.Next() {
		var rk, rl, ck string
		var amt int64
		var cnt int
		if err := rows.Scan(&rk, &rl, &ck, &amt, &cnt); err != nil {
			return nil, err
		}
		spend := -amt
		if spend < 0 {
			spend = -spend
		}
		if !rowSeen[rk] {
			rowSeen[rk] = true
			pt.RowKeys = append(pt.RowKeys, rk)
			rowLabel[rk] = rl
		}
		if !colSeen[ck] {
			colSeen[ck] = true
			pt.ColKeys = append(pt.ColKeys, ck)
		}
		if pt.Cells[rk] == nil {
			pt.Cells[rk] = map[string]PivotCell{}
		}
		pt.Cells[rk][ck] = PivotCell{Amount: spend, Count: cnt}
		pt.RowTotals[rk] += spend
		pt.ColTotals[ck] += spend
		pt.Grand += spend
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Strings(pt.ColKeys)
	sort.Slice(pt.RowKeys, func(i, j int) bool {
		return pt.RowTotals[pt.RowKeys[i]] > pt.RowTotals[pt.RowKeys[j]]
	})
	for _, rk := range pt.RowKeys {
		pt.RowLabels = append(pt.RowLabels, rowLabel[rk])
	}
	if groupBy == "category" {
		pt.RowColors = s.categoryColors()
	}
	fillPivotColumns(pt, grain, f.DateFrom, f.DateTo, now, h)
	return pt, nil
}

// PeriodInProgress reports whether a cash-flow period key is the current incomplete bucket.
func PeriodInProgress(grain, period string, h *models.Household, now time.Time) bool {
	return period == currentPeriodKey(grain, now, h)
}

func (pt *PivotTable) CellAmount(row, col string) int64 {
	if pt == nil {
		return 0
	}
	if m, ok := pt.Cells[row]; ok {
		return m[col].Amount
	}
	return 0
}

func (pt *PivotTable) RowTotal(row string) int64 {
	if pt == nil {
		return 0
	}
	return pt.RowTotals[row]
}

func (pt *PivotTable) ColTotal(col string) int64 {
	if pt == nil {
		return 0
	}
	return pt.ColTotals[col]
}

// FillCashFlowGaps inserts a zero point for every bucket from the start of the
// range through min(range end, today). Buckets after today are not invented.
// Real points after today are kept, without filling the gaps between them.
func FillCashFlowGaps(points []CashFlowPoint, grain, from, to string, now time.Time, h *models.Household) []CashFlowPoint {
	keys := boundedPeriodKeys(grain, from, to, now, h)
	if len(keys) == 0 {
		return points
	}
	by := make(map[string]CashFlowPoint, len(points))
	for _, p := range points {
		by[p.Period] = p
	}
	current := currentPeriodKey(grain, now, h)
	out := make([]CashFlowPoint, 0, len(keys))
	used := map[string]bool{}
	for _, k := range keys {
		if p, ok := by[k]; ok {
			out = append(out, p)
			used[k] = true
			continue
		}
		out = append(out, CashFlowPoint{
			Period:     k,
			Label:      formatPeriodLabel(grain, k),
			InProgress: k == current,
		})
	}
	last := keys[len(keys)-1]
	var extra []CashFlowPoint
	for _, p := range points {
		if used[p.Period] || p.Period <= last {
			continue
		}
		extra = append(extra, p)
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].Period < extra[j].Period })
	return append(out, extra...)
}

func fillPivotColumns(pt *PivotTable, grain, from, to string, now time.Time, h *models.Household) {
	if pt == nil {
		return
	}
	keys := boundedPeriodKeys(grain, from, to, now, h)
	if len(keys) > 0 {
		inRange := make(map[string]bool, len(keys))
		for _, k := range keys {
			inRange[k] = true
		}
		var before, after []string
		for _, k := range pt.ColKeys {
			if inRange[k] {
				continue
			}
			if k < keys[0] {
				before = append(before, k)
			} else {
				after = append(after, k)
			}
		}
		sort.Strings(before)
		sort.Strings(after)
		cols := make([]string, 0, len(before)+len(keys)+len(after))
		cols = append(cols, before...)
		cols = append(cols, keys...)
		cols = append(cols, after...)
		pt.ColKeys = cols
	}
	pt.ColLabels = make([]string, len(pt.ColKeys))
	for i, ck := range pt.ColKeys {
		pt.ColLabels[i] = formatPeriodLabel(grain, ck)
	}
	pt.InProgCol = currentPeriodKey(grain, now, h)
}

func boundedPeriodKeys(grain, from, to string, now time.Time, h *models.Household) []string {
	if from == "" || to == "" {
		return nil
	}
	end := to
	today := todayISO(now, h)
	if today < end {
		end = today
	}
	if end < from {
		return nil
	}
	return enumeratePeriodKeys(grain, from, end)
}

func todayISO(now time.Time, h *models.Household) string {
	if h != nil && h.Timezone != "" {
		if loc, err := time.LoadLocation(h.Timezone); err == nil {
			now = now.In(loc)
		}
	}
	return now.Format("2006-01-02")
}

func enumeratePeriodKeys(grain, from, to string) []string {
	start, err1 := time.Parse("2006-01-02", from)
	end, err2 := time.Parse("2006-01-02", to)
	if err1 != nil || err2 != nil || end.Before(start) {
		return nil
	}
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	end = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	var keys []string
	switch defaultGrain(grain) {
	case "day":
		for !start.After(end) {
			keys = append(keys, start.Format("2006-01-02"))
			start = start.AddDate(0, 0, 1)
		}
	case "week":
		start = mondayUTC(start)
		for !start.After(end) {
			keys = append(keys, start.Format("2006-01-02"))
			start = start.AddDate(0, 0, 7)
		}
	case "quarter":
		cur := quarterStartUTC(start)
		for !cur.After(end) {
			q := (int(cur.Month()) + 2) / 3
			keys = append(keys, fmt.Sprintf("%04d-Q%d", cur.Year(), q))
			cur = cur.AddDate(0, 3, 0)
		}
	case "year":
		for y := start.Year(); y <= end.Year(); y++ {
			keys = append(keys, fmt.Sprintf("%04d", y))
		}
	default:
		cur := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
		for !cur.After(end) {
			keys = append(keys, cur.Format("2006-01"))
			cur = cur.AddDate(0, 1, 0)
		}
	}
	return keys
}

func mondayUTC(t time.Time) time.Time {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return t.AddDate(0, 0, -(wd - 1))
}

func quarterStartUTC(t time.Time) time.Time {
	q := (int(t.Month()) - 1) / 3
	return time.Date(t.Year(), time.Month(q*3+1), 1, 0, 0, 0, 0, time.UTC)
}
