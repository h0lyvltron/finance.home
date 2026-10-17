package store

import (
	"database/sql"
	"strings"

	"finance.home/internal/models"
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
func MatchPayeeRule(rules []models.PayeeRule, payee, notes string, amount, accountID int64) *models.PayeeRule {
	payeeL := strings.ToLower(strings.TrimSpace(payee))
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
		val := strings.ToLower(strings.TrimSpace(r.PayeeValue))
		ok := false
		switch r.PayeeMatch {
		case "equals":
			ok = payeeL == val
		case "starts_with":
			ok = strings.HasPrefix(payeeL, val)
		default: // contains
			ok = strings.Contains(payeeL, val)
		}
		if ok {
			return r
		}
	}
	return nil
}
