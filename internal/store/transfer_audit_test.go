package store_test

import (
	"testing"

	"finance.home/internal/store"
)

type auditFixture struct {
	s                                    *store.Store
	stcu, savings, gcu, apple, dad, loan int64
}

func newAuditFixture(t *testing.T) auditFixture {
	t.Helper()
	s := setupStore(t)
	mk := func(name, typ, owner string, anchor int64, aliases string) int64 {
		id, err := s.CreateAccount(store.AccountInput{Name: name, Type: typ, Owner: owner,
			AnchorBalance: anchor, AnchorDate: "2026-01-01", TransferAliases: aliases})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	return auditFixture{
		s:       s,
		stcu:    mk("STCU", "checking", "joint", 100000, ""),
		savings: mk("STCU Savings", "savings", "joint", 50000, "SAV"),
		gcu:     mk("Global CU (CHK)", "checking", "b", 300000, ""),
		apple:   mk("Apple (Goldman Sachs)", "credit", "b", -50000, ""),
		dad:     mk("Capital One (Dad)", "credit", "b", -20000, ""),
		loan:    mk("Global CU Auto Loan (4Runner)", "loan", "b", -1700000, "LOAN 02"),
	}
}

func (f auditFixture) tx(t *testing.T, acct int64, date string, amount int64, payee, category string) int64 {
	t.Helper()
	cat := catByName(t, f.s, category)
	id, err := f.s.CreateTransaction(store.TxInput{AccountID: acct, PayeeName: payee, Amount: amount,
		TransactionDate: date, CategoryID: &cat, CreatedBy: 1})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f auditFixture) balance(t *testing.T, acct int64) int64 {
	t.Helper()
	b, err := f.s.AccountBalance(acct)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTransferAuditFindsAndRepairs(t *testing.T) {
	f := newAuditFixture(t)
	s := f.s

	applePay := f.tx(t, f.gcu, "2026-04-01", -106729, "Withdrawal ACH APPLECARD GSBANK TYPE: PAYMENT", "Credit card payment")
	appleDep := f.tx(t, f.apple, "2026-03-31", 106729, "ACH DEPOSIT INTERNET TRANSFER FROM ACCOUNT ENDING IN 3212", "Transfer")

	gcuDad := f.tx(t, f.gcu, "2026-10-05", -5000, "Withdrawal ACH CAPITAL ONE TYPE: CRCARDPMT", "Transfer")
	deposit := f.tx(t, f.stcu, "2026-10-09", 5000, "Remote Deposit: 392523470", "Transfer")
	dadPay := f.tx(t, f.dad, "2026-10-04", 5000, "CAPITAL ONE AUTOPAY PYMT", "Credit card payment")
	if _, err := s.LinkTransfers([][2]int64{{gcuDad, deposit}}, 1); err != nil {
		t.Fatal(err)
	}

	loanOut := f.tx(t, f.gcu, "2026-10-03", -73700, "Withdrawal Home Banking To Loan 02", "Loan payment")
	loanIn := f.tx(t, f.loan, "2026-10-03", 63776, "Finance Charge/Fee: $99.24, Loan Payment Home Banking From Share 70", "Transfer")

	oneSided := f.tx(t, f.stcu, "2026-08-21", -30000, "Online Banking Transfer", "Transfer")
	f.tx(t, f.stcu, "2026-08-22", -4500, "GROCERY MART", "Groceries")

	audit, err := s.BuildTransferAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Missed) != 1 || audit.Missed[0].Out.ID != applePay || audit.Missed[0].In.ID != appleDep || !audit.Missed[0].HighConfidence {
		t.Fatalf("missed %+v", audit.Missed)
	}
	if len(audit.Suspicious) != 1 || audit.Suspicious[0].Suggest == nil ||
		audit.Suspicious[0].Suggest.A.ID != gcuDad || audit.Suspicious[0].Suggest.B.ID != dadPay {
		t.Fatalf("suspicious %+v", audit.Suspicious)
	}
	if len(audit.Loans) != 1 || !audit.Loans[0].Exact || audit.Loans[0].Out.ID != loanOut ||
		audit.Loans[0].Loan.ID != loanIn || audit.Loans[0].Interest != 9924 {
		t.Fatalf("loans %+v", audit.Loans)
	}
	if len(audit.OneSided) != 1 || audit.OneSided[0].ID != oneSided {
		t.Fatalf("one-sided %+v", audit.OneSided)
	}
	if audit.StaleSplits != 0 {
		t.Fatalf("stale splits %d", audit.StaleSplits)
	}

	// Link all high-confidence: the Apple payment.
	if _, n, err := s.LinkHighConfidenceTransfers(1); err != nil || n != 1 {
		t.Fatalf("link all n=%d err=%v", n, err)
	}
	if tx, _ := s.GetTransaction(applePay); tx.TransferID == nil || tx.CategoryName != "Transfer" {
		t.Fatalf("apple payment not linked as transfer: %+v", tx)
	}

	// Re-pair the $50 autopay; the stray deposit goes back to review.
	if _, err := s.RePairTransfer(gcuDad, dadPay, 1); err != nil {
		t.Fatal(err)
	}
	if tx, _ := s.GetTransaction(deposit); tx.TransferID != nil || !tx.NeedsReview {
		t.Fatalf("deposit after re-pair %+v", tx)
	}
	if tx, _ := s.GetTransaction(dadPay); tx.TransferID == nil {
		t.Fatalf("autopay not linked")
	}

	// Loan payment with interest keeps both balances and books the interest.
	gcuBefore, loanBefore := f.balance(t, f.gcu), f.balance(t, f.loan)
	undoID, err := s.PairLoanPaymentWithInterest(loanOut, loanIn, 9924, 1)
	if err != nil {
		t.Fatal(err)
	}
	if f.balance(t, f.gcu) != gcuBefore || f.balance(t, f.loan) != loanBefore {
		t.Fatalf("balances moved: gcu %d->%d loan %d->%d", gcuBefore, f.balance(t, f.gcu), loanBefore, f.balance(t, f.loan))
	}
	loanTx, _ := s.GetTransaction(loanIn)
	if loanTx.Amount != 73700 || loanTx.TransferID == nil {
		t.Fatalf("loan side %+v", loanTx)
	}
	var interestAmt int64
	if err := s.DB.QueryRow(`SELECT t.amount FROM transactions t JOIN categories c ON c.id=t.category_id
		WHERE t.account_id=? AND c.name='Loan interest' AND t.deleted_at IS NULL`, f.loan).Scan(&interestAmt); err != nil || interestAmt != -9924 {
		t.Fatalf("interest tx amount=%d err=%v", interestAmt, err)
	}

	// Undo restores the principal-only loan row and removes the interest.
	if err := s.Undo(undoID); err != nil {
		t.Fatal(err)
	}
	loanTx, _ = s.GetTransaction(loanIn)
	if loanTx.Amount != 63776 || loanTx.TransferID != nil || loanTx.CategoryName != "Transfer" {
		t.Fatalf("loan after undo %+v", loanTx)
	}
	if out, _ := s.GetTransaction(loanOut); out.TransferID != nil || out.CategoryName != "Loan payment" {
		t.Fatalf("payment after undo %+v", out)
	}
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(1) FROM transactions WHERE account_id=? AND payee_name='Interest'`, f.loan).Scan(&n)
	if n != 0 {
		t.Fatalf("interest tx survived undo")
	}

	// Counterpart into savings (before its anchor? no: anchor 2026-01-01, so it moves the balance).
	savBefore := f.balance(t, f.savings)
	res, err := s.CreateCounterparts([]int64{oneSided}, f.savings, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 || res.ChangedAnchor != 1 || f.balance(t, f.savings) != savBefore+30000 {
		t.Fatalf("counterpart %+v balance %d", res, f.balance(t, f.savings))
	}
	if tx, _ := s.GetTransaction(oneSided); tx.TransferID == nil {
		t.Fatal("one-sided not linked")
	}
	if err := s.Undo(res.UndoID); err != nil {
		t.Fatal(err)
	}
	if f.balance(t, f.savings) != savBefore {
		t.Fatalf("counterpart undo left savings at %d", f.balance(t, f.savings))
	}
}

func TestStaleTransferSplitsNormalize(t *testing.T) {
	f := newAuditFixture(t)
	s := f.s
	if _, err := s.CreateTransfer(store.TransferInput{FromAccountID: f.stcu, ToAccountID: f.savings,
		Amount: 2500, TransactionDate: "2026-05-01", CreatedBy: 1}); err != nil {
		t.Fatal(err)
	}
	groceries := catByName(t, s, "Groceries")
	if _, err := s.DB.Exec(`UPDATE transaction_splits SET category_id=? WHERE transaction_id =
		(SELECT id FROM transactions WHERE account_id=? AND amount=-2500)`, groceries, f.stcu); err != nil {
		t.Fatal(err)
	}
	if n, err := s.StaleTransferSplitCount(); err != nil || n != 1 {
		t.Fatalf("stale=%d err=%v", n, err)
	}
	if n, err := s.NormalizeTransferSplits(); err != nil || n != 1 {
		t.Fatalf("normalized=%d err=%v", n, err)
	}
	if n, _ := s.StaleTransferSplitCount(); n != 0 {
		t.Fatalf("still stale %d", n)
	}
}

func TestMarkNotTransferSendsToReview(t *testing.T) {
	f := newAuditFixture(t)
	id := f.tx(t, f.stcu, "2026-05-01", 5000, "Remote Deposit", "Transfer")
	undoID, err := f.s.MarkNotTransfer([]int64{id}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if tx, _ := f.s.GetTransaction(id); tx.CategoryID != nil || !tx.NeedsReview {
		t.Fatalf("after mark %+v", tx)
	}
	if err := f.s.Undo(undoID); err != nil {
		t.Fatal(err)
	}
	if tx, _ := f.s.GetTransaction(id); tx.CategoryName != "Transfer" || tx.NeedsReview {
		t.Fatalf("after undo %+v", tx)
	}
}
