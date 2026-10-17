package store_test

import (
	"testing"

	"finance.home/internal/models"
	"finance.home/internal/store"
)

func addTx(t *testing.T, s *store.Store, acctID, catID int64, payee string, amount int64, date string) int64 {
	t.Helper()
	id, err := s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: payee, Amount: amount,
		TransactionDate: date, CategoryID: &catID, CreatedBy: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func tagNames(t *testing.T, s *store.Store, id int64) []string {
	t.Helper()
	m, err := s.TagsForTransactions([]int64{id})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tg := range m[id] {
		out = append(out, tg.Name)
	}
	return out
}

func mustTag(t *testing.T, s *store.Store, name string) models.Tag {
	t.Helper()
	tg, _, err := s.EnsureActiveTag(name)
	if err != nil {
		t.Fatal(err)
	}
	return tg
}

func TestTagApplyRemoveAndUndo(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catID := firstExpenseCat(t, s)
	a := addTx(t, s, acctID, catID, "HILTON HAWAIIAN VILLAGE", -45000, "2026-06-03")
	b := addTx(t, s, acctID, catID, "ALOHA POKE", -2500, "2026-06-04")
	c := addTx(t, s, acctID, catID, "SAFEWAY", -8000, "2026-06-20")

	trip, created, err := s.EnsureActiveTag("  #Hawaii   trip 2026 ")
	if err != nil || !created || trip.Name != "Hawaii trip 2026" {
		t.Fatalf("tag=%+v created=%v err=%v", trip, created, err)
	}
	if again, created, _ := s.EnsureActiveTag("hawaii TRIP 2026"); created || again.ID != trip.ID {
		t.Fatalf("case-insensitive ensure made a new tag: %+v", again)
	}
	if _, err := store.CleanTagName("a, b"); err == nil {
		t.Fatal("commas should be rejected")
	}
	reimb := mustTag(t, s, "Reimbursable")

	changed, undo1, err := s.TagTransactions([]int64{a, b}, []int64{trip.ID}, nil, 1)
	if err != nil || len(changed) != 2 || undo1 == "" {
		t.Fatalf("changed=%v undo=%q err=%v", changed, undo1, err)
	}
	if got := tagNames(t, s, a); len(got) != 1 || got[0] != "Hawaii trip 2026" {
		t.Fatalf("tags on a=%v", got)
	}
	if changed, undo, _ := s.TagTransactions([]int64{a, b}, []int64{trip.ID}, nil, 1); len(changed) != 0 || undo != "" {
		t.Fatalf("re-tagging should be a no-op: %v %q", changed, undo)
	}

	f := store.QueryFilter{TagIDs: []int64{trip.ID}}
	sum, err := s.SummarizeTransactions(f)
	if err != nil || sum.Count != 2 || sum.Out != -47500 || sum.FirstDate != "2026-06-03" || sum.LastDate != "2026-06-04" {
		t.Fatalf("summary=%+v err=%v", sum, err)
	}
	if ids, _ := s.FilteredTransactionIDs(f); len(ids) != 2 {
		t.Fatalf("filtered ids=%v", ids)
	}

	// Swap: add Reimbursable, remove the trip, in one change.
	changed, undo2, err := s.TagTransactions([]int64{a, c}, []int64{reimb.ID}, []int64{trip.ID}, 1)
	if err != nil || len(changed) != 2 {
		t.Fatalf("swap changed=%v err=%v", changed, err)
	}
	if got := tagNames(t, s, a); len(got) != 1 || got[0] != "Reimbursable" {
		t.Fatalf("tags on a after swap=%v", got)
	}
	counts, _ := s.TagCounts([]int64{a, b, c})
	if counts[trip.ID] != 1 || counts[reimb.ID] != 2 {
		t.Fatalf("counts=%v", counts)
	}

	if err := s.Undo(undo2); err != nil {
		t.Fatal(err)
	}
	if got := tagNames(t, s, a); len(got) != 1 || got[0] != "Hawaii trip 2026" {
		t.Fatalf("tags on a after undo=%v", got)
	}
	if got := tagNames(t, s, c); len(got) != 0 {
		t.Fatalf("tags on c after undo=%v", got)
	}
	if err := s.Undo(undo1); err != nil {
		t.Fatal(err)
	}
	if counts, _ := s.TagCounts([]int64{a, b, c}); len(counts) != 0 {
		t.Fatalf("counts after full undo=%v", counts)
	}

	opts, err := s.TagOptions()
	if err != nil || len(opts) != 2 {
		t.Fatalf("options=%+v err=%v", opts, err)
	}
}

func TestTagBulkByFilterAndSimilar(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catID := firstExpenseCat(t, s)
	var june []int64
	for i, p := range []string{"UBER *TRIP 123", "UBER *TRIP 456", "KONA COFFEE", "UBER *EATS"} {
		june = append(june, addTx(t, s, acctID, catID, p, int64(-1000*(i+1)), "2026-06-1"+string(rune('0'+i))))
	}
	addTx(t, s, acctID, catID, "UBER *TRIP 999", -500, "2026-07-01")

	f := store.QueryFilter{DatePreset: "custom", DateFrom: "2026-06-01", DateTo: "2026-06-30"}
	ids, err := s.FilteredTransactionIDs(f)
	if err != nil || len(ids) != 4 {
		t.Fatalf("filtered=%v err=%v", ids, err)
	}
	similar, err := s.SimilarInFilter(f, "uber  *trip")
	if err != nil || len(similar) != 2 {
		t.Fatalf("similar=%v err=%v", similar, err)
	}

	trip := mustTag(t, s, "Hawaii trip 2026")
	changed, undoID, err := s.TagTransactions(ids, []int64{trip.ID}, nil, 1)
	if err != nil || len(changed) != 4 {
		t.Fatalf("bulk changed=%v err=%v", changed, err)
	}
	v, err := s.GetTag(trip.ID)
	if err != nil || v.Count != 4 || v.Out != -10000 {
		t.Fatalf("tag view=%+v err=%v", v, err)
	}
	if _, err := s.SoftDeleteTransaction(june[0], 1); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetTag(trip.ID); v.Count != 3 {
		t.Fatalf("deleted transactions should not count: %+v", v)
	}
	if err := s.Undo(undoID); err != nil {
		t.Fatal(err)
	}
	if counts, _ := s.TagCounts(ids); counts[trip.ID] != 0 {
		t.Fatalf("bulk undo left tags: %v", counts)
	}
}

func TestTagManagerRenameMergeArchiveDelete(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	catID := firstExpenseCat(t, s)
	a := addTx(t, s, acctID, catID, "CAKE SHOP", -3000, "2026-03-01")
	b := addTx(t, s, acctID, catID, "BALLOONS", -1500, "2026-03-02")

	bday := mustTag(t, s, "Bday")
	birthday := mustTag(t, s, "Birthday")
	if _, _, err := s.TagTransactions([]int64{a, b}, []int64{bday.ID}, nil, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TagTransactions([]int64{a}, []int64{birthday.ID}, nil, 1); err != nil {
		t.Fatal(err)
	}
	ruleID, err := s.CreatePayeeRule(store.PayeeRuleInput{Name: "Cakes", PayeeValue: "cake", SetCategoryID: &catID, SetTags: "bday, Party"})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RenameTag(bday.ID, "birthday"); err == nil {
		t.Fatal("rename onto an existing tag should fail")
	}
	if err := s.RenameTag(bday.ID, "B-day"); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetPayeeRule(ruleID); r.SetTags != "B-day, Party" {
		t.Fatalf("rule tags after rename=%q", r.SetTags)
	}

	res, err := s.MergeTag(bday.ID, birthday.ID, 1)
	if err != nil || res.Transactions != 2 || res.Rules != 1 {
		t.Fatalf("merge=%+v err=%v", res, err)
	}
	if got := tagNames(t, s, b); len(got) != 1 || got[0] != "Birthday" {
		t.Fatalf("b after merge=%v", got)
	}
	if v, _ := s.GetTag(bday.ID); !v.Archived || v.Count != 0 {
		t.Fatalf("source after merge=%+v", v)
	}
	if r, _ := s.GetPayeeRule(ruleID); r.SetTags != "Birthday, Party" {
		t.Fatalf("rule tags after merge=%q", r.SetTags)
	}
	if err := s.Undo(res.UndoID); err != nil {
		t.Fatal(err)
	}
	if got := tagNames(t, s, a); len(got) != 2 {
		t.Fatalf("a after merge undo=%v", got)
	}
	if got := tagNames(t, s, b); len(got) != 1 || got[0] != "B-day" {
		t.Fatalf("b after merge undo=%v", got)
	}
	if v, _ := s.GetTag(bday.ID); v.Archived {
		t.Fatal("merge undo should unarchive the source")
	}
	if r, _ := s.GetPayeeRule(ruleID); r.SetTags != "B-day, Party" {
		t.Fatalf("rule tags after merge undo=%q", r.SetTags)
	}

	undoArch, err := s.ArchiveTag(birthday.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if opts, _ := s.TagOptions(); len(opts) != 1 || opts[0].ID != bday.ID {
		t.Fatalf("archived tag still offered: %+v", opts)
	}
	if got := tagNames(t, s, a); len(got) != 2 {
		t.Fatalf("archiving should keep tags on transactions: %v", got)
	}
	if err := s.Undo(undoArch); err != nil {
		t.Fatal(err)
	}
	if opts, _ := s.TagOptions(); len(opts) != 2 {
		t.Fatalf("archive undo: %+v", opts)
	}

	undoDel, n, err := s.DeleteTag(bday.ID, 1)
	if err != nil || n != 2 {
		t.Fatalf("delete n=%d err=%v", n, err)
	}
	if got := tagNames(t, s, b); len(got) != 0 {
		t.Fatalf("b after delete=%v", got)
	}
	if r, _ := s.GetPayeeRule(ruleID); r.SetTags != "Party" {
		t.Fatalf("rule tags after delete=%q", r.SetTags)
	}
	if err := s.Undo(undoDel); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetTag(bday.ID)
	if err != nil || v.Name != "B-day" || v.Count != 2 {
		t.Fatalf("restored tag=%+v err=%v", v, err)
	}
	if r, _ := s.GetPayeeRule(ruleID); r.SetTags != "B-day, Party" {
		t.Fatalf("rule tags after delete undo=%q", r.SetTags)
	}
}
