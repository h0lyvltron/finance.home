package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"finance.home/internal/auth"
	"finance.home/internal/store"
)

func (a *App) renderCategories(w http.ResponseWriter, r *http.Request, sess *auth.Session, errMsg string) {
	d := a.base(r, sess)
	d.Nav = "more"
	d.Error = errMsg
	a.withQuickAdd(&d)
	groups, archived, err := a.Store.CategoryTree()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.CategoryGroups = groups
	d.ArchivedCategories = archived
	d.Swatches = store.CategorySwatches
	d.RuleQuery = r.URL.Query().Get("q")
	a.Render.RenderHTTP(w, "categories", d)
}

func (a *App) categoriesPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	a.renderCategories(w, r, sess, "")
}

func formParentID(r *http.Request) *int64 {
	if v := r.FormValue("parent_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil && id > 0 {
			return &id
		}
	}
	return nil
}

// formColor reads the swatch choice; "custom" means the native color input.
func formColor(r *http.Request) string {
	c := r.FormValue("color")
	if c == "custom" {
		c = r.FormValue("color_custom")
	}
	return c
}

func categoryAnchor(id int64) string {
	return "/categories#cat-" + strconv.FormatInt(id, 10)
}

func (a *App) categoryCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	id, err := a.Store.CreateCategoryChecked(r.FormValue("name"), r.FormValue("kind"),
		r.FormValue("fixed_flexible"), formColor(r), formParentID(r))
	if err != nil {
		a.renderCategories(w, r, sess, err.Error())
		return
	}
	http.Redirect(w, r, categoryAnchor(id), http.StatusSeeOther)
}

func (a *App) categoryUpdate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	err := a.Store.UpdateCategory(id, store.CategoryInput{
		Name:          r.FormValue("name"),
		ParentID:      formParentID(r),
		FixedFlexible: r.FormValue("fixed_flexible"),
		ExcludeBudget: r.FormValue("exclude_budget") == "1",
		Color:         formColor(r),
		ColorChildren: r.FormValue("color_children") == "1",
	})
	if err != nil {
		a.renderCategories(w, r, sess, err.Error())
		return
	}
	http.Redirect(w, r, categoryAnchor(id), http.StatusSeeOther)
}

func (a *App) categoryMove(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	_ = a.Store.MoveCategory(id, r.FormValue("dir"))
	http.Redirect(w, r, categoryAnchor(id), http.StatusSeeOther)
}

func (a *App) categoryArchive(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, err := a.Store.GetCategory(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "Category not found."})
		return
	}
	undoID, ids, err := a.Store.ArchiveCategory(id, sess.PersonID)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	msg := "Archived " + c.Name
	if len(ids) > 1 {
		msg += fmt.Sprintf(" and %d subcategories", len(ids)-1)
	}
	writeJSON(w, 200, map[string]any{"undoId": undoID, "message": msg})
}

func (a *App) categoryRestore(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := a.Store.RestoreCategory(id); err != nil {
		a.renderCategories(w, r, sess, err.Error())
		return
	}
	http.Redirect(w, r, categoryAnchor(id), http.StatusSeeOther)
}

func (a *App) categoryMerge(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	dst, _ := strconv.ParseInt(r.FormValue("category_id"), 10, 64)
	res, err := a.Store.MergeCategory(id, dst, sess.PersonID)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	var parts []string
	if res.Transactions > 0 {
		parts = append(parts, plural(res.Transactions, "transaction"))
	}
	if res.Rules > 0 {
		parts = append(parts, plural(res.Rules, "rule"))
	}
	if res.Children > 0 {
		parts = append(parts, plural(res.Children, "subcategory"))
	}
	msg := fmt.Sprintf("Merged %s into %s", res.From, res.Into)
	if len(parts) > 0 {
		msg += " · moved " + strings.Join(parts, ", ")
	}
	writeJSON(w, 200, map[string]any{"undoId": res.UndoID, "message": msg, "id": dst})
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "y") {
		return fmt.Sprintf("%d %sies", n, strings.TrimSuffix(word, "y"))
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func (a *App) payeesPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	payees, _ := a.Store.ListPayees(200)
	d.Payees = payees
	a.Render.RenderHTTP(w, "payees", d)
}

func (a *App) payeeCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	d := a.base(r, sess)
	payees, _ := a.Store.ListPayees(200)
	d.Payees = payees
	if name == "" {
		d.Error = "Name required"
		a.Render.RenderHTTP(w, "payees", d)
		return
	}
	if _, err := a.Store.CreatePayee(name); err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "payees", d)
		return
	}
	http.Redirect(w, r, "/payees", http.StatusSeeOther)
}
