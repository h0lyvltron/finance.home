package store

import (
	"database/sql"

	"finance.home/internal/models"
)

func (s *Store) NeedsReviewCount() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(1) FROM transactions WHERE deleted_at IS NULL AND needs_review=1`).Scan(&n)
	return n, err
}

func (s *Store) ListNeedsReview(limit int) ([]models.Transaction, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.Query(`SELECT t.id, t.account_id, a.name, t.payee_id, COALESCE(t.payee_name,''), COALESCE(t.notes,''),
		t.amount, t.transaction_date, t.person, t.status, t.needs_review, t.category_id,
		COALESCE(c.name,''), t.is_split, t.transfer_id
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.deleted_at IS NULL AND t.needs_review=1
		ORDER BY t.transaction_date DESC, t.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Transaction
	for rows.Next() {
		var t models.Transaction
		var payeeID, catID, xferID sql.NullInt64
		var needs, split int
		if err := rows.Scan(&t.ID, &t.AccountID, &t.AccountName, &payeeID, &t.PayeeName, &t.Notes,
			&t.Amount, &t.TransactionDate, &t.Person, &t.Status, &needs, &catID, &t.CategoryName, &split, &xferID); err != nil {
			return nil, err
		}
		if payeeID.Valid {
			v := payeeID.Int64
			t.PayeeID = &v
		}
		if catID.Valid {
			v := catID.Int64
			t.CategoryID = &v
		}
		if xferID.Valid {
			v := xferID.Int64
			t.TransferID = &v
		}
		t.NeedsReview = needs == 1
		t.IsSplit = split == 1
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]int64, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	tags, err := s.TagsForTransactions(ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tags = tags[out[i].ID]
	}
	return out, nil
}

func (s *Store) ConfirmReview(id int64, categoryID *int64, person, payee, notes string) error {
	if person == "" {
		person = "shared"
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`UPDATE transactions SET needs_review=0, person=?,
		payee_name=COALESCE(NULLIF(?,''), payee_name),
		notes=COALESCE(NULLIF(?,''), notes),
		category_id=COALESCE(?, category_id),
		updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id=? AND deleted_at IS NULL`, person, payee, notes, categoryID, id)
	if err != nil {
		return err
	}
	if categoryID != nil {
		var isSplit int
		_ = tx.QueryRow(`SELECT is_split FROM transactions WHERE id=?`, id).Scan(&isSplit)
		if isSplit == 0 {
			var amt int64
			_ = tx.QueryRow(`SELECT amount FROM transactions WHERE id=?`, id).Scan(&amt)
			_, _ = tx.Exec(`DELETE FROM transaction_splits WHERE transaction_id=?`, id)
			_, err = tx.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, display_order)
				VALUES (?,?,?,?,0)`, id, *categoryID, person, amt)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) ConfirmReviewAll(ids []int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		_, err = tx.Exec(`UPDATE transactions SET needs_review=0,
			updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND deleted_at IS NULL`, id)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
