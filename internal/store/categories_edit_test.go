package store_test

import (
	"testing"

	"finance.home/internal/models"
	"finance.home/internal/store"
)

func catByName(t *testing.T, s *store.Store, name string) int64 {
	t.Helper()
	cats, err := s.ListCategories("", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cats {
		if c.Name == name {
			return c.ID
		}
	}
	t.Fatalf("no category %q", name)
	return 0
}

func TestMergeCategoryAndUndo(t *testing.T) {
	s := setupStore(t)
	acctID := ensureChecking(t, s)
	gifts := catByName(t, s, "Gifts")
	shopping := catByName(t, s, "Shopping")

	txID, err := s.CreateTransaction(store.TxInput{
		AccountID: acctID, PayeeName: "ETSY", Amount: -2500,
		TransactionDate: "2024-05-01", CategoryID: &gifts, CreatedBy: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ruleID, err := s.CreatePayeeRule(store.PayeeRuleInput{Name: "ETSY → Gifts", PayeeValue: "ETSY", SetCategoryID: &gifts})
	if err != nil {
		t.Fatal(err)
	}

	res, err := s.MergeCategory(gifts, shopping, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Transactions != 1 || res.Rules != 1 || res.UndoID == "" {
		t.Fatalf("merge result=%+v", res)
	}
	tx, _ := s.GetTransaction(txID)
	if tx.CategoryID == nil || *tx.CategoryID != shopping {
		t.Fatalf("transaction not moved: %+v", tx)
	}
	r, _ := s.GetPayeeRule(ruleID)
	if *r.SetCategoryID != shopping || r.Name != "ETSY → Shopping" {
		t.Fatalf("rule not moved/renamed: %+v", r)
	}
	if c, _ := s.GetCategory(gifts); !c.Archived {
		t.Fatal("merged category should be archived")
	}

	if err := s.Undo(res.UndoID); err != nil {
		t.Fatal(err)
	}
	tx, _ = s.GetTransaction(txID)
	if *tx.CategoryID != gifts {
		t.Fatalf("undo left transaction in %d", *tx.CategoryID)
	}
	r, _ = s.GetPayeeRule(ruleID)
	if *r.SetCategoryID != gifts || r.Name != "ETSY → Gifts" {
		t.Fatalf("undo left rule %+v", r)
	}
	if c, _ := s.GetCategory(gifts); c.Archived {
		t.Fatal("undo should unarchive")
	}

	if _, err := s.MergeCategory(gifts, gifts, 1); err == nil {
		t.Fatal("merging into itself should fail")
	}
	personal := catByName(t, s, "Personal")
	if _, err := s.MergeCategory(personal, shopping, 1); err == nil {
		t.Fatal("a parent with subcategories should not merge into a subcategory")
	}
}

func TestArchiveCategoryCascadeAndUndo(t *testing.T) {
	s := setupStore(t)
	food := catByName(t, s, "Food")
	undoID, ids, err := s.ArchiveCategory(food, 1)
	if err != nil || len(ids) != 4 {
		t.Fatalf("archive ids=%v err=%v", ids, err)
	}
	if active, _ := s.ListCategories("", false); containsCat(active, "Groceries") {
		t.Fatal("children should be archived with the parent")
	}
	if err := s.Undo(undoID); err != nil {
		t.Fatal(err)
	}
	if active, _ := s.ListCategories("", false); !containsCat(active, "Groceries") || !containsCat(active, "Food") {
		t.Fatal("undo should restore parent and children")
	}
	transfer := catByName(t, s, "Transfer")
	if _, _, err := s.ArchiveCategory(transfer, 1); err == nil {
		t.Fatal("protected categories should not archive")
	}
}

func TestUpdateCategoryRulesAndMove(t *testing.T) {
	s := setupStore(t)
	coffee := catByName(t, s, "Coffee")
	food := catByName(t, s, "Food")
	personal := catByName(t, s, "Personal")
	paycheck := catByName(t, s, "Paycheck")

	if err := s.UpdateCategory(coffee, store.CategoryInput{Name: "Coffee shops", ParentID: &personal, FixedFlexible: "flexible"}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.GetCategory(coffee)
	if c.Name != "Coffee shops" || *c.ParentID != personal {
		t.Fatalf("update=%+v", c)
	}
	if err := s.UpdateCategory(coffee, store.CategoryInput{Name: "Coffee", ParentID: &paycheck}); err == nil {
		t.Fatal("parent of another kind should be rejected")
	}
	if err := s.UpdateCategory(food, store.CategoryInput{Name: "Food", ParentID: &personal}); err == nil {
		t.Fatal("a category with children should stay top level")
	}
	if err := s.UpdateCategory(coffee, store.CategoryInput{Name: "shopping", ParentID: &personal}); err == nil {
		t.Fatal("duplicate sibling name should be rejected")
	}
	if _, err := s.CreateCategoryChecked("Bakery", "", "", "", &food); err != nil {
		t.Fatal(err)
	}

	before := siblingOrder(t, s, food)
	if err := s.MoveCategory(catByName(t, s, "Dining out"), "up"); err != nil {
		t.Fatal(err)
	}
	after := siblingOrder(t, s, food)
	if before[0] != after[1] || before[1] != after[0] {
		t.Fatalf("move up: before=%v after=%v", before, after)
	}
}

func TestCategoryColor(t *testing.T) {
	s := setupStore(t)
	food := catByName(t, s, "Food")
	groceries := catByName(t, s, "Groceries")
	colorOf := func(id int64) string {
		t.Helper()
		c, err := s.GetCategory(id)
		if err != nil {
			t.Fatal(err)
		}
		return c.Color
	}

	if err := s.UpdateCategory(food, store.CategoryInput{Name: "Food", Color: "#e15759"}); err != nil {
		t.Fatal(err)
	}
	if got := colorOf(food); got != "#E15759" {
		t.Fatalf("color=%q, want #E15759", got)
	}
	if colorOf(groceries) == "#E15759" {
		t.Fatal("children should keep their color unless asked")
	}

	for _, bad := range []string{"red", "#12345", "#1234567", "#GGGGGG", "#123456;background:url(x)", "expression(1)"} {
		if err := s.UpdateCategory(food, store.CategoryInput{Name: "Food", Color: bad}); err == nil {
			t.Fatalf("color %q should be rejected", bad)
		}
	}
	if got := colorOf(food); got != "#E15759" {
		t.Fatalf("rejected update changed color to %q", got)
	}

	if err := s.UpdateCategory(food, store.CategoryInput{Name: "Food", Color: "#4E79A7", ColorChildren: true}); err != nil {
		t.Fatal(err)
	}
	if got := colorOf(groceries); got != "#4E79A7" {
		t.Fatalf("child color=%q after apply-to-subcategories", got)
	}

	bakery, err := s.CreateCategoryChecked("Bakery", "", "", "", &food)
	if err != nil {
		t.Fatal(err)
	}
	if got := colorOf(bakery); got != "#4E79A7" {
		t.Fatalf("new subcategory color=%q, want parent's", got)
	}
	cake, err := s.CreateCategoryChecked("Cake", "", "", "#59a14f", &food)
	if err != nil {
		t.Fatal(err)
	}
	if got := colorOf(cake); got != "#59A14F" {
		t.Fatalf("chosen subcategory color=%q", got)
	}
	if _, err := s.CreateCategoryChecked("Pies", "", "", "nope", &food); err == nil {
		t.Fatal("invalid color on create should be rejected")
	}
	top, err := s.CreateCategoryChecked("Hobbies", "expense", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if colorOf(top) == "" {
		t.Fatal("a new top-level category should get a palette color")
	}

	if err := s.UpdateCategory(food, store.CategoryInput{Name: "Food", Color: ""}); err != nil {
		t.Fatal(err)
	}
	if got := colorOf(food); got != "" {
		t.Fatalf("empty color should clear, got %q", got)
	}
}

func containsCat(cats []models.Category, name string) bool {
	for _, c := range cats {
		if c.Name == name {
			return true
		}
	}
	return false
}

func siblingOrder(t *testing.T, s *store.Store, parent int64) []string {
	t.Helper()
	groups, _, err := s.CategoryTree()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		for _, it := range g.Items {
			if it.ID == parent {
				var names []string
				for _, c := range it.Children {
					names = append(names, c.Name)
				}
				return names
			}
		}
	}
	t.Fatalf("parent %d not found", parent)
	return nil
}
