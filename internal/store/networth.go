package store

import (
	"fmt"
	"time"

	"finance.home/internal/models"
)

// NetWorthAsOf sums all non-archived account balances as of a date.
func (s *Store) NetWorthAsOf(asOf string) (int64, error) {
	accts, err := s.ListAccounts(false)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, a := range accts {
		b, err := s.AccountBalanceAsOf(a.ID, asOf)
		if err != nil {
			return 0, err
		}
		total += b
	}
	return total, nil
}

// NetWorthSeries reconstructs household + per-account balances over a date range.
func (s *Store) NetWorthSeries(from, to string, includeAccounts bool) (models.NetWorthSeries, error) {
	out := models.NetWorthSeries{}
	if from == "" || to == "" {
		return out, fmt.Errorf("date range required")
	}
	start, err := time.Parse("2006-01-02", from)
	if err != nil {
		return out, err
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil {
		return out, err
	}
	if end.Before(start) {
		return out, fmt.Errorf("invalid range")
	}

	accts, err := s.ListAccounts(false)
	if err != nil {
		return out, err
	}

	days := int(end.Sub(start).Hours()/24) + 1
	step := 1
	if days > 180 {
		step = 7
	} else if days > 90 {
		step = 3
	}

	type snap struct {
		date string
		bals map[int64]int64
		nw   int64
	}
	var snaps []snap
	for d := start; !d.After(end); d = d.AddDate(0, 0, step) {
		ds := d.Format("2006-01-02")
		bals := map[int64]int64{}
		var nw int64
		for _, a := range accts {
			b, err := s.AccountBalanceAsOf(a.ID, ds)
			if err != nil {
				return out, err
			}
			bals[a.ID] = b
			nw += b
		}
		snaps = append(snaps, snap{date: ds, bals: bals, nw: nw})
	}
	if len(snaps) == 0 || snaps[len(snaps)-1].date != to {
		bals := map[int64]int64{}
		var nw int64
		for _, a := range accts {
			b, err := s.AccountBalanceAsOf(a.ID, to)
			if err != nil {
				return out, err
			}
			bals[a.ID] = b
			nw += b
		}
		snaps = append(snaps, snap{date: to, bals: bals, nw: nw})
	}

	for _, sn := range snaps {
		out.Points = append(out.Points, models.BalancePoint{
			Date: sn.date, Label: sn.date, Amount: sn.nw,
		})
	}
	if len(out.Points) > 0 {
		out.Start = out.Points[0].Amount
		out.End = out.Points[len(out.Points)-1].Amount
		out.Change = out.End - out.Start
	}

	if includeAccounts {
		for _, a := range accts {
			ser := models.AccountSeries{
				AccountID: a.ID, AccountName: a.Name, AccountType: a.Type,
			}
			for _, sn := range snaps {
				ser.Points = append(ser.Points, models.BalancePoint{
					Date: sn.date, Label: sn.date, Amount: sn.bals[a.ID],
				})
			}
			out.Accounts = append(out.Accounts, ser)
		}
	}
	return out, nil
}

// SavingsRate computes income/expense/saved for a filter range vs optional household monthly target.
func (s *Store) SavingsRate(f QueryFilter, h *models.Household, now time.Time) (models.SavingsRateReport, error) {
	out := models.SavingsRateReport{}
	f = f.Clone()
	f.ExcludeTransfers = true
	f.ExcludeStarting = true
	f.ResolveDates(h, now)
	out.YearMonth = YearMonth(h, now)

	points, err := s.CashFlow(f, "month", h, now)
	if err != nil {
		return out, err
	}
	for _, p := range points {
		out.Income += p.Income
		out.Expense += p.Expense
	}
	out.Saved = out.Income - out.Expense
	if out.Income > 0 {
		out.RatePct = float64(out.Saved) / float64(out.Income) * 100
	}

	if h != nil && h.SavingsTargetCents > 0 {
		out.HasTarget = true
		// Scale monthly target by number of month buckets in the range (at least 1).
		months := len(points)
		if months < 1 {
			months = 1
			// If no cash-flow months, estimate from date span
			if f.DateFrom != "" && f.DateTo != "" {
				from, e1 := time.Parse("2006-01-02", f.DateFrom)
				to, e2 := time.Parse("2006-01-02", f.DateTo)
				if e1 == nil && e2 == nil {
					m := int(to.Sub(from).Hours()/24/30) + 1
					if m < 1 {
						m = 1
					}
					months = m
				}
			}
		}
		out.TargetCents = h.SavingsTargetCents * int64(months)
		if h.ExpectedIncomeCents > 0 {
			out.TargetRatePct = float64(h.SavingsTargetCents) / float64(h.ExpectedIncomeCents) * 100
		}
		out.VsTargetCents = out.Saved - out.TargetCents
	}
	return out, nil
}

// MonthIncomeExpense returns cash-flow totals for one fiscal month.
func (s *Store) MonthIncomeExpense(ym string, h *models.Household) (income, expense int64, err error) {
	from, to, err := FiscalMonthBounds(h, ym)
	if err != nil {
		return 0, 0, err
	}
	f := QueryFilter{
		DatePreset:       "custom",
		DateFrom:         from,
		DateTo:           to,
		ExcludeTransfers: true,
		ExcludeStarting:  true,
	}
	points, err := s.CashFlow(f, "month", h, time.Now())
	if err != nil {
		return 0, 0, err
	}
	for _, p := range points {
		income += p.Income
		expense += p.Expense
	}
	return income, expense, nil
}
