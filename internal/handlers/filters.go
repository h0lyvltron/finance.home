package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"finance.home/internal/auth"
	"finance.home/internal/models"
	"finance.home/internal/store"
)

type filterChip struct {
	Key   string
	Label string
	Href  string // link that removes this chip
}

type filterView struct {
	Filter       store.QueryFilter
	QueryString  string
	Chips        []filterChip
	Saved        []store.SavedFilter
	DateLabel    string
	Presets      []presetOpt
	Grain        string
	GroupBy      string
	ReportPath   string
	AmountMinStr string
	AmountMaxStr string
	ReviewSel    string // "" | "1" | "0"
}

type presetOpt struct {
	Value string
	Label string
}

func (a *App) loadFilter(r *http.Request, sess *auth.Session) (store.QueryFilter, error) {
	_ = r.ParseForm()
	vals := r.Form
	if idStr := strings.TrimSpace(vals.Get("saved")); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err == nil && id > 0 {
			sf, err := a.Store.GetSavedFilter(id)
			if err == nil {
				f := store.FilterFromSaved(sf)
				f.ResolveDates(a.household(), time.Now())
				return f, nil
			}
		}
	}
	f := store.ParseQueryFilter(vals)
	// Only apply default this_month when any filter intent or on report pages —
	// Activity without params stays "all recent" unless date fields present.
	if f.DatePreset == "" && f.DateFrom == "" && f.DateTo == "" && f.RollingDays == 0 {
		if strings.HasPrefix(r.URL.Path, "/explore") || strings.HasPrefix(r.URL.Path, "/reports") {
			f.DatePreset = "this_month"
		}
	}
	f.ResolveDates(a.household(), time.Now())
	return f, nil
}

func (a *App) household() *models.Household {
	h, _ := a.Store.Household()
	return h
}

func (a *App) filterViewFor(r *http.Request, sess *auth.Session, f store.QueryFilter, reportPath, grain, groupBy string) filterView {
	qs := f.EncodeQuery().Encode()
	saved, _ := a.Store.ListSavedFilters(sess.PersonID)
	fv := filterView{
		Filter:      f,
		QueryString: qs,
		Chips:       a.buildChips(r, f, reportPath, grain, groupBy),
		Saved:       saved,
		DateLabel:   dateChipLabel(f),
		Grain:       grain,
		GroupBy:     groupBy,
		ReportPath:  reportPath,
		Presets: []presetOpt{
			{Value: "this_month", Label: "This month"},
			{Value: "last_month", Label: "Last month"},
			{Value: "last_30", Label: "Last 30 days"},
			{Value: "last_90", Label: "Last 90 days"},
			{Value: "ytd", Label: "YTD"},
			{Value: "this_year", Label: "This year"},
			{Value: "rolling", Label: "Rolling"},
			{Value: "custom", Label: "Custom"},
			{Value: "all", Label: "All time"},
		},
	}
	if f.AmountMin != nil {
		fv.AmountMinStr = formatFilterAmount(*f.AmountMin)
	}
	if f.AmountMax != nil {
		fv.AmountMaxStr = formatFilterAmount(*f.AmountMax)
	}
	if f.NeedsReview != nil {
		if *f.NeedsReview {
			fv.ReviewSel = "1"
		} else {
			fv.ReviewSel = "0"
		}
	}
	return fv
}

func dateChipLabel(f store.QueryFilter) string {
	switch f.DatePreset {
	case "this_month":
		return "This month"
	case "last_month":
		return "Last month"
	case "last_30":
		return "Last 30 days"
	case "last_90":
		return "Last 90 days"
	case "ytd":
		return "YTD"
	case "this_year":
		return "This year"
	case "rolling":
		return fmt.Sprintf("Last %d days", f.RollingDays)
	case "all":
		return "All time"
	case "custom":
		if f.DateFrom != "" && f.DateTo != "" {
			return f.DateFrom + " → " + f.DateTo
		}
		return "Custom"
	default:
		if f.DateFrom != "" || f.DateTo != "" {
			return strings.TrimSpace(f.DateFrom + " → " + f.DateTo)
		}
		return "Dates"
	}
}

func (a *App) buildChips(r *http.Request, f store.QueryFilter, basePath, grain, groupBy string) []filterChip {
	if basePath == "" {
		basePath = r.URL.Path
	}
	var chips []filterChip
	add := func(key, label string, clear func(url.Values)) {
		v := f.EncodeQuery()
		clear(v)
		if grain != "" {
			v.Set("grain", grain)
		}
		if groupBy != "" {
			v.Set("group", groupBy)
		}
		q := v.Encode()
		href := basePath
		if q != "" {
			href += "?" + q
		}
		chips = append(chips, filterChip{Key: key, Label: label, Href: href})
	}

	if f.DatePreset != "" && f.DatePreset != "all" || (f.DateFrom != "" || f.DateTo != "") {
		add("date", dateChipLabel(f), func(v url.Values) {
			v.Del("preset")
			v.Del("from")
			v.Del("to")
			v.Del("rolling")
			v.Set("preset", "all")
		})
	}
	accts, _ := a.Store.ListAccounts(false)
	acctName := map[int64]string{}
	for _, ac := range accts {
		acctName[ac.ID] = ac.Name
	}
	for _, id := range f.AccountIDs {
		id := id
		name := acctName[id]
		if name == "" {
			name = fmt.Sprintf("Account %d", id)
		}
		add("account:"+strconv.FormatInt(id, 10), name, func(v url.Values) {
			removeIntFromValues(v, "account", id)
		})
	}
	cats, _ := a.Store.ListCategories("", true)
	catName := map[int64]string{}
	for _, c := range cats {
		label := c.Name
		if c.ParentName != "" {
			label = c.ParentName + " › " + c.Name
		}
		catName[c.ID] = label
	}
	for _, id := range f.CategoryIDs {
		id := id
		name := catName[id]
		if name == "" {
			name = fmt.Sprintf("Category %d", id)
		}
		add("category:"+strconv.FormatInt(id, 10), name, func(v url.Values) {
			removeIntFromValues(v, "category", id)
		})
	}
	tags, _ := a.Store.ListTags()
	tagName := map[int64]string{}
	for _, tg := range tags {
		tagName[tg.ID] = "#" + tg.Name
	}
	for _, id := range f.TagIDs {
		id := id
		name := tagName[id]
		if name == "" {
			name = fmt.Sprintf("Tag %d", id)
		}
		add("tag:"+strconv.FormatInt(id, 10), name, func(v url.Values) {
			removeIntFromValues(v, "tag", id)
		})
	}
	h := a.household()
	nameA, nameB := "A", "B"
	if h != nil {
		// names from people
	}
	people, _ := a.Store.People()
	for _, p := range people {
		if p.Slot == "a" {
			nameA = p.Name
		}
		if p.Slot == "b" {
			nameB = p.Name
		}
	}
	for _, p := range f.People {
		p := p
		label := "Shared"
		if p == "a" {
			label = nameA
		} else if p == "b" {
			label = nameB
		}
		add("person:"+p, label, func(v url.Values) {
			removeStrFromValues(v, "person", p)
		})
	}
	if f.Payee != "" {
		payee := f.Payee
		add("payee", "Payee: "+payee, func(v url.Values) { v.Del("payee") })
	}
	if f.AmountMin != nil || f.AmountMax != nil {
		label := "Amount"
		if f.AmountMin != nil && f.AmountMax != nil {
			label = formatFilterAmount(*f.AmountMin) + "–" + formatFilterAmount(*f.AmountMax)
		} else if f.AmountMin != nil {
			label = "≥ " + formatFilterAmount(*f.AmountMin)
		} else {
			label = "≤ " + formatFilterAmount(*f.AmountMax)
		}
		add("amount", label, func(v url.Values) {
			v.Del("amin")
			v.Del("amax")
		})
	}
	for _, st := range f.Statuses {
		st := st
		add("status:"+st, capitalize(st), func(v url.Values) {
			removeStrFromValues(v, "status", st)
		})
	}
	if f.NeedsReview != nil {
		label := "Needs review"
		if !*f.NeedsReview {
			label = "Reviewed"
		}
		add("review", label, func(v url.Values) { v.Del("review") })
	}
	if f.Text != "" {
		text := f.Text
		add("q", "“"+text+"”", func(v url.Values) { v.Del("q") })
	}
	return chips
}

func removeIntFromValues(v url.Values, key string, id int64) {
	want := strconv.FormatInt(id, 10)
	var keep []string
	for _, x := range v[key] {
		if x != want {
			keep = append(keep, x)
		}
	}
	v.Del(key)
	for _, x := range keep {
		v.Add(key, x)
	}
}

func formatFilterAmount(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := fmt.Sprintf("%d.%02d", cents/100, cents%100)
	if neg {
		return "-" + s
	}
	return s
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func removeStrFromValues(v url.Values, key, val string) {
	var keep []string
	for _, x := range v[key] {
		if !strings.EqualFold(x, val) {
			keep = append(keep, x)
		}
	}
	v.Del(key)
	for _, x := range keep {
		v.Add(key, x)
	}
}

func (a *App) saveFilterPost(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	f := store.ParseQueryFilter(r.Form)
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name required", 400)
		return
	}
	id, err := a.Store.SaveFilter(sess.PersonID, name, f)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	redirect := r.FormValue("redirect")
	if redirect == "" {
		redirect = "/transactions"
	}
	u, err := url.Parse(redirect)
	if err != nil {
		u = &url.URL{Path: "/transactions"}
	}
	q := u.Query()
	q.Set("saved", strconv.FormatInt(id, 10))
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

func (a *App) deleteSavedFilter(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = a.Store.DeleteSavedFilter(id, sess.PersonID)
	redirect := r.FormValue("redirect")
	if redirect == "" {
		redirect = "/transactions"
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}
