package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"finance.home/internal/models"
	"github.com/google/uuid"
)

// CategoryView is a category with how much it is used.
type CategoryView struct {
	models.Category
	Count    int   // transactions (split lines) filed here
	Total    int64 // their signed sum
	Rules    int   // payee rules that file into it
	Children []CategoryView
}

// TreeCount includes subcategories.
func (v CategoryView) TreeCount() int {
	n := v.Count
	for _, c := range v.Children {
		n += c.Count
	}
	return n
}

// TreeTotal includes subcategories.
func (v CategoryView) TreeTotal() int64 {
	t := v.Total
	for _, c := range v.Children {
		t += c.Total
	}
	return t
}

// TreeRules includes subcategories.
func (v CategoryView) TreeRules() int {
	n := v.Rules
	for _, c := range v.Children {
		n += c.Rules
	}
	return n
}

// FormJSON holds the editable fields keyed by form input name.
func (v CategoryView) FormJSON() string {
	parent := ""
	if v.ParentID != nil {
		parent = fmt.Sprint(*v.ParentID)
	}
	excl := ""
	if v.ExcludeBudget {
		excl = "1"
	}
	b, _ := json.Marshal(map[string]any{
		"name":           v.Name,
		"parent_id":      parent,
		"fixed_flexible": v.FixedFlexible,
		"exclude_budget": excl,
		"color":          strings.ToUpper(v.Color),
		"kind":           v.Kind,
		"has_children":   len(v.Children) > 0,
	})
	return string(b)
}

// CategoryGroup is one kind of category (spending, income, transfers).
type CategoryGroup struct {
	Kind  string
	Label string
	Items []CategoryView // top-level categories with their children
}

// CategoryTree returns active categories grouped by kind, plus archived ones.
func (s *Store) CategoryTree() ([]CategoryGroup, []CategoryView, error) {
	cats, err := s.ListCategories("", true)
	if err != nil {
		return nil, nil, err
	}
	type usage struct {
		n int
		t int64
	}
	used := map[int64]usage{}
	rows, err := s.DB.Query(`SELECT sp.category_id, COUNT(*), COALESCE(SUM(sp.amount),0)
		FROM transaction_splits sp JOIN transactions t ON t.id = sp.transaction_id
		WHERE t.deleted_at IS NULL GROUP BY sp.category_id`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id int64
		var u usage
		if err := rows.Scan(&id, &u.n, &u.t); err != nil {
			rows.Close()
			return nil, nil, err
		}
		used[id] = u
	}
	rows.Close()
	rules := map[int64]int{}
	rows, err = s.DB.Query(`SELECT set_category_id, COUNT(*) FROM payee_rules WHERE set_category_id IS NOT NULL GROUP BY set_category_id`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, nil, err
		}
		rules[id] = n
	}
	rows.Close()

	view := func(c models.Category) CategoryView {
		u := used[c.ID]
		return CategoryView{Category: c, Count: u.n, Total: u.t, Rules: rules[c.ID]}
	}
	kids := map[int64][]CategoryView{}
	var archived []CategoryView
	for _, c := range cats {
		if c.Kind == "starting_balance" {
			continue
		}
		if c.Archived {
			archived = append(archived, view(c))
			continue
		}
		if c.ParentID != nil {
			kids[*c.ParentID] = append(kids[*c.ParentID], view(c))
		}
	}
	groups := []CategoryGroup{
		{Kind: "expense", Label: "Spending"},
		{Kind: "income", Label: "Income"},
		{Kind: "transfer", Label: "Not spending or income"},
	}
	idx := map[string]int{"expense": 0, "income": 1, "transfer": 2}
	for _, c := range cats {
		if c.Archived || c.ParentID != nil {
			continue
		}
		gi, ok := idx[c.Kind]
		if !ok {
			continue
		}
		v := view(c)
		v.Children = kids[c.ID]
		groups[gi].Items = append(groups[gi].Items, v)
	}
	return groups, archived, nil
}

// CategorySwatches are the preset category colors: the seeded palette, then
// extra distinct hues.
var CategorySwatches = []string{
	"#5B7C99", "#6B9E76", "#C4A35A", "#8B6B8B", "#9A7B6B", "#7A8B6B", "#A66B6B", "#5A8B8B", "#4A7C59",
	"#4E79A7", "#F28E2B", "#E15759", "#76B7B2", "#59A14F", "#EDC948", "#B07AA1", "#FF9DA7", "#9C755F",
}

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// NormalizeColor accepts "" (no color) or #RRGGBB and returns it upper-cased.
func NormalizeColor(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if !hexColor.MatchString(s) {
		return "", errors.New("Pick a color like #5B7C99.")
	}
	return strings.ToUpper(s), nil
}

// nextSwatch returns the first preset color no top-level category uses yet.
func (s *Store) nextSwatch() string {
	used := map[string]bool{}
	rows, err := s.DB.Query(`SELECT UPPER(COALESCE(color,'')) FROM categories WHERE parent_id IS NULL AND archived=0`)
	if err == nil {
		for rows.Next() {
			var c string
			if rows.Scan(&c) == nil {
				used[c] = true
			}
		}
		rows.Close()
	}
	for _, c := range CategorySwatches {
		if !used[c] {
			return c
		}
	}
	return CategorySwatches[0]
}

// CategoryInput is the editable part of a category.
type CategoryInput struct {
	Name          string
	ParentID      *int64
	FixedFlexible string
	ExcludeBudget bool
	Color         string
	ColorChildren bool // also recolor its subcategories
}

func (s *Store) siblingNameTaken(kind string, parentID *int64, name string, except int64) (bool, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM categories WHERE archived=0 AND kind=? AND COALESCE(parent_id,0)=COALESCE(?,0)
		AND name=? COLLATE NOCASE AND id != ?`, kind, parentID, name, except).Scan(&n)
	return n > 0, err
}

func (s *Store) activeChildren(id int64) ([]int64, error) {
	rows, err := s.DB.Query(`SELECT id FROM categories WHERE parent_id=? AND archived=0`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var c int64
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// validParent checks that parentID can hold a subcategory of the given kind.
func (s *Store) validParent(parentID *int64, kind string, self int64) error {
	if parentID == nil {
		return nil
	}
	p, err := s.GetCategory(*parentID)
	if err != nil || p.Archived {
		return errors.New("That parent category no longer exists.")
	}
	if p.ID == self {
		return errors.New("A category can't be inside itself.")
	}
	if p.ParentID != nil {
		return errors.New("Subcategories can only go one level deep.")
	}
	if p.Kind != kind {
		return errors.New("Pick a parent of the same kind (spending or income).")
	}
	return nil
}

// CreateCategoryChecked adds a category. A subcategory inherits its parent's
// kind and, unless one is given, its color; a new top-level category gets an
// unused preset color.
func (s *Store) CreateCategoryChecked(name, kind, fixed, color string, parentID *int64) (int64, error) {
	name = strings.TrimSpace(name)
	color, err := NormalizeColor(color)
	if err != nil {
		return 0, err
	}
	if parentID != nil {
		p, err := s.GetCategory(*parentID)
		if err != nil {
			return 0, errors.New("That parent category no longer exists.")
		}
		kind = p.Kind
		if fixed == "" {
			fixed = p.FixedFlexible
		}
		if color == "" {
			color = p.Color
		}
	} else if color == "" {
		color = s.nextSwatch()
	}
	if name == "" {
		return 0, errors.New("Give the category a name.")
	}
	if kind != "expense" && kind != "income" && !(kind == "transfer" && parentID != nil) {
		return 0, errors.New("Pick spending or income.")
	}
	if err := s.validParent(parentID, kind, 0); err != nil {
		return 0, err
	}
	if taken, err := s.siblingNameTaken(kind, parentID, name, 0); err != nil {
		return 0, err
	} else if taken {
		return 0, fmt.Errorf("There's already a “%s” there.", name)
	}
	id, err := s.CreateCategory(name, kind, fixed, parentID)
	if err != nil {
		return 0, err
	}
	_, err = s.DB.Exec(`UPDATE categories SET color=? WHERE id=?`, nullStr(color), id)
	return id, err
}

func (s *Store) UpdateCategory(id int64, in CategoryInput) error {
	c, err := s.GetCategory(id)
	if err != nil {
		return err
	}
	if c.Protected {
		return errors.New("System categories can't be changed.")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return errors.New("Give the category a name.")
	}
	color, err := NormalizeColor(in.Color)
	if err != nil {
		return err
	}
	if err := s.validParent(in.ParentID, c.Kind, id); err != nil {
		return err
	}
	if in.ParentID != nil {
		kids, err := s.activeChildren(id)
		if err != nil {
			return err
		}
		if len(kids) > 0 {
			return errors.New("This category has subcategories, so it has to stay at the top level.")
		}
	}
	if taken, err := s.siblingNameTaken(c.Kind, in.ParentID, in.Name, id); err != nil {
		return err
	} else if taken {
		return fmt.Errorf("There's already a “%s” there.", in.Name)
	}
	var ff any
	if in.FixedFlexible == "fixed" || in.FixedFlexible == "flexible" {
		ff = in.FixedFlexible
	}
	excl := 0
	if in.ExcludeBudget {
		excl = 1
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE categories SET name=?, parent_id=?, fixed_flexible=?, exclude_budget=?, color=? WHERE id=?`,
		in.Name, in.ParentID, ff, excl, nullStr(color), id); err != nil {
		return err
	}
	if in.ColorChildren && in.ParentID == nil {
		if _, err := tx.Exec(`UPDATE categories SET color=? WHERE parent_id=?`, nullStr(color), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MoveCategory swaps a category with its previous or next sibling.
func (s *Store) MoveCategory(id int64, dir string) error {
	c, err := s.GetCategory(id)
	if err != nil {
		return err
	}
	rows, err := s.DB.Query(`SELECT id, display_order FROM categories
		WHERE archived=0 AND kind=? AND COALESCE(parent_id,0)=COALESCE(?,0) ORDER BY display_order, name`, c.Kind, c.ParentID)
	if err != nil {
		return err
	}
	var ids []int64
	var orders []int
	for rows.Next() {
		var sid int64
		var o int
		if err := rows.Scan(&sid, &o); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, sid)
		orders = append(orders, o)
	}
	rows.Close()
	i := -1
	for k, sid := range ids {
		if sid == id {
			i = k
		}
	}
	j := i - 1
	if dir == "down" {
		j = i + 1
	}
	if i < 0 || j < 0 || j >= len(ids) {
		return nil
	}
	ids[i], ids[j] = ids[j], ids[i]
	sort.Ints(orders)
	for k := 1; k < len(orders); k++ {
		if orders[k] <= orders[k-1] {
			orders[k] = orders[k-1] + 1
		}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, sid := range ids {
		if _, err := tx.Exec(`UPDATE categories SET display_order=? WHERE id=?`, orders[k], sid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func newUndo(tx *sql.Tx, kind string, payload any, personID int64) (string, error) {
	b, _ := json.Marshal(payload)
	id := uuid.NewString()
	exp := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339Nano)
	_, err := tx.Exec(`INSERT INTO undo_ops(id, kind, payload, expires_at, created_by) VALUES (?,?,?,?,?)`,
		id, kind, string(b), exp, personID)
	return id, err
}

// ArchiveCategory hides a category (and its subcategories) from pickers.
// Saved transactions keep it.
func (s *Store) ArchiveCategory(id, personID int64) (string, []int64, error) {
	c, err := s.GetCategory(id)
	if err != nil {
		return "", nil, err
	}
	if c.Protected {
		return "", nil, errors.New("System categories can't be archived.")
	}
	ids, err := s.activeChildren(id)
	if err != nil {
		return "", nil, err
	}
	ids = append(ids, id)
	tx, err := s.DB.Begin()
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	for _, cid := range ids {
		if _, err := tx.Exec(`UPDATE categories SET archived=1 WHERE id=?`, cid); err != nil {
			return "", nil, err
		}
	}
	undoID, err := newUndo(tx, "category_archive", map[string]any{"ids": ids}, personID)
	if err != nil {
		return "", nil, err
	}
	return undoID, ids, tx.Commit()
}

func undoCategoryArchive(tx *sql.Tx, payload string) error {
	var p struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	for _, id := range p.IDs {
		if _, err := tx.Exec(`UPDATE categories SET archived=0 WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

// RestoreCategory brings an archived category back, along with its parent.
func (s *Store) RestoreCategory(id int64) error {
	c, err := s.GetCategory(id)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE categories SET archived=0 WHERE id=?`, id); err != nil {
		return err
	}
	if c.ParentID != nil {
		if _, err := tx.Exec(`UPDATE categories SET archived=0 WHERE id=?`, *c.ParentID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type ruleRename struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type categoryMerge struct {
	Src           int64        `json:"src"`
	Dst           int64        `json:"dst"`
	Splits        []int64      `json:"splits"`
	Txs           []int64      `json:"txs"`
	Rules         []ruleRename `json:"rules"`
	Payees        []int64      `json:"payees"`
	Schedules     []int64      `json:"schedules"`
	Drafts        []int64      `json:"drafts"`
	Staging       []int64      `json:"staging"`
	Children      []int64      `json:"children"`
	BudgetDefault bool         `json:"budget_default"`
	BudgetMonths  []string     `json:"budget_months"`
	WasArchived   bool         `json:"was_archived"`
}

// MergeResult summarizes a merge for the person who asked for it.
type MergeResult struct {
	UndoID       string
	Transactions int
	Rules        int
	Children     int
	From, Into   string
}

func idsWhere(tx *sql.Tx, q string, args ...any) ([]int64, error) {
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func setCategoryFor(tx *sql.Tx, table, col string, ids []int64, catID int64) error {
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE `+table+` SET `+col+`=? WHERE id=?`, catID, id); err != nil {
			return err
		}
	}
	return nil
}

// MergeCategory moves everything filed under src into dst and archives src.
func (s *Store) MergeCategory(src, dst, personID int64) (*MergeResult, error) {
	if src == dst {
		return nil, errors.New("Pick a different category to merge into.")
	}
	from, err := s.GetCategory(src)
	if err != nil {
		return nil, err
	}
	into, err := s.GetCategory(dst)
	if err != nil || into.Archived || into.Kind == "starting_balance" {
		return nil, errors.New("Pick an active category to merge into.")
	}
	if from.Protected {
		return nil, errors.New("System categories can't be merged away.")
	}
	children, err := s.activeChildren(src)
	if err != nil {
		return nil, err
	}
	if len(children) > 0 && (into.ParentID != nil || into.Kind != from.Kind) {
		return nil, errors.New("This category has subcategories, so it can only merge into a top-level category of the same kind.")
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	m := categoryMerge{Src: src, Dst: dst, WasArchived: from.Archived, Children: children}
	steps := []struct {
		dst   *[]int64
		q     string
		table string
		col   string
	}{
		{&m.Splits, `SELECT id FROM transaction_splits WHERE category_id=?`, "transaction_splits", "category_id"},
		{&m.Txs, `SELECT id FROM transactions WHERE category_id=?`, "transactions", "category_id"},
		{&m.Payees, `SELECT id FROM payees WHERE default_category_id=?`, "payees", "default_category_id"},
		{&m.Schedules, `SELECT id FROM schedule_templates WHERE category_id=?`, "schedule_templates", "category_id"},
		{&m.Drafts, `SELECT id FROM draft_transactions WHERE category_id=?`, "draft_transactions", "category_id"},
		{&m.Staging, `SELECT id FROM import_staging_rows WHERE category_id=?`, "import_staging_rows", "category_id"},
	}
	for _, st := range steps {
		ids, err := idsWhere(tx, st.q, src)
		if err != nil {
			return nil, err
		}
		*st.dst = ids
		if err := setCategoryFor(tx, st.table, st.col, ids, dst); err != nil {
			return nil, err
		}
	}

	rows, err := tx.Query(`SELECT id, name, payee_value FROM payee_rules WHERE set_category_id=?`, src)
	if err != nil {
		return nil, err
	}
	type ruleRow struct {
		id          int64
		name, value string
	}
	var rules []ruleRow
	for rows.Next() {
		var r ruleRow
		if err := rows.Scan(&r.id, &r.name, &r.value); err != nil {
			rows.Close()
			return nil, err
		}
		rules = append(rules, r)
	}
	rows.Close()
	for _, r := range rules {
		name := r.name
		if name == autoRuleName(r.value, from.Name) {
			name = autoRuleName(r.value, into.Name)
		}
		if _, err := tx.Exec(`UPDATE payee_rules SET set_category_id=?, name=? WHERE id=?`, dst, name, r.id); err != nil {
			return nil, err
		}
		m.Rules = append(m.Rules, ruleRename{ID: r.id, Name: r.name})
	}

	for _, c := range children {
		if _, err := tx.Exec(`UPDATE categories SET parent_id=? WHERE id=?`, dst, c); err != nil {
			return nil, err
		}
	}

	res, err := tx.Exec(`UPDATE budget_defaults SET category_id=? WHERE category_id=?
		AND NOT EXISTS (SELECT 1 FROM budget_defaults WHERE category_id=?)`, dst, src, dst)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		m.BudgetDefault = true
	}
	mrows, err := tx.Query(`SELECT year_month FROM budget_months b WHERE category_id=?
		AND NOT EXISTS (SELECT 1 FROM budget_months o WHERE o.category_id=? AND o.year_month=b.year_month)`, src, dst)
	if err != nil {
		return nil, err
	}
	for mrows.Next() {
		var ym string
		if err := mrows.Scan(&ym); err != nil {
			mrows.Close()
			return nil, err
		}
		m.BudgetMonths = append(m.BudgetMonths, ym)
	}
	mrows.Close()
	for _, ym := range m.BudgetMonths {
		if _, err := tx.Exec(`UPDATE budget_months SET category_id=? WHERE category_id=? AND year_month=?`, dst, src, ym); err != nil {
			return nil, err
		}
	}

	if _, err := tx.Exec(`UPDATE categories SET archived=1 WHERE id=?`, src); err != nil {
		return nil, err
	}
	undoID, err := newUndo(tx, "category_merge", m, personID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	n := len(m.Splits)
	if len(m.Txs) > n {
		n = len(m.Txs)
	}
	return &MergeResult{UndoID: undoID, Transactions: n, Rules: len(m.Rules), Children: len(children),
		From: from.Name, Into: into.Name}, nil
}

func undoCategoryMerge(tx *sql.Tx, payload string) error {
	var m categoryMerge
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return err
	}
	for _, st := range []struct {
		ids   []int64
		table string
		col   string
	}{
		{m.Splits, "transaction_splits", "category_id"},
		{m.Txs, "transactions", "category_id"},
		{m.Payees, "payees", "default_category_id"},
		{m.Schedules, "schedule_templates", "category_id"},
		{m.Drafts, "draft_transactions", "category_id"},
		{m.Staging, "import_staging_rows", "category_id"},
	} {
		if err := setCategoryFor(tx, st.table, st.col, st.ids, m.Src); err != nil {
			return err
		}
	}
	for _, r := range m.Rules {
		if _, err := tx.Exec(`UPDATE payee_rules SET set_category_id=?, name=? WHERE id=?`, m.Src, r.Name, r.ID); err != nil {
			return err
		}
	}
	for _, c := range m.Children {
		if _, err := tx.Exec(`UPDATE categories SET parent_id=? WHERE id=?`, m.Src, c); err != nil {
			return err
		}
	}
	if m.BudgetDefault {
		if _, err := tx.Exec(`UPDATE budget_defaults SET category_id=? WHERE category_id=?`, m.Src, m.Dst); err != nil {
			return err
		}
	}
	for _, ym := range m.BudgetMonths {
		if _, err := tx.Exec(`UPDATE budget_months SET category_id=? WHERE category_id=? AND year_month=?`, m.Src, m.Dst, ym); err != nil {
			return err
		}
	}
	archived := 0
	if m.WasArchived {
		archived = 1
	}
	_, err := tx.Exec(`UPDATE categories SET archived=? WHERE id=?`, archived, m.Src)
	return err
}
