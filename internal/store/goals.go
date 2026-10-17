package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"finance.home/internal/models"
)

type GoalInput struct {
	Kind           string
	Name           string
	TargetCents    int64
	TargetDate     string // empty or YYYY-MM-DD
	PaceWindowDays int
	AccountIDs     []int64
}

func (in GoalInput) Validate() error {
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if in.Kind != "savings" && in.Kind != "debt" {
		return fmt.Errorf("kind must be savings or debt")
	}
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if in.TargetCents < 0 {
		return fmt.Errorf("target must be ≥ 0")
	}
	if in.TargetDate != "" {
		if _, err := time.Parse("2006-01-02", in.TargetDate); err != nil {
			return fmt.Errorf("invalid target date")
		}
	}
	if in.PaceWindowDays <= 0 {
		in.PaceWindowDays = 90
	}
	if in.PaceWindowDays > 730 {
		return fmt.Errorf("pace window too large")
	}
	if len(in.AccountIDs) == 0 {
		return fmt.Errorf("link at least one account")
	}
	return nil
}

func (s *Store) CreateGoal(in GoalInput) (int64, error) {
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if in.PaceWindowDays <= 0 {
		in.PaceWindowDays = 90
	}
	if err := in.Validate(); err != nil {
		return 0, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var targetDate any
	if in.TargetDate != "" {
		targetDate = in.TargetDate
	}
	res, err := tx.Exec(`INSERT INTO goals(kind, name, target_cents, target_date, pace_window_days)
		VALUES (?,?,?,?,?)`, in.Kind, strings.TrimSpace(in.Name), in.TargetCents, targetDate, in.PaceWindowDays)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, aid := range uniqueIDs(in.AccountIDs) {
		if _, err := tx.Exec(`INSERT INTO goal_accounts(goal_id, account_id) VALUES (?,?)`, id, aid); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

func (s *Store) UpdateGoal(id int64, in GoalInput) error {
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if in.PaceWindowDays <= 0 {
		in.PaceWindowDays = 90
	}
	if err := in.Validate(); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var targetDate any
	if in.TargetDate != "" {
		targetDate = in.TargetDate
	}
	res, err := tx.Exec(`UPDATE goals SET kind=?, name=?, target_cents=?, target_date=?, pace_window_days=?,
		updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND archived=0`,
		in.Kind, strings.TrimSpace(in.Name), in.TargetCents, targetDate, in.PaceWindowDays, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("goal not found")
	}
	if _, err := tx.Exec(`DELETE FROM goal_accounts WHERE goal_id=?`, id); err != nil {
		return err
	}
	for _, aid := range uniqueIDs(in.AccountIDs) {
		if _, err := tx.Exec(`INSERT INTO goal_accounts(goal_id, account_id) VALUES (?,?)`, id, aid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ArchiveGoal(id int64) error {
	_, err := s.DB.Exec(`UPDATE goals SET archived=1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, id)
	return err
}

func (s *Store) GetGoal(id int64) (*models.Goal, error) {
	g := &models.Goal{}
	var targetDate sql.NullString
	var arch int
	err := s.DB.QueryRow(`SELECT id, kind, name, target_cents, target_date, pace_window_days, archived
		FROM goals WHERE id=?`, id).
		Scan(&g.ID, &g.Kind, &g.Name, &g.TargetCents, &targetDate, &g.PaceWindowDays, &arch)
	if err != nil {
		return nil, err
	}
	g.Archived = arch == 1
	if targetDate.Valid {
		g.TargetDate = targetDate.String
	}
	if err := s.loadGoalAccounts(g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Store) ListGoals(includeArchived bool) ([]models.Goal, error) {
	q := `SELECT id, kind, name, target_cents, target_date, pace_window_days, archived FROM goals`
	if !includeArchived {
		q += ` WHERE archived = 0`
	}
	q += ` ORDER BY kind, name`
	rows, err := s.DB.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Goal
	for rows.Next() {
		var g models.Goal
		var targetDate sql.NullString
		var arch int
		if err := rows.Scan(&g.ID, &g.Kind, &g.Name, &g.TargetCents, &targetDate, &g.PaceWindowDays, &arch); err != nil {
			return nil, err
		}
		g.Archived = arch == 1
		if targetDate.Valid {
			g.TargetDate = targetDate.String
		}
		if err := s.loadGoalAccounts(&g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) loadGoalAccounts(g *models.Goal) error {
	rows, err := s.DB.Query(`SELECT ga.account_id, a.name FROM goal_accounts ga
		JOIN accounts a ON a.id = ga.account_id WHERE ga.goal_id=? ORDER BY a.display_order, a.name`, g.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	g.AccountIDs = nil
	g.AccountNames = nil
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		g.AccountIDs = append(g.AccountIDs, id)
		g.AccountNames = append(g.AccountNames, name)
	}
	return rows.Err()
}

func uniqueIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// AccountBalanceAsOf returns NW-oriented balance as of end of asOfDate (inclusive).
func (s *Store) AccountBalanceAsOf(accountID int64, asOfDate string) (int64, error) {
	var anchor int64
	var anchorDate string
	err := s.DB.QueryRow(`SELECT anchor_balance, anchor_date FROM accounts WHERE id=?`, accountID).
		Scan(&anchor, &anchorDate)
	if err != nil {
		return 0, err
	}
	if asOfDate < anchorDate {
		return 0, nil
	}
	var sum sql.NullInt64
	err = s.DB.QueryRow(`
		SELECT SUM(amount) FROM transactions
		WHERE account_id=? AND deleted_at IS NULL AND transaction_date >= ? AND transaction_date <= ?`,
		accountID, anchorDate, asOfDate).Scan(&sum)
	if err != nil {
		return 0, err
	}
	if sum.Valid {
		return anchor + sum.Int64, nil
	}
	return anchor, nil
}

func (s *Store) linkedBalancesAsOf(accountIDs []int64, asOf string) (int64, error) {
	var total int64
	for _, id := range accountIDs {
		b, err := s.AccountBalanceAsOf(id, asOf)
		if err != nil {
			return 0, err
		}
		total += b
	}
	return total, nil
}

// GoalProgress computes pace, catch-up, and milestone state for one goal.
func (s *Store) GoalProgress(g models.Goal, now time.Time) (models.GoalProgress, error) {
	h, _ := s.Household()
	now = localize(h, now)
	today := now.Format("2006-01-02")
	out := models.GoalProgress{Goal: g}

	window := g.PaceWindowDays
	if window <= 0 {
		window = 90
	}
	windowStart := now.AddDate(0, 0, -window).Format("2006-01-02")

	current, err := s.linkedBalancesAsOf(g.AccountIDs, today)
	if err != nil {
		return out, err
	}
	startBal, err := s.linkedBalancesAsOf(g.AccountIDs, windowStart)
	if err != nil {
		return out, err
	}
	out.StartWindowCents = startBal
	out.DeltaWindowCents = current - startBal

	if g.Kind == "debt" {
		// Display outstanding as positive owed (NW balances are typically ≤0 for liabilities).
		owed := -current
		if owed < 0 {
			owed = 0
		}
		out.CurrentCents = owed
		target := g.TargetCents // usually 0 = paid off
		startOwed := -startBal
		if startOwed < 0 {
			startOwed = 0
		}
		paidDown := startOwed - owed
		out.DeltaWindowCents = paidDown // positive = good for debt
		if startOwed > target {
			denom := startOwed - target
			if denom > 0 {
				out.Pct = float64(startOwed-owed) / float64(denom) * 100
			}
		}
		if owed <= target {
			out.Pct = 100
			out.OnTrack = true
		}
	} else {
		out.CurrentCents = current
		if g.TargetCents > 0 {
			out.Pct = float64(current) / float64(g.TargetCents) * 100
			if out.Pct > 100 {
				out.Pct = 100
			}
		}
	}

	// Weekly pace from window
	weeks := float64(window) / 7.0
	if weeks < 0.1 {
		weeks = 0.1
	}
	out.RecentPerWeek = int64(float64(out.DeltaWindowCents) / weeks)

	if g.TargetDate != "" {
		td, err := time.Parse("2006-01-02", g.TargetDate)
		if err == nil {
			days := int(td.Sub(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())).Hours() / 24)
			out.DaysLeft = days
			if days > 0 {
				out.WeeksLeft = (days + 6) / 7
				if out.WeeksLeft < 1 {
					out.WeeksLeft = 1
				}
				var remaining int64
				if g.Kind == "debt" {
					remaining = out.CurrentCents - g.TargetCents
					if remaining < 0 {
						remaining = 0
					}
				} else {
					remaining = g.TargetCents - out.CurrentCents
					if remaining < 0 {
						remaining = 0
					}
				}
				out.NeededPerWeek = remaining / int64(out.WeeksLeft)
				if remaining > 0 && out.NeededPerWeek == 0 {
					out.NeededPerWeek = 1
				}
				if remaining == 0 {
					out.OnTrack = true
					out.Behind = false
				} else if out.RecentPerWeek >= out.NeededPerWeek {
					out.OnTrack = true
					out.Behind = false
				} else {
					out.Behind = true
					out.OnTrack = false
					gap := out.NeededPerWeek - out.RecentPerWeek
					if gap < 0 {
						gap = -gap
					}
					out.WeeklyCatchUp = gap
					if out.WeeklyCatchUp == 0 && remaining > 0 {
						out.WeeklyCatchUp = out.NeededPerWeek
					}
				}
			} else if days <= 0 {
				// Past due
				var remaining int64
				if g.Kind == "debt" {
					remaining = out.CurrentCents - g.TargetCents
				} else {
					remaining = g.TargetCents - out.CurrentCents
				}
				if remaining > 0 {
					out.Behind = true
					out.WeeklyCatchUp = remaining // treat as immediate
					out.WeeksLeft = 1
					out.NeededPerWeek = remaining
				} else {
					out.OnTrack = true
				}
			}
		}
	} else {
		// No date: on track if progressing; no weekly catch-up without a deadline.
		if g.Kind == "savings" {
			out.OnTrack = out.CurrentCents >= g.TargetCents || out.DeltaWindowCents > 0
		} else {
			out.OnTrack = out.CurrentCents <= g.TargetCents || out.DeltaWindowCents > 0
		}
	}

	// Milestone: crossed 25/50/75/100% within the pace window
	out.MilestoneHit = s.goalMilestoneHit(g, windowStart, today)

	return out, nil
}

func (s *Store) goalMilestoneHit(g models.Goal, from, to string) bool {
	if g.Kind != "savings" || g.TargetCents <= 0 {
		return false
	}
	start, err := s.linkedBalancesAsOf(g.AccountIDs, from)
	if err != nil {
		return false
	}
	end, err := s.linkedBalancesAsOf(g.AccountIDs, to)
	if err != nil {
		return false
	}
	for _, m := range []int64{25, 50, 75, 100} {
		thresh := g.TargetCents * m / 100
		if start < thresh && end >= thresh {
			return true
		}
	}
	return false
}

func (s *Store) ListGoalProgress(now time.Time) ([]models.GoalProgress, error) {
	goals, err := s.ListGoals(false)
	if err != nil {
		return nil, err
	}
	out := make([]models.GoalProgress, 0, len(goals))
	for _, g := range goals {
		p, err := s.GoalProgress(g, now)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// DebtBalanceSeries returns daily (or weekly-sampled) balance for a debt goal's accounts.
func (s *Store) DebtBalanceSeries(g models.Goal, from, to string) ([]models.BalancePoint, error) {
	if from == "" || to == "" {
		return nil, fmt.Errorf("date range required")
	}
	start, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, err
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, err
	}
	days := int(end.Sub(start).Hours()/24) + 1
	step := 1
	if days > 120 {
		step = 7
	}
	var pts []models.BalancePoint
	for d := start; !d.After(end); d = d.AddDate(0, 0, step) {
		ds := d.Format("2006-01-02")
		bal, err := s.linkedBalancesAsOf(g.AccountIDs, ds)
		if err != nil {
			return nil, err
		}
		owed := -bal
		if owed < 0 {
			owed = 0
		}
		pts = append(pts, models.BalancePoint{Date: ds, Label: ds, Amount: owed})
	}
	// Always include end
	if len(pts) == 0 || pts[len(pts)-1].Date != to {
		bal, err := s.linkedBalancesAsOf(g.AccountIDs, to)
		if err != nil {
			return nil, err
		}
		owed := -bal
		if owed < 0 {
			owed = 0
		}
		pts = append(pts, models.BalancePoint{Date: to, Label: to, Amount: owed})
	}
	return pts, nil
}
