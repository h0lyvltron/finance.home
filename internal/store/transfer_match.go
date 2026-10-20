package store

import (
	"database/sql"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Transfer matching scores whether two transactions on different accounts are
// the two sides of one transfer. Amounts must be exact opposites; everything
// else (dates, descriptions, categories, account types) only moves the score.
const (
	TransferAutoScore     = 50 // safe to pair without asking
	TransferPossibleScore = 30 // worth showing for review
	transferMaxDays       = 7
	transferAmbiguity     = 10 // a runner-up this close makes the best guess unsafe
)

type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// transferSide is one candidate side of a transfer (a transaction or a staged import row).
type transferSide struct {
	TxID       int64
	AccountID  int64
	Date       string
	Amount     int64
	Payee      string
	Notes      string
	CategoryID *int64
}

type matchAccount struct {
	ID     int64
	Name   string
	Type   string
	Owner  string
	tokens []string
}

type matchCategory struct {
	kind string
	debt bool // under "Debt payments" (payments are really transfers)
}

type transferMatcher struct {
	accounts   map[int64]*matchAccount
	categories map[int64]matchCategory
}

// Words in account names too generic to identify an account in a bank description.
var accountNameStopwords = map[string]bool{
	"CREDIT": true, "UNION": true, "CARD": true, "BANK": true, "ACCOUNT": true,
	"CHECKING": true, "SAVINGS": true, "LOAN": true, "AUTO": true, "THE": true,
	"JOINT": true, "MAIN": true, "HOUSEHOLD": true, "FAMILY": true,
}

var transferKeywords = []string{
	"PAYMENT", "PMT", "PYMT", "AUTOPAY", "TRANSFER", "TFR", "XFER",
	"TO LOAN", "FROM SHARE", "TO SHARE",
}

var cashAdvanceKeywords = []string{"ADVANCE", "CASH", "ATM", "BALANCE TRANSFER"}

func loadTransferMatcher(q querier) (*transferMatcher, error) {
	m := &transferMatcher{
		accounts:   map[int64]*matchAccount{},
		categories: map[int64]matchCategory{},
	}
	rows, err := q.Query(`SELECT id, name, type, owner, transfer_aliases FROM accounts`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		a := &matchAccount{}
		var aliases string
		if err := rows.Scan(&a.ID, &a.Name, &a.Type, &a.Owner, &aliases); err != nil {
			rows.Close()
			return nil, err
		}
		a.tokens = accountTokens(a.Name, aliases)
		m.accounts[a.ID] = a
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.Query(`SELECT c.id, c.kind,
		CASE WHEN c.kind = 'expense' AND c.name != 'Loan interest'
			AND (c.name = 'Debt payments' OR p.name = 'Debt payments') THEN 1 ELSE 0 END
		FROM categories c LEFT JOIN categories p ON p.id = c.parent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var c matchCategory
		var debt int
		if err := rows.Scan(&id, &c.kind, &debt); err != nil {
			return nil, err
		}
		c.debt = debt == 1
		m.categories[id] = c
	}
	return m, rows.Err()
}

// accountTokens returns upper-case text that identifies an account inside
// other accounts' bank descriptions: its aliases plus distinctive name words.
func accountTokens(name, aliases string) []string {
	var out []string
	for _, a := range strings.Split(aliases, ",") {
		a = strings.ToUpper(strings.TrimSpace(a))
		if a != "" {
			out = append(out, a)
		}
	}
	words := strings.FieldsFunc(strings.ToUpper(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		if len(w) >= 4 && !accountNameStopwords[w] {
			out = append(out, w)
		}
	}
	return out
}

func sideText(x transferSide) string {
	return strings.ToUpper(x.Payee + " " + x.Notes)
}

func containsAny(text string, words []string) bool {
	for _, w := range words {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// mentions lists other accounts named in text. Tokens shared with the
// account the text belongs to are ignored ("CAPITAL ONE" on a Capital One card
// names its own issuer, not a sibling card).
func (m *transferMatcher) mentions(text string, self int64) map[int64]bool {
	own := map[string]bool{}
	if a := m.accounts[self]; a != nil {
		for _, t := range a.tokens {
			own[t] = true
		}
	}
	out := map[int64]bool{}
	for id, a := range m.accounts {
		if id == self {
			continue
		}
		for _, t := range a.tokens {
			if !own[t] && strings.Contains(text, t) {
				out[id] = true
				break
			}
		}
	}
	return out
}

// mentionedAccount returns the single other account named in a transaction's
// text, or 0 when none or several are named.
func (m *transferMatcher) mentionedAccount(x transferSide) int64 {
	ment := m.mentions(sideText(x), x.AccountID)
	if len(ment) != 1 {
		return 0
	}
	for id := range ment {
		return id
	}
	return 0
}

func isLiabilityType(t string) bool {
	return t == "credit" || t == "loan"
}

func dayDiff(a, b string) (int, bool) {
	ta, err1 := time.Parse("2006-01-02", a)
	tb, err2 := time.Parse("2006-01-02", b)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	d := int(ta.Sub(tb).Hours() / 24)
	if d < 0 {
		d = -d
	}
	return d, true
}

// score rates a and b as one transfer; ok is false when they can't be one.
func (m *transferMatcher) score(a, b transferSide) (int, bool) {
	if a.AccountID == b.AccountID || a.Amount == 0 || a.Amount != -b.Amount {
		return 0, false
	}
	days, ok := dayDiff(a.Date, b.Date)
	if !ok || days > transferMaxDays {
		return 0, false
	}
	score := 30 - 4*days
	score += m.sideScore(a, b) + m.sideScore(b, a)

	out, in := a, b
	if a.Amount > 0 {
		out, in = b, a
	}
	oa, ia := m.accounts[out.AccountID], m.accounts[in.AccountID]
	if oa != nil && ia != nil {
		switch {
		case !isLiabilityType(oa.Type) && isLiabilityType(ia.Type):
			score += 10 // paying down a card or loan
		case isLiabilityType(oa.Type) && !isLiabilityType(ia.Type) && !containsAny(sideText(out), cashAdvanceKeywords):
			score -= 15 // a card charge landing in checking is rarely a transfer
		}
		if oa.Owner == ia.Owner {
			score += 5
		}
	}
	return score, true
}

func (m *transferMatcher) sideScore(x, other transferSide) int {
	text := sideText(x)
	ment := m.mentions(text, x.AccountID)
	kw := containsAny(text, transferKeywords)
	s := 0
	if ment[other.AccountID] {
		s += 40
	} else if len(ment) > 0 {
		s -= 25 // the description points at a different account
	}
	if kw {
		s += 10
	}
	if x.CategoryID != nil {
		c := m.categories[*x.CategoryID]
		switch {
		case c.kind == "transfer" || c.debt:
			s += 10
		case (c.kind == "expense" || c.kind == "income") && !kw && !ment[other.AccountID]:
			s -= 35 // looks like an ordinary purchase or paycheck
		}
	}
	return s
}

// pairCandidate is a scored (left, right) match; left/right are TxIDs of the sides.
type pairCandidate struct {
	Left, Right int64
	Score       int
	Days        int
	Ambiguous   bool
}

// pickPairs keeps candidates at or above minScore, marks a candidate ambiguous
// when its left side (or, if checkRight, its right side) has a runner-up within
// transferAmbiguity, then assigns greedily by score so each side is used once.
func pickPairs(cands []pairCandidate, minScore int, checkRight bool) []pairCandidate {
	var kept []pairCandidate
	for _, c := range cands {
		if c.Score >= minScore {
			kept = append(kept, c)
		}
	}
	bestL, secondL := map[int64]int{}, map[int64]int{}
	bestR, secondR := map[int64]int{}, map[int64]int{}
	track := func(best, second map[int64]int, id int64, s int) {
		if b, ok := best[id]; !ok || s > b {
			if ok {
				second[id] = b
			}
			best[id] = s
		} else if sc, ok := second[id]; !ok || s > sc {
			second[id] = s
		}
	}
	for _, c := range kept {
		track(bestL, secondL, c.Left, c.Score)
		track(bestR, secondR, c.Right, c.Score)
	}
	isClose := func(best, second map[int64]int, id int64) bool {
		sc, ok := second[id]
		return ok && best[id]-sc < transferAmbiguity
	}
	for i := range kept {
		c := &kept[i]
		c.Ambiguous = isClose(bestL, secondL, c.Left) || (checkRight && isClose(bestR, secondR, c.Right))
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Score != kept[j].Score {
			return kept[i].Score > kept[j].Score
		}
		if kept[i].Days != kept[j].Days {
			return kept[i].Days < kept[j].Days
		}
		if kept[i].Left != kept[j].Left {
			return kept[i].Left < kept[j].Left
		}
		return kept[i].Right > kept[j].Right
	})
	usedL, usedR := map[int64]bool{}, map[int64]bool{}
	var out []pairCandidate
	for _, c := range kept {
		if usedL[c.Left] || usedR[c.Right] {
			continue
		}
		usedL[c.Left], usedR[c.Right] = true, true
		out = append(out, c)
	}
	return out
}

const txSideCols = `t.id, t.account_id, t.transaction_date, t.amount, COALESCE(t.payee_name,''), COALESCE(t.notes,''), t.category_id`

func scanTxSide(row interface{ Scan(...any) error }) (transferSide, error) {
	var x transferSide
	var cat sql.NullInt64
	if err := row.Scan(&x.TxID, &x.AccountID, &x.Date, &x.Amount, &x.Payee, &x.Notes, &cat); err != nil {
		return x, err
	}
	if cat.Valid {
		v := cat.Int64
		x.CategoryID = &v
	}
	return x, nil
}

func loadTxSide(q querier, id int64) (transferSide, error) {
	return scanTxSide(q.QueryRow(`SELECT `+txSideCols+` FROM transactions t WHERE t.id=? AND t.deleted_at IS NULL`, id))
}

// opposingTxs lists live transactions on other accounts with the exact
// opposite amount within the match window.
func opposingTxs(q querier, x transferSide, unlinkedOnly bool) ([]transferSide, error) {
	query := `SELECT ` + txSideCols + ` FROM transactions t
		WHERE t.deleted_at IS NULL AND t.account_id != ? AND t.amount = ? AND t.id != ?
		AND ABS(julianday(t.transaction_date) - julianday(?)) <= ?`
	if unlinkedOnly {
		query += ` AND t.transfer_id IS NULL`
	}
	rows, err := q.Query(query, x.AccountID, -x.Amount, x.TxID, x.Date, transferMaxDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []transferSide
	for rows.Next() {
		c, err := scanTxSide(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
