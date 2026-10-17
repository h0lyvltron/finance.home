package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"finance.home/internal/auth"
	"finance.home/internal/money"
	"finance.home/internal/store"
)

func (a *App) planPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "plan"
	a.withQuickAdd(&d)

	h := d.Household
	now := time.Now()
	ym := store.ParseYearMonth(r.FormValue("month"), h, now)
	tab := strings.TrimSpace(r.FormValue("tab"))
	if tab == "" {
		tab = "budgets"
	}
	d.PlanTab = tab
	d.YearMonth = ym

	_ = a.Store.EnsureScheduleInstances(now, 60)

	lines, err := a.Store.MonthBudgetLines(ym)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.BudgetLines = lines
	bh, _ := a.Store.BudgetHealthSummary(ym)
	d.BudgetHealth = bh
	lts, _ := a.Store.LeftToSpend(h, ym)
	d.LeftToSpend = lts

	bills, _ := a.Store.UpcomingBills(h, now, 14)
	d.Bills = bills
	tpls, _ := a.Store.ListScheduleTemplates(true)
	d.Schedules = tpls
	sugs, _ := a.Store.SuggestRecurringFromHistory(now, 6)
	d.Suggestions = sugs

	a.Render.RenderHTTP(w, "plan", d)
}

func (a *App) planSettingsPost(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	income, _ := money.ParseDecimal(r.FormValue("expected_income"))
	savings, _ := money.ParseDecimal(r.FormValue("savings_target"))
	if income < 0 {
		income = -income
	}
	if savings < 0 {
		savings = -savings
	}
	if err := a.Store.UpdateHouseholdPlan(income, savings); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ym := store.ParseYearMonth(r.FormValue("month"), a.household(), time.Now())
	http.Redirect(w, r, "/plan?month="+ym+"&tab=budgets", http.StatusSeeOther)
}

func (a *App) planBudgetsPost(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ym := store.ParseYearMonth(r.FormValue("month"), a.household(), time.Now())
	amounts := map[int64]int64{}
	rollovers := map[int64]bool{}
	modes := map[int64]string{}

	for key, vals := range r.PostForm {
		if strings.HasPrefix(key, "amount_") {
			id, err := strconv.ParseInt(strings.TrimPrefix(key, "amount_"), 10, 64)
			if err != nil || id <= 0 || len(vals) == 0 {
				continue
			}
			cents, err := money.ParseDecimal(vals[0])
			if err != nil {
				continue
			}
			if cents < 0 {
				cents = -cents
			}
			amounts[id] = cents
		}
		if strings.HasPrefix(key, "rollover_") {
			id, err := strconv.ParseInt(strings.TrimPrefix(key, "rollover_"), 10, 64)
			if err == nil && id > 0 {
				rollovers[id] = true
			}
		}
		if strings.HasPrefix(key, "mode_") {
			id, err := strconv.ParseInt(strings.TrimPrefix(key, "mode_"), 10, 64)
			if err == nil && id > 0 && len(vals) > 0 {
				modes[id] = vals[0]
			}
		}
	}
	if err := a.Store.SaveBudgetForm(ym, amounts, rollovers, modes); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/plan?month="+ym+"&tab=budgets", http.StatusSeeOther)
}

func (a *App) planCopyMonth(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	ym := store.ParseYearMonth(r.FormValue("month"), a.household(), time.Now())
	if _, err := a.Store.CopyBudgetsFromPrevious(ym); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/plan?month="+ym+"&tab=budgets", http.StatusSeeOther)
}

func (a *App) scheduleCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	amt, err := money.ParseDecimal(r.FormValue("amount"))
	if err != nil || amt == 0 {
		http.Redirect(w, r, "/plan?tab=bills", http.StatusSeeOther)
		return
	}
	if amt < 0 {
		amt = -amt
	}
	in := store.ScheduleInput{
		Name:        strings.TrimSpace(r.FormValue("name")),
		PayeeName:   strings.TrimSpace(r.FormValue("payee")),
		AmountCents: amt,
		Kind:        r.FormValue("kind"),
		Frequency:   r.FormValue("frequency"),
		NextDue:     r.FormValue("next_due"),
		AutoMatch:   r.FormValue("auto_match") != "0",
	}
	if v := r.FormValue("interval_n"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			in.IntervalN = n
		}
	}
	if v := r.FormValue("account_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			in.AccountID = &id
		}
	}
	if v := r.FormValue("category_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			in.CategoryID = &id
		}
	}
	if _, err := a.Store.CreateScheduleTemplate(in); err != nil {
		d := a.base(r, sess)
		d.Nav = "plan"
		d.Error = err.Error()
		d.PlanTab = "bills"
		a.withQuickAdd(&d)
		a.Render.RenderHTTP(w, "plan", d)
		return
	}
	http.Redirect(w, r, "/plan?tab=bills", http.StatusSeeOther)
}

func (a *App) scheduleDeactivate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = a.Store.DeactivateScheduleTemplate(id)
	http.Redirect(w, r, "/plan?tab=bills", http.StatusSeeOther)
}

func (a *App) scheduleMarkPaid(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = a.Store.MarkSchedulePaid(id, nil)
	ref := r.FormValue("return")
	if ref == "" {
		ref = "/plan?tab=bills"
	}
	http.Redirect(w, r, ref, http.StatusSeeOther)
}

func (a *App) scheduleSkip(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = a.Store.SkipScheduleInstance(id)
	ref := r.FormValue("return")
	if ref == "" {
		ref = "/plan?tab=bills"
	}
	http.Redirect(w, r, ref, http.StatusSeeOther)
}

func (a *App) scheduleSuggestAccept(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	amt, _ := money.ParseDecimal(r.FormValue("amount"))
	if amt < 0 {
		amt = -amt
	}
	in := store.ScheduleInput{
		Name:        r.FormValue("name"),
		PayeeName:   r.FormValue("payee"),
		AmountCents: amt,
		Kind:        "expense",
		Frequency:   "monthly",
		NextDue:     r.FormValue("next_due"),
		AutoMatch:   true,
	}
	if in.NextDue == "" {
		in.NextDue = time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	}
	if v := r.FormValue("account_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			in.AccountID = &id
		}
	}
	if v := r.FormValue("category_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			in.CategoryID = &id
		}
	}
	_, _ = a.Store.CreateScheduleTemplate(in)
	http.Redirect(w, r, "/plan?tab=bills", http.StatusSeeOther)
}
