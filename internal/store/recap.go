package store

import (
	"fmt"
	"time"

	"finance.home/internal/models"
)

// BuildMonthlyRecap assembles the “money date” screen for a year-month.
func (s *Store) BuildMonthlyRecap(h *models.Household, ym string, now time.Time) (models.MonthlyRecap, error) {
	out := models.MonthlyRecap{YearMonth: ym}
	t, err := time.Parse("2006-01", ym)
	if err != nil {
		return out, fmt.Errorf("bad month")
	}
	out.Label = t.Format("January 2006")
	from, to, err := monthBounds(ym)
	if err != nil {
		return out, err
	}

	bh, err := s.BudgetHealthSummary(ym)
	if err != nil {
		return out, err
	}
	out.Budgeted = bh.Budgeted
	out.Spent = bh.Spent
	out.BudgetRemaining = bh.Budgeted - bh.Spent

	income, expense, err := s.MonthIncomeExpense(ym, h)
	if err != nil {
		return out, err
	}
	sr := models.SavingsRateReport{
		Income: income, Expense: expense, Saved: income - expense, YearMonth: ym,
	}
	if income > 0 {
		sr.RatePct = float64(sr.Saved) / float64(income) * 100
	}
	if h != nil && h.SavingsTargetCents > 0 {
		sr.HasTarget = true
		sr.TargetCents = h.SavingsTargetCents
		if h.ExpectedIncomeCents > 0 {
			sr.TargetRatePct = float64(h.SavingsTargetCents) / float64(h.ExpectedIncomeCents) * 100
		}
		sr.VsTargetCents = sr.Saved - sr.TargetCents
	}
	out.SavingsRate = sr

	out.NetWorthStart, err = s.NetWorthAsOf(from)
	if err != nil {
		return out, err
	}
	// End of month or today if current month
	endDate := to
	locNow := localize(h, now)
	if ym == YearMonth(h, now) {
		endDate = locNow.Format("2006-01-02")
	}
	out.NetWorthEnd, err = s.NetWorthAsOf(endDate)
	if err != nil {
		return out, err
	}
	out.NetWorthChange = out.NetWorthEnd - out.NetWorthStart

	goals, err := s.ListGoalProgress(now)
	if err != nil {
		return out, err
	}
	out.Goals = goals

	wins, watch := s.recapWinsAndWatch(h, ym, now, out)
	out.Wins = wins
	out.Watch = watch
	return out, nil
}

func (s *Store) recapWinsAndWatch(h *models.Household, ym string, now time.Time, recap models.MonthlyRecap) ([]models.CoachingInsight, *models.CoachingInsight) {
	var wins []models.CoachingInsight
	var watches []models.CoachingInsight

	if recap.BudgetRemaining > 0 && recap.Budgeted > 0 {
		wins = append(wins, models.CoachingInsight{
			Kind: "positive", Tone: "ok", Title: "Under budget",
			Detail: fmt.Sprintf("%s left against category targets", formatRough(recap.BudgetRemaining)),
			Href:   "/plan?month=" + ym, Amount: recap.BudgetRemaining, ShowAmt: true,
		})
	} else if recap.BudgetRemaining < 0 {
		watches = append(watches, models.CoachingInsight{
			Kind: "pace", Tone: "warn", Title: "Over budget",
			Detail: fmt.Sprintf("%s past category targets this month", formatRough(-recap.BudgetRemaining)),
			Href:   "/reports/budget?month=" + ym, Amount: -recap.BudgetRemaining, ShowAmt: true,
		})
	}

	if recap.NetWorthChange > 0 {
		wins = append(wins, models.CoachingInsight{
			Kind: "positive", Tone: "ok", Title: "Net worth up",
			Detail: fmt.Sprintf("%s this month", formatRough(recap.NetWorthChange)),
			Href:   "/reports/networth", Amount: recap.NetWorthChange, ShowAmt: true,
		})
	} else if recap.NetWorthChange < 0 {
		watches = append(watches, models.CoachingInsight{
			Kind: "networth", Tone: "warn", Title: "Net worth down",
			Detail: fmt.Sprintf("%s this month", formatRough(recap.NetWorthChange)),
			Href:   "/reports/networth", Amount: -recap.NetWorthChange, ShowAmt: true,
		})
	}

	if recap.SavingsRate.HasTarget {
		if recap.SavingsRate.VsTargetCents >= 0 {
			wins = append(wins, models.CoachingInsight{
				Kind: "positive", Tone: "ok", Title: "Hit savings target",
				Detail: fmt.Sprintf("Saved %s vs %s plan", formatRough(recap.SavingsRate.Saved), formatRough(recap.SavingsRate.TargetCents)),
				Href:   "/reports/savings", Amount: recap.SavingsRate.Saved, ShowAmt: true,
			})
		} else {
			watches = append(watches, models.CoachingInsight{
				Kind: "savings", Tone: "warn", Title: "Behind savings target",
				Detail: fmt.Sprintf("%s short of the household plan", formatRough(-recap.SavingsRate.VsTargetCents)),
				Href:   "/reports/savings", Amount: -recap.SavingsRate.VsTargetCents, ShowAmt: true,
			})
		}
	} else if recap.SavingsRate.RatePct >= 20 {
		wins = append(wins, models.CoachingInsight{
			Kind: "positive", Tone: "ok", Title: "Solid savings rate",
			Detail: fmt.Sprintf("%.0f%% of income kept", recap.SavingsRate.RatePct),
			Href:   "/reports/savings",
		})
	}

	for _, g := range recap.Goals {
		if g.MilestoneHit {
			wins = append(wins, models.CoachingInsight{
				Kind: "positive", Tone: "ok", Title: g.Goal.Name + " milestone",
				Detail: fmt.Sprintf("%.0f%% of the way there", g.Pct),
				Href:   "/goals", Amount: g.CurrentCents, ShowAmt: true,
			})
		}
		if g.Behind && g.WeeklyCatchUp > 0 {
			watches = append(watches, models.CoachingInsight{
				Kind: "goal", Tone: "warn", Title: g.Goal.Name + " behind",
				Detail: fmt.Sprintf("Catch up ~%s / week", formatRough(g.WeeklyCatchUp)),
				Href:   "/goals", Amount: g.WeeklyCatchUp, ShowAmt: true,
			})
		}
	}

	// Card balance rose this month
	if card := s.CardBalanceRiseInsight(h, ym, now); card != nil && card.Tone == "warn" {
		watches = append(watches, *card)
	} else if card != nil && card.Tone == "ok" {
		wins = append(wins, *card)
	}

	// Trim wins to 2
	if len(wins) > 2 {
		wins = wins[:2]
	}
	var watch *models.CoachingInsight
	if len(watches) > 0 {
		w := watches[0]
		watch = &w
	} else if len(wins) == 0 {
		// Calm fallback watch
		watch = &models.CoachingInsight{
			Kind: "positive", Tone: "info", Title: "Quiet month",
			Detail: "No big red flags — still worth a quick money date.",
			Href:   "/plan?month=" + ym,
		}
	}
	return wins, watch
}
