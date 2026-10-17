package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"finance.home/internal/auth"
)

func (a *App) categoriesPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	cats, err := a.Store.ListCategories("", false)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.Categories = cats
	a.Render.RenderHTTP(w, "categories", d)
}

func (a *App) categoryCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	kind := r.FormValue("kind")
	fixed := r.FormValue("fixed_flexible")
	var parent *int64
	if v := r.FormValue("parent_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil && id > 0 {
			parent = &id
		}
	}
	d := a.base(r, sess)
	cats, _ := a.Store.ListCategories("", false)
	d.Categories = cats
	if name == "" || (kind != "expense" && kind != "income") {
		d.Error = "Name and kind are required."
		a.Render.RenderHTTP(w, "categories", d)
		return
	}
	if _, err := a.Store.CreateCategory(name, kind, fixed, parent); err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "categories", d)
		return
	}
	http.Redirect(w, r, "/categories", http.StatusSeeOther)
}

func (a *App) tagsPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	tags, _ := a.Store.ListTags()
	d.Tags = tags
	a.Render.RenderHTTP(w, "tags", d)
}

func (a *App) tagCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	d := a.base(r, sess)
	tags, _ := a.Store.ListTags()
	d.Tags = tags
	if name == "" {
		d.Error = "Name required"
		a.Render.RenderHTTP(w, "tags", d)
		return
	}
	if _, err := a.Store.CreateTag(name); err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "tags", d)
		return
	}
	http.Redirect(w, r, "/tags", http.StatusSeeOther)
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
