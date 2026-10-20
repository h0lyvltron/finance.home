package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"finance.home/internal/money"
)

// AuditTx is one transaction as shown on the transfer audit page.
type AuditTx struct {
	ID           int64
	AccountID    int64
	AccountName  string
	AccountType  string
	Date         string
	Amount       int64
	Payee        string
	CategoryName string
	TransferID   *int64
}

// MissedPair is two unlinked transactions that look like one transfer.
type MissedPair struct {
	Out, In        AuditTx // Out is the negative side
	Score          int
	HighConfidence bool
	Ambiguous      bool
}

// RepairSuggestion links A and B, unlinking the pairs listed in Breaks first.
type RepairSuggestion struct {
	A, B   AuditTx
	Score  int
	Breaks []int64
}

// SuspiciousPair is an existing transfer that probably joins the wrong transactions.
type SuspiciousPair struct {
	TransferID int64
	From, To   AuditTx
	Score      int
	Reason     string
	Suggest    *RepairSuggestion
}

// LoanPaymentFix pairs a payment with a loan row that recorded principal only.
type LoanPaymentFix struct {
	Out      AuditTx
	Loan     AuditTx
	Interest int64 // parsed from the loan description
	Exact    bool  // payment == principal + interest
	Note     string
}

// OneSidedTx is a transfer-like transaction with no counterpart.
type OneSidedTx struct {
	AuditTx
	SuggestedAccountID int64
}

type TransferAudit struct {
	Missed      []MissedPair
	Suspicious  []SuspiciousPair
	Loans       []LoanPaymentFix
	OneSided    []OneSidedTx
	StaleSplits int
}

// ExactLoanCount is how many loan payments "Fix all" will repair.
func (a TransferAudit) ExactLoanCount() int {
	n := 0
	for _, l := range a.Loans {
		if l.Exact {
			n++
		}
	}
	return n
}

// Empty reports whether the audit found nothing to fix.
func (a TransferAudit) Empty() bool {
	return len(a.Missed) == 0 && len(a.Suspicious) == 0 && len(a.Loans) == 0 &&
		len(a.OneSided) == 0 && a.StaleSplits == 0
}

// HighConfidenceCount is how many missed pairs "Link all" will link.
func (a TransferAudit) HighConfidenceCount() int {
	n := 0
	for _, p := range a.Missed {
		if p.HighConfidence {
			n++
		}
	}
	return n
}

type auditRow struct {
	side transferSide
	info AuditTx
}

func (s *Store) loadAuditRows() (map[int64]*auditRow, map[int64][]*auditRow, error) {
	rows, err := s.DB.Query(`SELECT ` + txSideCols + `, a.name, a.type, COALESCE(c.name,''), t.transfer_id
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.deleted_at IS NULL
		ORDER BY t.transaction_date, t.id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	byID := map[int64]*auditRow{}
	byAmount := map[int64][]*auditRow{}
	for rows.Next() {
		r := &auditRow{}
		var cat, xfer sql.NullInt64
		x := &r.side
		if err := rows.Scan(&x.TxID, &x.AccountID, &x.Date, &x.Amount, &x.Payee, &x.Notes, &cat,
			&r.info.AccountName, &r.info.AccountType, &r.info.CategoryName, &xfer); err != nil {
			return nil, nil, err
		}
		if cat.Valid {
			v := cat.Int64
			x.CategoryID = &v
		}
		r.info.ID, r.info.AccountID, r.info.Date = x.TxID, x.AccountID, x.Date
		r.info.Amount, r.info.Payee = x.Amount, x.Payee
		if xfer.Valid {
			v := xfer.Int64
			r.info.TransferID = &v
		}
		byID[x.TxID] = r
		byAmount[x.Amount] = append(byAmount[x.Amount], r)
	}
	return byID, byAmount, rows.Err()
}

var financeChargeRe = regexp.MustCompile(`(?i)(?:finance charge(?:/fee)?|interest)[^0-9$]*\$?\s*([0-9][0-9,]*\.[0-9]{2})`)

// parseFinanceCharge finds "Finance Charge/Fee: $99.24"-style interest in a loan description.
func parseFinanceCharge(text string) int64 {
	m := financeChargeRe.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	v, err := money.ParseDecimal(m[1])
	if err != nil || v <= 0 {
		return 0
	}
	return v
}

// BuildTransferAudit inspects live transactions for missed, wrong, and one-sided transfers.
func (s *Store) BuildTransferAudit() (TransferAudit, error) {
	var out TransferAudit
	m, err := loadTransferMatcher(s.DB)
	if err != nil {
		return out, err
	}
	byID, byAmount, err := s.loadAuditRows()
	if err != nil {
		return out, err
	}
	pairs, err := s.liveTransferPairs()
	if err != nil {
		return out, err
	}
	used := map[int64]bool{}

	// Loan payments recorded as principal only on the loan side.
	out.Loans = s.auditLoanPayments(m, byID, used)

	// Missed pairs among unlinked transactions.
	var cands []pairCandidate
	for _, r := range byID {
		if r.info.TransferID != nil || r.side.Amount >= 0 || used[r.side.TxID] {
			continue
		}
		for _, c := range byAmount[-r.side.Amount] {
			if c.info.TransferID != nil || used[c.side.TxID] {
				continue
			}
			score, ok := m.score(r.side, c.side)
			if !ok {
				continue
			}
			days, _ := dayDiff(r.side.Date, c.side.Date)
			cands = append(cands, pairCandidate{Left: r.side.TxID, Right: c.side.TxID, Score: score, Days: days})
		}
	}
	for _, c := range pickPairs(cands, TransferPossibleScore, true) {
		used[c.Left], used[c.Right] = true, true
		out.Missed = append(out.Missed, MissedPair{
			Out: byID[c.Left].info, In: byID[c.Right].info, Score: c.Score,
			HighConfidence: c.Score >= TransferAutoScore && !c.Ambiguous,
			Ambiguous:      c.Ambiguous,
		})
	}
	sort.SliceStable(out.Missed, func(i, j int) bool {
		return out.Missed[i].Out.Date < out.Missed[j].Out.Date
	})

	out.Suspicious = auditSuspicious(m, pairs, byID, byAmount)
	for _, sp := range out.Suspicious {
		if sp.Suggest != nil {
			used[sp.Suggest.A.ID], used[sp.Suggest.B.ID] = true, true
		}
	}

	// Transfer-like transactions with nothing on the other side.
	for _, r := range byID {
		if r.info.TransferID != nil || used[r.side.TxID] || r.side.CategoryID == nil {
			continue
		}
		c := m.categories[*r.side.CategoryID]
		if c.kind != "transfer" && !c.debt {
			continue
		}
		out.OneSided = append(out.OneSided, OneSidedTx{AuditTx: r.info, SuggestedAccountID: m.mentionedAccount(r.side)})
	}
	sort.SliceStable(out.OneSided, func(i, j int) bool {
		a, b := out.OneSided[i], out.OneSided[j]
		if a.AccountName != b.AccountName {
			return a.AccountName < b.AccountName
		}
		return a.Date < b.Date
	})

	out.StaleSplits, err = s.StaleTransferSplitCount()
	return out, err
}

type livePair struct {
	id, from, to int64
}

func (s *Store) liveTransferPairs() ([]livePair, error) {
	rows, err := s.DB.Query(`SELECT x.id, x.from_tx_id, x.to_tx_id FROM transfers x
		JOIN transactions f ON f.id = x.from_tx_id AND f.deleted_at IS NULL
		JOIN transactions t ON t.id = x.to_tx_id AND t.deleted_at IS NULL
		ORDER BY x.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []livePair
	for rows.Next() {
		var p livePair
		if err := rows.Scan(&p.id, &p.from, &p.to); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) auditLoanPayments(m *transferMatcher, byID map[int64]*auditRow, used map[int64]bool) []LoanPaymentFix {
	var loans, payments []*auditRow
	for _, r := range byID {
		if r.info.TransferID != nil {
			continue
		}
		if r.info.AccountType == "loan" && r.side.Amount > 0 {
			loans = append(loans, r)
		} else if !isLiabilityType(r.info.AccountType) && r.side.Amount < 0 {
			payments = append(payments, r)
		}
	}
	sort.Slice(loans, func(i, j int) bool { return loans[i].side.Date < loans[j].side.Date })

	var out []LoanPaymentFix
	for _, l := range loans {
		interest := parseFinanceCharge(sideText(l.side))
		if interest <= 0 {
			continue
		}
		full := l.side.Amount + interest
		var best, near *auditRow
		bestScore, nearDays := -1<<31, transferMaxDays+1
		for _, p := range payments {
			if used[p.side.TxID] {
				continue
			}
			days, ok := dayDiff(p.side.Date, l.side.Date)
			if !ok || days > transferMaxDays {
				continue
			}
			if -p.side.Amount == full {
				probe := l.side
				probe.Amount = full
				if score, ok := m.score(p.side, probe); ok && score > bestScore {
					best, bestScore = p, score
				}
				continue
			}
			if days < nearDays && (m.mentions(sideText(p.side), p.side.AccountID)[l.side.AccountID] ||
				containsAny(sideText(p.side), []string{"LOAN"})) {
				near, nearDays = p, days
			}
		}
		fix := LoanPaymentFix{Loan: l.info, Interest: interest}
		switch {
		case best != nil:
			fix.Out, fix.Exact = best.info, true
			used[best.side.TxID] = true
		case near != nil:
			fix.Out = near.info
			used[near.side.TxID] = true
			fix.Note = fmt.Sprintf("Payment is %s but principal + interest is %s; check the loan statement.",
				money.FormatAbs(near.side.Amount, ""), money.Format(full, ""))
		default:
			fix.Note = "No matching payment found within a week."
		}
		used[l.side.TxID] = true
		out = append(out, fix)
	}
	return out
}

func auditSuspicious(m *transferMatcher, pairs []livePair, byID map[int64]*auditRow, byAmount map[int64][]*auditRow) []SuspiciousPair {
	pairScore := map[int64]int{}   // tx id -> score of its current pair
	pairOf := map[int64]livePair{} // tx id -> its pair
	scores := map[int64]int{}      // transfer id -> score
	okPair := map[int64]bool{}
	for _, p := range pairs {
		f, t := byID[p.from], byID[p.to]
		if f == nil || t == nil {
			continue
		}
		sc, ok := m.score(f.side, t.side)
		if !ok {
			sc = -100
		}
		scores[p.id], okPair[p.id] = sc, ok
		pairScore[p.from], pairScore[p.to] = sc, sc
		pairOf[p.from], pairOf[p.to] = p, p
	}

	var out []SuspiciousPair
	covered := map[int64]bool{}
	for _, p := range pairs {
		f, t := byID[p.from], byID[p.to]
		if f == nil || t == nil || covered[p.id] {
			continue
		}
		cur := scores[p.id]
		var best *RepairSuggestion
		for _, side := range []*auditRow{f, t} {
			partner := p.to
			if side == t {
				partner = p.from
			}
			for _, c := range byAmount[-side.side.Amount] {
				if c.side.TxID == partner || c.side.AccountID == side.side.AccountID {
					continue
				}
				sc, ok := m.score(side.side, c.side)
				if !ok {
					continue
				}
				if c.info.TransferID != nil && pairScore[c.side.TxID] >= sc {
					continue
				}
				if best == nil || sc > best.Score {
					a, b := side.info, c.info
					if a.Amount > 0 {
						a, b = b, a
					}
					breaks := []int64{p.id}
					if cp, linked := pairOf[c.side.TxID]; linked && cp.id != p.id {
						breaks = append(breaks, cp.id)
					}
					best = &RepairSuggestion{A: a, B: b, Score: sc, Breaks: breaks}
				}
			}
		}
		betterExists := best != nil && best.Score >= TransferAutoScore && best.Score >= cur+transferAmbiguity
		if cur >= TransferPossibleScore && !betterExists {
			continue
		}
		sp := SuspiciousPair{TransferID: p.id, From: f.info, To: t.info, Score: cur}
		switch {
		case !okPair[p.id]:
			sp.Reason = "Amounts or dates no longer line up."
		case betterExists && cur >= TransferPossibleScore:
			sp.Reason = "A much better match exists."
		default:
			sp.Reason = "The descriptions don't look like the same transfer."
		}
		if best != nil && best.Score >= TransferAutoScore {
			sp.Suggest = best
			for _, id := range best.Breaks {
				covered[id] = true
			}
		}
		out = append(out, sp)
	}
	return out
}

// StaleTransferSplitCount counts linked transfers whose splits aren't a single
// Transfer split for the full amount.
func (s *Store) StaleTransferSplitCount() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(1) FROM transactions t
		WHERE t.deleted_at IS NULL AND t.transfer_id IS NOT NULL AND (
			t.is_split = 1
			OR (SELECT COUNT(1) FROM transaction_splits sp WHERE sp.transaction_id = t.id) != 1
			OR EXISTS (SELECT 1 FROM transaction_splits sp JOIN categories c ON c.id = sp.category_id
				WHERE sp.transaction_id = t.id AND (c.kind != 'transfer' OR sp.amount != t.amount))
			OR COALESCE((SELECT kind FROM categories WHERE id = t.category_id), '') != 'transfer')`).Scan(&n)
	return n, err
}

// NormalizeTransferSplits rewrites stale linked transfers to one Transfer split.
func (s *Store) NormalizeTransferSplits() (int, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	xferCat, err := categoryIDByKindNameTx(tx, "transfer", "Transfer")
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(`SELECT t.id FROM transactions t
		WHERE t.deleted_at IS NULL AND t.transfer_id IS NOT NULL AND (
			t.is_split = 1 OR t.category_id IS NOT ?
			OR (SELECT COUNT(1) FROM transaction_splits sp WHERE sp.transaction_id = t.id) != 1
			OR EXISTS (SELECT 1 FROM transaction_splits sp WHERE sp.transaction_id = t.id
				AND (sp.category_id != ? OR sp.amount != t.amount)))`, xferCat, xferCat)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := setTransferCategoryTx(tx, id, xferCat); err != nil {
			return 0, err
		}
	}
	return len(ids), tx.Commit()
}

// --- edits with undo ---

type splitSnap struct {
	CategoryID int64   `json:"category_id"`
	Person     string  `json:"person"`
	Amount     int64   `json:"amount"`
	Notes      *string `json:"notes"`
	Order      int     `json:"order"`
}

type txSnapshot struct {
	ID          int64       `json:"id"`
	Amount      int64       `json:"amount"`
	CategoryID  *int64      `json:"category_id"`
	IsSplit     int         `json:"is_split"`
	NeedsReview int         `json:"needs_review"`
	Splits      []splitSnap `json:"splits"`
}

type transferUndo struct {
	Snapshots []txSnapshot `json:"snapshots"`
	Removed   [][2]int64   `json:"removed"` // from, to of pairs that were unlinked
	Added     []int64      `json:"added"`   // transfer ids created
	Created   []int64      `json:"created"` // transactions created
}

// transferEdit batches transfer changes in one DB transaction and records undo.
type transferEdit struct {
	tx      *sql.Tx
	xferCat int64
	undo    transferUndo
	snapped map[int64]bool
}

func (s *Store) beginTransferEdit() (*transferEdit, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	xferCat, err := categoryIDByKindNameTx(tx, "transfer", "Transfer")
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("transfer category missing: %w", err)
	}
	return &transferEdit{tx: tx, xferCat: xferCat, snapped: map[int64]bool{}}, nil
}

func (e *transferEdit) rollback() {
	_ = e.tx.Rollback()
}

func (e *transferEdit) commit(kind string, personID int64) (string, error) {
	undoID, err := newUndo(e.tx, kind, e.undo, personID)
	if err != nil {
		e.rollback()
		return "", err
	}
	return undoID, e.tx.Commit()
}

func (e *transferEdit) snap(id int64) error {
	if e.snapped[id] {
		return nil
	}
	var sn txSnapshot
	var cat sql.NullInt64
	err := e.tx.QueryRow(`SELECT id, amount, category_id, is_split, needs_review FROM transactions
		WHERE id=? AND deleted_at IS NULL`, id).Scan(&sn.ID, &sn.Amount, &cat, &sn.IsSplit, &sn.NeedsReview)
	if err == sql.ErrNoRows {
		return fmt.Errorf("transaction #%d not found", id)
	}
	if err != nil {
		return err
	}
	if cat.Valid {
		v := cat.Int64
		sn.CategoryID = &v
	}
	rows, err := e.tx.Query(`SELECT category_id, person, amount, notes, display_order FROM transaction_splits
		WHERE transaction_id=? ORDER BY display_order, id`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sp splitSnap
		var notes sql.NullString
		if err := rows.Scan(&sp.CategoryID, &sp.Person, &sp.Amount, &notes, &sp.Order); err != nil {
			return err
		}
		if notes.Valid {
			v := notes.String
			sp.Notes = &v
		}
		sn.Splits = append(sn.Splits, sp)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	e.snapped[id] = true
	e.undo.Snapshots = append(e.undo.Snapshots, sn)
	return nil
}

// unlink removes the pair txID belongs to (if any); both sides go to review.
func (e *transferEdit) unlink(txID int64) error {
	var xfer sql.NullInt64
	if err := e.tx.QueryRow(`SELECT transfer_id FROM transactions WHERE id=?`, txID).Scan(&xfer); err != nil {
		return err
	}
	if !xfer.Valid {
		return nil
	}
	return e.unlinkPair(xfer.Int64)
}

func (e *transferEdit) unlinkPair(xferID int64) error {
	var from, to int64
	if err := e.tx.QueryRow(`SELECT from_tx_id, to_tx_id FROM transfers WHERE id=?`, xferID).Scan(&from, &to); err != nil {
		return err
	}
	for _, id := range []int64{from, to} {
		if err := e.snap(id); err != nil {
			return err
		}
	}
	if _, err := e.tx.Exec(`UPDATE transactions SET transfer_id=NULL, needs_review=1,
		updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id IN (?,?)`, from, to); err != nil {
		return err
	}
	if _, err := e.tx.Exec(`DELETE FROM transfers WHERE id=?`, xferID); err != nil {
		return err
	}
	e.undo.Removed = append(e.undo.Removed, [2]int64{from, to})
	return nil
}

// link joins two unlinked transactions with exactly opposite amounts.
func (e *transferEdit) link(aID, bID int64) error {
	a, err := loadTxSide(e.tx, aID)
	if err != nil {
		return fmt.Errorf("transaction #%d not found", aID)
	}
	b, err := loadTxSide(e.tx, bID)
	if err != nil {
		return fmt.Errorf("transaction #%d not found", bID)
	}
	if a.AccountID == b.AccountID {
		return fmt.Errorf("#%d and #%d are on the same account", aID, bID)
	}
	if a.Amount == 0 || a.Amount != -b.Amount {
		return fmt.Errorf("#%d and #%d don't have opposite amounts", aID, bID)
	}
	var linked int
	if err := e.tx.QueryRow(`SELECT COUNT(1) FROM transactions WHERE id IN (?,?) AND transfer_id IS NOT NULL`,
		aID, bID).Scan(&linked); err != nil {
		return err
	}
	if linked > 0 {
		return fmt.Errorf("#%d or #%d is already part of a transfer", aID, bID)
	}
	for _, id := range []int64{aID, bID} {
		if err := e.snap(id); err != nil {
			return err
		}
		if err := setTransferCategoryTx(e.tx, id, e.xferCat); err != nil {
			return err
		}
	}
	from, to := aID, bID
	if a.Amount > 0 {
		from, to = bID, aID
	}
	res, err := e.tx.Exec(`INSERT INTO transfers(from_tx_id, to_tx_id) VALUES (?,?)`, from, to)
	if err != nil {
		return err
	}
	xferID, _ := res.LastInsertId()
	if _, err := e.tx.Exec(`UPDATE transactions SET transfer_id=? WHERE id IN (?,?)`, xferID, from, to); err != nil {
		return err
	}
	e.undo.Added = append(e.undo.Added, xferID)
	return nil
}

// LinkTransfers links each (a, b) pair as one transfer.
func (s *Store) LinkTransfers(pairs [][2]int64, personID int64) (string, error) {
	if len(pairs) == 0 {
		return "", fmt.Errorf("nothing to link")
	}
	e, err := s.beginTransferEdit()
	if err != nil {
		return "", err
	}
	for _, p := range pairs {
		if err := e.link(p[0], p[1]); err != nil {
			e.rollback()
			return "", err
		}
	}
	return e.commit("transfer_link", personID)
}

// LinkHighConfidenceTransfers links every high-confidence missed pair.
func (s *Store) LinkHighConfidenceTransfers(personID int64) (string, int, error) {
	audit, err := s.BuildTransferAudit()
	if err != nil {
		return "", 0, err
	}
	var pairs [][2]int64
	for _, p := range audit.Missed {
		if p.HighConfidence {
			pairs = append(pairs, [2]int64{p.Out.ID, p.In.ID})
		}
	}
	if len(pairs) == 0 {
		return "", 0, nil
	}
	undoID, err := s.LinkTransfers(pairs, personID)
	return undoID, len(pairs), err
}

// UnlinkTransfer splits a pair apart; both sides go back to review.
func (s *Store) UnlinkTransfer(xferID, personID int64) (string, error) {
	e, err := s.beginTransferEdit()
	if err != nil {
		return "", err
	}
	if err := e.unlinkPair(xferID); err != nil {
		e.rollback()
		return "", err
	}
	return e.commit("transfer_unlink", personID)
}

// RePairTransfer unlinks whatever a and b are paired with, then links a with b.
func (s *Store) RePairTransfer(aID, bID, personID int64) (string, error) {
	e, err := s.beginTransferEdit()
	if err != nil {
		return "", err
	}
	for _, id := range []int64{aID, bID} {
		if err := e.unlink(id); err != nil {
			e.rollback()
			return "", err
		}
	}
	if err := e.link(aID, bID); err != nil {
		e.rollback()
		return "", err
	}
	return e.commit("transfer_link", personID)
}

// MarkNotTransfer clears the category so the transactions land in review.
func (s *Store) MarkNotTransfer(ids []int64, personID int64) (string, error) {
	if len(ids) == 0 {
		return "", fmt.Errorf("nothing selected")
	}
	e, err := s.beginTransferEdit()
	if err != nil {
		return "", err
	}
	for _, id := range ids {
		if err := e.unlink(id); err != nil {
			e.rollback()
			return "", err
		}
		if err := e.snap(id); err != nil {
			e.rollback()
			return "", err
		}
		if _, err := e.tx.Exec(`DELETE FROM transaction_splits WHERE transaction_id=?`, id); err != nil {
			e.rollback()
			return "", err
		}
		if _, err := e.tx.Exec(`UPDATE transactions SET category_id=NULL, is_split=0, needs_review=1,
			updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, id); err != nil {
			e.rollback()
			return "", err
		}
	}
	return e.commit("transfer_unlink", personID)
}

// CounterpartResult reports what CreateCounterparts did.
type CounterpartResult struct {
	UndoID        string
	Created       int
	ChangedAnchor int // counterparts dated on/after the target's anchor (current balance moved)
	TargetName    string
}

// CreateCounterparts mirrors each one-sided transaction into accountID and links the pair.
func (s *Store) CreateCounterparts(ids []int64, accountID, personID int64) (CounterpartResult, error) {
	var res CounterpartResult
	if len(ids) == 0 {
		return res, fmt.Errorf("nothing selected")
	}
	var anchorDate string
	if err := s.DB.QueryRow(`SELECT name, anchor_date FROM accounts WHERE id=?`, accountID).Scan(&res.TargetName, &anchorDate); err != nil {
		return res, fmt.Errorf("pick an account")
	}
	e, err := s.beginTransferEdit()
	if err != nil {
		return res, err
	}
	for _, id := range ids {
		var srcAcct int64
		var srcName, date, person string
		var amount int64
		var payee, notes sql.NullString
		var xfer sql.NullInt64
		err := e.tx.QueryRow(`SELECT t.account_id, a.name, t.transaction_date, t.person, t.amount, t.payee_name, t.notes, t.transfer_id
			FROM transactions t JOIN accounts a ON a.id = t.account_id
			WHERE t.id=? AND t.deleted_at IS NULL`, id).
			Scan(&srcAcct, &srcName, &date, &person, &amount, &payee, &notes, &xfer)
		if err != nil {
			e.rollback()
			return res, fmt.Errorf("transaction #%d not found", id)
		}
		if xfer.Valid {
			e.rollback()
			return res, fmt.Errorf("#%d is already part of a transfer", id)
		}
		if srcAcct == accountID {
			e.rollback()
			return res, fmt.Errorf("#%d is already on %s", id, res.TargetName)
		}
		label := "Transfer from " + srcName
		if amount > 0 {
			label = "Transfer to " + srcName
		}
		ins, err := e.tx.Exec(`INSERT INTO transactions(account_id, payee_name, notes, amount, transaction_date, person,
			status, category_id, is_split, created_by, updated_by)
			VALUES (?,?,?,?,?,?, 'cleared', ?, 0, ?, ?)`,
			accountID, label, nullStr(payee.String), -amount, date, person, e.xferCat, personID, personID)
		if err != nil {
			e.rollback()
			return res, err
		}
		newID, _ := ins.LastInsertId()
		e.undo.Created = append(e.undo.Created, newID)
		e.snapped[newID] = true
		if _, err := e.tx.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, display_order)
			VALUES (?,?,?,?,0)`, newID, e.xferCat, person, -amount); err != nil {
			e.rollback()
			return res, err
		}
		if err := e.link(id, newID); err != nil {
			e.rollback()
			return res, err
		}
		res.Created++
		if date >= anchorDate {
			res.ChangedAnchor++
		}
	}
	res.UndoID, err = e.commit("transfer_link", personID)
	return res, err
}

// PairLoanPaymentWithInterest records the full payment on the loan (so the pair
// is symmetric), adds the interest as an expense on the loan, and links the pair.
func (s *Store) PairLoanPaymentWithInterest(outID, loanID, interest, personID int64) (string, error) {
	e, err := s.beginTransferEdit()
	if err != nil {
		return "", err
	}
	if err := e.pairLoan(outID, loanID, interest, personID); err != nil {
		e.rollback()
		return "", err
	}
	return e.commit("transfer_link", personID)
}

// PairExactLoanPayments applies PairLoanPaymentWithInterest to every exact
// match in the audit, as one undoable change.
func (s *Store) PairExactLoanPayments(personID int64) (string, int, error) {
	audit, err := s.BuildTransferAudit()
	if err != nil {
		return "", 0, err
	}
	e, err := s.beginTransferEdit()
	if err != nil {
		return "", 0, err
	}
	n := 0
	for _, l := range audit.Loans {
		if !l.Exact {
			continue
		}
		if err := e.pairLoan(l.Out.ID, l.Loan.ID, l.Interest, personID); err != nil {
			e.rollback()
			return "", 0, err
		}
		n++
	}
	if n == 0 {
		e.rollback()
		return "", 0, nil
	}
	undoID, err := e.commit("transfer_link", personID)
	return undoID, n, err
}

func (e *transferEdit) pairLoan(outID, loanID, interest, personID int64) error {
	if interest <= 0 {
		return fmt.Errorf("interest must be positive")
	}
	out, err := loadTxSide(e.tx, outID)
	if err != nil {
		return fmt.Errorf("payment #%d not found", outID)
	}
	var loanAcct int64
	var loanType, date, person string
	var amount int64
	var batch sql.NullInt64
	err = e.tx.QueryRow(`SELECT t.account_id, a.type, t.transaction_date, t.person, t.amount, t.import_batch_id
		FROM transactions t JOIN accounts a ON a.id = t.account_id
		WHERE t.id=? AND t.deleted_at IS NULL`, loanID).Scan(&loanAcct, &loanType, &date, &person, &amount, &batch)
	if err != nil {
		return fmt.Errorf("loan transaction #%d not found", loanID)
	}
	if !isLiabilityType(loanType) {
		return fmt.Errorf("#%d is not on a loan or credit account", loanID)
	}
	if out.Amount >= 0 || -out.Amount != amount+interest {
		return fmt.Errorf("payment %s doesn't equal principal %s + interest %s",
			money.FormatAbs(out.Amount, ""), money.Format(amount, ""), money.Format(interest, ""))
	}
	interestCat, err := loanInterestCategoryTx(e.tx)
	if err != nil {
		return err
	}
	if err := e.snap(loanID); err != nil {
		return err
	}
	if _, err := e.tx.Exec(`UPDATE transactions SET amount=? WHERE id=?`, -out.Amount, loanID); err != nil {
		return err
	}
	newID, err := insertInterestTx(e.tx, loanAcct, date, person, interest, interestCat, personID, batch)
	if err != nil {
		return err
	}
	e.undo.Created = append(e.undo.Created, newID)
	e.snapped[newID] = true
	return e.link(outID, loanID)
}

func loanInterestCategoryTx(q querier) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT id FROM categories WHERE kind='expense' AND name='Loan interest' AND archived=0
		ORDER BY id LIMIT 1`).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("the Loan interest category is missing or archived")
	}
	return id, nil
}

func insertInterestTx(q querier, accountID int64, date, person string, interest, catID, personID int64, batch sql.NullInt64) (int64, error) {
	if person == "" {
		person = "shared"
	}
	res, err := q.Exec(`INSERT INTO transactions(account_id, payee_name, notes, amount, transaction_date, person,
		status, category_id, is_split, created_by, updated_by, import_batch_id)
		VALUES (?, 'Interest', 'Interest portion of loan payment', ?, ?, ?, 'cleared', ?, 0, ?, ?, ?)`,
		accountID, -interest, date, person, catID, personID, personID, batch)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	_, err = q.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, display_order)
		VALUES (?,?,?,?,0)`, id, catID, person, -interest)
	return id, err
}

func undoTransferChange(tx *sql.Tx, payload string) error {
	var u transferUndo
	if err := json.Unmarshal([]byte(payload), &u); err != nil {
		return err
	}
	for _, id := range u.Added {
		if _, err := tx.Exec(`UPDATE transactions SET transfer_id=NULL WHERE transfer_id=?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM transfers WHERE id=?`, id); err != nil {
			return err
		}
	}
	for _, id := range u.Created {
		for _, q := range []string{
			`DELETE FROM transaction_splits WHERE transaction_id=?`,
			`DELETE FROM transaction_tags WHERE transaction_id=?`,
			`DELETE FROM transactions WHERE id=?`,
		} {
			if _, err := tx.Exec(q, id); err != nil {
				return err
			}
		}
	}
	for _, sn := range u.Snapshots {
		if _, err := tx.Exec(`UPDATE transactions SET amount=?, category_id=?, is_split=?, needs_review=?,
			updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`,
			sn.Amount, sn.CategoryID, sn.IsSplit, sn.NeedsReview, sn.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM transaction_splits WHERE transaction_id=?`, sn.ID); err != nil {
			return err
		}
		for _, sp := range sn.Splits {
			if _, err := tx.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, notes, display_order)
				VALUES (?,?,?,?,?,?)`, sn.ID, sp.CategoryID, sp.Person, sp.Amount, sp.Notes, sp.Order); err != nil {
				return err
			}
		}
	}
	for _, p := range u.Removed {
		res, err := tx.Exec(`INSERT INTO transfers(from_tx_id, to_tx_id) VALUES (?,?)`, p[0], p[1])
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if _, err := tx.Exec(`UPDATE transactions SET transfer_id=? WHERE id IN (?,?)`, id, p[0], p[1]); err != nil {
			return err
		}
	}
	return nil
}
