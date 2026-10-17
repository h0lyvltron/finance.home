package handlers

import (
	"net/http"
	"strings"

	"finance.home/internal/auth"
	"finance.home/internal/store"
)

func (a *App) setupGet(w http.ResponseWriter, r *http.Request) {
	needs, err := needsSetup(a.DB)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !needs {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.Render.RenderHTTP(w, "setup", pageData{Currency: "USD", Timezone: "America/New_York"})
}

func (a *App) setupPost(w http.ResponseWriter, r *http.Request) {
	needs, err := needsSetup(a.DB)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !needs {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	in := store.SetupInput{
		HouseholdName: strings.TrimSpace(r.FormValue("household_name")),
		Currency:      r.FormValue("currency"),
		Timezone:      strings.TrimSpace(r.FormValue("timezone")),
		PersonAName:   strings.TrimSpace(r.FormValue("person_a_name")),
		PersonAPass:   r.FormValue("person_a_pass"),
		PersonBName:   strings.TrimSpace(r.FormValue("person_b_name")),
		PersonBPass:   r.FormValue("person_b_pass"),
	}
	d := pageData{
		HouseholdName: in.HouseholdName,
		Currency:      in.Currency,
		Timezone:      in.Timezone,
		PersonAName:   in.PersonAName,
		PersonBName:   in.PersonBName,
	}
	if in.HouseholdName == "" || in.PersonAName == "" || in.PersonBName == "" {
		d.Error = "Household and both names are required."
		a.Render.RenderHTTP(w, "setup", d)
		return
	}
	if len(in.PersonAPass) < 8 || len(in.PersonBPass) < 8 {
		d.Error = "Passwords must be at least 8 characters."
		a.Render.RenderHTTP(w, "setup", d)
		return
	}
	if strings.EqualFold(in.PersonAName, in.PersonBName) {
		d.Error = "Person names must be different so you can sign in."
		a.Render.RenderHTTP(w, "setup", d)
		return
	}
	if err := a.Store.CompleteSetup(in); err != nil {
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "setup", d)
		return
	}
	// Auto-sign in as person A
	p, err := a.Store.PersonByName(in.PersonAName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	sess, err := auth.CreateSession(a.DB, p.ID)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	auth.SetSessionCookie(w, sess, a.Cfg.SecureCookie)
	http.Redirect(w, r, "/accounts/new", http.StatusSeeOther)
}

func (a *App) loginGet(w http.ResponseWriter, r *http.Request) {
	needs, err := needsSetup(a.DB)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if needs {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	h, _ := a.Store.Household()
	people, _ := a.Store.People()
	name := ""
	if h != nil {
		name = h.Name
	}
	a.Render.RenderHTTP(w, "login", pageData{HouseholdName: name, People: people})
}

func (a *App) loginPost(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	pass := r.FormValue("password")
	h, _ := a.Store.Household()
	people, _ := a.Store.People()
	hh := ""
	if h != nil {
		hh = h.Name
	}
	p, err := a.Store.PersonByName(name)
	if err != nil || !auth.CheckPassword(p.PasswordHash, pass) {
		a.Render.RenderHTTP(w, "login", pageData{
			HouseholdName: hh,
			People:        people,
			Name:          name,
			Error:         "Name or password is incorrect.",
		})
		return
	}
	sess, err := auth.CreateSession(a.DB, p.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	auth.SetSessionCookie(w, sess, a.Cfg.SecureCookie)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		_ = auth.DestroySession(a.DB, c.Value)
	}
	auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
