package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// txSnap is enough prior state to put a transaction back after a transfer repair.
type txSnap struct {
	ID          int64        `json:"id"`
	CategoryID  *int64       `json:"category_id"`
	NeedsReview int          `json:"needs_review"`
	TransferID  *int64       `json:"transfer_id"`
	Person      string       `json:"person"`
	Amount      int64        `json:"amount"`
	Splits      []recatSplit `json:"splits"`
}

type xferSnap struct {
	ID      int64 `json:"id"`
	From    int64 `json:"from"`
	To      int64 `json:"to"`
	Created bool  `json:"created,omitempty"`
	Deleted bool  `json:"deleted,omitempty"`
}

type transferRepairPayload struct {
	Txs       []txSnap   `json:"txs"`
	Transfers []xferSnap `json:"transfers"`
}

// RepairTransferOutliers fixes the known card-payment mismatches: purchases
// paired with unrelated deposits, equal payments crossed between cards, the
// unpaired $85 Capital One payment, and the Oct 16 Old Navy payment credit
// filed as spending. It is a no-op once those rows are already in the right
// shape, and the changes can be undone.
func (s *Store) RepairTransferOutliers(personID int64) (string, bool, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()

	var payload transferRepairPayload
	seenTx := map[int64]bool{}
	snap := func(id int64) error {
		if seenTx[id] {
			return nil
		}
		item, err := loadTxSnap(tx, id)
		if err != nil {
			return err
		}
		seenTx[id] = true
		payload.Txs = append(payload.Txs, item)
		return nil
	}

	xferCat, err := categoryIDByKindNameTx(tx, "transfer", "Transfer")
	if err != nil {
		return "", false, err
	}

	// Purchases that were swallowed by an opposite deposit.
	if err := unlinkFalsePair(tx, snap, &payload, xferCat,
		"ANDERSONS", -27237, "2026-08-24", "%PAYPAL%", 27237, "2026-08-26"); err != nil {
		return "", false, err
	}
	if err := unlinkFalsePair(tx, snap, &payload, xferCat,
		"%ONCEMOREWITHLOVE%", -5000, "2026-05-16", "%ONLINE BANKING TRANSFER%", 5000, "2026-05-19"); err != nil {
		return "", false, err
	}

	// Same-day equal amounts crossed between the two cards.
	if err := swapCrossed(tx, snap, &payload,
		"%CAPITAL ONE%", -30000, "2026-05-04", "%PAYMENT RECEIVED%",
		"%BARCLAY%", -30000, "2026-05-04", "%CAPITAL ONE%"); err != nil {
		return "", false, err
	}
	if err := swapCrossed(tx, snap, &payload,
		"%CAPITAL ONE%", -10000, "2026-05-26", "%PAYMENT RECEIVED%",
		"%BARCLAY%", -10000, "2026-05-26", "%CAPITAL ONE%"); err != nil {
		return "", false, err
	}

	if err := linkPayment(tx, snap, &payload, xferCat,
		"%MOBILE PMT CAPITAL ONE%", -8500, "2026-05-11",
		"%CAPITAL ONE MOBILE PYMT%", 8500, "2026-05-06"); err != nil {
		return "", false, err
	}
	if err := filePaymentCredit(tx, snap, xferCat, "%PAYMENT RECEIVED%", 35000, "2026-10-16"); err != nil {
		return "", false, err
	}

	if len(payload.Txs) == 0 && len(payload.Transfers) == 0 {
		return "", false, nil
	}
	raw, _ := json.Marshal(payload)
	undoID := uuid.NewString()
	exp := time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339Nano)
	var created any
	if personID > 0 {
		created = personID
	}
	if _, err := tx.Exec(`INSERT INTO undo_ops(id, kind, payload, expires_at, created_by) VALUES (?,?,?,?,?)`,
		undoID, "transfer_repair", string(raw), exp, created); err != nil {
		return "", false, err
	}
	return undoID, true, tx.Commit()
}

func loadTxSnap(tx *sql.Tx, id int64) (txSnap, error) {
	var item txSnap
	var cat, xfer sql.NullInt64
	var needs int
	err := tx.QueryRow(`SELECT category_id, needs_review, person, amount, transfer_id
		FROM transactions WHERE id=?`, id).Scan(&cat, &needs, &item.Person, &item.Amount, &xfer)
	if err != nil {
		return item, err
	}
	item.ID = id
	item.NeedsReview = needs
	if cat.Valid {
		v := cat.Int64
		item.CategoryID = &v
	}
	if xfer.Valid {
		v := xfer.Int64
		item.TransferID = &v
	}
	rows, err := tx.Query(`SELECT category_id, person, amount, COALESCE(notes,''), display_order
		FROM transaction_splits WHERE transaction_id=?`, id)
	if err != nil {
		return item, err
	}
	defer rows.Close()
	for rows.Next() {
		var sp recatSplit
		if err := rows.Scan(&sp.CategoryID, &sp.Person, &sp.Amount, &sp.Notes, &sp.Order); err != nil {
			return item, err
		}
		item.Splits = append(item.Splits, sp)
	}
	return item, rows.Err()
}

type pairHit struct {
	xferID, fromID, toID int64
}

func findLinked(tx *sql.Tx, fromLike string, fromAmt int64, fromDate, toLike string, toAmt int64, toDate string) (*pairHit, error) {
	var h pairHit
	err := tx.QueryRow(`SELECT tr.id, tf.id, tt.id
		FROM transfers tr
		JOIN transactions tf ON tf.id = tr.from_tx_id
		JOIN transactions tt ON tt.id = tr.to_tx_id
		WHERE tf.deleted_at IS NULL AND tt.deleted_at IS NULL
		  AND tf.amount=? AND tf.transaction_date=? AND UPPER(tf.payee_name) LIKE UPPER(?)
		  AND tt.amount=? AND tt.transaction_date=? AND UPPER(tt.payee_name) LIKE UPPER(?)`,
		fromAmt, fromDate, fromLike, toAmt, toDate, toLike).Scan(&h.xferID, &h.fromID, &h.toID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// unlinkFalsePair separates a purchase from the deposit it was paired with.
// The purchase goes back to review with no category. The deposit keeps a
// transfer category so it does not become income, but it is no longer linked.
func unlinkFalsePair(tx *sql.Tx, snap func(int64) error, payload *transferRepairPayload, xferCat int64, purchaseLike string, purchaseAmt int64, purchaseDate, depositLike string, depositAmt int64, depositDate string) error {
	// The purchase is the negative leg, so it may be stored as from_tx.
	h, err := findLinked(tx, purchaseLike, purchaseAmt, purchaseDate, depositLike, depositAmt, depositDate)
	if err != nil {
		return err
	}
	purchaseID, depositID := int64(0), int64(0)
	var xferID int64
	if h != nil {
		xferID, purchaseID, depositID = h.xferID, h.fromID, h.toID
	} else {
		h, err = findLinked(tx, depositLike, depositAmt, depositDate, purchaseLike, purchaseAmt, purchaseDate)
		if err != nil || h == nil {
			return err
		}
		xferID, depositID, purchaseID = h.xferID, h.fromID, h.toID
	}
	if err := snap(purchaseID); err != nil {
		return err
	}
	if err := snap(depositID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE transactions SET transfer_id=NULL WHERE id IN (?,?)`, purchaseID, depositID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM transfers WHERE id=?`, xferID); err != nil {
		return err
	}
	payload.Transfers = append(payload.Transfers, xferSnap{ID: h.xferID, From: h.fromID, To: h.toID, Deleted: true})
	if err := clearCategory(tx, purchaseID); err != nil {
		return err
	}
	return setTransferCategory(tx, depositID, xferCat, nil)
}

func clearCategory(tx *sql.Tx, id int64) error {
	if _, err := tx.Exec(`UPDATE transactions SET category_id=NULL, needs_review=1,
		updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM transaction_splits WHERE transaction_id=?`, id)
	return err
}

func setTransferCategory(tx *sql.Tx, id, catID int64, transferID *int64) error {
	var amt int64
	var person string
	if err := tx.QueryRow(`SELECT amount, person FROM transactions WHERE id=?`, id).Scan(&amt, &person); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE transactions SET category_id=?, needs_review=0, transfer_id=?,
		updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, catID, transferID, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM transaction_splits WHERE transaction_id=?`, id); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, display_order)
		VALUES (?,?,?,?,0)`, id, catID, person, amt)
	return err
}

func swapCrossed(tx *sql.Tx, snap func(int64) error, payload *transferRepairPayload, aFrom string, aAmt int64, aDate, aTo string, bFrom string, bAmt int64, bDate, bTo string) error {
	a, err := findLinkedLoose(tx, aFrom, aAmt, aDate, aTo, -aAmt)
	if err != nil || a == nil {
		return err
	}
	b, err := findLinkedLoose(tx, bFrom, bAmt, bDate, bTo, -bAmt)
	if err != nil || b == nil {
		return err
	}
	if a.xferID == b.xferID {
		return nil
	}
	for _, id := range []int64{a.fromID, a.toID, b.fromID, b.toID} {
		if err := snap(id); err != nil {
			return err
		}
	}
	payload.Transfers = append(payload.Transfers,
		xferSnap{ID: a.xferID, From: a.fromID, To: a.toID},
		xferSnap{ID: b.xferID, From: b.fromID, To: b.toID},
	)
	// Park one card leg on its own checking transaction (not currently a to_tx)
	// so the two transfer rows can trade card legs without a unique collision.
	if _, err := tx.Exec(`UPDATE transfers SET to_tx_id=? WHERE id=?`, b.fromID, b.xferID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE transfers SET to_tx_id=? WHERE id=?`, b.toID, a.xferID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE transfers SET to_tx_id=? WHERE id=?`, a.toID, b.xferID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE transactions SET transfer_id=? WHERE id=?`, a.xferID, b.toID); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE transactions SET transfer_id=? WHERE id=?`, b.xferID, a.toID)
	return err
}

func findLinkedLoose(tx *sql.Tx, fromLike string, fromAmt int64, fromDate, toLike string, toAmt int64) (*pairHit, error) {
	var h pairHit
	err := tx.QueryRow(`SELECT tr.id, tf.id, tt.id
		FROM transfers tr
		JOIN transactions tf ON tf.id = tr.from_tx_id
		JOIN transactions tt ON tt.id = tr.to_tx_id
		WHERE tf.deleted_at IS NULL AND tt.deleted_at IS NULL
		  AND tf.amount=? AND tf.transaction_date=? AND UPPER(tf.payee_name) LIKE UPPER(?)
		  AND tt.amount=? AND UPPER(tt.payee_name) LIKE UPPER(?)`,
		fromAmt, fromDate, fromLike, toAmt, toLike).Scan(&h.xferID, &h.fromID, &h.toID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

func linkPayment(tx *sql.Tx, snap func(int64) error, payload *transferRepairPayload, xferCat int64, fromLike string, fromAmt int64, fromDate, toLike string, toAmt int64, toDate string) error {
	var fromID, toID int64
	err := tx.QueryRow(`SELECT id FROM transactions
		WHERE deleted_at IS NULL AND transfer_id IS NULL AND amount=? AND transaction_date=?
		  AND UPPER(payee_name) LIKE UPPER(?)`, fromAmt, fromDate, fromLike).Scan(&fromID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	err = tx.QueryRow(`SELECT id FROM transactions
		WHERE deleted_at IS NULL AND transfer_id IS NULL AND amount=? AND transaction_date=?
		  AND UPPER(payee_name) LIKE UPPER(?)`, toAmt, toDate, toLike).Scan(&toID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if err := snap(fromID); err != nil || snap(toID) != nil {
		if err == nil {
			err = snap(toID)
		}
		return err
	}
	res, err := tx.Exec(`INSERT INTO transfers(from_tx_id, to_tx_id) VALUES (?,?)`, fromID, toID)
	if err != nil {
		return err
	}
	xferID, _ := res.LastInsertId()
	payload.Transfers = append(payload.Transfers, xferSnap{ID: xferID, From: fromID, To: toID, Created: true})
	if err := setTransferCategory(tx, fromID, xferCat, &xferID); err != nil {
		return err
	}
	return setTransferCategory(tx, toID, xferCat, &xferID)
}

func filePaymentCredit(tx *sql.Tx, snap func(int64) error, xferCat int64, like string, amt int64, date string) error {
	var id int64
	var kind string
	err := tx.QueryRow(`SELECT t.id, COALESCE(c.kind,'')
		FROM transactions t LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.deleted_at IS NULL AND t.transfer_id IS NULL AND t.amount=? AND t.transaction_date=?
		  AND UPPER(t.payee_name) LIKE UPPER(?)`, amt, date, like).Scan(&id, &kind)
	if err == sql.ErrNoRows || kind == "transfer" {
		return nil
	}
	if err != nil {
		return err
	}
	if err := snap(id); err != nil {
		return err
	}
	return setTransferCategory(tx, id, xferCat, nil)
}

func undoTransferRepair(tx *sql.Tx, payload string) error {
	var p transferRepairPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	for _, xf := range p.Transfers {
		if xf.Created {
			_, _ = tx.Exec(`UPDATE transactions SET transfer_id=NULL WHERE transfer_id=?`, xf.ID)
			if _, err := tx.Exec(`DELETE FROM transfers WHERE id=?`, xf.ID); err != nil {
				return err
			}
		}
	}
	for _, xf := range p.Transfers {
		if !xf.Deleted {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO transfers(id, from_tx_id, to_tx_id) VALUES (?,?,?)`, xf.ID, xf.From, xf.To); err != nil {
			return err
		}
	}
	for _, xf := range p.Transfers {
		if xf.Created || xf.Deleted {
			continue
		}
		if _, err := tx.Exec(`UPDATE transfers SET to_tx_id=? WHERE id=?`, xf.From, xf.ID); err != nil {
			return err
		}
	}
	for _, xf := range p.Transfers {
		if xf.Created || xf.Deleted {
			continue
		}
		if _, err := tx.Exec(`UPDATE transfers SET from_tx_id=?, to_tx_id=? WHERE id=?`, xf.From, xf.To, xf.ID); err != nil {
			return err
		}
	}
	for _, it := range p.Txs {
		if _, err := tx.Exec(`UPDATE transactions SET category_id=?, needs_review=?, transfer_id=?, person=?,
			updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`,
			it.CategoryID, it.NeedsReview, it.TransferID, it.Person, it.ID); err != nil {
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
