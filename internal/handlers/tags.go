package handlers

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"finance.home/internal/auth"
	"finance.home/internal/models"
	"finance.home/internal/store"
)

type tagJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type tagOptionJSON struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
	Have  int    `json:"have"`
}

// txSummaryView totals the Activity filter for the tag summary and bulk tagging.
type txSummaryView struct {
	store.TxSummary
	Shown         int
	Query         string
	Tags          []models.Tag
	ByCategoryURL template.URL
}

func (a *App) txSummary(f store.QueryFilter, shown int, query string) *txSummaryView {
	sum, err := a.Store.SummarizeTransactions(f)
	if err != nil {
		return nil
	}
	v := &txSummaryView{TxSummary: sum, Shown: shown, Query: query}
	if len(f.TagIDs) > 0 {
		all, _ := a.Store.ListAllTags()
		for _, id := range f.TagIDs {
			for _, t := range all {
				if t.ID == id {
					v.Tags = append(v.Tags, t)
				}
			}
		}
		q := f.EncodeQuery()
		q.Set("group", "category")
		v.ByCategoryURL = template.URL("/explore?" + q.Encode())
	}
	return v
}

// activityFilter parses an Activity query string the way the Activity page
// does: no dates means all time.
func (a *App) activityFilter(raw string) (store.QueryFilter, error) {
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return store.QueryFilter{}, err
	}
	f := store.ParseQueryFilter(vals)
	if f.DatePreset == "" && f.DateFrom == "" && f.DateTo == "" && f.RollingDays == 0 {
		f.DatePreset = "all"
	}
	f.ResolveDates(a.household(), time.Now())
	return f, nil
}

// tagTargets resolves which transactions a tag request is about: the listed
// ids, or every transaction matching the Activity filter when all=1.
func (a *App) tagTargets(get func(string) string) ([]int64, error) {
	if get("all") == "1" {
		f, err := a.activityFilter(get("filter"))
		if err != nil {
			return nil, err
		}
		return a.Store.FilteredTransactionIDs(f)
	}
	return parseIDs(get("ids")), nil
}

func (a *App) tagSimilar(get func(string) string, targets []int64) ([]int64, error) {
	match := strings.TrimSpace(get("match"))
	if get("all") == "1" || match == "" || get("filter") == "" {
		return nil, nil
	}
	f, err := a.activityFilter(get("filter"))
	if err != nil {
		return nil, err
	}
	ids, err := a.Store.SimilarInFilter(f, match)
	if err != nil {
		return nil, err
	}
	skip := map[int64]bool{}
	for _, id := range targets {
		skip[id] = true
	}
	var out []int64
	for _, id := range ids {
		if !skip[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func (a *App) tagOptions(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	q := r.URL.Query()
	targets, err := a.tagTargets(q.Get)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	similar, err := a.tagSimilar(q.Get, targets)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	tags, err := a.Store.TagOptions()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	have, err := a.Store.TagCounts(targets)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	opts := make([]tagOptionJSON, 0, len(tags))
	listed := map[int64]bool{}
	for _, t := range tags {
		listed[t.ID] = true
		opts = append(opts, tagOptionJSON{ID: t.ID, Name: t.Name, Count: t.Count, Have: have[t.ID]})
	}
	// Archived tags still on these transactions stay removable.
	if len(have) > len(listed) {
		all, _ := a.Store.ListAllTags()
		for _, t := range all {
			if have[t.ID] > 0 && !listed[t.ID] {
				opts = append(opts, tagOptionJSON{ID: t.ID, Name: t.Name, Have: have[t.ID]})
			}
		}
	}
	writeJSON(w, 200, map[string]any{"tags": opts, "total": len(targets), "similar": len(similar)})
}

func (a *App) tagApply(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	targets, err := a.tagTargets(r.FormValue)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if r.FormValue("similar") == "1" {
		more, err := a.tagSimilar(r.FormValue, targets)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		targets = append(targets, more...)
	}
	if len(targets) == 0 {
		writeJSON(w, 400, map[string]string{"error": "Pick some transactions first."})
		return
	}
	add := parseIDs(r.FormValue("add"))
	remove := parseIDs(r.FormValue("remove"))
	var names []string
	for _, raw := range r.Form["create"] {
		t, _, err := a.Store.EnsureActiveTag(raw)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		add = append(add, t.ID)
	}
	if len(add) == 0 && len(remove) == 0 {
		writeJSON(w, 400, map[string]string{"error": "Pick a tag."})
		return
	}
	all, _ := a.Store.ListAllTags()
	nameOf := map[int64]string{}
	for _, t := range all {
		nameOf[t.ID] = "#" + t.Name
	}
	for _, id := range add {
		names = append(names, nameOf[id])
	}

	changed, undoID, err := a.Store.TagTransactions(targets, add, remove, sess.PersonID)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	rows, err := a.Store.TagsForTransactions(changed)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	out := make(map[string][]tagJSON, len(rows))
	for id, tags := range rows {
		list := make([]tagJSON, len(tags))
		for i, t := range tags {
			list[i] = tagJSON{ID: t.ID, Name: t.Name}
		}
		out[strconv.FormatInt(id, 10)] = list
	}

	n := len(changed)
	var msg string
	switch {
	case n == 0 && len(add) > 0 && len(remove) == 0:
		msg = "Already tagged " + strings.Join(names, " ")
	case n == 0:
		msg = "No changes"
	case len(add) > 0 && len(remove) == 0:
		msg = "Tagged " + plural(n, "transaction") + " " + strings.Join(names, " ")
	case len(add) == 0 && len(remove) == 1:
		msg = fmt.Sprintf("Removed %s from %s", nameOf[remove[0]], plural(n, "transaction"))
	default:
		msg = "Updated tags on " + plural(n, "transaction")
	}
	writeJSON(w, 200, map[string]any{"ids": changed, "rows": out, "undoId": undoID, "message": msg})
}

func (a *App) renderTags(w http.ResponseWriter, r *http.Request, sess *auth.Session, errMsg string) {
	d := a.base(r, sess)
	d.Nav = "more"
	d.Error = errMsg
	views, err := a.Store.ListTagViews()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, v := range views {
		if v.Archived {
			d.ArchivedTagViews = append(d.ArchivedTagViews, v)
		} else {
			d.TagViews = append(d.TagViews, v)
		}
	}
	d.RuleQuery = r.URL.Query().Get("q")
	a.Render.RenderHTTP(w, "tags", d)
}

func tagAnchor(id int64) string {
	return "/tags#tag-" + strconv.FormatInt(id, 10)
}

func (a *App) tagsPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	a.renderTags(w, r, sess, "")
}

func (a *App) tagCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	id, err := a.Store.CreateTag(r.FormValue("name"))
	if err != nil {
		a.renderTags(w, r, sess, err.Error())
		return
	}
	http.Redirect(w, r, tagAnchor(id), http.StatusSeeOther)
}

func (a *App) tagRename(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	if err := a.Store.RenameTag(id, r.FormValue("name")); err != nil {
		a.renderTags(w, r, sess, err.Error())
		return
	}
	http.Redirect(w, r, tagAnchor(id), http.StatusSeeOther)
}

func (a *App) tagMerge(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	dst, _ := strconv.ParseInt(r.FormValue("tag_id"), 10, 64)
	res, err := a.Store.MergeTag(id, dst, sess.PersonID)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	msg := fmt.Sprintf("Merged #%s into #%s", res.From, res.Into)
	if res.Transactions > 0 {
		msg += " · moved " + plural(res.Transactions, "transaction")
	}
	writeJSON(w, 200, map[string]any{"undoId": res.UndoID, "message": msg, "id": dst})
}

func (a *App) tagArchive(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	t, err := a.Store.GetTag(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "Tag not found."})
		return
	}
	undoID, err := a.Store.ArchiveTag(id, sess.PersonID)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"undoId": undoID, "message": "Archived #" + t.Name})
}

func (a *App) tagRestore(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := a.Store.RestoreTag(id); err != nil {
		a.renderTags(w, r, sess, err.Error())
		return
	}
	http.Redirect(w, r, tagAnchor(id), http.StatusSeeOther)
}

func (a *App) tagDelete(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	t, err := a.Store.GetTag(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "Tag not found."})
		return
	}
	undoID, n, err := a.Store.DeleteTag(id, sess.PersonID)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	msg := "Deleted #" + t.Name
	if n > 0 {
		msg += " from " + plural(n, "transaction")
	}
	writeJSON(w, 200, map[string]any{"undoId": undoID, "message": msg})
}
