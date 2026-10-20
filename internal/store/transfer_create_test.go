package store_test

import (
	"testing"

	"finance.home/internal/store"
)

func TestCreateTransferMovesBothEnds(t *testing.T) {
	f := newAuditFixture(t)
	s := f.s
	cases := []struct {
		name             string
		from, to         int64
		amount, interest int64
		wantFrom, wantTo int64 // expected balance change
	}{
		{"checking pays credit card", f.gcu, f.apple, 25000, 0, -25000, 25000},
		{"checking pays loan with interest", f.gcu, f.loan, 73700, 9924, -73700, 63776},
		{"savings to checking", f.savings, f.stcu, 10000, 0, -10000, 10000},
		{"cash advance from card", f.dad, f.stcu, 5000, 0, -5000, 5000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fromBefore, toBefore := f.balance(t, c.from), f.balance(t, c.to)
			id, err := s.CreateTransfer(store.TransferInput{FromAccountID: c.from, ToAccountID: c.to,
				Amount: c.amount, InterestCents: c.interest, TransactionDate: "2026-10-01", CreatedBy: 1})
			if err != nil {
				t.Fatal(err)
			}
			if got := f.balance(t, c.from) - fromBefore; got != c.wantFrom {
				t.Errorf("from changed by %d, want %d", got, c.wantFrom)
			}
			if got := f.balance(t, c.to) - toBefore; got != c.wantTo {
				t.Errorf("to changed by %d, want %d", got, c.wantTo)
			}
			var fromAmt, toAmt int64
			if err := s.DB.QueryRow(`SELECT f.amount, t.amount FROM transfers x
				JOIN transactions f ON f.id = x.from_tx_id JOIN transactions t ON t.id = x.to_tx_id
				WHERE x.id=?`, id).Scan(&fromAmt, &toAmt); err != nil {
				t.Fatal(err)
			}
			if fromAmt != -c.amount || toAmt != c.amount {
				t.Errorf("pair amounts %d / %d, want symmetric %d", fromAmt, toAmt, c.amount)
			}
		})
	}

	var interestCat string
	if err := s.DB.QueryRow(`SELECT c.name FROM transactions t JOIN categories c ON c.id = t.category_id
		WHERE t.account_id=? AND t.amount=-9924 AND t.transfer_id IS NULL`, f.loan).Scan(&interestCat); err != nil || interestCat != "Loan interest" {
		t.Fatalf("interest row category=%q err=%v", interestCat, err)
	}
}

func TestCreateTransferValidation(t *testing.T) {
	f := newAuditFixture(t)
	bad := []store.TransferInput{
		{FromAccountID: f.gcu, ToAccountID: f.gcu, Amount: 100, TransactionDate: "2026-10-01"},
		{FromAccountID: f.gcu, ToAccountID: f.stcu, Amount: 0, TransactionDate: "2026-10-01"},
		{FromAccountID: f.gcu, ToAccountID: f.stcu, Amount: 100, InterestCents: 10, TransactionDate: "2026-10-01"},
		{FromAccountID: f.gcu, ToAccountID: f.loan, Amount: 100, InterestCents: 200, TransactionDate: "2026-10-01"},
		{FromAccountID: f.gcu, ToAccountID: 9999, Amount: 100, TransactionDate: "2026-10-01"},
		{FromAccountID: f.gcu, ToAccountID: f.stcu, Amount: 100},
	}
	for i, in := range bad {
		if _, err := f.s.CreateTransfer(in); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
}
