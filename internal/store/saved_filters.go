package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type SavedFilter struct {
	ID        int64
	PersonID  *int64
	Name      string
	QueryJSON string
	Filter    QueryFilter
	CreatedAt string
	UpdatedAt string
}

func (s *Store) ListSavedFilters(personID int64) ([]SavedFilter, error) {
	rows, err := s.DB.Query(`SELECT id, person_id, name, query_json, created_at, updated_at
		FROM saved_filters
		WHERE person_id IS NULL OR person_id = ?
		ORDER BY name COLLATE NOCASE`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedFilter
	for rows.Next() {
		var sf SavedFilter
		var pid sql.NullInt64
		if err := rows.Scan(&sf.ID, &pid, &sf.Name, &sf.QueryJSON, &sf.CreatedAt, &sf.UpdatedAt); err != nil {
			return nil, err
		}
		if pid.Valid {
			v := pid.Int64
			sf.PersonID = &v
		}
		_ = json.Unmarshal([]byte(sf.QueryJSON), &sf.Filter)
		out = append(out, sf)
	}
	return out, rows.Err()
}

func (s *Store) GetSavedFilter(id int64) (*SavedFilter, error) {
	sf := &SavedFilter{}
	var pid sql.NullInt64
	err := s.DB.QueryRow(`SELECT id, person_id, name, query_json, created_at, updated_at FROM saved_filters WHERE id=?`, id).
		Scan(&sf.ID, &pid, &sf.Name, &sf.QueryJSON, &sf.CreatedAt, &sf.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if pid.Valid {
		v := pid.Int64
		sf.PersonID = &v
	}
	_ = json.Unmarshal([]byte(sf.QueryJSON), &sf.Filter)
	return sf, nil
}

func (s *Store) SaveFilter(personID int64, name string, f QueryFilter) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, sql.ErrNoRows
	}
	// Persist unresolved form fields; dates resolve at apply time
	payload, err := json.Marshal(savedFilterPayload(f))
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.DB.Exec(`INSERT INTO saved_filters(person_id, name, query_json, created_at, updated_at) VALUES (?,?,?,?,?)`,
		personID, name, string(payload), now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeleteSavedFilter(id, personID int64) error {
	_, err := s.DB.Exec(`DELETE FROM saved_filters WHERE id=? AND (person_id=? OR person_id IS NULL)`, id, personID)
	return err
}

func savedFilterPayload(f QueryFilter) map[string]any {
	m := map[string]any{
		"preset":       f.DatePreset,
		"from":         f.DateFrom,
		"to":           f.DateTo,
		"rolling_days": f.RollingDays,
		"account_ids":  f.AccountIDs,
		"category_ids": f.CategoryIDs,
		"tag_ids":      f.TagIDs,
		"people":       f.People,
		"payee":        f.Payee,
		"statuses":     f.Statuses,
		"text":         f.Text,
	}
	if f.AmountMin != nil {
		m["amount_min"] = *f.AmountMin
	}
	if f.AmountMax != nil {
		m["amount_max"] = *f.AmountMax
	}
	if f.NeedsReview != nil {
		m["needs_review"] = *f.NeedsReview
	}
	return m
}

// FilterFromSaved reconstructs a QueryFilter from saved JSON (supports both EncodeQuery style and payload).
func FilterFromSaved(sf *SavedFilter) QueryFilter {
	if sf == nil {
		return QueryFilter{}
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(sf.QueryJSON), &raw); err != nil {
		return sf.Filter
	}
	// Prefer structured payload
	f := QueryFilter{
		DatePreset:  strVal(raw["preset"]),
		DateFrom:    strVal(raw["from"]),
		DateTo:      strVal(raw["to"]),
		RollingDays: intVal(raw["rolling_days"]),
		Payee:       strVal(raw["payee"]),
		Text:        strVal(raw["text"]),
		AccountIDs:  int64Slice(raw["account_ids"]),
		CategoryIDs: int64Slice(raw["category_ids"]),
		TagIDs:      int64Slice(raw["tag_ids"]),
		People:      strSlice(raw["people"]),
		Statuses:    strSlice(raw["statuses"]),
	}
	if v, ok := raw["amount_min"]; ok {
		n := int64Val(v)
		f.AmountMin = &n
	}
	if v, ok := raw["amount_max"]; ok {
		n := int64Val(v)
		f.AmountMax = &n
	}
	if v, ok := raw["needs_review"]; ok {
		b := boolVal(v)
		f.NeedsReview = &b
	}
	f.NormalizeAccounts()
	return f
}

func strVal(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return ""
	}
}

func intVal(v any) int {
	return int(int64Val(v))
}

func int64Val(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case json.Number:
		n, _ := t.Int64()
		return n
	default:
		return 0
	}
}

func boolVal(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	default:
		return false
	}
}

func int64Slice(v any) []int64 {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []int64
	for _, x := range arr {
		n := int64Val(x)
		if n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func strSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range arr {
		if s := strVal(x); s != "" {
			out = append(out, s)
		}
	}
	return out
}
