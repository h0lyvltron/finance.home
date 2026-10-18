package store_test

import (
	"testing"

	"finance.home/internal/store"
)

func TestRepairTransferOutliers(t *testing.T) {
	s := setupStore(t)
	checking := ensureChecking(t, s)
	navy, err := s.CreateAccount(store.AccountInput{Name: "Old Navy", Type: "credit", Owner: "joint", AnchorDate: "2024-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	capone, err := s.CreateAccount(store.AccountInput{Name: "Capitol One", Type: "credit", Owner: "a", AnchorDate: "2024-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	xfer := categoryID(t, s, "transfer", "Transfer")
	expense := firstExpenseCat(t, s)

	andersons := mustTx(t, s, navy, "ANDERSONS", -27237, "2026-08-24", xfer)
	paypal := mustTx(t, s, checking, "TRANSFER PAYPAL IDPAYPALSD11", 27237, "2026-08-26", xfer)
	linkTx(t, s, andersons, paypal)

	once := mustTx(t, s, capone, "SP ONCEMOREWITHLOVE", -5000, "2026-05-16", xfer)
	bank := mustTx(t, s, checking, "Online Banking Transfer", 5000, "2026-05-19", xfer)
	linkTx(t, s, once, bank)

	capPay300 := mustTx(t, s, checking, "MOBILE PMT CAPITAL ONE", -30000, "2026-05-04", xfer)
	navyRecv300 := mustTx(t, s, navy, "Payment Received", 30000, "2026-05-01", xfer)
	linkTx(t, s, capPay300, navyRecv300)
	barclay300 := mustTx(t, s, checking, "CREDITCARD BARCLAYCARD US", -30000, "2026-05-04", xfer)
	capRecv300 := mustTx(t, s, capone, "CAPITAL ONE MOBILE PYMT", 30000, "2026-05-01", xfer)
	linkTx(t, s, barclay300, capRecv300)

	cap85 := mustTx(t, s, checking, "MOBILE PMT CAPITAL ONE ID9279744380", -8500, "2026-05-11", expense)
	card85 := mustTx(t, s, capone, "CAPITAL ONE MOBILE PYMT", 8500, "2026-05-06", expense)
	pay350 := mustTx(t, s, navy, "Payment Received", 35000, "2026-10-16", expense)

	undoID, changed, err := s.RepairTransferOutliers(1)
	if err != nil || !changed || undoID == "" {
		t.Fatalf("repair changed=%v undo=%q err=%v", changed, undoID, err)
	}

	if tx, _ := s.GetTransaction(andersons); tx.TransferID != nil || tx.CategoryID != nil || !tx.NeedsReview {
		t.Fatalf("andersons should be back in review: %+v", tx)
	}
	if tx, _ := s.GetTransaction(paypal); tx.TransferID != nil || tx.CategoryID == nil || *tx.CategoryID != xfer {
		t.Fatalf("paypal deposit should stay a transfer, unlinked: %+v", tx)
	}
	if tx, _ := s.GetTransaction(once); tx.CategoryID != nil || !tx.NeedsReview {
		t.Fatalf("oncemore should be back in review: %+v", tx)
	}
	if got := linkedPayee(t, s, capPay300); got != "CAPITAL ONE MOBILE PYMT" {
		t.Fatalf("capital one payment linked to %q", got)
	}
	if got := linkedPayee(t, s, barclay300); got != "Payment Received" {
		t.Fatalf("barclay payment linked to %q", got)
	}
	if tx, _ := s.GetTransaction(cap85); tx.TransferID == nil || tx.CategoryID == nil || *tx.CategoryID != xfer {
		t.Fatalf("$85 checking leg: %+v", tx)
	}
	if tx, _ := s.GetTransaction(card85); tx.TransferID == nil || *tx.TransferID != *mustTxTransfer(t, s, cap85) {
		t.Fatalf("$85 card leg: %+v", tx)
	}
	if tx, _ := s.GetTransaction(pay350); tx.TransferID != nil || tx.CategoryID == nil || *tx.CategoryID != xfer {
		t.Fatalf("$350 payment credit: %+v", tx)
	}

	if _, changed, err := s.RepairTransferOutliers(1); err != nil || changed {
		t.Fatalf("second repair changed=%v err=%v", changed, err)
	}
	if err := s.Undo(undoID); err != nil {
		t.Fatal(err)
	}
	if tx, _ := s.GetTransaction(andersons); tx.TransferID == nil || tx.CategoryID == nil || *tx.CategoryID != xfer {
		t.Fatalf("undo should relink andersons: %+v", tx)
	}
	if tx, _ := s.GetTransaction(cap85); tx.TransferID != nil || tx.CategoryID == nil || *tx.CategoryID != expense {
		t.Fatalf("undo should unlink the $85: %+v", tx)
	}
}

func categoryID(t *testing.T, s *store.Store, kind, name string) int64 {
	t.Helper()
	var id int64
	if err := s.DB.QueryRow(`SELECT id FROM categories WHERE kind=? AND name=?`, kind, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustTx(t *testing.T, s *store.Store, acct int64, payee string, amt int64, date string, cat int64) int64 {
	t.Helper()
	id, err := s.CreateTransaction(store.TxInput{
		AccountID: acct, PayeeName: payee, Amount: amt,
		TransactionDate: date, CategoryID: &cat, CreatedBy: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func linkTx(t *testing.T, s *store.Store, from, to int64) {
	t.Helper()
	res, err := s.DB.Exec(`INSERT INTO transfers(from_tx_id, to_tx_id) VALUES (?,?)`, from, to)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := s.DB.Exec(`UPDATE transactions SET transfer_id=? WHERE id IN (?,?)`, id, from, to); err != nil {
		t.Fatal(err)
	}
}

func linkedPayee(t *testing.T, s *store.Store, from int64) string {
	t.Helper()
	var name string
	err := s.DB.QueryRow(`SELECT COALESCE(tt.payee_name,'') FROM transfers tr
		JOIN transactions tt ON tt.id = tr.to_tx_id WHERE tr.from_tx_id=?`, from).Scan(&name)
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func mustTxTransfer(t *testing.T, s *store.Store, id int64) *int64 {
	t.Helper()
	tx, err := s.GetTransaction(id)
	if err != nil {
		t.Fatal(err)
	}
	return tx.TransferID
}
