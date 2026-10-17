package store

import (
	"database/sql"
	"fmt"
	"time"

	"finance.home/internal/models"
)

const SoftDeleteRetentionDays = 30

// SoftDeletedTx is a recycle-bin row.
type SoftDeletedTx struct {
	models.Transaction
	DeletedAt string
	DaysLeft  int
}

// ListSoftDeleted returns soft-deleted transactions still within retention.
func (s *Store) ListSoftDeleted(limit int) ([]SoftDeletedTx, error) {
	if limit <= 0 {
		limit = 100
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -SoftDeleteRetentionDays).Format(time.RFC3339Nano)
	rows, err := s.DB.Query(`
		SELECT t.id, t.account_id, a.name, t.payee_id, COALESCE(t.payee_name,''), COALESCE(t.notes,''),
			t.amount, t.transaction_date, t.person, t.status, t.needs_review,
			t.category_id, COALESCE(c.name,''), t.is_split, t.transfer_id, COALESCE(t.deleted_at,'')
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.deleted_at IS NOT NULL AND t.deleted_at >= ?
		ORDER BY t.deleted_at DESC
		LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SoftDeletedTx
	now := time.Now().UTC()
	for rows.Next() {
		var tx SoftDeletedTx
		var payeeID, catID, xferID sql.NullInt64
		var needs, isSplit int
		if err := rows.Scan(&tx.ID, &tx.AccountID, &tx.AccountName, &payeeID, &tx.PayeeName, &tx.Notes,
			&tx.Amount, &tx.TransactionDate, &tx.Person, &tx.Status, &needs,
			&catID, &tx.CategoryName, &isSplit, &xferID, &tx.DeletedAt); err != nil {
			return nil, err
		}
		tx.NeedsReview = needs != 0
		tx.IsSplit = isSplit != 0
		if payeeID.Valid {
			v := payeeID.Int64
			tx.PayeeID = &v
		}
		if catID.Valid {
			v := catID.Int64
			tx.CategoryID = &v
		}
		if xferID.Valid {
			v := xferID.Int64
			tx.TransferID = &v
		}
		tx.DaysLeft = SoftDeleteRetentionDays
		if del, err := time.Parse(time.RFC3339Nano, tx.DeletedAt); err == nil {
			exp := del.AddDate(0, 0, SoftDeleteRetentionDays)
			d := int(exp.Sub(now).Hours() / 24)
			if d < 0 {
				d = 0
			}
			tx.DaysLeft = d
		}
		out = append(out, tx)
	}
	return out, rows.Err()
}

// RestoreTransaction clears soft-delete (and paired transfer leg if any).
func (s *Store) RestoreTransaction(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var transferID sql.NullInt64
	err = tx.QueryRow(`SELECT transfer_id FROM transactions WHERE id=? AND deleted_at IS NOT NULL`, id).Scan(&transferID)
	if err != nil {
		return fmt.Errorf("not found in recycle bin")
	}
	ids := []int64{id}
	if transferID.Valid {
		var fromID, toID int64
		err = tx.QueryRow(`SELECT from_tx_id, to_tx_id FROM transfers WHERE id=?`, transferID.Int64).Scan(&fromID, &toID)
		if err == nil {
			ids = []int64{fromID, toID}
		}
	}
	for _, tid := range ids {
		_, err = tx.Exec(`UPDATE transactions SET deleted_at=NULL, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, tid)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PurgeExpiredSoftDeletes is a no-op for now: retention is enforced by ListSoftDeleted.
// Hard-delete would need careful transfer / import FK handling.
func (s *Store) PurgeExpiredSoftDeletes() (int64, error) {
	return 0, nil
}
