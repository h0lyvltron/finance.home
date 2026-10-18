package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"finance.home/internal/models"
)

// MonthStartDay returns the household month-start day (1–28), default 1.
func MonthStartDay(h *models.Household) int {
	if h == nil || h.MonthStartDay < 1 {
		return 1
	}
	if h.MonthStartDay > 28 {
		return 28
	}
	return h.MonthStartDay
}

// YearMonth returns the fiscal YYYY-MM in the household timezone (respects month_start_day).
func YearMonth(h *models.Household, now time.Time) string {
	now = localize(h, now)
	start := MonthStartDay(h)
	if start <= 1 {
		return now.Format("2006-01")
	}
	if now.Day() < start {
		prev := now.AddDate(0, -1, 0)
		return prev.Format("2006-01")
	}
	return now.Format("2006-01")
}

func localize(h *models.Household, now time.Time) time.Time {
	if h != nil && h.Timezone != "" {
		if loc, err := time.LoadLocation(h.Timezone); err == nil {
			return now.In(loc)
		}
	}
	return now
}

func monthBounds(ym string) (from, to string, err error) {
	return monthBoundsWithStart(ym, 1)
}

// FiscalMonthBounds returns inclusive date bounds for a fiscal YYYY-MM using month_start_day.
func FiscalMonthBounds(h *models.Household, ym string) (from, to string, err error) {
	return monthBoundsWithStart(ym, MonthStartDay(h))
}

func monthBoundsWithStart(ym string, startDay int) (from, to string, err error) {
	t, err := time.Parse("2006-01", ym)
	if err != nil {
		return "", "", fmt.Errorf("bad year_month %q", ym)
	}
	if startDay <= 1 {
		from = t.Format("2006-01-02")
		to = t.AddDate(0, 1, -1).Format("2006-01-02")
		return from, to, nil
	}
	clamp := func(y int, m time.Month, day int) time.Time {
		last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
		if day > last {
			day = last
		}
		return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
	}
	fromT := clamp(t.Year(), t.Month(), startDay)
	next := t.AddDate(0, 1, 0)
	toT := clamp(next.Year(), next.Month(), startDay).AddDate(0, 0, -1)
	return fromT.Format("2006-01-02"), toT.Format("2006-01-02"), nil
}

func (s *Store) monthBoundsFor(ym string) (from, to string, err error) {
	h, err := s.Household()
	if err != nil {
		return monthBounds(ym)
	}
	return FiscalMonthBounds(h, ym)
}

func prevYearMonth(ym string) (string, error) {
	t, err := time.Parse("2006-01", ym)
	if err != nil {
		return "", err
	}
	return t.AddDate(0, -1, 0).Format("2006-01"), nil
}

type budgetAmount struct {
	Amount   int64
	Rollover bool
}

func (s *Store) budgetDefaultsMap() (map[int64]budgetAmount, error) {
	rows, err := s.DB.Query(`SELECT category_id, amount_cents, rollover FROM budget_defaults`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]budgetAmount{}
	for rows.Next() {
		var id, amt int64
		var roll int
		if err := rows.Scan(&id, &amt, &roll); err != nil {
			return nil, err
		}
		out[id] = budgetAmount{Amount: amt, Rollover: roll == 1}
	}
	return out, rows.Err()
}

func (s *Store) budgetMonthMap(ym string) (map[int64]budgetAmount, error) {
	rows, err := s.DB.Query(`SELECT category_id, amount_cents, rollover FROM budget_months WHERE year_month=?`, ym)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]budgetAmount{}
	for rows.Next() {
		var id, amt int64
		var roll int
		if err := rows.Scan(&id, &amt, &roll); err != nil {
			return nil, err
		}
		out[id] = budgetAmount{Amount: amt, Rollover: roll == 1}
	}
	return out, rows.Err()
}

// UpsertBudgetDefault sets the recurring monthly target for a category.
func (s *Store) UpsertBudgetDefault(categoryID, amountCents int64, rollover bool) error {
	roll := 0
	if rollover {
		roll = 1
	}
	_, err := s.DB.Exec(`INSERT INTO budget_defaults(category_id, amount_cents, rollover) VALUES (?,?,?)
		ON CONFLICT(category_id) DO UPDATE SET amount_cents=excluded.amount_cents, rollover=excluded.rollover`,
		categoryID, amountCents, roll)
	return err
}

// UpsertBudgetMonth sets a per-month override (also used after copy).
func (s *Store) UpsertBudgetMonth(ym string, categoryID, amountCents int64, rollover bool) error {
	if _, err := time.Parse("2006-01", ym); err != nil {
		return fmt.Errorf("bad year_month")
	}
	roll := 0
	if rollover {
		roll = 1
	}
	_, err := s.DB.Exec(`INSERT INTO budget_months(year_month, category_id, amount_cents, rollover) VALUES (?,?,?,?)
		ON CONFLICT(year_month, category_id) DO UPDATE SET amount_cents=excluded.amount_cents, rollover=excluded.rollover`,
		ym, categoryID, amountCents, roll)
	return err
}

// CopyBudgetsFromPrevious copies resolved base amounts from prev month into ym.
// Falls back to defaults when the previous month has no rows.
func (s *Store) CopyBudgetsFromPrevious(ym string) (int, error) {
	prev, err := prevYearMonth(ym)
	if err != nil {
		return 0, err
	}
	prevMap, err := s.budgetMonthMap(prev)
	if err != nil {
		return 0, err
	}
	defs, err := s.budgetDefaultsMap()
	if err != nil {
		return 0, err
	}
	src := prevMap
	if len(src) == 0 {
		src = defs
	}
	n := 0
	for id, ba := range src {
		if err := s.UpsertBudgetMonth(ym, id, ba.Amount, ba.Rollover); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// EnsureMonthBudgets seeds ym from defaults when empty (does not overwrite).
func (s *Store) EnsureMonthBudgets(ym string) error {
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM budget_months WHERE year_month=?`, ym).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	defs, err := s.budgetDefaultsMap()
	if err != nil {
		return err
	}
	for id, ba := range defs {
		if err := s.UpsertBudgetMonth(ym, id, ba.Amount, ba.Rollover); err != nil {
			return err
		}
	}
	return nil
}

// categorySpendMap returns positive expense magnitude by category_id for the month.
func (s *Store) categorySpendMap(ym string) (map[int64]int64, error) {
	from, to, err := s.monthBoundsFor(ym)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(`
		SELECT s.category_id, COALESCE(SUM(s.amount), 0)
		FROM transaction_splits s
		JOIN transactions t ON t.id = s.transaction_id
		JOIN categories c ON c.id = s.category_id
		WHERE t.deleted_at IS NULL
		  AND t.transaction_date >= ? AND t.transaction_date <= ?
		  AND c.kind = 'expense'
		  AND c.exclude_budget = 0
		GROUP BY s.category_id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, amt int64
		if err := rows.Scan(&id, &amt); err != nil {
			return nil, err
		}
		if amt < 0 {
			amt = -amt
		} else {
			amt = 0
		}
		out[id] = amt
	}
	return out, rows.Err()
}

func (s *Store) resolvedBaseAmounts(ym string) (map[int64]budgetAmount, error) {
	if err := s.EnsureMonthBudgets(ym); err != nil {
		return nil, err
	}
	month, err := s.budgetMonthMap(ym)
	if err != nil {
		return nil, err
	}
	defs, err := s.budgetDefaultsMap()
	if err != nil {
		return nil, err
	}
	// Prefer month rows; fill gaps from defaults so new categories still appear.
	out := map[int64]budgetAmount{}
	for id, ba := range defs {
		out[id] = ba
	}
	for id, ba := range month {
		out[id] = ba
	}
	return out, nil
}

// MonthBudgetLines returns display lines with parent rollup/independent rules applied.
func (s *Store) MonthBudgetLines(ym string) ([]models.BudgetLine, error) {
	cats, err := s.ListCategories("expense", false)
	if err != nil {
		return nil, err
	}
	amounts, err := s.resolvedBaseAmounts(ym)
	if err != nil {
		return nil, err
	}
	spend, err := s.categorySpendMap(ym)
	if err != nil {
		return nil, err
	}
	prevYM, _ := prevYearMonth(ym)
	prevSpend, _ := s.categorySpendMap(prevYM)
	prevAmounts, _ := s.resolvedBaseAmounts(prevYM)

	children := map[int64][]int64{}
	byID := map[int64]models.Category{}
	for _, c := range cats {
		byID[c.ID] = c
		if c.ParentID != nil {
			children[*c.ParentID] = append(children[*c.ParentID], c.ID)
		}
	}

	baseFor := func(id int64, m map[int64]budgetAmount) budgetAmount {
		if ba, ok := m[id]; ok {
			return ba
		}
		return budgetAmount{}
	}

	attrFromMap := func(c models.Category, spentMap map[int64]int64) int64 {
		kids := children[c.ID]
		if len(kids) == 0 {
			return spentMap[c.ID]
		}
		mode := c.BudgetMode
		if mode == "" {
			mode = "rollup"
		}
		if mode == "independent" {
			return spentMap[c.ID]
		}
		var sum int64
		for _, kid := range kids {
			sum += spentMap[kid]
			for _, g := range children[kid] {
				sum += spentMap[g]
			}
		}
		return sum
	}

	rollupBase := func(c models.Category, amts map[int64]budgetAmount) (base int64, rollover bool) {
		kids := children[c.ID]
		mode := c.BudgetMode
		if mode == "" {
			mode = "rollup"
		}
		ba := baseFor(c.ID, amts)
		rollover = ba.Rollover
		if len(kids) > 0 && mode == "rollup" {
			var sum int64
			any := false
			for _, kid := range kids {
				kba := baseFor(kid, amts)
				if _, ok := amts[kid]; ok {
					any = true
				}
				sum += kba.Amount
			}
			if any {
				return sum, rollover
			}
		}
		return ba.Amount, rollover
	}

	var lines []models.BudgetLine
	for _, c := range cats {
		if c.ExcludeBudget || c.Protected {
			continue
		}
		kids := children[c.ID]
		hasKids := len(kids) > 0
		mode := c.BudgetMode
		if mode == "" {
			mode = "rollup"
		}
		ba := baseFor(c.ID, amounts)
		base, rollover := rollupBase(c, amounts)
		carry := int64(0)
		if rollover && prevYM != "" {
			prevBase, _ := rollupBase(c, prevAmounts)
			unused := prevBase - attrFromMap(c, prevSpend)
			if unused > 0 {
				carry = unused
			}
		}
		effective := base + carry
		spent := attrFromMap(c, spend)

		if !hasKids && effective == 0 && spent == 0 && ba.Amount == 0 {
			continue
		}

		lines = append(lines, models.BudgetLine{
			CategoryID:    c.ID,
			CategoryName:  c.Name,
			ParentID:      c.ParentID,
			ParentName:    c.ParentName,
			FixedFlexible: c.FixedFlexible,
			BudgetMode:    mode,
			IsParent:      hasKids,
			HasChildren:   hasKids,
			AmountCents:   effective,
			BaseCents:     base,
			Rollover:      rollover,
			RolloverCarry: carry,
			SpentCents:    spent,
			Remaining:     effective - spent,
			ExcludeBudget: c.ExcludeBudget,
			Color:         c.Color,
		})
	}

	sort.SliceStable(lines, func(i, j int) bool {
		pi, pj := lines[i].ParentName, lines[j].ParentName
		if lines[i].ParentID == nil {
			pi = lines[i].CategoryName
		}
		if lines[j].ParentID == nil {
			pj = lines[j].CategoryName
		}
		if pi != pj {
			return pi < pj
		}
		if lines[i].ParentID == nil && lines[j].ParentID != nil {
			return true
		}
		if lines[i].ParentID != nil && lines[j].ParentID == nil {
			return false
		}
		return lines[i].CategoryName < lines[j].CategoryName
	})
	_ = byID
	return lines, nil
}

// BudgetHealthSummary aggregates non-double-counted budget lines.
// Rollup parents contribute; their children are excluded from totals.
// Independent parents and their children both contribute (different spend).
func (s *Store) BudgetHealthSummary(ym string) (models.BudgetHealth, error) {
	lines, err := s.MonthBudgetLines(ym)
	if err != nil {
		return models.BudgetHealth{}, err
	}
	cats, _ := s.ListCategories("expense", false)
	modeByID := map[int64]string{}
	parentOf := map[int64]int64{}
	for _, c := range cats {
		modeByID[c.ID] = c.BudgetMode
		if c.ParentID != nil {
			parentOf[c.ID] = *c.ParentID
		}
	}

	var h models.BudgetHealth
	for _, ln := range lines {
		if pid, ok := parentOf[ln.CategoryID]; ok {
			if modeByID[pid] == "rollup" || modeByID[pid] == "" {
				// Child of rollup — skip in household totals (parent counts).
				continue
			}
		}
		if ln.AmountCents == 0 && ln.SpentCents == 0 {
			continue
		}
		h.Budgeted += ln.AmountCents
		h.Spent += ln.SpentCents
		if ln.Remaining < 0 {
			h.OverCount++
			h.OverCents += -ln.Remaining
		} else if ln.AmountCents > 0 && ln.SpentCents*100 <= ln.AmountCents*90 {
			h.UnderCount++
			h.UnderCents += ln.Remaining
		} else if ln.AmountCents > 0 {
			h.OnTrackCount++
		}
	}
	return h, nil
}

// FlexibleSpentMonth sums spend in flexible expense categories (actuals).
func (s *Store) FlexibleSpentMonth(ym string) (int64, error) {
	from, to, err := s.monthBoundsFor(ym)
	if err != nil {
		return 0, err
	}
	var amt sql.NullInt64
	err = s.DB.QueryRow(`
		SELECT COALESCE(SUM(s.amount), 0)
		FROM transaction_splits s
		JOIN transactions t ON t.id = s.transaction_id
		JOIN categories c ON c.id = s.category_id
		WHERE t.deleted_at IS NULL
		  AND t.transaction_date >= ? AND t.transaction_date <= ?
		  AND c.kind = 'expense'
		  AND c.exclude_budget = 0
		  AND COALESCE(c.fixed_flexible,'') = 'flexible'`, from, to).Scan(&amt)
	if err != nil {
		return 0, err
	}
	v := amt.Int64
	if v < 0 {
		v = -v
	} else {
		v = 0
	}
	return v, nil
}

// LeftToSpend computes expected income − fixed bills − flexible spent − savings target.
func (s *Store) LeftToSpend(h *models.Household, ym string) (models.LeftToSpend, error) {
	out := models.LeftToSpend{YearMonth: ym}
	if h != nil {
		out.ExpectedIncome = h.ExpectedIncomeCents
		out.SavingsTarget = h.SavingsTargetCents
	}
	flex, err := s.FlexibleSpentMonth(ym)
	if err != nil {
		return out, err
	}
	out.FlexibleSpent = flex
	fixed, err := s.FixedBillsForMonth(ym)
	if err != nil {
		return out, err
	}
	out.FixedBills = fixed
	out.Amount = out.ExpectedIncome - out.FixedBills - out.FlexibleSpent - out.SavingsTarget
	return out, nil
}

// PaceInsight picks the most urgent overspending-or-tight category for coaching.
func (s *Store) PaceInsight(h *models.Household, ym string, now time.Time) *models.CoachingInsight {
	lines, err := s.MonthBudgetLines(ym)
	if err != nil {
		return nil
	}
	now = localize(h, now)
	_, monthEnd, err := FiscalMonthBounds(h, ym)
	if err != nil {
		return nil
	}
	end, _ := time.ParseInLocation("2006-01-02", monthEnd, now.Location())
	daysLeft := int(end.Sub(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())).Hours()/24) + 1
	if daysLeft < 1 {
		daysLeft = 1
	}

	var best *models.BudgetLine
	var bestScore int64 = -1
	for i := range lines {
		ln := &lines[i]
		if ln.AmountCents <= 0 || ln.HasChildren {
			continue
		}
		if ln.ParentID != nil {
			// Prefer leaves
		}
		// Pace pressure: spent/budget vs day fraction
		day := now.Day()
		if day < 1 {
			day = 1
		}
		expectedPace := ln.AmountCents * int64(day) / int64(end.Day())
		over := ln.SpentCents - expectedPace
		if over > bestScore {
			bestScore = over
			best = ln
		}
	}
	if best == nil {
		return nil
	}
	perDay := best.Remaining / int64(daysLeft)
	href := fmt.Sprintf("/transactions?preset=this_month&category=%d", best.CategoryID)
	if best.Remaining < 0 {
		return &models.CoachingInsight{
			Kind:    "pace",
			Tone:    "warn",
			Title:   best.CategoryName + " is over pace",
			Detail:  fmt.Sprintf("%s over budget · %d days left", formatRough(best.Remaining), daysLeft),
			Href:    href,
			Amount:  -best.Remaining,
			ShowAmt: true,
		}
	}
	return &models.CoachingInsight{
		Kind:    "pace",
		Tone:    "info",
		Title:   best.CategoryName + " pace",
		Detail:  fmt.Sprintf("%s/day left · %d days remaining", formatRough(perDay), daysLeft),
		Href:    href,
		Amount:  perDay,
		ShowAmt: true,
	}
}

func formatRough(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := fmt.Sprintf("$%d", cents/100)
	if cents%100 != 0 {
		s = fmt.Sprintf("$%d.%02d", cents/100, cents%100)
	}
	if neg {
		return "-" + s
	}
	return s
}

// PositiveBudgetInsight celebrates a category under budget.
func (s *Store) PositiveBudgetInsight(ym string) *models.CoachingInsight {
	lines, err := s.MonthBudgetLines(ym)
	if err != nil {
		return nil
	}
	var best *models.BudgetLine
	for i := range lines {
		ln := &lines[i]
		if ln.HasChildren || ln.AmountCents <= 0 {
			continue
		}
		if ln.SpentCents == 0 {
			continue
		}
		if ln.Remaining > 0 && (best == nil || ln.Remaining > best.Remaining) {
			best = ln
		}
	}
	if best == nil {
		// Celebrate zero spend against a budgeted category
		for i := range lines {
			ln := &lines[i]
			if !ln.HasChildren && ln.AmountCents > 0 && ln.SpentCents == 0 {
				best = ln
				break
			}
		}
	}
	if best == nil {
		return nil
	}
	return &models.CoachingInsight{
		Kind:    "positive",
		Tone:    "ok",
		Title:   best.CategoryName + " looks good",
		Detail:  fmt.Sprintf("%s still under budget this month", formatRough(best.Remaining)),
		Href:    fmt.Sprintf("/transactions?preset=this_month&category=%d", best.CategoryID),
		Amount:  best.Remaining,
		ShowAmt: true,
	}
}

// SavePlanSettings updates expected income + savings target and optional budget rows from form maps.
func (s *Store) SaveBudgetForm(ym string, amounts map[int64]int64, rollovers map[int64]bool, modes map[int64]string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for id, mode := range modes {
		if mode != "rollup" && mode != "independent" {
			continue
		}
		if _, err := tx.Exec(`UPDATE categories SET budget_mode=? WHERE id=?`, mode, id); err != nil {
			return err
		}
	}
	for id, amt := range amounts {
		roll := 0
		if rollovers[id] {
			roll = 1
		}
		if _, err := tx.Exec(`INSERT INTO budget_months(year_month, category_id, amount_cents, rollover) VALUES (?,?,?,?)
			ON CONFLICT(year_month, category_id) DO UPDATE SET amount_cents=excluded.amount_cents, rollover=excluded.rollover`,
			ym, id, amt, roll); err != nil {
			return err
		}
		// Keep defaults in sync so copy/next month has a baseline.
		if _, err := tx.Exec(`INSERT INTO budget_defaults(category_id, amount_cents, rollover) VALUES (?,?,?)
			ON CONFLICT(category_id) DO UPDATE SET amount_cents=excluded.amount_cents, rollover=excluded.rollover`,
			id, amt, roll); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ParseYearMonth validates or defaults ym.
func ParseYearMonth(raw string, h *models.Household, now time.Time) string {
	raw = strings.TrimSpace(raw)
	if _, err := time.Parse("2006-01", raw); err == nil {
		return raw
	}
	return YearMonth(h, now)
}
