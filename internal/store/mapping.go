package store

import (
	"database/sql"
	"strings"

	"finance.home/internal/models"
)

func (s *Store) ListMappingProfiles() ([]models.MappingProfile, error) {
	rows, err := s.DB.Query(`SELECT id, name, account_id, date_column, date_format, amount_mode,
		COALESCE(amount_column,''), COALESCE(debit_column,''), COALESCE(credit_column,''),
		COALESCE(payee_column,''), COALESCE(notes_column,''), invert_sign, has_header, delimiter
		FROM csv_mapping_profiles ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.MappingProfile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetMappingProfile(id int64) (*models.MappingProfile, error) {
	row := s.DB.QueryRow(`SELECT id, name, account_id, date_column, date_format, amount_mode,
		COALESCE(amount_column,''), COALESCE(debit_column,''), COALESCE(credit_column,''),
		COALESCE(payee_column,''), COALESCE(notes_column,''), invert_sign, has_header, delimiter
		FROM csv_mapping_profiles WHERE id=?`, id)
	p, err := scanProfile(row)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

type profileScanner interface {
	Scan(dest ...any) error
}

func scanProfile(row profileScanner) (models.MappingProfile, error) {
	var p models.MappingProfile
	var acct sql.NullInt64
	var inv, hh int
	if err := row.Scan(&p.ID, &p.Name, &acct, &p.DateColumn, &p.DateFormat, &p.AmountMode,
		&p.AmountColumn, &p.DebitColumn, &p.CreditColumn, &p.PayeeColumn, &p.NotesColumn,
		&inv, &hh, &p.Delimiter); err != nil {
		return p, err
	}
	if acct.Valid {
		v := acct.Int64
		p.AccountID = &v
	}
	p.InvertSign = inv == 1
	p.HasHeader = hh == 1
	if p.Delimiter == "" {
		p.Delimiter = ","
	}
	return p, nil
}

type MappingProfileInput struct {
	Name         string
	AccountID    *int64
	DateColumn   string
	DateFormat   string
	AmountMode   string
	AmountColumn string
	DebitColumn  string
	CreditColumn string
	PayeeColumn  string
	NotesColumn  string
	InvertSign   bool
	HasHeader    bool
	Delimiter    string
}

func (s *Store) UpsertMappingProfile(id int64, in MappingProfileInput) (int64, error) {
	if in.DateFormat == "" {
		in.DateFormat = "auto"
	}
	if in.AmountMode == "" {
		in.AmountMode = "signed"
	}
	if in.Delimiter == "" {
		in.Delimiter = ","
	}
	inv, hh := 0, 0
	if in.InvertSign {
		inv = 1
	}
	if in.HasHeader {
		hh = 1
	}
	if id > 0 {
		_, err := s.DB.Exec(`UPDATE csv_mapping_profiles SET
			name=?, account_id=?, date_column=?, date_format=?, amount_mode=?,
			amount_column=?, debit_column=?, credit_column=?, payee_column=?, notes_column=?,
			invert_sign=?, has_header=?, delimiter=?,
			updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
			WHERE id=?`,
			in.Name, in.AccountID, in.DateColumn, in.DateFormat, in.AmountMode,
			nullStr(in.AmountColumn), nullStr(in.DebitColumn), nullStr(in.CreditColumn),
			nullStr(in.PayeeColumn), nullStr(in.NotesColumn), inv, hh, in.Delimiter, id)
		return id, err
	}
	res, err := s.DB.Exec(`INSERT INTO csv_mapping_profiles(
		name, account_id, date_column, date_format, amount_mode,
		amount_column, debit_column, credit_column, payee_column, notes_column,
		invert_sign, has_header, delimiter)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.Name, in.AccountID, in.DateColumn, in.DateFormat, in.AmountMode,
		nullStr(in.AmountColumn), nullStr(in.DebitColumn), nullStr(in.CreditColumn),
		nullStr(in.PayeeColumn), nullStr(in.NotesColumn), inv, hh, in.Delimiter)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeleteMappingProfile(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM csv_mapping_profiles WHERE id=?`, id)
	return err
}

func delimRune(s string) rune {
	s = strings.TrimSpace(s)
	if s == "\\t" || s == "tab" {
		return '\t'
	}
	if s == "" {
		return ','
	}
	return []rune(s)[0]
}

func DelimRune(s string) rune { return delimRune(s) }
