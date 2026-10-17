package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"finance.home/internal/auth"
	"finance.home/internal/money"
	"finance.home/internal/store"
)

func (a *App) rulesPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	rules, err := a.Store.ListPayeeRules()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.Rules = rules
	a.Render.RenderHTTP(w, "rules", d)
}

func (a *App) ruleCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	in := store.PayeeRuleInput{
		Name:          r.FormValue("name"),
		PayeeMatch:    r.FormValue("payee_match"),
		PayeeValue:    r.FormValue("payee_value"),
		NotesContains: r.FormValue("notes_contains"),
		SetPerson:     r.FormValue("set_person"),
		RenamePayee:   r.FormValue("rename_payee"),
		SetTags:       r.FormValue("set_tags"),
	}
	if in.Name == "" {
		in.Name = in.PayeeValue
	}
	if v := r.FormValue("account_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil && id > 0 {
			in.AccountID = &id
		}
	}
	if v := r.FormValue("set_category_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil && id > 0 {
			in.SetCategoryID = &id
		}
	}
	if v := strings.TrimSpace(r.FormValue("amount_min")); v != "" {
		c, err := money.ParseDecimal(v)
		if err == nil {
			in.AmountMin = &c
		}
	}
	if v := strings.TrimSpace(r.FormValue("amount_max")); v != "" {
		c, err := money.ParseDecimal(v)
		if err == nil {
			in.AmountMax = &c
		}
	}
	if _, err := a.Store.CreatePayeeRule(in); err != nil {
		d := a.base(r, sess)
		d.Error = err.Error()
		a.withQuickAdd(&d)
		d.Rules, _ = a.Store.ListPayeeRules()
		a.Render.RenderHTTP(w, "rules", d)
		return
	}
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (a *App) ruleDelete(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = a.Store.DeletePayeeRule(id)
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (a *App) ruleMove(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	dir := r.FormValue("dir")
	_ = a.Store.MovePayeeRule(id, dir)
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (a *App) profilesPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	profiles, _ := a.Store.ListMappingProfiles()
	d.Profiles = profiles
	a.Render.RenderHTTP(w, "profiles", d)
}

func (a *App) profileDelete(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = a.Store.DeleteMappingProfile(id)
	http.Redirect(w, r, "/profiles", http.StatusSeeOther)
}
