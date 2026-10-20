package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"finance.home/internal/models"
)

const accountCols = `id, name, type, owner, currency, anchor_balance, anchor_date, display_order, archived, transfer_aliases`

func scanAccount(row interface{ Scan(...any) error }, a *models.Account) error {
	var arch int
	if err := row.Scan(&a.ID, &a.Name, &a.Type, &a.Owner, &a.Currency, &a.AnchorBalance, &a.AnchorDate,
		&a.DisplayOrder, &arch, &a.TransferAliases); err != nil {
		return err
	}
	a.Archived = arch == 1
	return nil
}

func (s *Store) ListAccounts(includeArchived bool) ([]models.Account, error) {
	q := `SELECT ` + accountCols + ` FROM accounts`
	if !includeArchived {
		q += ` WHERE archived = 0`
	}
	q += ` ORDER BY display_order, name`
	rows, err := s.DB.Query(q)
	if err != nil {
		return nil, err
	}
	var out []models.Account
	for rows.Next() {
		var a models.Account
		if err := scanAccount(rows, &a); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		bal, err := s.AccountBalance(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Balance = bal
	}
	return out, nil
}

func (s *Store) GetAccount(id int64) (*models.Account, error) {
	a := &models.Account{}
	if err := scanAccount(s.DB.QueryRow(`SELECT `+accountCols+` FROM accounts WHERE id = ?`, id), a); err != nil {
		return nil, err
	}
	bal, err := s.AccountBalance(a.ID)
	if err != nil {
		return nil, err
	}
	a.Balance = bal
	return a, nil
}

// AccountBalance = anchor + sum(non-deleted amounts). Pending included (affects balance).
func (s *Store) AccountBalance(accountID int64) (int64, error) {
	var anchor int64
	var anchorDate string
	err := s.DB.QueryRow(`SELECT anchor_balance, anchor_date FROM accounts WHERE id = ?`, accountID).
		Scan(&anchor, &anchorDate)
	if err != nil {
		return 0, err
	}
	var sum sql.NullInt64
	err = s.DB.QueryRow(`
		SELECT SUM(amount) FROM transactions
		WHERE account_id = ? AND deleted_at IS NULL AND transaction_date >= ?`,
		accountID, anchorDate).Scan(&sum)
	if err != nil {
		return 0, err
	}
	if sum.Valid {
		return anchor + sum.Int64, nil
	}
	return anchor, nil
}

type AccountInput struct {
	Name            string
	Type            string
	Owner           string
	Currency        string
	AnchorBalance   int64
	AnchorDate      string
	TransferAliases string
}

// NormalizeAliases trims and de-duplicates a comma-separated alias list.
func NormalizeAliases(raw string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[strings.ToUpper(p)] {
			continue
		}
		seen[strings.ToUpper(p)] = true
		out = append(out, p)
	}
	return strings.Join(out, ", ")
}

func (s *Store) CreateAccount(in AccountInput) (int64, error) {
	if in.Currency == "" {
		in.Currency = "USD"
	}
	if in.AnchorDate == "" {
		in.AnchorDate = time.Now().Format("2006-01-02")
	}
	var maxOrd int
	_ = s.DB.QueryRow(`SELECT COALESCE(MAX(display_order),0) FROM accounts`).Scan(&maxOrd)
	res, err := s.DB.Exec(`INSERT INTO accounts(name, type, owner, currency, anchor_balance, anchor_date, display_order, transfer_aliases)
		VALUES (?,?,?,?,?,?,?,?)`, in.Name, in.Type, in.Owner, in.Currency, in.AnchorBalance, in.AnchorDate, maxOrd+1,
		NormalizeAliases(in.TransferAliases))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateAccount(id int64, in AccountInput) error {
	_, err := s.DB.Exec(`UPDATE accounts SET name=?, type=?, owner=?, currency=?, anchor_balance=?, anchor_date=?,
		transfer_aliases=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`,
		in.Name, in.Type, in.Owner, in.Currency, in.AnchorBalance, in.AnchorDate, NormalizeAliases(in.TransferAliases), id)
	return err
}

func (s *Store) ArchiveAccount(id int64, archived bool) error {
	v := 0
	if archived {
		v = 1
	}
	_, err := s.DB.Exec(`UPDATE accounts SET archived=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, v, id)
	return err
}

func (s *Store) CountTxBeforeAnchor(accountID int64, anchorDate string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(1) FROM transactions WHERE account_id=? AND deleted_at IS NULL AND transaction_date < ?`,
		accountID, anchorDate).Scan(&n)
	return n, err
}

func ValidAccountType(t string) bool {
	switch t {
	case "checking", "savings", "cash", "credit", "loan":
		return true
	}
	return false
}

func ValidOwner(o string) bool {
	switch o {
	case "a", "b", "joint":
		return true
	}
	return false
}

func ValidateAccountInput(in AccountInput) error {
	if in.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !ValidAccountType(in.Type) {
		return fmt.Errorf("invalid account type")
	}
	if !ValidOwner(in.Owner) {
		return fmt.Errorf("invalid owner")
	}
	if in.AnchorDate == "" {
		return fmt.Errorf("anchor date is required")
	}
	return nil
}
