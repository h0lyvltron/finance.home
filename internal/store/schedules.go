package store

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"finance.home/internal/models"
)

type ScheduleInput struct {
	Name                 string
	PayeeName            string
	AmountCents          int64
	Kind                 string
	AccountID            *int64
	CategoryID           *int64
	Frequency            string
	IntervalN            int
	NextDue              string
	AutoMatch            bool
	AmountToleranceCents int64
	DateWindowDays       int
}

func (s *Store) ListScheduleTemplates(activeOnly bool) ([]models.ScheduleTemplate, error) {
	q := `SELECT t.id, t.name, COALESCE(t.payee_name,''), t.amount_cents, t.kind,
		t.account_id, COALESCE(a.name,''), t.category_id, COALESCE(c.name,''),
		t.frequency, t.interval_n, t.next_due, t.auto_match, t.amount_tolerance_cents,
		t.date_window_days, t.active
		FROM schedule_templates t
		LEFT JOIN accounts a ON a.id = t.account_id
		LEFT JOIN categories c ON c.id = t.category_id
		WHERE 1=1`
	if activeOnly {
		q += ` AND t.active = 1`
	}
	q += ` ORDER BY t.next_due, t.name`
	rows, err := s.DB.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ScheduleTemplate
	for rows.Next() {
		var t models.ScheduleTemplate
		var acct, cat sql.NullInt64
		var auto, active int
		if err := rows.Scan(&t.ID, &t.Name, &t.PayeeName, &t.AmountCents, &t.Kind,
			&acct, &t.AccountName, &cat, &t.CategoryName,
			&t.Frequency, &t.IntervalN, &t.NextDue, &auto, &t.AmountToleranceCents,
			&t.DateWindowDays, &active); err != nil {
			return nil, err
		}
		if acct.Valid {
			v := acct.Int64
			t.AccountID = &v
		}
		if cat.Valid {
			v := cat.Int64
			t.CategoryID = &v
		}
		t.AutoMatch = auto == 1
		t.Active = active == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetScheduleTemplate(id int64) (*models.ScheduleTemplate, error) {
	list, err := s.ListScheduleTemplates(false)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *Store) CreateScheduleTemplate(in ScheduleInput) (int64, error) {
	if err := normalizeScheduleInput(&in); err != nil {
		return 0, err
	}
	auto := 1
	if !in.AutoMatch {
		auto = 0
	}
	res, err := s.DB.Exec(`INSERT INTO schedule_templates(
		name, payee_name, amount_cents, kind, account_id, category_id, frequency, interval_n,
		next_due, auto_match, amount_tolerance_cents, date_window_days)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.Name, nullStr(in.PayeeName), in.AmountCents, in.Kind, in.AccountID, in.CategoryID,
		in.Frequency, in.IntervalN, in.NextDue, auto, in.AmountToleranceCents, in.DateWindowDays)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := s.EnsureScheduleInstances(time.Now(), 60); err != nil {
		return id, err
	}
	return id, nil
}

func (s *Store) UpdateScheduleTemplate(id int64, in ScheduleInput) error {
	if err := normalizeScheduleInput(&in); err != nil {
		return err
	}
	auto := 1
	if !in.AutoMatch {
		auto = 0
	}
	_, err := s.DB.Exec(`UPDATE schedule_templates SET
		name=?, payee_name=?, amount_cents=?, kind=?, account_id=?, category_id=?,
		frequency=?, interval_n=?, next_due=?, auto_match=?, amount_tolerance_cents=?,
		date_window_days=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id=?`,
		in.Name, nullStr(in.PayeeName), in.AmountCents, in.Kind, in.AccountID, in.CategoryID,
		in.Frequency, in.IntervalN, in.NextDue, auto, in.AmountToleranceCents, in.DateWindowDays, id)
	return err
}

func (s *Store) DeactivateScheduleTemplate(id int64) error {
	_, err := s.DB.Exec(`UPDATE schedule_templates SET active=0, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, id)
	return err
}

func normalizeScheduleInput(in *ScheduleInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return fmt.Errorf("name required")
	}
	if in.PayeeName == "" {
		in.PayeeName = in.Name
	}
	if in.AmountCents <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	if in.Kind == "" {
		in.Kind = "expense"
	}
	if in.Kind != "expense" && in.Kind != "income" {
		return fmt.Errorf("kind must be expense or income")
	}
	switch in.Frequency {
	case "weekly", "biweekly", "monthly", "yearly":
	default:
		return fmt.Errorf("invalid frequency")
	}
	if in.IntervalN <= 0 {
		in.IntervalN = 1
	}
	if _, err := time.Parse("2006-01-02", in.NextDue); err != nil {
		return fmt.Errorf("next due date required")
	}
	if in.AmountToleranceCents < 0 {
		in.AmountToleranceCents = 500
	}
	if in.DateWindowDays <= 0 {
		in.DateWindowDays = 5
	}
	return nil
}

func advanceDue(due time.Time, freq string, interval int) time.Time {
	if interval == 0 {
		interval = 1
	}
	switch freq {
	case "weekly":
		return due.AddDate(0, 0, 7*interval)
	case "biweekly":
		return due.AddDate(0, 0, 14*interval)
	case "yearly":
		return due.AddDate(interval, 0, 0)
	default: // monthly
		return due.AddDate(0, interval, 0)
	}
}

// EnsureScheduleInstances materializes instances through horizonDays ahead and marks overdue.
func (s *Store) EnsureScheduleInstances(now time.Time, horizonDays int) error {
	if horizonDays <= 0 {
		horizonDays = 60
	}
	templates, err := s.ListScheduleTemplates(true)
	if err != nil {
		return err
	}
	today := now.Format("2006-01-02")
	horizon := now.AddDate(0, 0, horizonDays).Format("2006-01-02")

	for _, tpl := range templates {
		due, err := time.Parse("2006-01-02", tpl.NextDue)
		if err != nil {
			continue
		}
		// Generate from next_due forward while <= horizon; also backfill missing current month.
		cursor := due
		// Walk back a few periods so we don't miss unpaid instances if next_due advanced.
		for i := 0; i < 6; i++ {
			prev := advanceDue(cursor, tpl.Frequency, -tpl.IntervalN)
			if tpl.Frequency == "monthly" && tpl.IntervalN > 0 {
				prev = cursor.AddDate(0, -tpl.IntervalN, 0)
			} else if tpl.Frequency == "weekly" {
				prev = cursor.AddDate(0, 0, -7*tpl.IntervalN)
			} else if tpl.Frequency == "biweekly" {
				prev = cursor.AddDate(0, 0, -14*tpl.IntervalN)
			} else if tpl.Frequency == "yearly" {
				prev = cursor.AddDate(-tpl.IntervalN, 0, 0)
			}
			if prev.Format("2006-01-02") < now.AddDate(0, -2, 0).Format("2006-01-02") {
				break
			}
			cursor = prev
		}
		for cursor.Format("2006-01-02") <= horizon {
			ds := cursor.Format("2006-01-02")
			_, err := s.DB.Exec(`INSERT OR IGNORE INTO schedule_instances(template_id, due_date, expected_amount, status)
				VALUES (?,?,?,'upcoming')`, tpl.ID, ds, tpl.AmountCents)
			if err != nil {
				return err
			}
			cursor = advanceDue(cursor, tpl.Frequency, tpl.IntervalN)
		}
		// Advance template next_due to first upcoming/unmatched on or after today.
		var next sql.NullString
		_ = s.DB.QueryRow(`SELECT MIN(due_date) FROM schedule_instances
			WHERE template_id=? AND status IN ('upcoming','overdue') AND due_date >= ?`,
			tpl.ID, today).Scan(&next)
		if next.Valid {
			_, _ = s.DB.Exec(`UPDATE schedule_templates SET next_due=? WHERE id=?`, next.String, tpl.ID)
		}
	}

	// Mark overdue
	_, err = s.DB.Exec(`UPDATE schedule_instances SET status='overdue'
		WHERE status='upcoming' AND due_date < ?`, today)
	if err != nil {
		return err
	}
	return s.MatchScheduleInstances(now)
}

// MatchScheduleInstances pairs upcoming/overdue instances with ledger txs within tolerance.
func (s *Store) MatchScheduleInstances(now time.Time) error {
	rows, err := s.DB.Query(`
		SELECT i.id, i.due_date, i.expected_amount,
			COALESCE(t.payee_name,''), t.kind, t.account_id,
			t.amount_tolerance_cents, t.date_window_days
		FROM schedule_instances i
		JOIN schedule_templates t ON t.id = i.template_id
		WHERE i.status IN ('upcoming','overdue') AND t.active=1 AND t.auto_match=1`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type item struct {
		id, expected, tol int64
		due, payee, kind  string
		accountID         sql.NullInt64
		windowDays        int
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.due, &it.expected, &it.payee, &it.kind, &it.accountID,
			&it.tol, &it.windowDays); err != nil {
			return err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	usedTx := map[int64]bool{}
	mrows, err := s.DB.Query(`SELECT matched_tx_id FROM schedule_instances WHERE matched_tx_id IS NOT NULL`)
	if err != nil {
		return err
	}
	for mrows.Next() {
		var id int64
		if err := mrows.Scan(&id); err == nil {
			usedTx[id] = true
		}
	}
	mrows.Close()

	for _, it := range items {
		due, err := time.Parse("2006-01-02", it.due)
		if err != nil {
			continue
		}
		from := due.AddDate(0, 0, -it.windowDays).Format("2006-01-02")
		to := due.AddDate(0, 0, it.windowDays).Format("2006-01-02")
		wantExpense := it.kind != "income"
		q := `SELECT t.id, t.amount, COALESCE(t.payee_name,'') FROM transactions t
			WHERE t.deleted_at IS NULL
			  AND t.transaction_date >= ? AND t.transaction_date <= ?
			  AND t.transfer_id IS NULL`
		args := []any{from, to}
		if it.accountID.Valid {
			q += ` AND t.account_id = ?`
			args = append(args, it.accountID.Int64)
		}
		q += ` ORDER BY ABS(julianday(t.transaction_date) - julianday(?)), ABS(ABS(t.amount) - ?)`
		args = append(args, it.due, it.expected)

		txRows, err := s.DB.Query(q, args...)
		if err != nil {
			return err
		}
		var matchID int64
		for txRows.Next() {
			var id, amt int64
			var payee string
			if err := txRows.Scan(&id, &amt, &payee); err != nil {
				txRows.Close()
				return err
			}
			if usedTx[id] {
				continue
			}
			if wantExpense && amt >= 0 {
				continue
			}
			if !wantExpense && amt <= 0 {
				continue
			}
			mag := amt
			if mag < 0 {
				mag = -mag
			}
			if abs64(mag-it.expected) > it.tol {
				continue
			}
			matchID = id
			break
		}
		txRows.Close()
		if matchID == 0 {
			continue
		}
		if _, err = s.DB.Exec(`UPDATE schedule_instances SET status='matched', matched_tx_id=? WHERE id=?`, matchID, it.id); err != nil {
			return err
		}
		usedTx[matchID] = true
	}
	_ = now
	return nil
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func (s *Store) ListScheduleInstances(from, to string) ([]models.ScheduleInstance, error) {
	rows, err := s.DB.Query(`
		SELECT i.id, i.template_id, t.name, COALESCE(t.payee_name,''), i.due_date, i.expected_amount,
			i.status, i.matched_tx_id, t.kind, COALESCE(c.name,''), COALESCE(a.name,''), t.account_id
		FROM schedule_instances i
		JOIN schedule_templates t ON t.id = i.template_id
		LEFT JOIN categories c ON c.id = t.category_id
		LEFT JOIN accounts a ON a.id = t.account_id
		WHERE i.due_date >= ? AND i.due_date <= ?
		ORDER BY i.due_date, t.name`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	today := time.Now().Format("2006-01-02")
	var out []models.ScheduleInstance
	for rows.Next() {
		var inst models.ScheduleInstance
		var matched, acct sql.NullInt64
		if err := rows.Scan(&inst.ID, &inst.TemplateID, &inst.TemplateName, &inst.PayeeName,
			&inst.DueDate, &inst.ExpectedAmount, &inst.Status, &matched, &inst.Kind,
			&inst.CategoryName, &inst.AccountName, &acct); err != nil {
			return nil, err
		}
		if matched.Valid {
			v := matched.Int64
			inst.MatchedTxID = &v
		}
		if acct.Valid {
			v := acct.Int64
			inst.AccountID = &v
		}
		d1, _ := time.Parse("2006-01-02", today)
		d2, _ := time.Parse("2006-01-02", inst.DueDate)
		inst.DaysUntil = int(d2.Sub(d1).Hours() / 24)
		out = append(out, inst)
	}
	return out, rows.Err()
}

// UpcomingBills returns instances in the next n days, plus all overdue unmatched.
func (s *Store) UpcomingBills(h *models.Household, now time.Time, days int) ([]models.ScheduleInstance, error) {
	if err := s.EnsureScheduleInstances(localize(h, now), days+45); err != nil {
		return nil, err
	}
	now = localize(h, now)
	today := now.Format("2006-01-02")
	to := now.AddDate(0, 0, days).Format("2006-01-02")
	// Wide lookback so overdue rows are not clipped by the 14-day window.
	from := now.AddDate(0, -3, 0).Format("2006-01-02")
	all, err := s.ListScheduleInstances(from, to)
	if err != nil {
		return nil, err
	}
	// At most one overdue row per template (latest due); upcoming/matched in window as-is.
	latestOverdue := map[int64]models.ScheduleInstance{}
	var out []models.ScheduleInstance
	for _, inst := range all {
		switch inst.Status {
		case "overdue":
			if prev, ok := latestOverdue[inst.TemplateID]; !ok || inst.DueDate > prev.DueDate {
				latestOverdue[inst.TemplateID] = inst
			}
		case "upcoming":
			if inst.DueDate >= today && inst.DueDate <= to {
				out = append(out, inst)
			}
		case "matched", "skipped":
			if inst.DueDate >= today && inst.DueDate <= to {
				out = append(out, inst)
			}
		}
	}
	for _, inst := range latestOverdue {
		out = append(out, inst)
	}
	// Sort by due date
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].DueDate < out[i].DueDate {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// FixedBillsForMonth sums expected expense schedule amounts due in ym (planned fixed outflows).
func (s *Store) FixedBillsForMonth(ym string) (int64, error) {
	from, to, err := s.monthBoundsFor(ym)
	if err != nil {
		return 0, err
	}
	_ = s.EnsureScheduleInstances(time.Now(), 45)
	var sum sql.NullInt64
	err = s.DB.QueryRow(`
		SELECT COALESCE(SUM(i.expected_amount), 0)
		FROM schedule_instances i
		JOIN schedule_templates t ON t.id = i.template_id
		WHERE i.due_date >= ? AND i.due_date <= ?
		  AND t.kind = 'expense'
		  AND i.status != 'skipped'
		  AND t.active = 1`, from, to).Scan(&sum)
	if err != nil {
		return 0, err
	}
	return sum.Int64, nil
}

func (s *Store) MarkSchedulePaid(instanceID int64, txID *int64) error {
	if txID != nil {
		_, err := s.DB.Exec(`UPDATE schedule_instances SET status='matched', matched_tx_id=? WHERE id=?`, *txID, instanceID)
		return err
	}
	// Mark matched without a tx (manual "paid") — keep status matched, no tx.
	_, err := s.DB.Exec(`UPDATE schedule_instances SET status='matched' WHERE id=? AND status IN ('upcoming','overdue')`, instanceID)
	return err
}

func (s *Store) SkipScheduleInstance(instanceID int64) error {
	_, err := s.DB.Exec(`UPDATE schedule_instances SET status='skipped' WHERE id=? AND status IN ('upcoming','overdue')`, instanceID)
	return err
}

func (s *Store) UnmatchedOverdueCount() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(1) FROM schedule_instances WHERE status='overdue'`).Scan(&n)
	return n, err
}

// SuggestRecurringFromHistory finds payees with ~monthly repeats (basic heuristic).
func (s *Store) SuggestRecurringFromHistory(now time.Time, limit int) ([]models.RecurringSuggestion, error) {
	if limit <= 0 {
		limit = 8
	}
	since := now.AddDate(0, -6, 0).Format("2006-01-02")
	rows, err := s.DB.Query(`
		SELECT COALESCE(payee_name,''), amount, transaction_date, account_id, category_id
		FROM transactions
		WHERE deleted_at IS NULL AND transfer_id IS NULL
		  AND payee_name IS NOT NULL AND TRIM(payee_name) != ''
		  AND transaction_date >= ?
		  AND amount < 0
		ORDER BY payee_name COLLATE NOCASE, transaction_date`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type hit struct {
		date string
		amt  int64
		acct int64
		cat  sql.NullInt64
	}
	byPayee := map[string][]hit{}
	for rows.Next() {
		var payee string
		var amt, acct int64
		var date string
		var cat sql.NullInt64
		if err := rows.Scan(&payee, &amt, &date, &acct, &cat); err != nil {
			return nil, err
		}
		payee = strings.TrimSpace(payee)
		byPayee[payee] = append(byPayee[payee], hit{date: date, amt: amt, acct: acct, cat: cat})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Existing template payees
	existing := map[string]bool{}
	tpls, _ := s.ListScheduleTemplates(true)
	for _, t := range tpls {
		existing[strings.ToLower(t.PayeeName)] = true
		existing[strings.ToLower(t.Name)] = true
	}

	var out []models.RecurringSuggestion
	for payee, hits := range byPayee {
		if existing[strings.ToLower(payee)] {
			continue
		}
		if len(hits) < 2 {
			continue
		}
		// Cluster by similar amount (±$5 or 5%)
		type cluster struct {
			amounts []int64
			dates   []string
			acct    int64
			cat     *int64
		}
		var clusters []cluster
		for _, h := range hits {
			mag := -h.amt
			placed := false
			for i := range clusters {
				ref := clusters[i].amounts[0]
				tol := int64(500)
				if float64(tol) < float64(ref)*0.05 {
					tol = int64(math.Round(float64(ref) * 0.05))
				}
				if abs64(mag-ref) <= tol {
					clusters[i].amounts = append(clusters[i].amounts, mag)
					clusters[i].dates = append(clusters[i].dates, h.date)
					placed = true
					break
				}
			}
			if !placed {
				var catPtr *int64
				if h.cat.Valid {
					v := h.cat.Int64
					catPtr = &v
				}
				clusters = append(clusters, cluster{
					amounts: []int64{mag},
					dates:   []string{h.date},
					acct:    h.acct,
					cat:     catPtr,
				})
			}
		}
		for _, cl := range clusters {
			if len(cl.amounts) < 2 {
				continue
			}
			// Check gaps roughly 25–35 days for monthly
			monthly := 0
			for i := 1; i < len(cl.dates); i++ {
				d0, _ := time.Parse("2006-01-02", cl.dates[i-1])
				d1, _ := time.Parse("2006-01-02", cl.dates[i])
				gap := int(d1.Sub(d0).Hours() / 24)
				if gap >= 25 && gap <= 35 {
					monthly++
				}
			}
			if monthly < 1 && len(cl.amounts) < 3 {
				continue
			}
			var sum int64
			for _, a := range cl.amounts {
				sum += a
			}
			avg := sum / int64(len(cl.amounts))
			acct := cl.acct
			sug := models.RecurringSuggestion{
				PayeeName:   payee,
				AmountCents: avg,
				Frequency:   "monthly",
				Occurrences: len(cl.amounts),
				LastDate:    cl.dates[len(cl.dates)-1],
				CategoryID:  cl.cat,
				AccountID:   &acct,
			}
			out = append(out, sug)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
