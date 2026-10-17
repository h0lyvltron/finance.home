package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"finance.home/internal/models"
	"finance.home/internal/payee"
)

const maxTagName = 40

// TagView is a tag with its usage, for the tag manager and the tag picker.
type TagView struct {
	ID        int64
	Name      string
	Archived  bool
	Count     int
	Out       int64 // sum of outflows (negative cents)
	In        int64 // sum of inflows
	FirstDate string
	LastDate  string
	lastSeq   int64
}

func (v TagView) Net() int64 { return v.Out + v.In }

// TxSummary totals the transactions matching a filter.
type TxSummary struct {
	Count     int
	Out       int64
	In        int64
	FirstDate string
	LastDate  string
}

func (s TxSummary) Net() int64 { return s.Out + s.In }

// CleanTagName trims a tag name, drops a leading '#', and rejects names that
// can't round-trip through comma-separated rule tags.
func CleanTagName(name string) (string, error) {
	name = strings.Join(strings.Fields(strings.TrimLeft(strings.TrimSpace(name), "#")), " ")
	if name == "" {
		return "", errors.New("Give the tag a name.")
	}
	if strings.Contains(name, ",") {
		return "", errors.New("Tag names can't contain commas.")
	}
	if len([]rune(name)) > maxTagName {
		return "", fmt.Errorf("Keep tag names under %d characters.", maxTagName)
	}
	return name, nil
}

// ListAllTags includes archived tags, for labelling filters and chips.
func (s *Store) ListAllTags() ([]models.Tag, error) {
	rows, err := s.DB.Query(`SELECT id, name FROM tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Tag
	for rows.Next() {
		var t models.Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTag(id int64) (*TagView, error) {
	views, err := s.tagViews(`WHERE g.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(views) == 0 {
		return nil, sql.ErrNoRows
	}
	return &views[0], nil
}

// ListTagViews returns every tag with usage totals, by name.
func (s *Store) ListTagViews() ([]TagView, error) {
	return s.tagViews(``)
}

// TagOptions returns active tags for the picker, most recently applied first.
func (s *Store) TagOptions() ([]TagView, error) {
	views, err := s.tagViews(`WHERE g.archived = 0`)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].lastSeq != views[j].lastSeq {
			return views[i].lastSeq > views[j].lastSeq
		}
		return views[i].ID > views[j].ID
	})
	return views, nil
}

func (s *Store) tagViews(where string, args ...any) ([]TagView, error) {
	rows, err := s.DB.Query(`SELECT g.id, g.name, g.archived, COUNT(t.id),
		COALESCE(SUM(CASE WHEN t.amount < 0 THEN t.amount END), 0),
		COALESCE(SUM(CASE WHEN t.amount > 0 THEN t.amount END), 0),
		COALESCE(MIN(t.transaction_date), ''), COALESCE(MAX(t.transaction_date), ''),
		COALESCE(MAX(CASE WHEN t.id IS NOT NULL THEN tt.rowid END), 0)
		FROM tags g
		LEFT JOIN transaction_tags tt ON tt.tag_id = g.id
		LEFT JOIN transactions t ON t.id = tt.transaction_id AND t.deleted_at IS NULL
		`+where+`
		GROUP BY g.id ORDER BY g.name COLLATE NOCASE`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagView
	for rows.Next() {
		var v TagView
		var arch int
		if err := rows.Scan(&v.ID, &v.Name, &arch, &v.Count, &v.Out, &v.In, &v.FirstDate, &v.LastDate, &v.lastSeq); err != nil {
			return nil, err
		}
		v.Archived = arch == 1
		out = append(out, v)
	}
	return out, rows.Err()
}

// EnsureActiveTag finds a tag by name (case-insensitive), restoring it if it
// was archived, or creates it.
func (s *Store) EnsureActiveTag(name string) (models.Tag, bool, error) {
	name, err := CleanTagName(name)
	if err != nil {
		return models.Tag{}, false, err
	}
	var t models.Tag
	var arch int
	err = s.DB.QueryRow(`SELECT id, name, archived FROM tags WHERE name = ? COLLATE NOCASE`, name).Scan(&t.ID, &t.Name, &arch)
	if err == nil {
		if arch == 1 {
			if _, err := s.DB.Exec(`UPDATE tags SET archived=0 WHERE id=?`, t.ID); err != nil {
				return t, false, err
			}
		}
		return t, false, nil
	}
	if err != sql.ErrNoRows {
		return t, false, err
	}
	res, err := s.DB.Exec(`INSERT INTO tags(name) VALUES (?)`, name)
	if err != nil {
		return t, false, err
	}
	t.ID, err = res.LastInsertId()
	t.Name = name
	return t, true, err
}

func chunkIDs(ids []int64, fn func([]int64) error) error {
	const size = 500
	for len(ids) > 0 {
		n := size
		if len(ids) < n {
			n = len(ids)
		}
		if err := fn(ids[:n]); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

func int64Args(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// TagsForTransactions returns the tags on each transaction, by name.
func (s *Store) TagsForTransactions(ids []int64) (map[int64][]models.Tag, error) {
	out := make(map[int64][]models.Tag, len(ids))
	for _, id := range ids {
		out[id] = []models.Tag{}
	}
	err := chunkIDs(ids, func(part []int64) error {
		rows, err := s.DB.Query(`SELECT tt.transaction_id, g.id, g.name FROM transaction_tags tt
			JOIN tags g ON g.id = tt.tag_id
			WHERE tt.transaction_id IN (`+placeholders(len(part))+`) ORDER BY g.name`, int64Args(part)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var txID int64
			var t models.Tag
			if err := rows.Scan(&txID, &t.ID, &t.Name); err != nil {
				return err
			}
			out[txID] = append(out[txID], t)
		}
		return rows.Err()
	})
	return out, err
}

// TagCounts reports how many of the transactions carry each tag.
func (s *Store) TagCounts(ids []int64) (map[int64]int, error) {
	out := map[int64]int{}
	err := chunkIDs(ids, func(part []int64) error {
		rows, err := s.DB.Query(`SELECT tag_id, COUNT(1) FROM transaction_tags
			WHERE transaction_id IN (`+placeholders(len(part))+`) GROUP BY tag_id`, int64Args(part)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				return err
			}
			out[id] += n
		}
		return rows.Err()
	})
	return out, err
}

type tagPair struct {
	Tx  int64 `json:"tx"`
	Tag int64 `json:"tag"`
}

type tagChange struct {
	Added   []tagPair `json:"added"`
	Removed []tagPair `json:"removed"`
}

// TagTransactions adds and removes tags on transactions. It returns the
// transactions that changed and an undo id ("" when nothing changed).
func (s *Store) TagTransactions(ids, add, remove []int64, personID int64) ([]int64, string, error) {
	adding := map[int64]bool{}
	for _, id := range add {
		adding[id] = true
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()

	var addTags []int64
	for _, id := range add {
		var ok int
		if err := tx.QueryRow(`SELECT 1 FROM tags WHERE id=?`, id).Scan(&ok); err == sql.ErrNoRows {
			return nil, "", errors.New("That tag no longer exists.")
		} else if err != nil {
			return nil, "", err
		}
		addTags = append(addTags, id)
	}

	var ch tagChange
	var changed []int64
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var ok int
		err := tx.QueryRow(`SELECT 1 FROM transactions WHERE id=? AND deleted_at IS NULL`, id).Scan(&ok)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		touched := false
		for _, tg := range addTags {
			res, err := tx.Exec(`INSERT OR IGNORE INTO transaction_tags(transaction_id, tag_id) VALUES (?,?)`, id, tg)
			if err != nil {
				return nil, "", err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				ch.Added = append(ch.Added, tagPair{Tx: id, Tag: tg})
				touched = true
			}
		}
		for _, tg := range remove {
			if adding[tg] {
				continue
			}
			res, err := tx.Exec(`DELETE FROM transaction_tags WHERE transaction_id=? AND tag_id=?`, id, tg)
			if err != nil {
				return nil, "", err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				ch.Removed = append(ch.Removed, tagPair{Tx: id, Tag: tg})
				touched = true
			}
		}
		if touched {
			changed = append(changed, id)
		}
	}
	if len(changed) == 0 {
		return nil, "", nil
	}
	undoID, err := newUndo(tx, "tag_change", ch, personID)
	if err != nil {
		return nil, "", err
	}
	return changed, undoID, tx.Commit()
}

func undoTagChange(tx *sql.Tx, payload string) error {
	var ch tagChange
	if err := json.Unmarshal([]byte(payload), &ch); err != nil {
		return err
	}
	for _, p := range ch.Added {
		if _, err := tx.Exec(`DELETE FROM transaction_tags WHERE transaction_id=? AND tag_id=?`, p.Tx, p.Tag); err != nil {
			return err
		}
	}
	return restoreTagPairs(tx, ch.Removed)
}

func restoreTagPairs(tx *sql.Tx, pairs []tagPair) error {
	for _, p := range pairs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO transaction_tags(transaction_id, tag_id)
			SELECT ?, ? WHERE EXISTS (SELECT 1 FROM tags WHERE id=?) AND EXISTS (SELECT 1 FROM transactions WHERE id=?)`,
			p.Tx, p.Tag, p.Tag, p.Tx); err != nil {
			return err
		}
	}
	return nil
}

type ruleTags struct {
	ID   int64  `json:"id"`
	Tags string `json:"tags"`
}

// rewriteRuleTags renames (or, with to == "", removes) a tag in payee rules'
// comma-separated set_tags, returning the previous values for undo.
func rewriteRuleTags(tx *sql.Tx, from, to string) ([]ruleTags, error) {
	rows, err := tx.Query(`SELECT id, set_tags FROM payee_rules WHERE COALESCE(set_tags,'') != ''`)
	if err != nil {
		return nil, err
	}
	var all []ruleTags
	for rows.Next() {
		var r ruleTags
		if err := rows.Scan(&r.ID, &r.Tags); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var prev []ruleTags
	for _, r := range all {
		var out []string
		hit := false
		have := map[string]bool{}
		for _, part := range strings.Split(r.Tags, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if strings.EqualFold(part, from) {
				hit = true
				part = to
			}
			if part == "" || have[strings.ToLower(part)] {
				continue
			}
			have[strings.ToLower(part)] = true
			out = append(out, part)
		}
		if !hit {
			continue
		}
		if _, err := tx.Exec(`UPDATE payee_rules SET set_tags=? WHERE id=?`, nullStr(strings.Join(out, ", ")), r.ID); err != nil {
			return nil, err
		}
		prev = append(prev, r)
	}
	return prev, nil
}

func restoreRuleTags(tx *sql.Tx, rules []ruleTags) error {
	for _, r := range rules {
		if _, err := tx.Exec(`UPDATE payee_rules SET set_tags=? WHERE id=?`, nullStr(r.Tags), r.ID); err != nil {
			return err
		}
	}
	return nil
}

// RenameTag renames a tag and the payee rules that add it.
func (s *Store) RenameTag(id int64, name string) error {
	name, err := CleanTagName(name)
	if err != nil {
		return err
	}
	t, err := s.GetTag(id)
	if err != nil {
		return err
	}
	if t.Name == name {
		return nil
	}
	var other int64
	err = s.DB.QueryRow(`SELECT id FROM tags WHERE name = ? COLLATE NOCASE AND id != ?`, name, id).Scan(&other)
	if err == nil {
		return fmt.Errorf("There's already a tag called “%s”. Use Merge to combine them.", name)
	}
	if err != sql.ErrNoRows {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE tags SET name=? WHERE id=?`, name, id); err != nil {
		return err
	}
	if _, err := rewriteRuleTags(tx, t.Name, name); err != nil {
		return err
	}
	return tx.Commit()
}

type tagMerge struct {
	Src         int64      `json:"src"`
	Dst         int64      `json:"dst"`
	SrcTxs      []int64    `json:"src_txs"`
	DstAdded    []int64    `json:"dst_added"`
	Rules       []ruleTags `json:"rules"`
	WasArchived bool       `json:"was_archived"`
}

// TagMergeResult summarizes a merge for the person who asked for it.
type TagMergeResult struct {
	UndoID       string
	Transactions int
	Rules        int
	From, Into   string
}

// MergeTag moves every use of src onto dst, then archives src.
func (s *Store) MergeTag(src, dst, personID int64) (*TagMergeResult, error) {
	if src == dst {
		return nil, errors.New("Pick a different tag to merge into.")
	}
	from, err := s.GetTag(src)
	if err != nil {
		return nil, err
	}
	into, err := s.GetTag(dst)
	if err != nil || into.Archived {
		return nil, errors.New("Pick an active tag to merge into.")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	m := tagMerge{Src: src, Dst: dst, WasArchived: from.Archived}
	if m.SrcTxs, err = idsWhere(tx, `SELECT transaction_id FROM transaction_tags WHERE tag_id=?`, src); err != nil {
		return nil, err
	}
	for _, id := range m.SrcTxs {
		res, err := tx.Exec(`INSERT OR IGNORE INTO transaction_tags(transaction_id, tag_id) VALUES (?,?)`, id, dst)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			m.DstAdded = append(m.DstAdded, id)
		}
	}
	if _, err := tx.Exec(`DELETE FROM transaction_tags WHERE tag_id=?`, src); err != nil {
		return nil, err
	}
	if m.Rules, err = rewriteRuleTags(tx, from.Name, into.Name); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE tags SET archived=1 WHERE id=?`, src); err != nil {
		return nil, err
	}
	undoID, err := newUndo(tx, "tag_merge", m, personID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &TagMergeResult{UndoID: undoID, Transactions: from.Count, Rules: len(m.Rules), From: from.Name, Into: into.Name}, nil
}

func undoTagMerge(tx *sql.Tx, payload string) error {
	var m tagMerge
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return err
	}
	for _, id := range m.DstAdded {
		if _, err := tx.Exec(`DELETE FROM transaction_tags WHERE transaction_id=? AND tag_id=?`, id, m.Dst); err != nil {
			return err
		}
	}
	pairs := make([]tagPair, len(m.SrcTxs))
	for i, id := range m.SrcTxs {
		pairs[i] = tagPair{Tx: id, Tag: m.Src}
	}
	if err := restoreTagPairs(tx, pairs); err != nil {
		return err
	}
	if err := restoreRuleTags(tx, m.Rules); err != nil {
		return err
	}
	archived := 0
	if m.WasArchived {
		archived = 1
	}
	_, err := tx.Exec(`UPDATE tags SET archived=? WHERE id=?`, archived, m.Src)
	return err
}

// ArchiveTag hides a tag from the picker and filters; transactions keep it.
func (s *Store) ArchiveTag(id, personID int64) (string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE tags SET archived=1 WHERE id=? AND archived=0`, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", errors.New("That tag is already archived.")
	}
	undoID, err := newUndo(tx, "tag_archive", map[string]int64{"id": id}, personID)
	if err != nil {
		return "", err
	}
	return undoID, tx.Commit()
}

func undoTagArchive(tx *sql.Tx, payload string) error {
	var p struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE tags SET archived=0 WHERE id=?`, p.ID)
	return err
}

func (s *Store) RestoreTag(id int64) error {
	_, err := s.DB.Exec(`UPDATE tags SET archived=0 WHERE id=?`, id)
	return err
}

type tagDelete struct {
	ID       int64      `json:"id"`
	Name     string     `json:"name"`
	Archived int        `json:"archived"`
	Created  string     `json:"created"`
	Txs      []int64    `json:"txs"`
	Rules    []ruleTags `json:"rules"`
}

// DeleteTag removes a tag from every transaction and payee rule.
func (s *Store) DeleteTag(id, personID int64) (string, int, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	d := tagDelete{ID: id}
	if err := tx.QueryRow(`SELECT name, archived, created_at FROM tags WHERE id=?`, id).
		Scan(&d.Name, &d.Archived, &d.Created); err != nil {
		return "", 0, err
	}
	if d.Txs, err = idsWhere(tx, `SELECT transaction_id FROM transaction_tags WHERE tag_id=?`, id); err != nil {
		return "", 0, err
	}
	var live int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM transaction_tags tt JOIN transactions t ON t.id = tt.transaction_id
		WHERE tt.tag_id=? AND t.deleted_at IS NULL`, id).Scan(&live); err != nil {
		return "", 0, err
	}
	if d.Rules, err = rewriteRuleTags(tx, d.Name, ""); err != nil {
		return "", 0, err
	}
	if _, err := tx.Exec(`DELETE FROM transaction_tags WHERE tag_id=?`, id); err != nil {
		return "", 0, err
	}
	if _, err := tx.Exec(`DELETE FROM tags WHERE id=?`, id); err != nil {
		return "", 0, err
	}
	undoID, err := newUndo(tx, "tag_delete", d, personID)
	if err != nil {
		return "", 0, err
	}
	return undoID, live, tx.Commit()
}

func undoTagDelete(tx *sql.Tx, payload string) error {
	var d tagDelete
	if err := json.Unmarshal([]byte(payload), &d); err != nil {
		return err
	}
	var clash int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM tags WHERE name = ? COLLATE NOCASE`, d.Name).Scan(&clash); err != nil {
		return err
	}
	if clash > 0 {
		return fmt.Errorf("a new tag called “%s” exists now; rename it first", d.Name)
	}
	if _, err := tx.Exec(`INSERT INTO tags(id, name, archived, created_at) VALUES (?,?,?,?)`,
		d.ID, d.Name, d.Archived, d.Created); err != nil {
		return err
	}
	pairs := make([]tagPair, len(d.Txs))
	for i, id := range d.Txs {
		pairs[i] = tagPair{Tx: id, Tag: d.ID}
	}
	if err := restoreTagPairs(tx, pairs); err != nil {
		return err
	}
	return restoreRuleTags(tx, d.Rules)
}

// SummarizeTransactions totals every transaction matching the filter.
func (s *Store) SummarizeTransactions(f QueryFilter) (TxSummary, error) {
	var out TxSummary
	frag, err := s.buildTxFilter(f, true)
	if err != nil {
		return out, err
	}
	err = s.DB.QueryRow(`SELECT COUNT(1),
		COALESCE(SUM(CASE WHEN t.amount < 0 THEN t.amount END), 0),
		COALESCE(SUM(CASE WHEN t.amount > 0 THEN t.amount END), 0),
		COALESCE(MIN(t.transaction_date), ''), COALESCE(MAX(t.transaction_date), '')
		FROM transactions t`+frag.joins+` WHERE`+frag.where, frag.args...).
		Scan(&out.Count, &out.Out, &out.In, &out.FirstDate, &out.LastDate)
	return out, err
}

// FilteredTransactionIDs returns every transaction matching the filter, ignoring Limit.
func (s *Store) FilteredTransactionIDs(f QueryFilter) ([]int64, error) {
	return s.filteredMatching(f, "")
}

// SimilarInFilter returns transactions matching the filter whose payee contains match.
func (s *Store) SimilarInFilter(f QueryFilter, match string) ([]int64, error) {
	if payee.Normalize(match) == "" {
		return nil, nil
	}
	return s.filteredMatching(f, match)
}

func (s *Store) filteredMatching(f QueryFilter, match string) ([]int64, error) {
	frag, err := s.buildTxFilter(f, true)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(`SELECT t.id, COALESCE(t.payee_name,'') FROM transactions t`+frag.joins+
		` WHERE`+frag.where+` ORDER BY t.transaction_date DESC, t.id DESC`, frag.args...)
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
		if match == "" || payee.Matches(name, match) {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}
