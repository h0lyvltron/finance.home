package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"finance.home/internal/auth"
	"finance.home/internal/models"
	"github.com/google/uuid"
)

type TxInput struct {
	AccountID       int64
	PayeeName       string
	Notes           string
	Amount          int64
	TransactionDate string
	Person          string
	Status          string
	CategoryID      *int64
	TagNames        []string
	Splits          []SplitInput
	CreatedBy       int64
}

type SplitInput struct {
	CategoryID int64
	Person     string
	Amount     int64
	Notes      string
}

type TransferInput struct {
	FromAccountID   int64
	ToAccountID     int64
	Amount          int64 // positive magnitude moved from → to
	TransactionDate string
	Notes           string
	Person          string
	CreatedBy       int64
}

func (s *Store) CreateTransaction(in TxInput) (int64, error) {
	if in.Person == "" {
		in.Person = "shared"
	}
	if in.Status == "" {
		in.Status = "cleared"
	}
	if in.TransactionDate == "" {
		return 0, fmt.Errorf("date required")
	}
	isSplit := len(in.Splits) > 0
	if isSplit {
		var sum int64
		for _, sp := range in.Splits {
			sum += sp.Amount
		}
		if sum != in.Amount {
			return 0, fmt.Errorf("splits must sum to amount (got %d, want %d)", sum, in.Amount)
		}
	} else if in.CategoryID == nil {
		return 0, fmt.Errorf("category required")
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var payeeID *int64
	if strings.TrimSpace(in.PayeeName) != "" {
		pid, err := ensurePayeeTx(tx, in.PayeeName, in.CategoryID, &in.AccountID)
		if err != nil {
			return 0, err
		}
		payeeID = &pid
	}

	var catID any
	if !isSplit && in.CategoryID != nil {
		catID = *in.CategoryID
	}
	splitFlag := 0
	if isSplit {
		splitFlag = 1
	}

	res, err := tx.Exec(`INSERT INTO transactions(
		account_id, payee_id, payee_name, notes, amount, transaction_date, person, status,
		category_id, is_split, created_by, updated_by)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.AccountID, payeeID, nullStr(in.PayeeName), nullStr(in.Notes), in.Amount, in.TransactionDate,
		in.Person, in.Status, catID, splitFlag, in.CreatedBy, in.CreatedBy)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	if isSplit {
		for i, sp := range in.Splits {
			person := sp.Person
			if person == "" {
				person = in.Person
			}
			_, err = tx.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, notes, display_order)
				VALUES (?,?,?,?,?,?)`, id, sp.CategoryID, person, sp.Amount, nullStr(sp.Notes), i)
			if err != nil {
				return 0, err
			}
		}
	} else {
		// Mirror single category as one split for consistent reporting later
		_, err = tx.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, notes, display_order)
			VALUES (?,?,?,?,?,0)`, id, *in.CategoryID, in.Person, in.Amount, nullStr(in.Notes))
		if err != nil {
			return 0, err
		}
	}

	for _, tn := range in.TagNames {
		tn = strings.TrimSpace(tn)
		if tn == "" {
			continue
		}
		tid, err := ensureTagTx(tx, tn)
		if err != nil {
			return 0, err
		}
		_, err = tx.Exec(`INSERT OR IGNORE INTO transaction_tags(transaction_id, tag_id) VALUES (?,?)`, id, tid)
		if err != nil {
			return 0, err
		}
	}

	return id, tx.Commit()
}

func (s *Store) CreateTransfer(in TransferInput) (int64, error) {
	if in.Amount <= 0 {
		return 0, fmt.Errorf("transfer amount must be positive")
	}
	if in.FromAccountID == in.ToAccountID {
		return 0, fmt.Errorf("accounts must differ")
	}
	if in.Person == "" {
		in.Person = "shared"
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	xferCat, err := categoryIDByKindNameTx(tx, "transfer", "Transfer")
	if err != nil {
		return 0, err
	}

	fromRes, err := tx.Exec(`INSERT INTO transactions(
		account_id, payee_name, notes, amount, transaction_date, person, status, category_id, is_split, created_by, updated_by)
		VALUES (?,?,?,?,?,?, 'cleared', ?, 0, ?, ?)`,
		in.FromAccountID, "Transfer", nullStr(in.Notes), -in.Amount, in.TransactionDate, in.Person,
		xferCat, in.CreatedBy, in.CreatedBy)
	if err != nil {
		return 0, err
	}
	fromID, _ := fromRes.LastInsertId()
	_, err = tx.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, display_order)
		VALUES (?,?,?,?,0)`, fromID, xferCat, in.Person, -in.Amount)
	if err != nil {
		return 0, err
	}

	toRes, err := tx.Exec(`INSERT INTO transactions(
		account_id, payee_name, notes, amount, transaction_date, person, status, category_id, is_split, created_by, updated_by)
		VALUES (?,?,?,?,?,?, 'cleared', ?, 0, ?, ?)`,
		in.ToAccountID, "Transfer", nullStr(in.Notes), in.Amount, in.TransactionDate, in.Person,
		xferCat, in.CreatedBy, in.CreatedBy)
	if err != nil {
		return 0, err
	}
	toID, _ := toRes.LastInsertId()
	_, err = tx.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, display_order)
		VALUES (?,?,?,?,0)`, toID, xferCat, in.Person, in.Amount)
	if err != nil {
		return 0, err
	}

	res, err := tx.Exec(`INSERT INTO transfers(from_tx_id, to_tx_id) VALUES (?,?)`, fromID, toID)
	if err != nil {
		return 0, err
	}
	xferID, _ := res.LastInsertId()
	_, err = tx.Exec(`UPDATE transactions SET transfer_id=? WHERE id IN (?,?)`, xferID, fromID, toID)
	if err != nil {
		return 0, err
	}
	return xferID, tx.Commit()
}

func (s *Store) SoftDeleteTransaction(id, personID int64) (undoID string, err error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var transferID sql.NullInt64
	err = tx.QueryRow(`SELECT transfer_id FROM transactions WHERE id=? AND deleted_at IS NULL`, id).Scan(&transferID)
	if err != nil {
		return "", err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	ids := []int64{id}
	if transferID.Valid {
		var fromID, toID int64
		err = tx.QueryRow(`SELECT from_tx_id, to_tx_id FROM transfers WHERE id=?`, transferID.Int64).Scan(&fromID, &toID)
		if err != nil {
			return "", err
		}
		ids = []int64{fromID, toID}
	}
	for _, tid := range ids {
		_, err = tx.Exec(`UPDATE transactions SET deleted_at=?, updated_by=?, updated_at=? WHERE id=?`,
			now, personID, now, tid)
		if err != nil {
			return "", err
		}
	}

	undoID = uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"transaction_ids": ids})
	// 30-minute undo toast window; recycle bin retains 30 days for restore.
	exp := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339Nano)
	_, err = tx.Exec(`INSERT INTO undo_ops(id, kind, payload, expires_at, created_by) VALUES (?,?,?,?,?)`,
		undoID, "soft_delete_tx", string(payload), exp, personID)
	if err != nil {
		return "", err
	}
	return undoID, tx.Commit()
}

func (s *Store) Undo(undoID string) error {
	var kind, payload, exp string
	err := s.DB.QueryRow(`SELECT kind, payload, expires_at FROM undo_ops WHERE id=?`, undoID).
		Scan(&kind, &payload, &exp)
	if err != nil {
		return err
	}
	t, err := time.Parse(time.RFC3339Nano, exp)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, exp)
	}
	if time.Now().UTC().After(t) {
		return fmt.Errorf("undo expired")
	}
	switch kind {
	case "soft_delete_tx":
		var p struct {
			TransactionIDs []int64 `json:"transaction_ids"`
		}
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return err
		}
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, id := range p.TransactionIDs {
			_, err = tx.Exec(`UPDATE transactions SET deleted_at=NULL, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, id)
			if err != nil {
				return err
			}
		}
		_, _ = tx.Exec(`DELETE FROM undo_ops WHERE id=?`, undoID)
		return tx.Commit()
	default:
		return fmt.Errorf("unknown undo kind")
	}
}

// AttachRunningBalances fills RunningBalance for a single-account register (newest first list).
func (s *Store) AttachRunningBalances(accountID int64, txs []models.Transaction) error {
	bal, err := s.AccountBalance(accountID)
	if err != nil {
		return err
	}
	// Newest first: running balance at each row is balance after that tx (current), then walk back
	for i := range txs {
		txs[i].RunningBalance = bal
		bal -= txs[i].Amount
	}
	return nil
}

func (s *Store) GetTransaction(id int64) (*models.Transaction, error) {
	list, err := s.ListTransactions(RegisterFilter{Limit: 10000})
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *Store) SetTransactionStatus(id int64, status string) error {
	_, err := s.DB.Exec(`UPDATE transactions SET status=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND deleted_at IS NULL`,
		status, id)
	return err
}

func (s *Store) Reconcile(accountID int64, statementDate string, endingBalance int64, clearIDs []int64, personID int64) (diff int64, err error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	for _, id := range clearIDs {
		_, err = tx.Exec(`UPDATE transactions SET status='reconciled', updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
			WHERE id=? AND account_id=? AND deleted_at IS NULL`, id, accountID)
		if err != nil {
			return 0, err
		}
	}

	var anchor int64
	var anchorDate string
	err = tx.QueryRow(`SELECT anchor_balance, anchor_date FROM accounts WHERE id=?`, accountID).Scan(&anchor, &anchorDate)
	if err != nil {
		return 0, err
	}
	var sum sql.NullInt64
	err = tx.QueryRow(`SELECT SUM(amount) FROM transactions
		WHERE account_id=? AND deleted_at IS NULL AND transaction_date >= ? AND transaction_date <= ?
		AND status IN ('cleared','reconciled')`, accountID, anchorDate, statementDate).Scan(&sum)
	if err != nil {
		return 0, err
	}
	clearedBal := anchor
	if sum.Valid {
		clearedBal += sum.Int64
	}
	diff = endingBalance - clearedBal

	_, err = tx.Exec(`INSERT INTO reconciliations(account_id, statement_date, ending_balance, difference, completed_at, created_by)
		VALUES (?,?,?,?, CASE WHEN ? = 0 THEN strftime('%Y-%m-%dT%H:%M:%fZ','now') ELSE NULL END, ?)`,
		accountID, statementDate, endingBalance, diff, diff, personID)
	if err != nil {
		return 0, err
	}
	return diff, tx.Commit()
}

func (s *Store) UnclearedForAccount(accountID int64) ([]models.Transaction, error) {
	return s.listByStatus(accountID, []string{"pending", "cleared"})
}

func (s *Store) listByStatus(accountID int64, statuses []string) ([]models.Transaction, error) {
	placeholders := make([]string, len(statuses))
	args := []any{accountID}
	for i, st := range statuses {
		placeholders[i] = "?"
		args = append(args, st)
	}
	q := fmt.Sprintf(`SELECT t.id, t.account_id, a.name, t.payee_id, COALESCE(t.payee_name,''), COALESCE(t.notes,''),
		t.amount, t.transaction_date, t.person, t.status, t.needs_review, t.category_id,
		COALESCE(c.name,''), t.is_split, t.transfer_id
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.deleted_at IS NULL AND t.account_id = ? AND t.status IN (%s)
		ORDER BY t.transaction_date ASC, t.id ASC`, strings.Join(placeholders, ","))
	rows, err := s.DB.Query(q, args...)
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
	return out, rows.Err()
}

func (s *Store) txTags(txID int64) ([]models.Tag, error) {
	rows, err := s.DB.Query(`SELECT tg.id, tg.name FROM tags tg
		JOIN transaction_tags tt ON tt.tag_id = tg.id WHERE tt.transaction_id=? ORDER BY tg.name`, txID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Tag
	for rows.Next() {
		var t models.Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) txSplits(txID int64) ([]models.SplitLine, error) {
	rows, err := s.DB.Query(`SELECT s.id, s.category_id, s.person, s.amount, COALESCE(s.notes,''), c.name
		FROM transaction_splits s JOIN categories c ON c.id = s.category_id
		WHERE s.transaction_id=? ORDER BY s.display_order`, txID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.SplitLine
	for rows.Next() {
		var sp models.SplitLine
		if err := rows.Scan(&sp.ID, &sp.CategoryID, &sp.Person, &sp.Amount, &sp.Notes, &sp.CategoryName); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func ensurePayeeTx(tx *sql.Tx, name string, categoryID, accountID *int64) (int64, error) {
	name = strings.TrimSpace(name)
	var id int64
	err := tx.QueryRow(`SELECT id FROM payees WHERE name = ? COLLATE NOCASE`, name).Scan(&id)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err == nil {
		_, _ = tx.Exec(`UPDATE payees SET last_used_at=? WHERE id=?`, now, id)
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO payees(name, default_category_id, default_account_id, last_used_at) VALUES (?,?,?,?)`,
		name, categoryID, accountID, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func ensureTagTx(tx *sql.Tx, name string) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM tags WHERE name = ? COLLATE NOCASE`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO tags(name) VALUES (?)`, name)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func categoryIDByKindNameTx(tx *sql.Tx, kind, name string) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM categories WHERE kind=? AND name=?`, kind, name).Scan(&id)
	return id, err
}

func nullStr(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
}

// NewCSRF helper re-export for handlers that create sessions after setup
var NewToken = auth.NewToken
