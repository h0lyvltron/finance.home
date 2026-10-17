package store

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"finance.home/internal/models"
)

type CashFlowPoint struct {
	Period     string
	Label      string
	Income     int64 // positive cents
	Expense    int64 // positive cents (magnitude of spend)
	Net        int64 // income - expense (signed)
	InProgress bool
}

type SegmentTotal struct {
	Key        string
	Label      string
	Amount     int64 // signed (expense negative) or spend magnitude depending on caller
	Count      int
	DrillQuery string // query string fragment for /transactions
	Color      string
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
	InProgCol string // column key that is in-progress, if any
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
		COALESCE(SUM(CASE WHEN c.kind = 'income' THEN s.amount ELSE 0 END), 0) AS income,
		COALESCE(SUM(CASE WHEN c.kind = 'expense' THEN s.amount ELSE 0 END), 0) AS expense
		FROM transaction_splits s
		JOIN transactions t ON t.id = s.transaction_id
		JOIN categories c ON c.id = s.category_id` + frag.joins + `
		WHERE` + frag.where + ` AND c.kind IN ('income','expense')
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
	return out, rows.Err()
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
		selectKey = `CAST(c.id AS TEXT)`
		selectLabel = `c.name`
		groupClause = `c.id, c.name`
	}

	q := `SELECT ` + selectKey + ` AS k, ` + selectLabel + ` AS lbl,
		COALESCE(SUM(s.amount), 0) AS amt, COUNT(DISTINCT t.id) AS cnt
		FROM transaction_splits s
		JOIN transactions t ON t.id = s.transaction_id
		JOIN categories c ON c.id = s.category_id` + frag.joins + extraJoin + `
		WHERE` + frag.where + ` AND c.kind = 'expense'
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

	sort.Slice(raw, func(i, j int) bool { return raw[i].Amount > raw[j].Amount })

	if len(raw) <= topN {
		return raw, grand, nil
	}
	out := make([]SegmentTotal, 0, topN+1)
	out = append(out, raw[:topN]...)
	var otherAmt int64
	var otherCnt int
	for _, seg := range raw[topN:] {
		otherAmt += seg.Amount
		otherCnt += seg.Count
	}
	out = append(out, SegmentTotal{
		Key:    "__other__",
		Label:  "Other",
		Amount: otherAmt,
		Count:  otherCnt,
	})
	return out, grand, nil
}

func exploreDrillQuery(f QueryFilter, groupBy, key, label string) string {
	base := f.Clone()
	// clear the dimension we'll set
	switch groupBy {
	case "category":
		id, _ := parseInt64(key)
		if id > 0 {
			base.CategoryIDs = []int64{id}
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
		selectKey = `CAST(c.id AS TEXT)`
		selectLabel = `c.name`
		groupExtra = `c.id, c.name`
	}

	q := `SELECT ` + selectKey + ` AS rk, ` + selectLabel + ` AS rl, ` + g + ` AS ck,
		COALESCE(SUM(s.amount), 0) AS amt, COUNT(DISTINCT t.id) AS cnt
		FROM transaction_splits s
		JOIN transactions t ON t.id = s.transaction_id
		JOIN categories c ON c.id = s.category_id` + frag.joins + extraJoin + `
		WHERE` + frag.where + ` AND c.kind = 'expense'
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
	for _, ck := range pt.ColKeys {
		pt.ColLabels = append(pt.ColLabels, formatPeriodLabel(grain, ck))
	}
	pt.InProgCol = currentPeriodKey(grain, now, h)
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
