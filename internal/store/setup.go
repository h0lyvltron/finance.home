package store

import (
	"database/sql"
	"fmt"

	"finance.home/internal/auth"
	"finance.home/internal/models"
)

func (s *Store) Household() (*models.Household, error) {
	h := &models.Household{}
	err := s.DB.QueryRow(`SELECT id, name, currency, timezone, week_starts,
		COALESCE(expected_income_cents,0), COALESCE(savings_target_cents,0)
		FROM households WHERE id = 1`).
		Scan(&h.ID, &h.Name, &h.Currency, &h.Timezone, &h.WeekStarts,
			&h.ExpectedIncomeCents, &h.SavingsTargetCents)
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (s *Store) UpdateHouseholdPlan(expectedIncome, savingsTarget int64) error {
	_, err := s.DB.Exec(`UPDATE households SET expected_income_cents=?, savings_target_cents=? WHERE id=1`,
		expectedIncome, savingsTarget)
	return err
}

func (s *Store) People() ([]models.Person, error) {
	rows, err := s.DB.Query(`SELECT id, slot, name FROM people ORDER BY slot`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Person
	for rows.Next() {
		var p models.Person
		if err := rows.Scan(&p.ID, &p.Slot, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) PersonByID(id int64) (*models.Person, error) {
	p := &models.Person{}
	err := s.DB.QueryRow(`SELECT id, slot, name, password_hash FROM people WHERE id = ?`, id).
		Scan(&p.ID, &p.Slot, &p.Name, &p.PasswordHash)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) PersonByName(name string) (*models.Person, error) {
	p := &models.Person{}
	err := s.DB.QueryRow(`SELECT id, slot, name, password_hash FROM people WHERE name = ? COLLATE NOCASE`, name).
		Scan(&p.ID, &p.Slot, &p.Name, &p.PasswordHash)
	if err != nil {
		return nil, err
	}
	return p, nil
}

type SetupInput struct {
	HouseholdName string
	Currency      string
	Timezone      string
	PersonAName   string
	PersonAPass   string
	PersonBName   string
	PersonBPass   string
}

func (s *Store) CompleteSetup(in SetupInput) error {
	if in.Currency == "" {
		in.Currency = "USD"
	}
	if in.Timezone == "" {
		in.Timezone = "America/New_York"
	}
	hashA, err := auth.HashPassword(in.PersonAPass)
	if err != nil {
		return err
	}
	hashB, err := auth.HashPassword(in.PersonBPass)
	if err != nil {
		return err
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var n int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM households`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("setup already completed")
	}

	_, err = tx.Exec(`INSERT INTO households(id, name, currency, timezone) VALUES (1,?,?,?)`,
		in.HouseholdName, in.Currency, in.Timezone)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO people(slot, name, password_hash) VALUES ('a',?,?)`, in.PersonAName, hashA)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO people(slot, name, password_hash) VALUES ('b',?,?)`, in.PersonBName, hashB)
	if err != nil {
		return err
	}
	if err := seedCategories(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func seedCategories(tx *sql.Tx) error {
	type cat struct {
		name, kind, fixed string
		color             string
		children          []string
	}
	pack := []cat{
		{name: "Housing", kind: "expense", fixed: "fixed", color: "#5B7C99", children: []string{"Rent/Mortgage", "Utilities", "Internet", "Insurance"}},
		{name: "Food", kind: "expense", fixed: "flexible", color: "#6B9E76", children: []string{"Groceries", "Dining out", "Coffee"}},
		{name: "Transport", kind: "expense", fixed: "flexible", color: "#C4A35A", children: []string{"Fuel", "Transit", "Parking", "Car maintenance"}},
		{name: "Health", kind: "expense", fixed: "flexible", color: "#8B6B8B", children: []string{"Medical", "Pharmacy", "Fitness"}},
		{name: "Personal", kind: "expense", fixed: "flexible", color: "#9A7B6B", children: []string{"Shopping", "Subscriptions", "Entertainment", "Gifts"}},
		{name: "Kids & pets", kind: "expense", fixed: "flexible", color: "#7A8B6B", children: []string{"Childcare", "Pet care", "Activities"}},
		{name: "Debt payments", kind: "expense", fixed: "fixed", color: "#A66B6B", children: []string{"Credit card payment", "Loan payment"}},
		{name: "Savings & goals", kind: "expense", fixed: "fixed", color: "#5A8B8B", children: []string{"Emergency fund", "Vacation"}},
		{name: "Income", kind: "income", fixed: "", color: "#4A7C59", children: []string{"Paycheck", "Side income", "Interest", "Other income"}},
	}

	ins := func(name, kind, fixed, color string, parent *int64, protected, order int) (int64, error) {
		var ff any
		if fixed != "" {
			ff = fixed
		}
		res, err := tx.Exec(`INSERT INTO categories(name, kind, parent_id, fixed_flexible, color, protected, display_order)
			VALUES (?,?,?,?,?,?,?)`, name, kind, parent, ff, color, protected, order)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}

	order := 0
	for _, p := range pack {
		order++
		pid, err := ins(p.name, p.kind, p.fixed, p.color, nil, 0, order)
		if err != nil {
			return err
		}
		for i, c := range p.children {
			order++
			if _, err := ins(c, p.kind, p.fixed, p.color, &pid, 0, order+i); err != nil {
				return err
			}
		}
	}
	// Protected system categories
	if _, err := ins("Transfer", "transfer", "", "#888888", nil, 1, 900); err != nil {
		return err
	}
	if _, err := ins("Starting balance", "starting_balance", "", "#888888", nil, 1, 901); err != nil {
		return err
	}
	return nil
}
