package store

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"finance.home/internal/models"
	"finance.home/internal/payee"
	"github.com/google/uuid"
)

// PickerCategory is a category offered by the categorize picker.
type PickerCategory struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"`
	Kind   string `json:"kind"`
	Color  string `json:"color,omitempty"`
}

func (s *Store) PickerCategories() ([]PickerCategory, error) {
	cats, err := s.ListCategories("", false)
	if err != nil {
		return nil, err
	}
	out := make([]PickerCategory, 0, len(cats))
	for _, c := range cats {
		if c.Kind == "starting_balance" {
			continue
		}
		color, _ := NormalizeColor(c.Color)
		out = append(out, PickerCategory{ID: c.ID, Name: c.Name, Parent: c.ParentName, Kind: c.Kind, Color: color})
	}
	return out, nil
}

func (s *Store) CategoryRef(id int64) (models.CategoryRef, error) {
	var r models.CategoryRef
	err := s.DB.QueryRow(`SELECT id, name FROM categories WHERE id=? AND archived=0 AND kind != 'starting_balance'`, id).
		Scan(&r.ID, &r.Name)
	return r, err
}

// Suggester proposes categories for a payee from payee rules and from how
// similar merchants were categorized before, using amount as a tie-break.
type Suggester struct {
	rules []models.PayeeRule
	names map[int64]string
	keys  []string
	hist  map[string][]histSample
}

type histSample struct {
	cat    int64
	amount int64
}

// SuggestedCategoryLabel marks a staging row whose category was pre-filled
// from a fuzzy or thin history match and still needs a look.
const SuggestedCategoryLabel = "Suggested"

func (s *Store) NewSuggester() (*Suggester, error) {
	rules, err := s.ListPayeeRules()
	if err != nil {
		return nil, err
	}
	g := &Suggester{rules: rules, names: map[int64]string{}, hist: map[string][]histSample{}}
	rows, err := s.DB.Query(`SELECT id, name FROM categories WHERE archived=0 AND kind != 'starting_balance'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		g.names[id] = name
	}
	rows.Close()

	rows, err = s.DB.Query(`SELECT COALESCE(t.payee_name,''), t.category_id, t.amount FROM transactions t
		JOIN categories c ON c.id = t.category_id
		WHERE t.deleted_at IS NULL AND t.needs_review=0 AND t.is_split=0 AND t.transfer_id IS NULL
		AND c.archived=0 AND c.kind NOT IN ('transfer', 'starting_balance')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var cat, amt int64
		if err := rows.Scan(&name, &cat, &amt); err != nil {
			return nil, err
		}
		k := payee.Key(name)
		if k == "" {
			continue
		}
		if _, ok := g.hist[k]; !ok {
			g.keys = append(g.keys, k)
		}
		g.hist[k] = append(g.hist[k], histSample{cat: cat, amount: amt})
	}
	return g, rows.Err()
}

// For returns up to three categories: the matching rule's first, then the
// ones most often used for a similar merchant.
func (g *Suggester) For(payeeName string, amount, accountID int64) []models.CategoryRef {
	var out []models.CategoryRef
	seen := map[int64]bool{}
	if r := MatchPayeeRule(g.rules, payeeName, "", amount, accountID); r != nil && r.SetCategoryID != nil {
		if name, ok := g.names[*r.SetCategoryID]; ok {
			seen[*r.SetCategoryID] = true
			out = append(out, models.CategoryRef{ID: *r.SetCategoryID, Name: name, Reason: "payee rule"})
		}
	}
	for _, ref := range g.ranked(payeeName, amount) {
		if seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		out = append(out, ref)
		if len(out) == 3 {
			break
		}
	}
	return out
}

// Usual returns a category only when history is confident enough to file
// without review: at least two similar transactions and one category with
// about 80% of them, or a single amount band when history is split.
func (g *Suggester) Usual(payeeName string, amount int64) *models.CategoryRef {
	ref, auto := g.classify(payeeName, amount)
	if !auto {
		return nil
	}
	return ref
}

// classify returns the best category and whether it is confident enough to
// skip review. A weaker match still pre-fills a suggestion.
func (g *Suggester) classify(payeeName string, amount int64) (*models.CategoryRef, bool) {
	stats := g.catStats(payeeName, amount)
	if len(stats) == 0 {
		return nil, false
	}
	best := stats[0]
	name, ok := g.names[best.id]
	if !ok {
		return nil, false
	}
	ref := &models.CategoryRef{ID: best.id, Name: name, Reason: best.reason}
	return ref, best.auto
}

type catStat struct {
	id       int64
	count    int
	exact    int
	weighted float64
	payee    float64
	amounts  []int64
	close    bool
	reason   string
	auto     bool
}

func (g *Suggester) ranked(payeeName string, amount int64) []models.CategoryRef {
	stats := g.catStats(payeeName, amount)
	out := make([]models.CategoryRef, 0, len(stats))
	for _, st := range stats {
		name, ok := g.names[st.id]
		if !ok {
			continue
		}
		out = append(out, models.CategoryRef{ID: st.id, Name: name, Reason: st.reason})
	}
	return out
}

func (g *Suggester) catStats(payeeName string, amount int64) []catStat {
	type acc struct {
		count    int
		exact    int
		weighted float64
		payee    float64
		amounts  []int64
		close    int
	}
	byCat := map[int64]*acc{}
	for _, k := range g.keys {
		sim := payee.Score(payeeName, k)
		if sim == 0 {
			continue
		}
		for _, sm := range g.hist[k] {
			a := byCat[sm.cat]
			if a == nil {
				a = &acc{}
				byCat[sm.cat] = a
			}
			a.count++
			if sim == 1 {
				a.exact++
			}
			w := amountWeight(amount, sm.amount)
			a.weighted += sim * w
			a.payee += sim
			a.amounts = append(a.amounts, sm.amount)
			if w >= 0.7 {
				a.close++
			}
		}
	}
	if len(byCat) == 0 {
		return nil
	}
	stats := make([]catStat, 0, len(byCat))
	total := 0
	var topPayee float64
	topPayeeID := int64(0)
	for id, a := range byCat {
		total += a.count
		if a.payee > topPayee || (a.payee == topPayee && (topPayeeID == 0 || id < topPayeeID)) {
			topPayee = a.payee
			topPayeeID = id
		}
		stats = append(stats, catStat{
			id: id, count: a.count, exact: a.exact,
			weighted: a.weighted, payee: a.payee, amounts: a.amounts, close: a.close > 0,
		})
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].weighted != stats[j].weighted {
			return stats[i].weighted > stats[j].weighted
		}
		if stats[i].count != stats[j].count {
			return stats[i].count > stats[j].count
		}
		return stats[i].id < stats[j].id
	})
	inRangeN := 0
	var onlyRange int64
	for i := range stats {
		if stats[i].count >= 2 && amountInRange(amount, stats[i].amounts) {
			inRangeN++
			onlyRange = stats[i].id
		}
	}
	top := &stats[0]
	auto := false
	if total >= 2 && top.count*5 >= total*4 {
		auto = true
	} else if total >= 2 && inRangeN == 1 && onlyRange == top.id && top.count >= 2 {
		auto = true
	}
	for i := range stats {
		st := &stats[i]
		switch {
		case st.exact > 0:
			st.reason = "same payee"
		case st.id != topPayeeID && st.close:
			st.reason = "similar amount"
		default:
			st.reason = "similar description"
		}
		if st.id == top.id {
			st.auto = auto
			if auto && inRangeN == 1 && !(total >= 2 && top.count*5 >= total*4) {
				st.reason = "similar amount"
			}
		}
	}
	return stats
}

func amountWeight(amount, sample int64) float64 {
	if amountNear(amount, sample) {
		return 1
	}
	if amountInRange(amount, []int64{sample}) {
		return 0.7
	}
	return 0.35
}

func amountNear(amount, sample int64) bool {
	diff := amount - sample
	if diff < 0 {
		diff = -diff
	}
	if diff <= 50 {
		return true
	}
	base := sample
	if base < 0 {
		base = -base
	}
	return base > 0 && diff*20 <= base
}

func amountInRange(amount int64, amounts []int64) bool {
	if len(amounts) == 0 {
		return false
	}
	min, max := amounts[0], amounts[0]
	for _, a := range amounts {
		if a < min {
			min = a
		}
		if a > max {
			max = a
		}
	}
	pad := (max - min) / 5
	if pad < 0 {
		pad = -pad
	}
	if pad < 200 {
		pad = 200
	}
	return amount >= min-pad && amount <= max+pad
}

// Rule returns the payee rule that would file this payee, if any.
func (g *Suggester) Rule(payeeName string, amount, accountID int64) *models.PayeeRule {
	return MatchPayeeRule(g.rules, payeeName, "", amount, accountID)
}

// SimilarOpenTransactions returns transactions still waiting for a category
// (uncategorized or in review) whose payee contains match.
func (s *Store) SimilarOpenTransactions(match string) ([]int64, error) {
	return s.similarTransactions(match, `(needs_review=1 OR category_id IS NULL)`)
}

// SimilarFiledTransactions returns already-categorized transactions whose
// payee contains match.
func (s *Store) SimilarFiledTransactions(match string) ([]int64, error) {
	return s.similarTransactions(match, `needs_review=0 AND category_id IS NOT NULL`)
}

func (s *Store) similarTransactions(match, cond string) ([]int64, error) {
	if payee.Normalize(match) == "" {
		return nil, nil
	}
	rows, err := s.DB.Query(`SELECT id, COALESCE(payee_name,'') FROM transactions
		WHERE deleted_at IS NULL AND transfer_id IS NULL AND is_split=0 AND ` + cond)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if payee.Matches(name, match) || payee.Similar(name, match) {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// SimilarStagingRows returns rows in the batch, not yet categorized by hand,
// whose payee contains match.
func (s *Store) SimilarStagingRows(batchID int64, match string) ([]int64, error) {
	if payee.Normalize(match) == "" {
		return nil, nil
	}
	rows, err := s.DB.Query(`SELECT id, COALESCE(payee_name,'') FROM import_staging_rows
		WHERE batch_id=? AND COALESCE(exception,'')='' AND action='import' AND needs_review=1`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if payee.Matches(name, match) || payee.Similar(name, match) {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

type recatSplit struct {
	CategoryID int64  `json:"category_id"`
	Person     string `json:"person"`
	Amount     int64  `json:"amount"`
	Notes      string `json:"notes"`
	Order      int    `json:"order"`
}

type recatItem struct {
	ID          int64        `json:"id"`
	CategoryID  *int64       `json:"category_id"`
	NeedsReview int          `json:"needs_review"`
	Person      string       `json:"person"`
	Splits      []recatSplit `json:"splits"`
}

// CategorizeTransactions puts each transaction in a single category (replacing
// split lines) and takes it out of review. Split and transfer transactions are
// left alone. It returns the ids changed and an undo id.
func (s *Store) CategorizeTransactions(ids []int64, categoryID int64, person string, personID int64) ([]int64, string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()

	var changed []int64
	var prev []recatItem
	for _, id := range ids {
		var cat, xfer sql.NullInt64
		var needs, split int
		var p string
		var amt int64
		err := tx.QueryRow(`SELECT category_id, needs_review, person, amount, is_split, transfer_id
			FROM transactions WHERE id=? AND deleted_at IS NULL`, id).Scan(&cat, &needs, &p, &amt, &split, &xfer)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if split == 1 || xfer.Valid {
			continue
		}
		if cat.Valid && cat.Int64 == categoryID && needs == 0 && (person == "" || person == p) {
			continue
		}
		item := recatItem{ID: id, NeedsReview: needs, Person: p}
		if cat.Valid {
			v := cat.Int64
			item.CategoryID = &v
		}
		srows, err := tx.Query(`SELECT category_id, person, amount, COALESCE(notes,''), display_order
			FROM transaction_splits WHERE transaction_id=?`, id)
		if err != nil {
			return nil, "", err
		}
		for srows.Next() {
			var sp recatSplit
			if err := srows.Scan(&sp.CategoryID, &sp.Person, &sp.Amount, &sp.Notes, &sp.Order); err != nil {
				srows.Close()
				return nil, "", err
			}
			item.Splits = append(item.Splits, sp)
		}
		srows.Close()
		prev = append(prev, item)

		newPerson := p
		if person != "" {
			newPerson = person
		}
		if _, err := tx.Exec(`UPDATE transactions SET category_id=?, needs_review=0, person=?, updated_by=?,
			updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, categoryID, newPerson, personID, id); err != nil {
			return nil, "", err
		}
		if _, err := tx.Exec(`DELETE FROM transaction_splits WHERE transaction_id=?`, id); err != nil {
			return nil, "", err
		}
		if _, err := tx.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, display_order)
			VALUES (?,?,?,?,0)`, id, categoryID, newPerson, amt); err != nil {
			return nil, "", err
		}
		changed = append(changed, id)
	}
	if len(changed) == 0 {
		return nil, "", nil
	}

	undoID := uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"items": prev})
	exp := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT INTO undo_ops(id, kind, payload, expires_at, created_by) VALUES (?,?,?,?,?)`,
		undoID, "recategorize", string(payload), exp, personID); err != nil {
		return nil, "", err
	}
	return changed, undoID, tx.Commit()
}

func undoRecategorize(tx *sql.Tx, payload string) error {
	var p struct {
		Items []recatItem `json:"items"`
		Rule  *RuleChange `json:"rule"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	if r := p.Rule; r != nil {
		var err error
		if r.Created {
			_, err = tx.Exec(`DELETE FROM payee_rules WHERE id=?`, r.ID)
		} else {
			en := 0
			if r.PrevEnabled {
				en = 1
			}
			_, err = tx.Exec(`UPDATE payee_rules SET set_category_id=?, name=?, enabled=? WHERE id=?`,
				r.PrevCategoryID, r.PrevName, en, r.ID)
		}
		if err != nil {
			return err
		}
	}
	for _, it := range p.Items {
		if _, err := tx.Exec(`UPDATE transactions SET category_id=?, needs_review=?, person=?,
			updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`,
			it.CategoryID, it.NeedsReview, it.Person, it.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM transaction_splits WHERE transaction_id=?`, it.ID); err != nil {
			return err
		}
		for _, sp := range it.Splits {
			if _, err := tx.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, notes, display_order)
				VALUES (?,?,?,?,?,?)`, it.ID, sp.CategoryID, sp.Person, sp.Amount, nullStr(sp.Notes), sp.Order); err != nil {
				return err
			}
		}
	}
	return nil
}

// RenameTransactionPayee changes the payee shown on one transaction.
func (s *Store) RenameTransactionPayee(id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	pid, err := ensurePayeeTx(tx, name, nil, nil)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE transactions SET payee_name=?, payee_id=?,
		updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND deleted_at IS NULL`, name, pid, id); err != nil {
		return err
	}
	return tx.Commit()
}

// CategorizeStagingRows sets the category on import rows and marks them as
// decided by hand, so they skip the review queue after commit.
func (s *Store) CategorizeStagingRows(batchID int64, ids []int64, categoryID int64, person string) ([]int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var changed []int64
	for _, id := range ids {
		res, err := tx.Exec(`UPDATE import_staging_rows SET category_id=?, needs_review=0,
			person=COALESCE(NULLIF(?,''), person) WHERE id=? AND batch_id=?`, categoryID, person, id, batchID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			changed = append(changed, id)
		}
	}
	return changed, tx.Commit()
}

// RuleChange records what RememberPayeeCategory did, so it can be undone.
type RuleChange struct {
	ID             int64  `json:"id"`
	Created        bool   `json:"created"`
	PrevCategoryID *int64 `json:"prev_category_id,omitempty"`
	PrevName       string `json:"prev_name,omitempty"`
	PrevEnabled    bool   `json:"prev_enabled,omitempty"`
}

// RememberPayeeCategory makes future imports whose payee contains match land
// in categoryID. A plain "contains" rule for the same text is updated;
// otherwise a rule is added at the top so it wins over older, broader rules.
func (s *Store) RememberPayeeCategory(match string, categoryID int64) (*RuleChange, error) {
	m := payee.Normalize(match)
	if m == "" {
		return nil, nil
	}
	ref, err := s.CategoryRef(categoryID)
	if err != nil {
		return nil, err
	}
	name := autoRuleName(m, ref.Name)
	var ch RuleChange
	var prevCat sql.NullInt64
	var prevEn int
	err = s.DB.QueryRow(`SELECT id, set_category_id, name, enabled FROM payee_rules
		WHERE payee_match='contains' AND UPPER(TRIM(payee_value))=?
		AND amount_min IS NULL AND amount_max IS NULL AND account_id IS NULL AND COALESCE(notes_contains,'')=''
		ORDER BY sort_order LIMIT 1`, m).Scan(&ch.ID, &prevCat, &ch.PrevName, &prevEn)
	if err == nil {
		if prevCat.Valid {
			v := prevCat.Int64
			ch.PrevCategoryID = &v
		}
		ch.PrevEnabled = prevEn == 1
		_, err = s.DB.Exec(`UPDATE payee_rules SET set_category_id=?, name=?, enabled=1 WHERE id=?`, categoryID, name, ch.ID)
		return &ch, err
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	var minOrd sql.NullInt64
	_ = s.DB.QueryRow(`SELECT MIN(sort_order) FROM payee_rules`).Scan(&minOrd)
	ord := int64(0)
	if minOrd.Valid {
		ord = minOrd.Int64 - 1
	}
	res, err := s.DB.Exec(`INSERT INTO payee_rules(sort_order, name, enabled, payee_match, payee_value, set_category_id)
		VALUES (?,?,1,'contains',?,?)`, ord, name, m, categoryID)
	if err != nil {
		return nil, err
	}
	ch.ID, _ = res.LastInsertId()
	ch.Created = true
	return &ch, nil
}

// AttachRuleUndo makes an existing recategorize undo also revert a rule change.
func (s *Store) AttachRuleUndo(undoID string, ch *RuleChange) error {
	if undoID == "" || ch == nil {
		return nil
	}
	var payload string
	if err := s.DB.QueryRow(`SELECT payload FROM undo_ops WHERE id=? AND kind='recategorize'`, undoID).Scan(&payload); err != nil {
		return err
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	raw, _ := json.Marshal(ch)
	p["rule"] = raw
	out, _ := json.Marshal(p)
	_, err := s.DB.Exec(`UPDATE undo_ops SET payload=? WHERE id=?`, string(out), undoID)
	return err
}

// ReviewGroup is a run of review-queue transactions from the same merchant.
type ReviewGroup struct {
	Key          string
	Items        []models.Transaction
	Total        int64
	CategoryID   *int64 // set when every item already shares one category
	CategoryName string
	Suggest      []models.CategoryRef
}

// IDs is the space-separated id list used by the categorize picker.
func (g ReviewGroup) IDs() string {
	parts := make([]string, len(g.Items))
	for i, t := range g.Items {
		parts[i] = strconv.FormatInt(t.ID, 10)
	}
	return strings.Join(parts, " ")
}

// GroupForReview buckets transactions by merchant, largest groups first.
// Truncated and fuzzy-similar descriptions share a group.
func GroupForReview(items []models.Transaction) []ReviewGroup {
	keys := make([]string, len(items))
	for i, t := range items {
		keys[i] = payee.Key(t.PayeeName)
	}
	root := clusterKeys(keys)

	idx := map[string]int{}
	var groups []ReviewGroup
	for i, t := range items {
		r := root[keys[i]]
		gi, ok := idx[r]
		if !ok {
			gi = len(groups)
			idx[r] = gi
			groups = append(groups, ReviewGroup{Key: r})
		}
		groups[gi].Items = append(groups[gi].Items, t)
		groups[gi].Total += t.Amount
	}
	for i := range groups {
		g := &groups[i]
		first := g.Items[0].CategoryID
		same := first != nil
		for _, t := range g.Items[1:] {
			if t.CategoryID == nil || *t.CategoryID != *first {
				same = false
				break
			}
		}
		if same {
			g.CategoryID = first
			g.CategoryName = g.Items[0].CategoryName
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if len(groups[i].Items) != len(groups[j].Items) {
			return len(groups[i].Items) > len(groups[j].Items)
		}
		return abs64(groups[i].Total) > abs64(groups[j].Total)
	})
	return groups
}

// clusterKeys maps each merchant key to the label of its cluster. Similar
// keys share the longer label so a truncated description joins the full one.
func clusterKeys(keys []string) map[string]string {
	index := map[string]int{}
	var uniq []string
	for _, k := range keys {
		if _, ok := index[k]; ok {
			continue
		}
		index[k] = len(uniq)
		uniq = append(uniq, k)
	}
	parent := make([]int, len(uniq))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if len(uniq[ra]) < len(uniq[rb]) || (len(uniq[ra]) == len(uniq[rb]) && uniq[ra] > uniq[rb]) {
			ra, rb = rb, ra
		}
		parent[rb] = ra
	}
	for i := 0; i < len(uniq); i++ {
		if uniq[i] == "" {
			continue
		}
		for j := i + 1; j < len(uniq); j++ {
			if uniq[j] == "" {
				continue
			}
			if payee.Similar(uniq[i], uniq[j]) {
				union(i, j)
			}
		}
	}
	out := make(map[string]string, len(uniq))
	for k, i := range index {
		out[k] = uniq[find(i)]
	}
	return out
}

// StagingGroup is a run of import-preview rows from the same merchant.
type StagingGroup struct {
	Key     string
	Rows    []models.StagingRow
	Total   int64
	Suggest []models.CategoryRef
}

// IDs is the space-separated staging-row id list used by the categorize picker.
func (g StagingGroup) IDs() string {
	parts := make([]string, len(g.Rows))
	for i, r := range g.Rows {
		parts[i] = strconv.FormatInt(r.ID, 10)
	}
	return strings.Join(parts, " ")
}

// GroupStaging clusters preview rows by merchant, keeping file order.
func GroupStaging(rows []models.StagingRow) []StagingGroup {
	if len(rows) == 0 {
		return nil
	}
	keys := make([]string, len(rows))
	for i, r := range rows {
		keys[i] = payee.Key(r.PayeeName)
	}
	root := clusterKeys(keys)
	idx := map[string]int{}
	var groups []StagingGroup
	for i, r := range rows {
		k := root[keys[i]]
		gi, ok := idx[k]
		if !ok {
			gi = len(groups)
			idx[k] = gi
			groups = append(groups, StagingGroup{Key: k})
		}
		groups[gi].Rows = append(groups[gi].Rows, r)
		groups[gi].Total += r.Amount
	}
	return groups
}
