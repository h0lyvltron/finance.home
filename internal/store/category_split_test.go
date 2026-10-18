package store_test

import "testing"

func TestSplitCrowdedCategory(t *testing.T) {
	s := setupStore(t)
	acct := ensureChecking(t, s)
	parent, err := s.CreateCategory("Personal", "expense", "flexible", nil)
	if err != nil {
		t.Fatal(err)
	}
	shop, err := s.CreateCategory("Shopping", "expense", "flexible", &parent)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		mustTx(t, s, acct, "WHATNOT", -1000, "2024-06-01", shop)
	}
	for i := 0; i < 6; i++ {
		mustTx(t, s, acct, "OTHER STORE "+string(rune('A'+i)), -500, "2024-06-02", shop)
	}
	sug, err := s.SplitSuggestions(0)
	if err != nil || len(sug) != 1 || sug[0].Name != "Whatnot" || sug[0].Count != 10 || sug[0].CategoryID != shop {
		t.Fatalf("suggestions=%+v err=%v", sug, err)
	}
	newID, undoID, n, err := s.SplitPayeeCategory(shop, sug[0].PayeeKey, sug[0].Name, 1)
	if err != nil || n != 10 || undoID == "" {
		t.Fatalf("split id=%d n=%d undo=%q err=%v", newID, n, undoID, err)
	}
	got, err := s.GetCategory(newID)
	if err != nil || got.ParentID == nil || *got.ParentID != parent || got.Name != "Whatnot" {
		t.Fatalf("new category=%+v err=%v", got, err)
	}
	var left int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM transactions WHERE category_id=? AND deleted_at IS NULL`, shop).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 6 {
		t.Fatalf("shopping still has %d, want 6", left)
	}
	rules, _ := s.ListPayeeRules()
	if len(rules) != 1 || rules[0].PayeeValue != "WHATNOT" {
		t.Fatalf("rule=%+v", rules)
	}
	if err := s.Undo(undoID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM transactions WHERE category_id=? AND deleted_at IS NULL`, shop).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 16 {
		t.Fatalf("undo left %d in shopping", left)
	}
	if _, err := s.GetCategory(newID); err == nil {
		t.Fatal("undo should remove the new category")
	}
	if err := s.DismissSplitPair(1, shop, "WHATNOT"); err != nil {
		t.Fatal(err)
	}
	if sug, _ := s.SplitSuggestions(1); len(sug) != 0 {
		t.Fatalf("dismissed pair still suggested: %+v", sug)
	}
}

func TestSplitSkipsCategoryThatIsOnePayee(t *testing.T) {
	s := setupStore(t)
	acct := ensureChecking(t, s)
	parent, err := s.CreateCategory("Debt payments extra", "expense", "fixed", nil)
	if err != nil {
		t.Fatal(err)
	}
	loan, err := s.CreateCategory("Loan extra", "expense", "fixed", &parent)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		mustTx(t, s, acct, "Online Banking Transfer", -10000, "2024-07-01", loan)
	}
	sug, err := s.SplitSuggestions(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range sug {
		if item.CategoryID == loan {
			t.Fatalf("a category that is one payee should not be split: %+v", item)
		}
	}
}
