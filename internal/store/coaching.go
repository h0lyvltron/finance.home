package store

import (
	"fmt"
	"strconv"
	"time"

	"finance.home/internal/models"
)

// HomeInsights returns up to max insights: actionable warns + one positive counterpart.
// Partner-comparison insights are off. personID filters snoozed/dismissed kinds.
func (s *Store) HomeInsights(h *models.Household, now time.Time, max int) ([]models.CoachingInsight, error) {
	return s.HomeInsightsFor(h, now, max, 0)
}

// HomeInsightsFor is HomeInsights with per-person coaching prefs + fewer_nudges.
func (s *Store) HomeInsightsFor(h *models.Household, now time.Time, max int, personID int64) ([]models.CoachingInsight, error) {
	if h != nil && h.FewerNudges {
		max = 1 // positives-only density
	} else if max <= 0 {
		max = 3
	}
	ym := YearMonth(h, now)
	_ = s.EnsureScheduleInstances(localize(h, now), 45)

	prefs := map[string]CoachingPref{}
	if personID > 0 {
		if m, err := s.coachingPrefMap(personID); err == nil {
			prefs = m
		}
	}
	today := localize(h, now).Format("2006-01-02")
	suppressed := func(kind string) bool {
		return IsCoachingSuppressed(prefs, kind, today)
	}

	var warn []models.CoachingInsight
	var positive *models.CoachingInsight

	if !suppressed("pace") {
		if pace := s.PaceInsight(h, ym, now); pace != nil && pace.Tone == "warn" {
			warn = append(warn, *pace)
		}
	}

	if !suppressed("review") {
		if n, err := s.NeedsReviewCount(); err == nil && n > 0 {
			warn = append(warn, models.CoachingInsight{
				Kind:   "review",
				Tone:   "warn",
				Title:  "Needs review",
				Detail: fmt.Sprintf("%d transaction%s waiting in the review queue", n, plural(n)),
				Href:   "/review",
			})
		}
	}

	if !suppressed("bills") {
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
	}

	if !suppressed("goal") {
		if g := s.GoalBehindInsight(now); g != nil {
			warn = append(warn, *g)
		}
	}
	if !suppressed("spike") {
		if spike := s.SpendSpikeInsight(h, ym, now); spike != nil {
			warn = append(warn, *spike)
		}
	}
	if !suppressed("card") {
		if card := s.CardBalanceRiseInsight(h, ym, now); card != nil && card.Tone == "warn" {
			warn = append(warn, *card)
		}
	}
	var split *models.CoachingInsight
	if !suppressed("split") && (h == nil || !h.FewerNudges) {
		split = s.SplitInsight(personID)
	}

	if !suppressed("positive") {
		positive = s.PositiveBudgetInsight(ym)
		if positive == nil {
			positive = s.GoalMilestoneInsight(now)
		}
		if positive == nil {
			if card := s.CardBalanceRiseInsight(h, ym, now); card != nil && card.Tone == "ok" {
				positive = card
			}
		}
		if positive == nil {
			if pace := s.PaceInsight(h, ym, now); pace != nil && pace.Tone != "warn" {
				positive = s.PositiveBudgetInsight(ym)
			}
		}
		if positive == nil {
			positive = &models.CoachingInsight{
				Kind:   "positive",
				Tone:   "ok",
				Title:  "Books look calm",
				Detail: "No pace alerts from flexible categories right now.",
				Href:   "/plan",
			}
		}
	}

	// Prefer actionable warns, always reserve one slot for a positive counterpart when present.
	room := max
	if positive != nil {
		room = max - 1
	}
	if split != nil {
		room--
	}
	if room < 0 {
		room = 0
	}
	var out []models.CoachingInsight
	for _, w := range warn {
		if len(out) >= room {
			break
		}
		out = append(out, w)
	}
	if split != nil {
		out = append(out, *split)
	}
	if positive != nil {
		out = append(out, *positive)
	}
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

// SplitInsight points at the largest payee hiding inside a crowded category.
func (s *Store) SplitInsight(personID int64) *models.CoachingInsight {
	list, err := s.SplitSuggestions(personID)
	if err != nil || len(list) == 0 {
		return nil
	}
	best := list[0]
	return &models.CoachingInsight{
		Kind:          "split",
		Tone:          "info",
		Title:         best.CategoryName + " is mostly " + best.Name,
		Detail:        best.Detail(),
		Href:          "/categories#cat-" + strconv.FormatInt(best.CategoryID, 10),
		SplitCategory: best.CategoryID,
		SplitKey:      best.PayeeKey,
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// GoalBehindInsight returns the worst behind goal with weekly catch-up coaching.
func (s *Store) GoalBehindInsight(now time.Time) *models.CoachingInsight {
	progress, err := s.ListGoalProgress(now)
	if err != nil {
		return nil
	}
	var best *models.GoalProgress
	for i := range progress {
		p := &progress[i]
		if !p.Behind || p.WeeklyCatchUp <= 0 {
			continue
		}
		if best == nil || p.WeeklyCatchUp > best.WeeklyCatchUp {
			best = p
		}
	}
	if best == nil {
		return nil
	}
	verb := "save"
	if best.Goal.Kind == "debt" {
		verb = "pay"
	}
	return &models.CoachingInsight{
		Kind:    "goal",
		Tone:    "warn",
		Title:   best.Goal.Name + " needs a catch-up",
		Detail:  fmt.Sprintf("%s %s / week to get back on track", verb, formatRough(best.WeeklyCatchUp)),
		Href:    "/goals",
		Amount:  best.WeeklyCatchUp,
		ShowAmt: true,
	}
}

// GoalMilestoneInsight celebrates a recent milestone.
func (s *Store) GoalMilestoneInsight(now time.Time) *models.CoachingInsight {
	progress, err := s.ListGoalProgress(now)
	if err != nil {
		return nil
	}
	for _, p := range progress {
		if p.MilestoneHit {
			return &models.CoachingInsight{
				Kind:    "positive",
				Tone:    "ok",
				Title:   p.Goal.Name + " milestone",
				Detail:  fmt.Sprintf("%.0f%% of the way — keep it going", p.Pct),
				Href:    "/goals",
				Amount:  p.CurrentCents,
				ShowAmt: true,
			}
		}
		if p.OnTrack && p.Goal.Kind == "savings" && p.Pct >= 100 {
			return &models.CoachingInsight{
				Kind:   "positive",
				Tone:   "ok",
				Title:  p.Goal.Name + " reached",
				Detail: "Target hit — nice work.",
				Href:   "/goals",
			}
		}
	}
	return nil
}

// SpendSpikeInsight flags a flexible category spending ≥2× its typical daily pace mid-month.
func (s *Store) SpendSpikeInsight(h *models.Household, ym string, now time.Time) *models.CoachingInsight {
	lines, err := s.MonthBudgetLines(ym)
	if err != nil {
		return nil
	}
	now = localize(h, now)
	day := now.Day()
	if day < 5 {
		return nil // too early
	}
	daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, now.Location()).Day()
	expectedFrac := float64(day) / float64(daysInMonth)

	var worst *models.BudgetLine
	var worstRatio float64
	for i := range lines {
		ln := &lines[i]
		if ln.HasChildren || ln.AmountCents <= 0 || ln.FixedFlexible == "fixed" {
			continue
		}
		expected := float64(ln.AmountCents) * expectedFrac
		if expected <= 0 {
			continue
		}
		ratio := float64(ln.SpentCents) / expected
		// Spike: ≥2× expected pace and at least $50 over expected
		if ratio >= 2.0 && float64(ln.SpentCents)-expected >= 5000 {
			if worst == nil || ratio > worstRatio {
				worst = ln
				worstRatio = ratio
			}
		}
	}
	if worst == nil {
		return nil
	}
	return &models.CoachingInsight{
		Kind:    "spike",
		Tone:    "warn",
		Title:   worst.CategoryName + " spending spike",
		Detail:  fmt.Sprintf("%.0f× expected pace this month", worstRatio),
		Href:    fmt.Sprintf("/transactions?preset=this_month&category=%d", worst.CategoryID),
		Amount:  worst.SpentCents,
		ShowAmt: true,
	}
}

// CardBalanceRiseInsight warns when credit card outstanding rose this month; celebrates when it fell.
func (s *Store) CardBalanceRiseInsight(h *models.Household, ym string, now time.Time) *models.CoachingInsight {
	from, to, err := FiscalMonthBounds(h, ym)
	if err != nil {
		return nil
	}
	end := to
	loc := localize(h, now)
	if ym == YearMonth(h, now) {
		end = loc.Format("2006-01-02")
	}
	accts, err := s.ListAccounts(false)
	if err != nil {
		return nil
	}
	var worstRise int64
	var bestDrop int64
	var riseName, dropName string
	var riseID, dropID int64
	for _, a := range accts {
		if a.Type != "credit" {
			continue
		}
		startBal, err := s.AccountBalanceAsOf(a.ID, from)
		if err != nil {
			continue
		}
		endBal, err := s.AccountBalanceAsOf(a.ID, end)
		if err != nil {
			continue
		}
		// NW-oriented: more negative = higher card balance owed
		startOwed := -startBal
		endOwed := -endBal
		if startOwed < 0 {
			startOwed = 0
		}
		if endOwed < 0 {
			endOwed = 0
		}
		delta := endOwed - startOwed
		if delta > 2500 && delta > worstRise { // > $25
			worstRise = delta
			riseName = a.Name
			riseID = a.ID
		}
		if delta < -2500 && -delta > bestDrop {
			bestDrop = -delta
			dropName = a.Name
			dropID = a.ID
		}
	}
	if worstRise > 0 {
		return &models.CoachingInsight{
			Kind:    "card",
			Tone:    "warn",
			Title:   riseName + " balance up",
			Detail:  fmt.Sprintf("%s higher than month start", formatRough(worstRise)),
			Href:    fmt.Sprintf("/accounts/%d", riseID),
			Amount:  worstRise,
			ShowAmt: true,
		}
	}
	if bestDrop > 0 {
		return &models.CoachingInsight{
			Kind:    "positive",
			Tone:    "ok",
			Title:   dropName + " balance down",
			Detail:  fmt.Sprintf("Paid down %s this month", formatRough(bestDrop)),
			Href:    fmt.Sprintf("/accounts/%d", dropID),
			Amount:  bestDrop,
			ShowAmt: true,
		}
	}
	return nil
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
	Goals        []models.GoalProgress
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
	goals, _ := s.ListGoalProgress(now)
	d.Goals = goals
	return d, nil
}

// BuildHomeDashboardFor is BuildHomeDashboard with per-person coaching prefs.
func (s *Store) BuildHomeDashboardFor(h *models.Household, now time.Time, personID int64) (HomeDashboard, error) {
	d, err := s.BuildHomeDashboard(h, now)
	if err != nil {
		return d, err
	}
	insights, err := s.HomeInsightsFor(h, now, 3, personID)
	if err != nil {
		return d, err
	}
	d.Insights = insights
	return d, nil
}
