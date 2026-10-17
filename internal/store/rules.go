package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"finance.home/internal/models"
	"finance.home/internal/payee"
	"github.com/google/uuid"
)

func (s *Store) ListPayeeRules() ([]models.PayeeRule, error) {
	rows, err := s.DB.Query(`SELECT r.id, r.sort_order, r.name, r.enabled, r.payee_match, r.payee_value,
		r.amount_min, r.amount_max, r.account_id, COALESCE(r.notes_contains,''),
		r.set_category_id, COALESCE(c.name,''), COALESCE(r.set_person,''),
		COALESCE(r.rename_payee,''), COALESCE(r.set_tags,'')
		FROM payee_rules r
		LEFT JOIN categories c ON c.id = r.set_category_id
		ORDER BY r.sort_order ASC, r.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.PayeeRule
	for rows.Next() {
		var r models.PayeeRule
		var en int
		var amin, amax, acct, cat sql.NullInt64
		if err := rows.Scan(&r.ID, &r.SortOrder, &r.Name, &en, &r.PayeeMatch, &r.PayeeValue,
			&amin, &amax, &acct, &r.NotesContains, &cat, &r.SetCategoryName, &r.SetPerson,
			&r.RenamePayee, &r.SetTags); err != nil {
			return nil, err
		}
		r.Enabled = en == 1
		if amin.Valid {
			v := amin.Int64
			r.AmountMin = &v
		}
		if amax.Valid {
			v := amax.Int64
			r.AmountMax = &v
		}
		if acct.Valid {
			v := acct.Int64
			r.AccountID = &v
		}
		if cat.Valid {
			v := cat.Int64
			r.SetCategoryID = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type PayeeRuleInput struct {
	Name          string
	PayeeMatch    string
	PayeeValue    string
	AmountMin     *int64
	AmountMax     *int64
	AccountID     *int64
	NotesContains string
	SetCategoryID *int64
	SetPerson     string
	RenamePayee   string
	SetTags       string
}

func (s *Store) CreatePayeeRule(in PayeeRuleInput) (int64, error) {
	if in.PayeeMatch == "" {
		in.PayeeMatch = "contains"
	}
	var maxOrd sql.NullInt64
	_ = s.DB.QueryRow(`SELECT MAX(sort_order) FROM payee_rules`).Scan(&maxOrd)
	ord := 0
	if maxOrd.Valid {
		ord = int(maxOrd.Int64) + 1
	}
	res, err := s.DB.Exec(`INSERT INTO payee_rules(
		sort_order, name, enabled, payee_match, payee_value, amount_min, amount_max,
		account_id, notes_contains, set_category_id, set_person, rename_payee, set_tags)
		VALUES (?,?,1,?,?,?,?,?,?,?,?,?,?)`,
		ord, in.Name, in.PayeeMatch, in.PayeeValue, in.AmountMin, in.AmountMax,
		in.AccountID, nullStr(in.NotesContains), in.SetCategoryID, nullStr(in.SetPerson),
		nullStr(in.RenamePayee), nullStr(in.SetTags))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeletePayeeRule(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM payee_rules WHERE id=?`, id)
	return err
}

func (s *Store) GetPayeeRule(id int64) (*models.PayeeRule, error) {
	rules, err := s.ListPayeeRules()
	if err != nil {
		return nil, err
	}
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i], nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *Store) UpdatePayeeRule(id int64, in PayeeRuleInput) error {
	if in.PayeeMatch == "" {
		in.PayeeMatch = "contains"
	}
	_, err := s.DB.Exec(`UPDATE payee_rules SET name=?, payee_match=?, payee_value=?, amount_min=?, amount_max=?,
		account_id=?, notes_contains=?, set_category_id=?, set_person=?, rename_payee=?, set_tags=?
		WHERE id=?`,
		in.Name, in.PayeeMatch, in.PayeeValue, in.AmountMin, in.AmountMax, in.AccountID,
		nullStr(in.NotesContains), in.SetCategoryID, nullStr(in.SetPerson), nullStr(in.RenamePayee),
		nullStr(in.SetTags), id)
	return err
}

func (s *Store) SetPayeeRuleEnabled(id int64, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := s.DB.Exec(`UPDATE payee_rules SET enabled=? WHERE id=?`, v, id)
	return err
}

func autoRuleName(value, category string) string {
	return payee.Normalize(value) + " → " + category
}

// SetPayeeRuleCategory changes what the rule files payees as. Rules still
// carrying their generated "MATCH → Category" name are renamed to match.
func (s *Store) SetPayeeRuleCategory(id, categoryID int64) (models.CategoryRef, error) {
	ref, err := s.CategoryRef(categoryID)
	if err != nil {
		return ref, err
	}
	r, err := s.GetPayeeRule(id)
	if err != nil {
		return ref, err
	}
	name := r.Name
	if name == "" || name == autoRuleName(r.PayeeValue, r.SetCategoryName) {
		name = autoRuleName(r.PayeeValue, ref.Name)
	}
	_, err = s.DB.Exec(`UPDATE payee_rules SET set_category_id=?, name=? WHERE id=?`, categoryID, name, id)
	return ref, err
}

// RuleView is a rule with how it applies to the transactions already saved.
type RuleView struct {
	models.PayeeRule
	Matches  int // saved transactions this rule is the first match for
	Misfiled int // of those, how many are not in the rule's category
}

// CustomName reports whether the rule has a name worth showing next to its match text.
func (v RuleView) CustomName() bool {
	n := strings.TrimSpace(v.Name)
	return n != "" && !strings.EqualFold(n, v.PayeeValue) && n != autoRuleName(v.PayeeValue, v.SetCategoryName)
}

// FormJSON holds the rule's editable fields keyed by form input name.
func (v RuleView) FormJSON() string {
	id := func(p *int64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatInt(*p, 10)
	}
	dec := func(p *int64) string {
		if p == nil {
			return ""
		}
		c, sign := *p, ""
		if c < 0 {
			c, sign = -c, "-"
		}
		return fmt.Sprintf("%s%d.%02d", sign, c/100, c%100)
	}
	b, _ := json.Marshal(map[string]string{
		"payee_match":     v.PayeeMatch,
		"payee_value":     v.PayeeValue,
		"set_category_id": id(v.SetCategoryID),
		"set_person":      v.SetPerson,
		"rename_payee":    v.RenamePayee,
		"name":            v.Name,
		"account_id":      id(v.AccountID),
		"notes_contains":  v.NotesContains,
		"amount_min":      dec(v.AmountMin),
		"amount_max":      dec(v.AmountMax),
		"set_tags":        v.SetTags,
	})
	return string(b)
}

type ruleTx struct {
	id      int64
	payee   string
	notes   string
	amount  int64
	account int64
	cat     sql.NullInt64
}

// ruleTransactions lists saved transactions a payee rule could re-file.
func (s *Store) ruleTransactions() ([]ruleTx, error) {
	rows, err := s.DB.Query(`SELECT id, COALESCE(payee_name,''), COALESCE(notes,''), amount, account_id, category_id
		FROM transactions WHERE deleted_at IS NULL AND transfer_id IS NULL AND is_split=0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ruleTx
	for rows.Next() {
		var t ruleTx
		if err := rows.Scan(&t.id, &t.payee, &t.notes, &t.amount, &t.account, &t.cat); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func misfiled(r *models.PayeeRule, t ruleTx) bool {
	return r.SetCategoryID != nil && (!t.cat.Valid || t.cat.Int64 != *r.SetCategoryID)
}

// RuleViews returns every rule with its match counts.
func (s *Store) RuleViews() ([]RuleView, error) {
	rules, err := s.ListPayeeRules()
	if err != nil {
		return nil, err
	}
	txs, err := s.ruleTransactions()
	if err != nil {
		return nil, err
	}
	idx := make(map[int64]int, len(rules))
	out := make([]RuleView, len(rules))
	for i, r := range rules {
		out[i].PayeeRule = r
		idx[r.ID] = i
	}
	for _, t := range txs {
		r := MatchPayeeRule(rules, t.payee, t.notes, t.amount, t.account)
		if r == nil {
			continue
		}
		v := &out[idx[r.ID]]
		v.Matches++
		if misfiled(r, t) {
			v.Misfiled++
		}
	}
	return out, nil
}

// MisfiledByRule returns saved transactions the rule matches first that sit
// in some other category (or none).
func (s *Store) MisfiledByRule(id int64) ([]int64, *models.PayeeRule, error) {
	rules, err := s.ListPayeeRules()
	if err != nil {
		return nil, nil, err
	}
	var rule *models.PayeeRule
	for i := range rules {
		if rules[i].ID == id {
			rule = &rules[i]
		}
	}
	if rule == nil {
		return nil, nil, sql.ErrNoRows
	}
	txs, err := s.ruleTransactions()
	if err != nil {
		return nil, nil, err
	}
	var ids []int64
	for _, t := range txs {
		if r := MatchPayeeRule(rules, t.payee, t.notes, t.amount, t.account); r != nil && r.ID == id && misfiled(r, t) {
			ids = append(ids, t.id)
		}
	}
	return ids, rule, nil
}

// DeletePayeeRuleUndoable deletes a rule and returns an undo id that restores it.
func (s *Store) DeletePayeeRuleUndoable(id, personID int64) (string, error) {
	r, err := s.GetPayeeRule(id)
	if err != nil {
		return "", err
	}
	payload, _ := json.Marshal(r)
	undoID := uuid.NewString()
	exp := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339Nano)
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM payee_rules WHERE id=?`, id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`INSERT INTO undo_ops(id, kind, payload, expires_at, created_by) VALUES (?,?,?,?,?)`,
		undoID, "rule_delete", string(payload), exp, personID); err != nil {
		return "", err
	}
	return undoID, tx.Commit()
}

func undoRuleDelete(tx *sql.Tx, payload string) error {
	var r models.PayeeRule
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		return err
	}
	en := 0
	if r.Enabled {
		en = 1
	}
	_, err := tx.Exec(`INSERT INTO payee_rules(id, sort_order, name, enabled, payee_match, payee_value,
		amount_min, amount_max, account_id, notes_contains, set_category_id, set_person, rename_payee, set_tags)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.SortOrder, r.Name, en, r.PayeeMatch, r.PayeeValue, r.AmountMin, r.AmountMax, r.AccountID,
		nullStr(r.NotesContains), r.SetCategoryID, nullStr(r.SetPerson), nullStr(r.RenamePayee), nullStr(r.SetTags))
	return err
}

func (s *Store) MovePayeeRule(id int64, dir string) error {
	rules, err := s.ListPayeeRules()
	if err != nil {
		return err
	}
	idx := -1
	for i, r := range rules {
		if r.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return sql.ErrNoRows
	}
	swap := idx
	if dir == "up" && idx > 0 {
		swap = idx - 1
	} else if dir == "down" && idx < len(rules)-1 {
		swap = idx + 1
	} else {
		return nil
	}
	a, b := rules[idx], rules[swap]
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE payee_rules SET sort_order=? WHERE id=?`, b.SortOrder, a.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE payee_rules SET sort_order=? WHERE id=?`, a.SortOrder, b.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// MatchPayeeRule returns the first matching enabled rule (ordered).
func MatchPayeeRule(rules []models.PayeeRule, payeeName, notes string, amount, accountID int64) *models.PayeeRule {
	payeeN := payee.Normalize(payeeName)
	notesL := strings.ToLower(notes)
	for i := range rules {
		r := &rules[i]
		if !r.Enabled {
			continue
		}
		if r.AccountID != nil && *r.AccountID != accountID {
			continue
		}
		if r.AmountMin != nil && amount < *r.AmountMin {
			continue
		}
		if r.AmountMax != nil && amount > *r.AmountMax {
			continue
		}
		if r.NotesContains != "" && !strings.Contains(notesL, strings.ToLower(r.NotesContains)) {
			continue
		}
		val := payee.Normalize(r.PayeeValue)
		ok := false
		switch r.PayeeMatch {
		case "equals":
			ok = payeeN == val
		case "starts_with":
			ok = strings.HasPrefix(payeeN, val)
		default: // contains
			ok = strings.Contains(payeeN, val)
		}
		if ok {
			return r
		}
	}
	return nil
}
