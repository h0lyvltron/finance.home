package store_test

import (
	"testing"

	"finance.home/internal/models"
	"finance.home/internal/store"
)

func importCSV(t *testing.T, s *store.Store, acctID int64, csv string) int64 {
	t.Helper()
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
	return batchID
}

func TestCategorizeSimilarRememberAndUndo(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catID := firstExpenseCat(t, s)

	batch := importCSV(t, s, acctID, "Date,Description,Amount\n"+
		"2024-06-01,AMAZON MKTPL*5151O6EQ0,-10.00\n"+
		"2024-06-02,AMAZON MKTPL*BC7L015P0,-20.00\n"+
		"2024-06-03,CHEVRON 0207253,-30.00\n")
	if n, err := s.CommitImport(batch, 1); err != nil || n != 3 {
		t.Fatalf("commit n=%d err=%v", n, err)
	}
	review, _ := s.ListNeedsReview(100)
	if len(review) != 3 {
		t.Fatalf("review=%d", len(review))
	}

	groups := store.GroupForReview(review)
	if len(groups) != 2 || groups[0].Key != "AMAZON MKTPL" || len(groups[0].Items) != 2 {
		t.Fatalf("groups=%+v", groups)
	}

	similar, err := s.SimilarOpenTransactions("amazon  mktpl")
	if err != nil || len(similar) != 2 {
		t.Fatalf("similar=%v err=%v", similar, err)
	}

	changed, undoID, err := s.CategorizeTransactions(similar, catID, "", 1)
	if err != nil || len(changed) != 2 || undoID == "" {
		t.Fatalf("changed=%v undo=%q err=%v", changed, undoID, err)
	}
	if n, _ := s.NeedsReviewCount(); n != 1 {
		t.Fatalf("review count after categorize = %d, want 1", n)
	}
	if rest, _ := s.SimilarOpenTransactions("AMAZON MKTPL"); len(rest) != 0 {
		t.Fatalf("categorized rows still open: %v", rest)
	}

	if err := s.Undo(undoID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.NeedsReviewCount(); n != 3 {
		t.Fatalf("review count after undo = %d, want 3", n)
	}
	tx, _ := s.GetTransaction(changed[0])
	if tx.CategoryID != nil {
		t.Fatalf("undo left category %v", *tx.CategoryID)
	}

	ch, err := s.RememberPayeeCategory("amazon mktpl", catID)
	if err != nil || ch == nil || !ch.Created {
		t.Fatalf("remember change=%+v err=%v", ch, err)
	}
	if ch, err := s.RememberPayeeCategory("AMAZON MKTPL", catID); err != nil || ch.Created {
		t.Fatalf("second remember should update, change=%+v err=%v", ch, err)
	}
	rules, _ := s.ListPayeeRules()
	if len(rules) != 1 || rules[0].SetCategoryID == nil || *rules[0].SetCategoryID != catID {
		t.Fatalf("rules=%+v", rules)
	}

	batch2 := importCSV(t, s, acctID, "Date,Description,Amount\n2024-07-01,AMAZON MKTPL*5X7V64U91,-5.00\n")
	rows, _, _ := s.ListStagingRows(store.StagingFilter{BatchID: batch2, PageSize: 10})
	if rows[0].CategoryID == nil || *rows[0].CategoryID != catID {
		t.Fatalf("remembered rule not applied on import: %+v", rows[0])
	}
}

func TestStagingUsualCategoryAndManualPick(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catID := firstExpenseCat(t, s)

	if _, err := s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "SP HUBMAN AND CHUBGIRL", Amount: -1500,
		TransactionDate: "2024-05-01", CategoryID: &catID, CreatedBy: 1,
	}); err != nil {
		t.Fatal(err)
	}
	batch := importCSV(t, s, acctID, "Date,Description,Amount\n"+
		"2024-06-01,SP HUBMAN AND CHUBGI,-12.00\n"+
		"2024-06-02,ANDERSONS,-8.00\n")
	rows, _, _ := s.ListStagingRows(store.StagingFilter{BatchID: batch, PageSize: 10})
	if rows[0].CategoryID == nil || *rows[0].CategoryID != catID || rows[0].MatchedRuleName != store.SuggestedCategoryLabel || !rows[0].NeedsReview {
		t.Fatalf("one prior transaction should suggest, not auto-file: %+v", rows[0])
	}
	if rows[1].CategoryID != nil {
		t.Fatalf("unexpected category on unknown merchant: %+v", rows[1])
	}

	similar, _ := s.SimilarStagingRows(batch, "andersons")
	if len(similar) != 1 {
		t.Fatalf("similar staging=%v", similar)
	}
	if changed, err := s.CategorizeStagingRows(batch, similar, catID, ""); err != nil || len(changed) != 1 {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, err := s.CommitImport(batch, 1); err != nil {
		t.Fatal(err)
	}
	review, _ := s.ListNeedsReview(10)
	if len(review) != 1 || review[0].PayeeName != "SP HUBMAN AND CHUBGI" {
		t.Fatalf("hand-picked row should skip review; review=%+v", review)
	}

	sg, err := s.NewSuggester()
	if err != nil {
		t.Fatal(err)
	}
	if got := sg.For("ANDERSONS #2", -100, acctID); len(got) == 0 || got[0].ID != catID {
		t.Fatalf("suggest=%v", got)
	}
}

func secondExpenseCat(t *testing.T, s *store.Store, not int64) int64 {
	t.Helper()
	cats, err := s.ListCategories("expense", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cats {
		if c.ID != not {
			return c.ID
		}
	}
	t.Fatal("need two expense categories")
	return 0
}

func TestUndoAlsoRevertsRememberedRule(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catA := firstExpenseCat(t, s)
	catB := secondExpenseCat(t, s, catA)

	batch := importCSV(t, s, acctID, "Date,Description,Amount\n2024-06-01,COSTCO WHSE #0123,-50.00\n")
	if _, err := s.CommitImport(batch, 1); err != nil {
		t.Fatal(err)
	}
	review, _ := s.ListNeedsReview(10)
	ids := []int64{review[0].ID}

	// First pick creates the rule; undo removes it.
	_, undo1, err := s.CategorizeTransactions(ids, catA, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := s.RememberPayeeCategory("COSTCO WHSE", catA)
	if err := s.AttachRuleUndo(undo1, ch); err != nil {
		t.Fatal(err)
	}
	if err := s.Undo(undo1); err != nil {
		t.Fatal(err)
	}
	if rules, _ := s.ListPayeeRules(); len(rules) != 0 {
		t.Fatalf("undo should delete the new rule: %+v", rules)
	}

	// Pick A, then change to B; undoing the change puts both back to A.
	_, undoA, _ := s.CategorizeTransactions(ids, catA, "", 1)
	chA, _ := s.RememberPayeeCategory("COSTCO WHSE", catA)
	_ = s.AttachRuleUndo(undoA, chA)
	_, undoB, err := s.CategorizeTransactions(ids, catB, "", 1)
	if err != nil || undoB == "" {
		t.Fatalf("change undo=%q err=%v", undoB, err)
	}
	chB, _ := s.RememberPayeeCategory("COSTCO WHSE", catB)
	if chB.Created || chB.PrevCategoryID == nil || *chB.PrevCategoryID != catA {
		t.Fatalf("change should update the rule: %+v", chB)
	}
	_ = s.AttachRuleUndo(undoB, chB)
	if err := s.Undo(undoB); err != nil {
		t.Fatal(err)
	}
	rules, _ := s.ListPayeeRules()
	if len(rules) != 1 || *rules[0].SetCategoryID != catA {
		t.Fatalf("rule after undo=%+v", rules)
	}
	if tx, _ := s.GetTransaction(ids[0]); tx.CategoryID == nil || *tx.CategoryID != catA {
		t.Fatalf("transaction after undo=%+v", tx)
	}

	// Re-picking the same category is a no-op.
	if changed, undo, _ := s.CategorizeTransactions(ids, catA, "", 1); len(changed) != 0 || undo != "" {
		t.Fatalf("same category should not change anything: %v %q", changed, undo)
	}
}

func TestRuleViewsMisfiledApplyAndDeleteUndo(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catA := firstExpenseCat(t, s)
	catB := secondExpenseCat(t, s, catA)

	for _, p := range []string{"TARGET 00011189", "TARGET 00022222", "SAFEWAY #12"} {
		if _, err := s.CreateTransaction(store.TxInput{
			AccountID: acctID, PayeeName: p, Amount: -1000,
			TransactionDate: "2024-05-01", CategoryID: &catB, CreatedBy: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	id, err := s.CreatePayeeRule(store.PayeeRuleInput{Name: "TARGET → x", PayeeValue: "target", SetCategoryID: &catA})
	if err != nil {
		t.Fatal(err)
	}
	views, err := s.RuleViews()
	if err != nil || len(views) != 1 || views[0].Matches != 2 || views[0].Misfiled != 2 {
		t.Fatalf("views=%+v err=%v", views, err)
	}

	ids, rule, err := s.MisfiledByRule(id)
	if err != nil || len(ids) != 2 || *rule.SetCategoryID != catA {
		t.Fatalf("misfiled=%v err=%v", ids, err)
	}
	if _, _, err := s.CategorizeTransactions(ids, catA, "", 1); err != nil {
		t.Fatal(err)
	}
	if views, _ := s.RuleViews(); views[0].Misfiled != 0 {
		t.Fatalf("still misfiled after apply: %+v", views[0])
	}

	ref, err := s.SetPayeeRuleCategory(id, catB)
	if err != nil || ref.ID != catB {
		t.Fatalf("set category ref=%+v err=%v", ref, err)
	}
	if views, _ := s.RuleViews(); views[0].Misfiled != 2 {
		t.Fatalf("misfiled after category change=%+v", views[0])
	}

	if err := s.SetPayeeRuleEnabled(id, false); err != nil {
		t.Fatal(err)
	}
	if views, _ := s.RuleViews(); views[0].Matches != 0 {
		t.Fatalf("disabled rule should not match: %+v", views[0])
	}

	undoID, err := s.DeletePayeeRuleUndoable(id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rules, _ := s.ListPayeeRules(); len(rules) != 0 {
		t.Fatalf("rule not deleted: %+v", rules)
	}
	if err := s.Undo(undoID); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetPayeeRule(id)
	if err != nil || r.Enabled || r.PayeeValue != "target" || *r.SetCategoryID != catB {
		t.Fatalf("restored rule=%+v err=%v", r, err)
	}
}

func TestConfidentUsualSkipsReview(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catID := firstExpenseCat(t, s)
	for _, p := range []string{"WL *STEAM PURCHASE", "STEAMGAMES.COM 4259522"} {
		if _, err := s.CreateTransaction(store.TxInput{
			AccountID: acctID, PayeeName: p, Amount: -999,
			TransactionDate: "2024-05-01", CategoryID: &catID, CreatedBy: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	batch := importCSV(t, s, acctID, "Date,Description,Amount\n2024-06-01,STEAM PURCHASE 998877,-9.99\n")
	rows, _, _ := s.ListStagingRows(store.StagingFilter{BatchID: batch, PageSize: 10})
	if rows[0].CategoryID == nil || *rows[0].CategoryID != catID || rows[0].MatchedRuleName != store.UsualCategoryLabel || rows[0].NeedsReview {
		t.Fatalf("confident steam match should auto-file: %+v", rows[0])
	}
}

func TestSplitAmountHistoryStaysASuggestion(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	groceries := firstExpenseCat(t, s)
	shopping := secondExpenseCat(t, s, groceries)
	for i := 0; i < 3; i++ {
		if _, err := s.CreateTransaction(store.TxInput{
			AccountID: acctID, PayeeName: "ANDERSONS", Amount: -2500 - int64(i*100),
			TransactionDate: "2024-05-01", CategoryID: &groceries, CreatedBy: 1,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateTransaction(store.TxInput{
			AccountID: acctID, PayeeName: "ANDERSONS", Amount: -2800 - int64(i*100),
			TransactionDate: "2024-05-02", CategoryID: &shopping, CreatedBy: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	batch := importCSV(t, s, acctID, "Date,Description,Amount\n2024-06-01,ANDERSONS,-26.00\n")
	rows, _, _ := s.ListStagingRows(store.StagingFilter{BatchID: batch, PageSize: 10})
	if rows[0].MatchedRuleName != store.SuggestedCategoryLabel || !rows[0].NeedsReview {
		t.Fatalf("overlapping amounts should not auto-file: %+v", rows[0])
	}
}

func TestGroupForReviewMergesTruncatedNames(t *testing.T) {
	cat := int64(7)
	items := []models.Transaction{
		{ID: 1, PayeeName: "SP HUBMAN AND CHUBGI", Amount: -100, CategoryID: &cat, CategoryName: "Gifts"},
		{ID: 2, PayeeName: "SP HUBMAN AND CHUBGIRL", Amount: -200, CategoryID: &cat, CategoryName: "Gifts"},
		{ID: 3, PayeeName: "TARGET        00011189", Amount: -50},
	}
	groups := store.GroupForReview(items)
	if len(groups) != 2 || len(groups[0].Items) != 2 || groups[0].IDs() != "1 2" {
		t.Fatalf("groups=%+v", groups)
	}
	if groups[0].CategoryName != "Gifts" || groups[0].Total != -300 {
		t.Fatalf("group summary=%+v", groups[0])
	}
	steam := []models.Transaction{
		{ID: 1, PayeeName: "WL *STEAM PURCHASE", Amount: -100},
		{ID: 2, PayeeName: "STEAMGAMES.COM 4259522", Amount: -200},
		{ID: 3, PayeeName: "COSTCO GAS", Amount: -50},
	}
	sg := store.GroupForReview(steam)
	if len(sg) != 2 || len(sg[0].Items) != 2 {
		t.Fatalf("steam groups=%+v", sg)
	}
}
