package store

import (
	"fmt"
	"time"

	"finance.home/internal/models"
)

// HomeInsights returns up to max insights: pace, review, bills, plus a positive when possible.
// Partner-comparison insights are off.
func (s *Store) HomeInsights(h *models.Household, now time.Time, max int) ([]models.CoachingInsight, error) {
	if max <= 0 {
		max = 3
	}
	ym := YearMonth(h, now)
	_ = s.EnsureScheduleInstances(localize(h, now), 45)

	var out []models.CoachingInsight
	var warn []models.CoachingInsight
	var positive *models.CoachingInsight

	if pace := s.PaceInsight(h, ym, now); pace != nil {
		if pace.Tone == "warn" {
			warn = append(warn, *pace)
		} else {
			// Keep pace info as candidate; may drop if we need room for review/bills
			warn = append(warn, *pace)
		}
	}

	if n, err := s.NeedsReviewCount(); err == nil && n > 0 {
		warn = append(warn, models.CoachingInsight{
			Kind:   "review",
			Tone:   "warn",
			Title:  "Needs review",
			Detail: fmt.Sprintf("%d transaction%s waiting in the review queue", n, plural(n)),
			Href:   "/review",
		})
	}

	overdue, _ := s.UnmatchedOverdueCount()
	upcoming, _ := s.UpcomingBills(h, now, 14)
	unmatchedSoon := 0
	for _, b := range upcoming {
		if b.Status == "upcoming" || b.Status == "overdue" {
			unmatchedSoon++
		}
	}
	if overdue > 0 {
		warn = append(warn, models.CoachingInsight{
			Kind:   "bills",
			Tone:   "warn",
			Title:  "Overdue bills",
			Detail: fmt.Sprintf("%d bill%s past due and unmatched", overdue, plural(overdue)),
			Href:   "/plan?tab=bills",
		})
	} else if unmatchedSoon > 0 {
		warn = append(warn, models.CoachingInsight{
			Kind:   "bills",
			Tone:   "info",
			Title:  "Bills coming up",
			Detail: fmt.Sprintf("%d bill%s in the next 14 days", unmatchedSoon, plural(unmatchedSoon)),
			Href:   "/plan?tab=bills",
		})
	}

	positive = s.PositiveBudgetInsight(ym)
	if positive == nil {
		positive = &models.CoachingInsight{
			Kind:   "positive",
			Tone:   "ok",
			Title:  "Books look calm",
			Detail: "No pace alerts from flexible categories right now.",
			Href:   "/plan",
		}
	}

	// Prefer actionable warns, always reserve one slot for a positive counterpart.
	room := max - 1
	if room < 0 {
		room = 0
	}
	for _, w := range warn {
		if len(out) >= room {
			break
		}
		out = append(out, w)
	}
	out = append(out, *positive)
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// HomeDashboard assembles curated Home widgets.
type HomeDashboard struct {
	LeftToSpend  models.LeftToSpend
	BudgetHealth models.BudgetHealth
	Bills        []models.ScheduleInstance
	Insights     []models.CoachingInsight
	NetWorth     int64
	ReviewCount  int
	YearMonth    string
}

func (s *Store) BuildHomeDashboard(h *models.Household, now time.Time) (HomeDashboard, error) {
	ym := YearMonth(h, now)
	d := HomeDashboard{YearMonth: ym}
	lts, err := s.LeftToSpend(h, ym)
	if err != nil {
		return d, err
	}
	d.LeftToSpend = lts
	bh, err := s.BudgetHealthSummary(ym)
	if err != nil {
		return d, err
	}
	d.BudgetHealth = bh
	bills, err := s.UpcomingBills(h, now, 14)
	if err != nil {
		return d, err
	}
	d.Bills = bills
	insights, err := s.HomeInsights(h, now, 3)
	if err != nil {
		return d, err
	}
	d.Insights = insights
	accts, err := s.ListAccounts(false)
	if err != nil {
		return d, err
	}
	for _, a := range accts {
		d.NetWorth += a.Balance
	}
	d.ReviewCount, _ = s.NeedsReviewCount()
	return d, nil
}
