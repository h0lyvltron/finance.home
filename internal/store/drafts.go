package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"finance.home/internal/models"
)

// UpsertDraft saves or updates a durable quick-add draft.
// When clientKey is set, upserts by (person_id, client_key) for offline sync idempotency.
func (s *Store) UpsertDraft(d models.DraftTransaction) (int64, error) {
	if d.PersonID == 0 {
		return 0, fmt.Errorf("person required")
	}
	if d.Person == "" {
		d.Person = "shared"
	}
	if d.Kind == "" {
		d.Kind = "expense"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	d.ClientKey = strings.TrimSpace(d.ClientKey)

	if d.ClientKey != "" {
		res, err := s.DB.Exec(`
			INSERT INTO draft_transactions(
				person_id, account_id, category_id, payee_name, notes, amount,
				transaction_date, person, kind, client_key, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(person_id, client_key) DO UPDATE SET
				account_id=excluded.account_id,
				category_id=excluded.category_id,
				payee_name=excluded.payee_name,
				notes=excluded.notes,
				amount=excluded.amount,
				transaction_date=excluded.transaction_date,
				person=excluded.person,
				kind=excluded.kind,
				updated_at=excluded.updated_at`,
			d.PersonID, d.AccountID, d.CategoryID, nullIfEmpty(d.PayeeName), nullIfEmpty(d.Notes),
			d.Amount, nullIfEmpty(d.TransactionDate), d.Person, d.Kind, d.ClientKey, now, now)
		if err != nil {
			return 0, err
		}
		id, _ := res.LastInsertId()
		if id == 0 {
			err = s.DB.QueryRow(`SELECT id FROM draft_transactions WHERE person_id=? AND client_key=?`,
				d.PersonID, d.ClientKey).Scan(&id)
			if err != nil {
				return 0, err
			}
		}
		return id, nil
	}

	res, err := s.DB.Exec(`
		INSERT INTO draft_transactions(
			person_id, account_id, category_id, payee_name, notes, amount,
			transaction_date, person, kind, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		d.PersonID, d.AccountID, d.CategoryID, nullIfEmpty(d.PayeeName), nullIfEmpty(d.Notes),
		d.Amount, nullIfEmpty(d.TransactionDate), d.Person, d.Kind, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// ListDrafts returns drafts for a person (newest first).
func (s *Store) ListDrafts(personID int64) ([]models.DraftTransaction, error) {
	rows, err := s.DB.Query(`
		SELECT d.id, d.person_id, d.account_id, d.category_id, COALESCE(d.payee_name,''), COALESCE(d.notes,''),
			d.amount, COALESCE(d.transaction_date,''), d.person, d.kind, COALESCE(d.client_key,''),
			d.created_at, d.updated_at, COALESCE(a.name,''), COALESCE(c.name,'')
		FROM draft_transactions d
		LEFT JOIN accounts a ON a.id = d.account_id
		LEFT JOIN categories c ON c.id = d.category_id
		WHERE d.person_id=?
		ORDER BY d.updated_at DESC
		LIMIT 50`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.DraftTransaction
	for rows.Next() {
		var d models.DraftTransaction
		var acctID, catID sql.NullInt64
		if err := rows.Scan(&d.ID, &d.PersonID, &acctID, &catID, &d.PayeeName, &d.Notes,
			&d.Amount, &d.TransactionDate, &d.Person, &d.Kind, &d.ClientKey,
			&d.CreatedAt, &d.UpdatedAt, &d.AccountName, &d.CategoryName); err != nil {
			return nil, err
		}
		if acctID.Valid {
			v := acctID.Int64
			d.AccountID = &v
		}
		if catID.Valid {
			v := catID.Int64
			d.CategoryID = &v
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDraft loads one draft owned by personID.
func (s *Store) GetDraft(id, personID int64) (*models.DraftTransaction, error) {
	d := &models.DraftTransaction{}
	var acctID, catID sql.NullInt64
	err := s.DB.QueryRow(`
		SELECT d.id, d.person_id, d.account_id, d.category_id, COALESCE(d.payee_name,''), COALESCE(d.notes,''),
			d.amount, COALESCE(d.transaction_date,''), d.person, d.kind, COALESCE(d.client_key,''),
			d.created_at, d.updated_at, COALESCE(a.name,''), COALESCE(c.name,'')
		FROM draft_transactions d
		LEFT JOIN accounts a ON a.id = d.account_id
		LEFT JOIN categories c ON c.id = d.category_id
		WHERE d.id=? AND d.person_id=?`, id, personID).
		Scan(&d.ID, &d.PersonID, &acctID, &catID, &d.PayeeName, &d.Notes,
			&d.Amount, &d.TransactionDate, &d.Person, &d.Kind, &d.ClientKey,
			&d.CreatedAt, &d.UpdatedAt, &d.AccountName, &d.CategoryName)
	if err != nil {
		return nil, err
	}
	if acctID.Valid {
		v := acctID.Int64
		d.AccountID = &v
	}
	if catID.Valid {
		v := catID.Int64
		d.CategoryID = &v
	}
	return d, nil
}

// DeleteDraft removes a draft owned by personID.
func (s *Store) DeleteDraft(id, personID int64) error {
	res, err := s.DB.Exec(`DELETE FROM draft_transactions WHERE id=? AND person_id=?`, id, personID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// PromoteDraft creates a real transaction from a draft and deletes the draft.
func (s *Store) PromoteDraft(id, personID int64) (int64, error) {
	d, err := s.GetDraft(id, personID)
	if err != nil {
		return 0, err
	}
	if d.AccountID == nil || *d.AccountID == 0 {
		return 0, fmt.Errorf("draft needs an account")
	}
	if d.CategoryID == nil || *d.CategoryID == 0 {
		return 0, fmt.Errorf("draft needs a category")
	}
	if d.Amount == 0 {
		return 0, fmt.Errorf("draft needs an amount")
	}
	date := d.TransactionDate
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	txID, err := s.CreateTransaction(TxInput{
		AccountID:       *d.AccountID,
		PayeeName:       d.PayeeName,
		Notes:           d.Notes,
		Amount:          d.Amount,
		TransactionDate: date,
		Person:          d.Person,
		Status:          "cleared",
		CategoryID:      d.CategoryID,
		CreatedBy:       personID,
	})
	if err != nil {
		return 0, err
	}
	_ = s.DeleteDraft(id, personID)
	return txID, nil
}
