package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"finance.home/internal/db"
	"finance.home/internal/importcsv"
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

func TestReportsIncludeUncategorizedImports(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	csv := "Date,Description,Amount\n2024-06-01,GROCERY MART,-80.00\n2024-06-03,PAYROLL,1500.00\n2024-07-02,GAS STATION,-40.00\n"
	batchID, err := s.CreateImportBatch(store.CreateBatchInput{Filename: "bank.csv", RawCSV: csv, CreatedBy: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MapAndStage(store.MapBatchInput{
		BatchID: batchID, AccountID: acctID, SaveAs: "bank",
		Profile: store.MappingProfileInput{
			DateColumn: "Date", AmountMode: "signed", AmountColumn: "Amount",
			PayeeColumn: "Description", HasHeader: true, Delimiter: ",",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CommitImport(batchID, 1); err != nil || n != 3 {
		t.Fatalf("commit n=%d err=%v", n, err)
	}

	h, _ := s.Household()
	now := time.Date(2024, 7, 15, 12, 0, 0, 0, time.UTC)
	f := store.QueryFilter{DatePreset: "custom", DateFrom: "2024-06-01", DateTo: "2024-07-31"}

	points, err := s.CashFlow(f, "month", h, now)
	if err != nil {
		t.Fatal(err)
	}
	var income, expense int64
	for _, p := range points {
		income += p.Income
		expense += p.Expense
	}
	if income != 150000 || expense != 12000 {
		t.Fatalf("cash flow income=%d expense=%d, want 150000/12000", income, expense)
	}

	segs, grand, err := s.SpendingExplore(f, "category", 8, h, now)
	if err != nil {
		t.Fatal(err)
	}
	if grand != 12000 || len(segs) != 1 || segs[0].Label != store.UncategorizedLabel || segs[0].SharePct != 100 {
		t.Fatalf("explore grand=%d segs=%+v", grand, segs)
	}

	pt, err := s.SegmentPivot(f, "category", "month", h, now)
	if err != nil {
		t.Fatal(err)
	}
	if pt.Grand != 12000 || len(pt.RowKeys) != 1 {
		t.Fatalf("pivot grand=%d rows=%v", pt.Grand, pt.RowKeys)
	}

	shared := f
	shared.People = []string{"shared"}
	if _, g, err := s.SpendingExplore(shared, "category", 8, h, now); err != nil || g != 12000 {
		t.Fatalf("person filter dropped uncategorized: grand=%d err=%v", g, err)
	}

	n, spend, err := s.UncategorizedSummary(f, h, now)
	if err != nil || n != 3 || spend != 12000 {
		t.Fatalf("uncategorized n=%d spend=%d err=%v", n, spend, err)
	}
}

func TestCapitalOneDebitCreditImport(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	csv := "Transaction Date,Posted Date,Card No.,Description,Category,Debit,Credit\n" +
		"2026-10-12,2026-10-13,1234,AMAZON MKTPL*5151O6EQ0,Merchandise,25.99,\n" +
		"2026-10-10,2026-10-11,1234,CAPITAL ONE MOBILE PYMT,Payment/Credit,,150.00\n" +
		"10/09/2026,10/10/2026,1234,BEST BUY 00012345,Merchandise,\"$1,234.56\",\n"
	stage := func(profileID int64) int64 {
		t.Helper()
		batchID, err := s.CreateImportBatch(store.CreateBatchInput{Filename: "capone.csv", RawCSV: csv, CreatedBy: 1})
		if err != nil {
			t.Fatal(err)
		}
		headers, err := s.BatchHeaders(batchID)
		if err != nil {
			t.Fatal(err)
		}
		in := store.MapBatchInput{BatchID: batchID, AccountID: acctID, ProfileID: profileID, SaveAs: "Capital One"}
		if profileID == 0 {
			g := importcsv.GuessColumns(headers)
			in.Profile = store.MappingProfileInput{
				DateColumn: g.DateColumn, DateFormat: g.DateFormat, AmountMode: g.AmountMode,
				DebitColumn: g.DebitColumn, CreditColumn: g.CreditColumn,
				PayeeColumn: g.PayeeColumn, NotesColumn: g.NotesColumn, HasHeader: true, Delimiter: ",",
			}
		}
		if err := s.MapAndStage(in); err != nil {
			t.Fatal(err)
		}
		return batchID
	}

	batchID := stage(0)
	rows, total, err := s.ListStagingRows(store.StagingFilter{BatchID: batchID, PageSize: 50})
	if err != nil || total != 3 {
		t.Fatalf("total=%d err=%v", total, err)
	}
	want := []struct {
		date   string
		amount int64
		payee  string
	}{
		{"2026-10-12", -2599, "AMAZON MKTPL*5151O6EQ0"},
		{"2026-10-10", 15000, "CAPITAL ONE MOBILE PYMT"},
		{"2026-10-09", -123456, "BEST BUY 00012345"},
	}
	for i, w := range want {
		r := rows[i]
		if r.Exception != "" || r.TransactionDate != w.date || r.Amount != w.amount || r.PayeeName != w.payee ||
			r.Notes != "" || r.Action != "import" {
			t.Fatalf("row%d %+v", i, r)
		}
	}
	if n, err := s.CommitImport(batchID, 1); err != nil || n != 3 {
		t.Fatalf("commit n=%d err=%v", n, err)
	}

	profiles, err := s.ListMappingProfiles()
	if err != nil {
		t.Fatal(err)
	}
	headers := []string{"Transaction Date", "Posted Date", "Card No.", "Description", "Category", "Debit", "Credit"}
	mp := store.MatchingProfile(profiles, headers)
	if mp == nil || mp.Name != "Capital One" || mp.AmountMode != "debit_credit" || mp.AccountID == nil || *mp.AccountID != acctID {
		t.Fatalf("saved profile not matched: %+v", mp)
	}
	if other := store.MatchingProfile(profiles, []string{"Date", "Description", "Amount"}); other != nil {
		t.Fatalf("unexpected match for other layout: %+v", other)
	}

	again := stage(mp.ID)
	rows, _, err = s.ListStagingRows(store.StagingFilter{BatchID: again, PageSize: 50})
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	for i, r := range rows {
		if r.DuplicateTxID == nil || r.Action != "skip" {
			t.Fatalf("re-import row%d not flagged duplicate: %+v", i, r)
		}
	}
	if after, _ := s.ListMappingProfiles(); len(after) != len(profiles) {
		t.Fatalf("reusing a profile created another one")
	}
}

func TestTransferPairsUseCounterpartOnce(t *testing.T) {
	s := setupStore(t)
	checking := ensureChecking(t, s)
	card, err := s.CreateAccount(store.AccountInput{Name: "Card", Type: "credit", Owner: "joint", AnchorDate: "2024-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	catID := firstExpenseCat(t, s)
	payment, err := s.CreateTransaction(store.TxInput{
		AccountID: checking, PayeeName: "MOBILE PMT CAPITAL ONE", Amount: -8500,
		TransactionDate: "2026-05-07", Person: "shared", CategoryID: &catID, CreatedBy: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	csv := "Transaction Date,Posted Date,Card No.,Description,Category,Debit,Credit\n" +
		"2026-05-08,2026-05-08,1234,CAPITAL ONE MOBILE PYMT,Payment/Credit,,85.00\n" +
		"2026-05-06,2026-05-06,1234,CAPITAL ONE MOBILE PYMT,Payment/Credit,,85.00\n" +
		"2026-05-06,2026-05-06,1234,COFFEE,Dining,4.50,\n"
	stage := func() int64 {
		t.Helper()
		batchID, err := s.CreateImportBatch(store.CreateBatchInput{Filename: "capone.csv", RawCSV: csv, CreatedBy: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MapAndStage(store.MapBatchInput{
			BatchID: batchID, AccountID: card, SaveAs: "Capital One",
			Profile: store.MappingProfileInput{
				DateColumn: "Transaction Date", AmountMode: "debit_credit", DebitColumn: "Debit",
				CreditColumn: "Credit", PayeeColumn: "Description", HasHeader: true, Delimiter: ",",
			},
		}); err != nil {
			t.Fatal(err)
		}
		return batchID
	}
	transfers := func() int {
		var n int
		if err := s.DB.QueryRow(`SELECT COUNT(1) FROM transfers`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	batchID := stage()
	rows, _, err := s.ListStagingRows(store.StagingFilter{BatchID: batchID, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	paired := 0
	for _, r := range rows {
		if r.Action == "transfer_pair" {
			paired++
			if r.TransferTxID == nil || *r.TransferTxID != payment {
				t.Fatalf("paired with wrong tx: %+v", r)
			}
		}
	}
	if paired != 1 {
		t.Fatalf("want 1 transfer pair for a single counterpart, got %d", paired)
	}

	// Staging written by the old matcher could still claim the same
	// counterpart twice; commit must cope instead of hitting the UNIQUE index.
	if _, err := s.DB.Exec(`UPDATE import_staging_rows SET action='transfer_pair', transfer_tx_id=?
		WHERE batch_id=? AND amount=8500`, payment, batchID); err != nil {
		t.Fatal(err)
	}
	n, err := s.CommitImport(batchID, 1)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if n != 3 {
		t.Fatalf("imported %d, want 3", n)
	}
	if got := transfers(); got != 1 {
		t.Fatalf("transfers=%d, want 1", got)
	}
	tx, err := s.GetTransaction(payment)
	if err != nil || tx.TransferID == nil {
		t.Fatalf("checking payment not linked: %+v err=%v", tx, err)
	}
	if _, err := s.CommitImport(batchID, 1); err == nil {
		t.Fatal("second commit of the same batch should fail")
	}

	csv = "Transaction Date,Posted Date,Card No.,Description,Category,Debit,Credit\n" +
		"2026-05-07,2026-05-07,1234,ONLINE PAYMENT THANK YOU,Payment/Credit,,85.00\n"
	again := stage()
	rows, _, _ = s.ListStagingRows(store.StagingFilter{BatchID: again, PageSize: 50})
	if len(rows) != 1 || rows[0].Action != "import" || rows[0].TransferTxID != nil {
		t.Fatalf("already-linked counterpart suggested again: %+v", rows)
	}
}

func TestMapRejectsStaleColumnsAndCancel(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	csv := "Barclays Bank Delaware\nAccount Number: XXXX7091\n \n" +
		"Transaction Date,Description,Category,Amount\n10/15/2026,\"AMAZON\",\"DEBIT\",\"-46.12\"\n"
	batchID, err := s.CreateImportBatch(store.CreateBatchInput{Filename: "cc.csv", RawCSV: csv, CreatedBy: 1})
	if err != nil {
		t.Fatal(err)
	}
	headers, err := s.BatchHeaders(batchID)
	if err != nil || len(headers) != 4 || headers[0] != "Transaction Date" {
		t.Fatalf("headers %q err %v", headers, err)
	}
	before, _ := s.ListMappingProfiles()
	err = s.MapAndStage(store.MapBatchInput{
		BatchID: batchID, AccountID: acctID, SaveAs: "stale",
		Profile: store.MappingProfileInput{
			DateColumn: "Barclays Bank Delaware", AmountMode: "signed",
			AmountColumn: "Barclays Bank Delaware", HasHeader: true, Delimiter: ",",
		},
	})
	if err == nil {
		t.Fatal("expected stale-column error")
	}
	if after, _ := s.ListMappingProfiles(); len(after) != len(before) {
		t.Fatalf("rejected mapping left a profile behind")
	}

	err = s.MapAndStage(store.MapBatchInput{
		BatchID: batchID, AccountID: acctID, SaveAs: "Barclays",
		Profile: store.MappingProfileInput{
			DateColumn: "Transaction Date", AmountMode: "signed", AmountColumn: "Amount",
			PayeeColumn: "Description", HasHeader: true, Delimiter: ",",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	n, _, err := s.StagingErrorSummary(batchID)
	if err != nil || n != 0 {
		t.Fatalf("errors=%d err=%v", n, err)
	}

	if err := s.CancelImportBatch(batchID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetImportBatch(batchID); err == nil {
		t.Fatal("batch still exists after cancel")
	}
	if rows, total, _ := s.ListStagingRows(store.StagingFilter{BatchID: batchID}); total != 0 || len(rows) != 0 {
		t.Fatalf("staging rows left behind: %d", total)
	}
}
