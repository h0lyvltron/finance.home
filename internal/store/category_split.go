package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"finance.home/internal/payee"
	"github.com/google/uuid"
)

// SplitSuggestion is one payee that dominates a crowded category.
type SplitSuggestion struct {
	CategoryID    int64
	CategoryName  string
	ParentName    string
	PayeeKey      string
	Name          string
	Count         int
	CategoryCount int
	Spend         int64
	TxIDs         []int64
}

// IDs is the space-separated transaction list for the categorize picker.
func (s SplitSuggestion) IDs() string {
	parts := make([]string, len(s.TxIDs))
	for i, id := range s.TxIDs {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, " ")
}

// Detail is the sentence shown on the category and on Home.
func (s SplitSuggestion) Detail() string {
	parent := s.ParentName
	if parent == "" {
		parent = "the top level"
	}
	return fmt.Sprintf("%s is mostly %s — %d of %d purchases. Split %s out under %s so it isn't buried in %s.",
		s.CategoryName, s.Name, s.Count, s.CategoryCount, s.Name, parent, s.CategoryName)
}

type splitTx struct {
	id     int64
	payee  string
	amount int64
	person string
}

// SplitSuggestions lists payee clusters worth pulling out of a crowded leaf
// category. A cluster needs 8 transactions and at least a quarter of that
// category, and the category itself needs 15. At most two per category.
// Dismissed pairs for personID are left out; pass 0 to skip that filter.
func (s *Store) SplitSuggestions(personID int64) ([]SplitSuggestion, error) {
	rows, err := s.DB.Query(`SELECT t.id, t.category_id, c.name, COALESCE(p.name,''),
		COALESCE(t.payee_name,''), t.amount, t.person
		FROM transactions t
		JOIN categories c ON c.id = t.category_id
		LEFT JOIN categories p ON p.id = c.parent_id
		WHERE t.deleted_at IS NULL AND t.transfer_id IS NULL AND t.is_split=0
		  AND c.archived=0 AND c.kind='expense'
		  AND NOT EXISTS (SELECT 1 FROM categories ch WHERE ch.parent_id=c.id AND ch.archived=0)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type catInfo struct {
		name, parent string
		txs          []splitTx
	}
	byCat := map[int64]*catInfo{}
	for rows.Next() {
		var id, cat, amt int64
		var name, parent, payee, person string
		if err := rows.Scan(&id, &cat, &name, &parent, &payee, &amt, &person); err != nil {
			return nil, err
		}
		info := byCat[cat]
		if info == nil {
			info = &catInfo{name: name, parent: parent}
			byCat[cat] = info
		}
		info.txs = append(info.txs, splitTx{id: id, payee: payee, amount: amt, person: person})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hidden := map[string]bool{}
	if personID > 0 {
		hrows, err := s.DB.Query(`SELECT kind FROM coaching_prefs WHERE person_id=? AND dismissed=1 AND kind LIKE 'splitpair:%'`, personID)
		if err != nil {
			return nil, err
		}
		for hrows.Next() {
			var k string
			if err := hrows.Scan(&k); err != nil {
				hrows.Close()
				return nil, err
			}
			hidden[k] = true
		}
		hrows.Close()
		if err := hrows.Err(); err != nil {
			return nil, err
		}
	}

	var out []SplitSuggestion
	for catID, info := range byCat {
		if len(info.txs) < 15 {
			continue
		}
		keys := make([]string, len(info.txs))
		for i, t := range info.txs {
			keys[i] = payee.Key(t.payee)
		}
		root := clusterKeys(keys)
		type cluster struct {
			key   string
			keys  []string
			ids   []int64
			spend int64
		}
		clusters := map[string]*cluster{}
		for i, t := range info.txs {
			k := root[keys[i]]
			c := clusters[k]
			if c == nil {
				c = &cluster{key: k}
				clusters[k] = c
			}
			c.ids = append(c.ids, t.id)
			c.keys = append(c.keys, keys[i])
			if t.amount < 0 {
				c.spend += -t.amount
			}
		}
		var catSpend int64
		for _, t := range info.txs {
			if t.amount < 0 {
				catSpend += -t.amount
			}
		}
		var picks []SplitSuggestion
		for _, c := range clusters {
			// A quarter of the transactions, or a quarter of the spend. Count
			// alone hides a merchant like Hubman: 87 purchases is only a fifth
			// of Shopping, but it is most of the dollars.
			if len(c.ids) < 8 || len(c.ids) >= len(info.txs) {
				continue
			}
			if len(c.ids)*4 < len(info.txs) && (catSpend == 0 || c.spend*4 < catSpend) {
				continue
			}
			rule := shortestKey(c.keys)
			if hidden[splitPairKind(catID, rule)] {
				continue
			}
			picks = append(picks, SplitSuggestion{
				CategoryID: catID, CategoryName: info.name, ParentName: info.parent,
				PayeeKey: rule, Name: payee.TitleName(rule),
				Count: len(c.ids), CategoryCount: len(info.txs), Spend: c.spend, TxIDs: c.ids,
			})
		}
		sort.Slice(picks, func(i, j int) bool {
			if picks[i].Count != picks[j].Count {
				return picks[i].Count > picks[j].Count
			}
			return picks[i].PayeeKey < picks[j].PayeeKey
		})
		if len(picks) > 2 {
			picks = picks[:2]
		}
		out = append(out, picks...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].CategoryID < out[j].CategoryID
	})
	return out, nil
}

func shortestKey(keys []string) string {
	best := ""
	for _, k := range keys {
		if k == "" {
			continue
		}
		if best == "" || len(k) < len(best) {
			best = k
		}
	}
	return best
}

func splitPairKind(categoryID int64, payeeKey string) string {
	return "splitpair:" + strconv.FormatInt(categoryID, 10) + ":" + payeeKey
}

// DismissSplitPair hides one payee-in-category suggestion for this person.
func (s *Store) DismissSplitPair(personID, categoryID int64, payeeKey string) error {
	kind := splitPairKind(categoryID, payee.Key(payeeKey))
	if payee.Key(payeeKey) == "" {
		return sql.ErrNoRows
	}
	_, err := s.DB.Exec(`
		INSERT INTO coaching_prefs(person_id, kind, snoozed_until, dismissed, updated_at)
		VALUES (?, ?, NULL, 1, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(person_id, kind) DO UPDATE SET
			snoozed_until=NULL, dismissed=1, updated_at=excluded.updated_at`, personID, kind)
	return err
}

type categorySplitPayload struct {
	CreatedCategory int64       `json:"created_category"`
	Items           []recatItem `json:"items"`
	Rule            *RuleChange `json:"rule,omitempty"`
}

// SplitPayeeCategory creates a sibling of sourceID and moves the payee's
// transactions into it, remembering the payee for later imports.
func (s *Store) SplitPayeeCategory(sourceID int64, payeeKey, name string, personID int64) (int64, string, int, error) {
	src, err := s.GetCategory(sourceID)
	if err != nil {
		return 0, "", 0, err
	}
	if src.Kind != "expense" || src.Archived {
		return 0, "", 0, fmt.Errorf("Pick a spending category.")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = payee.TitleName(payeeKey)
	}
	if name == "" {
		return 0, "", 0, fmt.Errorf("Give the category a name.")
	}
	key := payee.Key(payeeKey)
	if key == "" {
		return 0, "", 0, fmt.Errorf("That payee is blank.")
	}
	if taken, err := s.siblingNameTaken(src.Kind, src.ParentID, name, 0); err != nil {
		return 0, "", 0, err
	} else if taken {
		return 0, "", 0, fmt.Errorf("There's already a “%s” there.", name)
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, "", 0, err
	}
	defer tx.Rollback()

	var maxOrd int
	_ = tx.QueryRow(`SELECT COALESCE(MAX(display_order),0) FROM categories`).Scan(&maxOrd)
	var fixed any
	if src.FixedFlexible != "" {
		fixed = src.FixedFlexible
	}
	res, err := tx.Exec(`INSERT INTO categories(name, kind, parent_id, fixed_flexible, color, display_order)
		VALUES (?,?,?,?,?,?)`, name, src.Kind, src.ParentID, fixed, nullStr(src.Color), maxOrd+1)
	if err != nil {
		return 0, "", 0, err
	}
	newID, _ := res.LastInsertId()

	rows, err := tx.Query(`SELECT id, COALESCE(payee_name,'')
		FROM transactions WHERE deleted_at IS NULL AND transfer_id IS NULL AND is_split=0 AND category_id=?`, sourceID)
	if err != nil {
		return 0, "", 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var payeeName string
		if err := rows.Scan(&id, &payeeName); err != nil {
			rows.Close()
			return 0, "", 0, err
		}
		if payee.Similar(payeeName, key) || payee.Matches(payeeName, key) {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, "", 0, err
	}
	if len(ids) == 0 {
		return 0, "", 0, fmt.Errorf("No “%s” transactions in that category.", key)
	}
	var moving []txSnap
	for _, id := range ids {
		item, err := loadTxSnap(tx, id)
		if err != nil {
			return 0, "", 0, err
		}
		moving = append(moving, item)
		if err := setTransferCategory(tx, id, newID, nil); err != nil {
			return 0, "", 0, err
		}
	}

	rule, err := rememberPayeeCategoryTx(tx, key, newID)
	if err != nil {
		return 0, "", 0, err
	}
	items := make([]recatItem, len(moving))
	for i, it := range moving {
		items[i] = recatItem{ID: it.ID, CategoryID: it.CategoryID, NeedsReview: it.NeedsReview, Person: it.Person, Splits: it.Splits}
	}
	raw, _ := json.Marshal(categorySplitPayload{CreatedCategory: newID, Items: items, Rule: rule})
	undoID := uuid.NewString()
	exp := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339Nano)
	var created any
	if personID > 0 {
		created = personID
	}
	if _, err := tx.Exec(`INSERT INTO undo_ops(id, kind, payload, expires_at, created_by) VALUES (?,?,?,?,?)`,
		undoID, "category_split", string(raw), exp, created); err != nil {
		return 0, "", 0, err
	}
	return newID, undoID, len(moving), tx.Commit()
}

func rememberPayeeCategoryTx(tx *sql.Tx, match string, categoryID int64) (*RuleChange, error) {
	m := payee.Normalize(match)
	if m == "" {
		return nil, nil
	}
	var refName string
	if err := tx.QueryRow(`SELECT name FROM categories WHERE id=?`, categoryID).Scan(&refName); err != nil {
		return nil, err
	}
	name := autoRuleName(m, refName)
	var ch RuleChange
	var prevCat sql.NullInt64
	var prevEn int
	err := tx.QueryRow(`SELECT id, set_category_id, name, enabled FROM payee_rules
		WHERE payee_match='contains' AND UPPER(TRIM(payee_value))=?
		AND amount_min IS NULL AND amount_max IS NULL AND account_id IS NULL AND COALESCE(notes_contains,'')=''
		ORDER BY sort_order LIMIT 1`, m).Scan(&ch.ID, &prevCat, &ch.PrevName, &prevEn)
	if err == nil {
		if prevCat.Valid {
			v := prevCat.Int64
			ch.PrevCategoryID = &v
		}
		ch.PrevEnabled = prevEn == 1
		_, err = tx.Exec(`UPDATE payee_rules SET set_category_id=?, name=?, enabled=1 WHERE id=?`, categoryID, name, ch.ID)
		return &ch, err
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	var minOrd sql.NullInt64
	_ = tx.QueryRow(`SELECT MIN(sort_order) FROM payee_rules`).Scan(&minOrd)
	ord := int64(0)
	if minOrd.Valid {
		ord = minOrd.Int64 - 1
	}
	res, err := tx.Exec(`INSERT INTO payee_rules(sort_order, name, enabled, payee_match, payee_value, set_category_id)
		VALUES (?,?,1,'contains',?,?)`, ord, name, m, categoryID)
	if err != nil {
		return nil, err
	}
	ch.ID, _ = res.LastInsertId()
	ch.Created = true
	return &ch, nil
}

func undoCategorySplit(tx *sql.Tx, payload string) error {
	var p categorySplitPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
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
	if p.CreatedCategory != 0 {
		if _, err := tx.Exec(`DELETE FROM categories WHERE id=?`, p.CreatedCategory); err != nil {
			return err
		}
	}
	return nil
}
