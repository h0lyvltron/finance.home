package store_test

import (
	"net/url"
	"testing"
	"time"

	"finance.home/internal/store"
)

func TestUncategorizedFilterMatchesBanner(t *testing.T) {
	s := setupStore(t)
	acct, err := s.CreateAccount(store.AccountInput{Name: "Capital One (Mom)", Type: "credit", Owner: "a", AnchorDate: "2026-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	groceries := catByName(t, s, "Groceries")
	mk := func(payee string) int64 {
		id, err := s.CreateTransaction(store.TxInput{AccountID: acct, PayeeName: payee, Amount: -5000,
			TransactionDate: "2026-05-16", CategoryID: &groceries, CreatedBy: 1})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	mk("CATEGORIZED")
	orphan := mk("SP ONCEMOREWITHLOVE")
	// Category cleared, then marked reviewed without picking one.
	if _, err := s.DB.Exec(`UPDATE transactions SET category_id=NULL, needs_review=1 WHERE id=?`, orphan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DELETE FROM transaction_splits WHERE transaction_id=?`, orphan); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmReviewAll([]int64{orphan}); err != nil {
		t.Fatal(err)
	}

	h, err := s.Household()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	base := store.QueryFilter{DatePreset: "ytd"}
	n, spend, err := s.UncategorizedSummary(base, h, now)
	if err != nil || n != 1 || spend != 5000 {
		t.Fatalf("banner n=%d spend=%d err=%v", n, spend, err)
	}

	link := base.Clone()
	link.Uncategorized = true
	v, _ := url.ParseQuery(link.EncodeQuery().Encode())
	f := store.ParseQueryFilter(v)
	if !f.Uncategorized {
		t.Fatal("uncat lost in the link round-trip")
	}
	f.ResolveDates(h, now)
	txs, err := s.ListTransactions(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 || txs[0].ID != orphan {
		t.Fatalf("filtered list %+v, want only #%d", txs, orphan)
	}
}
