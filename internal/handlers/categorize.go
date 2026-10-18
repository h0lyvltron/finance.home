package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"finance.home/internal/auth"
	"finance.home/internal/models"
	"finance.home/internal/payee"
)

type catRefJSON struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}

type categorizeResult struct {
	IDs         []int64    `json:"ids"`
	Category    catRefJSON `json:"category"`
	UndoID      string     `json:"undoId,omitempty"`
	Payee       string     `json:"payee,omitempty"`
	Remembered  string     `json:"remembered,omitempty"`
	RuleID      int64      `json:"ruleId,omitempty"`
	Message     string     `json:"message"`
	ReviewCount int        `json:"reviewCount"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseIDs(s string) []int64 {
	var out []int64
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' }) {
		if id, err := strconv.ParseInt(f, 10, 64); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
}

func unionIDs(a, b []int64) []int64 {
	seen := make(map[int64]bool, len(a)+len(b))
	out := make([]int64, 0, len(a)+len(b))
	for _, id := range append(append([]int64{}, a...), b...) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func refsJSON(refs []models.CategoryRef) []catRefJSON {
	out := make([]catRefJSON, len(refs))
	for i, r := range refs {
		out[i] = catRefJSON{ID: r.ID, Name: r.Name, Reason: r.Reason}
	}
	return out
}

func (a *App) categorizeOptions(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	cats, err := a.Store.PickerCategories()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	d := a.base(r, sess)
	writeJSON(w, 200, map[string]any{
		"categories": cats,
		"people": []map[string]string{
			{"slot": "shared", "name": "Shared"},
			{"slot": "a", "name": d.NameA},
			{"slot": "b", "name": d.NameB},
		},
	})
}

// categorizeSimilar counts other open items matching the text and suggests
// categories for the payee being edited.
func (a *App) categorizeSimilar(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	q := r.URL.Query()
	match := q.Get("match")
	exclude := map[int64]bool{}
	for _, id := range parseIDs(q.Get("exclude")) {
		exclude[id] = true
	}
	count := func(ids []int64) int {
		n := 0
		for _, id := range ids {
			if !exclude[id] {
				n++
			}
		}
		return n
	}
	resp := map[string]any{}
	if q.Get("target") == "staging" {
		batchID, _ := strconv.ParseInt(q.Get("batch"), 10, 64)
		ids, err := a.Store.SimilarStagingRows(batchID, match)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		resp["count"] = count(ids)
	} else {
		open, err := a.Store.SimilarOpenTransactions(match)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		filed, err := a.Store.SimilarFiledTransactions(match)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		resp["count"] = count(open)
		resp["filed"] = count(filed)
	}
	if p := q.Get("payee"); p != "" {
		if sg, err := a.Store.NewSuggester(); err == nil {
			amount, _ := strconv.ParseInt(q.Get("amount"), 10, 64)
			account, _ := strconv.ParseInt(q.Get("account"), 10, 64)
			resp["suggest"] = refsJSON(sg.For(p, amount, account))
			if rule := sg.Rule(p, amount, account); rule != nil {
				resp["rule"] = map[string]any{
					"id":       rule.ID,
					"value":    payee.Normalize(rule.PayeeValue),
					"match":    rule.PayeeMatch,
					"category": rule.SetCategoryName,
				}
			}
		}
	}
	writeJSON(w, 200, resp)
}

func (a *App) categorizePost(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	_ = r.ParseForm()
	catID, _ := strconv.ParseInt(r.FormValue("category_id"), 10, 64)
	ref, err := a.Store.CategoryRef(catID)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "Pick a category."})
		return
	}
	ids := parseIDs(r.FormValue("ids"))
	match := strings.TrimSpace(r.FormValue("match"))
	similar := r.FormValue("similar") == "1" && match != ""
	person := r.FormValue("person")
	switch person {
	case "shared", "a", "b":
	default:
		person = ""
	}

	res := categorizeResult{Category: catRefJSON{ID: ref.ID, Name: ref.Name}}
	if r.FormValue("target") == "staging" {
		batchID, _ := strconv.ParseInt(r.FormValue("batch"), 10, 64)
		if similar {
			more, err := a.Store.SimilarStagingRows(batchID, match)
			if err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			ids = unionIDs(ids, more)
		}
		res.IDs, err = a.Store.CategorizeStagingRows(batchID, ids, catID, person)
	} else {
		if similar {
			more, err := a.Store.SimilarOpenTransactions(match)
			if err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			ids = unionIDs(ids, more)
		}
		if r.FormValue("similar_filed") == "1" && match != "" {
			more, err := a.Store.SimilarFiledTransactions(match)
			if err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			ids = unionIDs(ids, more)
		}
		res.IDs, res.UndoID, err = a.Store.CategorizeTransactions(ids, catID, person, sess.PersonID)
		if err == nil && len(ids) == 1 {
			if name := strings.TrimSpace(r.FormValue("payee")); name != "" {
				err = a.Store.RenameTransactionPayee(ids[0], name)
				res.Payee = name
			}
		}
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if r.FormValue("remember") == "1" && match != "" {
		ch, err := a.Store.RememberPayeeCategory(match, catID)
		if err == nil {
			err = a.Store.AttachRuleUndo(res.UndoID, ch)
		}
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		res.Remembered = payee.Normalize(match)
		if ch != nil {
			res.RuleID = ch.ID
		}
	}

	n := len(res.IDs)
	switch {
	case n == 0 && res.Remembered != "":
		res.Message = fmt.Sprintf("Future “%s” → %s", res.Remembered, ref.Name)
	case n == 0:
		res.Message = "Already in " + ref.Name
	case n == 1:
		res.Message = "Moved to " + ref.Name
	default:
		res.Message = fmt.Sprintf("%d transactions → %s", n, ref.Name)
	}
	if n > 0 && res.Remembered != "" {
		res.Message += " · remembered for future imports"
	}
	res.ReviewCount, _ = a.Store.NeedsReviewCount()
	writeJSON(w, 200, res)
}
