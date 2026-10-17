package store_test

import (
	"path/filepath"
	"testing"

	"finance.home/internal/db"
	"finance.home/internal/store"
)

func setupStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatal(err)
	}
	s := store.New(sqlDB)
	if err := s.CompleteSetup(store.SetupInput{
		HouseholdName: "Test House",
		Currency:      "USD",
		Timezone:      "UTC",
		PersonAName:   "Alex",
		PersonAPass:   "password1",
		PersonBName:   "Blake",
		PersonBPass:   "password1",
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func ensureChecking(t *testing.T, s *store.Store) int64 {
	t.Helper()
	accts, err := s.ListAccounts(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(accts) > 0 {
		return accts[0].ID
	}
	id, err := s.CreateAccount(store.AccountInput{
		Name: "Checking", Type: "checking", Owner: "joint",
		AnchorBalance: 100000, AnchorDate: "2024-01-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func firstExpenseCat(t *testing.T, s *store.Store) int64 {
	t.Helper()
	cats, err := s.ListCategories("expense", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cats {
		if c.ParentID != nil && !c.Protected {
			return c.ID
		}
	}
	for _, c := range cats {
		if !c.Protected {
			return c.ID
		}
	}
	t.Fatal("no category")
	return 0
}

func firstIncomeCat(t *testing.T, s *store.Store) int64 {
	t.Helper()
	cats, err := s.ListCategories("income", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cats {
		if c.ParentID != nil && !c.Protected {
			return c.ID
		}
	}
	for _, c := range cats {
		if !c.Protected {
			return c.ID
		}
	}
	t.Fatal("no income category")
	return 0
}

func TestImportRoundTrip(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catID := firstExpenseCat(t, s)

	_, err := s.CreatePayeeRule(store.PayeeRuleInput{
		Name: "Coffee", PayeeMatch: "contains", PayeeValue: "STARBUCKS",
		SetCategoryID: &catID, RenamePayee: "Starbucks",
	})
	if err != nil {
		t.Fatal(err)
	}

	csv := "Date,Description,Amount\n2024-06-01,STARBUCKS #123,-5.25\n2024-06-02,UNKNOWN STORE,-20.00\n"
	batchID, err := s.CreateImportBatch(store.CreateBatchInput{
		Filename: "bank.csv", RawCSV: csv, CreatedBy: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.MapAndStage(store.MapBatchInput{
		BatchID: batchID, AccountID: acctID, ProfileID: 0,
		SaveAs: "Test bank",
		Profile: store.MappingProfileInput{
			Name: "Test bank", DateColumn: "Date", DateFormat: "auto",
			AmountMode: "signed", AmountColumn: "Amount", PayeeColumn: "Description",
			HasHeader: true, Delimiter: ",",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	rows, total, err := s.ListStagingRows(store.StagingFilter{BatchID: batchID, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total=%d", total)
	}
	if rows[0].MatchedRuleName != "Coffee" {
		t.Fatalf("rule not applied: %+v", rows[0])
	}
	if rows[0].RenamePayee != "Starbucks" {
		t.Fatalf("rename %+v", rows[0])
	}
	if rows[1].MatchedRuleID != nil {
		t.Fatalf("unexpected rule on unknown")
	}

	n, err := s.CommitImport(batchID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("imported %d", n)
	}
	review, err := s.NeedsReviewCount()
	if err != nil || review < 1 {
		t.Fatalf("review count %d err %v", review, err)
	}

	if err := s.UndoImportBatch(batchID, 1); err != nil {
		t.Fatal(err)
	}
	b, _ := s.GetImportBatch(batchID)
	if b.Status != "undone" {
		t.Fatalf("status %s", b.Status)
	}
}

func TestManualMerge(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catID := firstExpenseCat(t, s)
	_, err := s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "Manual Coffee", Notes: "typed",
		Amount: -525, TransactionDate: "2024-06-02", Person: "a",
		CategoryID: &catID, CreatedBy: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	csv := "Date,Description,Amount\n2024-06-01,STARBUCKS,-5.25\n"
	batchID, err := s.CreateImportBatch(store.CreateBatchInput{Filename: "m.csv", RawCSV: csv, CreatedBy: 1})
	if err != nil {
		t.Fatal(err)
	}
	err = s.MapAndStage(store.MapBatchInput{
		BatchID: batchID, AccountID: acctID,
		Profile: store.MappingProfileInput{
			Name: "p", DateColumn: "Date", AmountMode: "signed", AmountColumn: "Amount",
			PayeeColumn: "Description", HasHeader: true, Delimiter: ",",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, _, _ := s.ListStagingRows(store.StagingFilter{BatchID: batchID, PageSize: 10})
	if rows[0].Action != "merge" || rows[0].MatchTxID == nil {
		t.Fatalf("expected merge suggestion %+v", rows[0])
	}
	n, err := s.CommitImport(batchID, 1)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	tx, err := s.GetTransaction(*rows[0].MatchTxID)
	if err != nil {
		t.Fatal(err)
	}
	if tx.PayeeName != "Manual Coffee" || tx.Notes != "typed" || tx.Person != "a" {
		t.Fatalf("manual fields lost %+v", tx)
	}
	if tx.TransactionDate != "2024-06-01" || tx.Status != "cleared" {
		t.Fatalf("bank fields not adopted %+v", tx)
	}
}
