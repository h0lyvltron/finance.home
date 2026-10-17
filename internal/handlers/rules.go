package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"finance.home/internal/auth"
	"finance.home/internal/money"
	"finance.home/internal/payee"
	"finance.home/internal/store"
)

func wantsJSON(r *http.Request) bool {
	return r.Header.Get("Accept") == "application/json"
}

func (a *App) rulesPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	views, err := a.Store.RuleViews()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.RuleViews = views
	d.RuleQuery = r.URL.Query().Get("q")
	a.Render.RenderHTTP(w, "rules", d)
}

func parseRuleForm(r *http.Request) store.PayeeRuleInput {
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
	return in
}

func (a *App) rulesError(w http.ResponseWriter, r *http.Request, sess *auth.Session, err error) {
	d := a.base(r, sess)
	d.Nav = "more"
	d.Error = err.Error()
	a.withQuickAdd(&d)
	d.RuleViews, _ = a.Store.RuleViews()
	a.Render.RenderHTTP(w, "rules", d)
}

func (a *App) ruleCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	in := parseRuleForm(r)
	id, err := a.Store.CreatePayeeRule(in)
	if err != nil {
		a.rulesError(w, r, sess, err)
		return
	}
	http.Redirect(w, r, "/rules#rule-"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (a *App) ruleUpdate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	in := parseRuleForm(r)
	if strings.TrimSpace(in.PayeeValue) == "" {
		a.rulesError(w, r, sess, fmt.Errorf("A rule needs payee text to match."))
		return
	}
	if err := a.Store.UpdatePayeeRule(id, in); err != nil {
		a.rulesError(w, r, sess, err)
		return
	}
	http.Redirect(w, r, "/rules#rule-"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (a *App) ruleCategory(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	catID, _ := strconv.ParseInt(r.FormValue("category_id"), 10, 64)
	ref, err := a.Store.SetPayeeRuleCategory(id, catID)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "Could not change the rule."})
		return
	}
	ids, rule, err := a.Store.MisfiledByRule(id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{
		"category": catRefJSON{ID: ref.ID, Name: ref.Name},
		"name":     rule.Name,
		"misfiled": len(ids),
		"message":  fmt.Sprintf("Future “%s” → %s", payee.Normalize(rule.PayeeValue), ref.Name),
	})
}

func (a *App) ruleEnabled(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	on := r.FormValue("enabled") == "1"
	if err := a.Store.SetPayeeRuleEnabled(id, on); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if wantsJSON(r) {
		writeJSON(w, 200, map[string]any{"enabled": on})
		return
	}
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

// ruleApply re-files saved transactions the rule matches into its category.
func (a *App) ruleApply(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ids, rule, err := a.Store.MisfiledByRule(id)
	if err != nil || rule.SetCategoryID == nil {
		writeJSON(w, 400, map[string]string{"error": "This rule has no category to apply."})
		return
	}
	changed, undoID, err := a.Store.CategorizeTransactions(ids, *rule.SetCategoryID, "", sess.PersonID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	res := categorizeResult{
		IDs:      changed,
		Category: catRefJSON{ID: *rule.SetCategoryID, Name: rule.SetCategoryName},
		UndoID:   undoID,
		Message:  fmt.Sprintf("Re-filed %d as %s", len(changed), rule.SetCategoryName),
	}
	res.ReviewCount, _ = a.Store.NeedsReviewCount()
	writeJSON(w, 200, res)
}

func (a *App) ruleDelete(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	undoID, err := a.Store.DeletePayeeRuleUndoable(id, sess.PersonID)
	if wantsJSON(r) {
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "Could not delete the rule."})
			return
		}
		writeJSON(w, 200, map[string]any{"undoId": undoID, "message": "Rule deleted"})
		return
	}
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
