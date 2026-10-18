package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"finance.home/internal/importcsv"
	"finance.home/internal/models"
)

type CreateBatchInput struct {
	Filename  string
	RawCSV    string
	CreatedBy int64
}

func (s *Store) CreateImportBatch(in CreateBatchInput) (int64, error) {
	delim := importcsv.DetectDelimiter(in.RawCSV)
	headers, _, _, err := importcsv.Read(strings.NewReader(in.RawCSV), delim)
	if err != nil {
		return 0, err
	}
	hj, _ := json.Marshal(headers)
	res, err := s.DB.Exec(`INSERT INTO import_batches(filename, status, raw_csv, headers_json, created_by)
		VALUES (?, 'uploaded', ?, ?, ?)`, in.Filename, in.RawCSV, string(hj), in.CreatedBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetImportBatch(id int64) (*models.ImportBatch, error) {
	var b models.ImportBatch
	var acctID, profileID sql.NullInt64
	var acctName, committed sql.NullString
	var headersJSON sql.NullString
	err := s.DB.QueryRow(`SELECT b.id, b.account_id, COALESCE(a.name,''), b.profile_id, COALESCE(b.filename,''),
		b.status, b.headers_json, b.row_count, b.imported_count, b.created_at, b.committed_at
		FROM import_batches b
		LEFT JOIN accounts a ON a.id = b.account_id
		WHERE b.id=?`, id).Scan(
		&b.ID, &acctID, &acctName, &profileID, &b.Filename, &b.Status, &headersJSON,
		&b.RowCount, &b.ImportedCount, &b.CreatedAt, &committed)
	if err != nil {
		return nil, err
	}
	if acctID.Valid {
		v := acctID.Int64
		b.AccountID = &v
	}
	if profileID.Valid {
		v := profileID.Int64
		b.ProfileID = &v
	}
	b.AccountName = acctName.String
	b.CommittedAt = committed.String
	if headersJSON.Valid && headersJSON.String != "" {
		_ = json.Unmarshal([]byte(headersJSON.String), &b.Headers)
	}
	return &b, nil
}

func (s *Store) ListImportBatches(limit int) ([]models.ImportBatch, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.Query(`SELECT b.id, b.account_id, COALESCE(a.name,''), b.profile_id, COALESCE(b.filename,''),
		b.status, b.row_count, b.imported_count, b.created_at, COALESCE(b.committed_at,'')
		FROM import_batches b
		LEFT JOIN accounts a ON a.id = b.account_id
		ORDER BY b.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ImportBatch
	for rows.Next() {
		var b models.ImportBatch
		var acctID, profileID sql.NullInt64
		if err := rows.Scan(&b.ID, &acctID, &b.AccountName, &profileID, &b.Filename,
			&b.Status, &b.RowCount, &b.ImportedCount, &b.CreatedAt, &b.CommittedAt); err != nil {
			return nil, err
		}
		if acctID.Valid {
			v := acctID.Int64
			b.AccountID = &v
		}
		if profileID.Valid {
			v := profileID.Int64
			b.ProfileID = &v
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) BatchRawCSV(id int64) (string, error) {
	var raw sql.NullString
	err := s.DB.QueryRow(`SELECT raw_csv FROM import_batches WHERE id=?`, id).Scan(&raw)
	if err != nil {
		return "", err
	}
	return raw.String, nil
}

// BatchHeaders detects headers from the stored CSV with the current parser;
// headers_json is only a snapshot from upload time.
func (s *Store) BatchHeaders(id int64) ([]string, bool, error) {
	raw, err := s.BatchRawCSV(id)
	if err != nil {
		return nil, false, err
	}
	headers, _, hasHeader, err := importcsv.Read(strings.NewReader(raw), importcsv.DetectDelimiter(raw))
	return headers, hasHeader, err
}

// CancelImportBatch discards a batch that hasn't been committed (staging rows cascade).
func (s *Store) CancelImportBatch(id int64) error {
	res, err := s.DB.Exec(`DELETE FROM import_batches WHERE id=? AND status IN ('uploaded','mapped')`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("only imports that haven't been committed can be cancelled")
	}
	return nil
}

// StagingErrorSummary counts rows that failed to parse and returns the first error.
func (s *Store) StagingErrorSummary(batchID int64) (int, string, error) {
	var n int
	var first sql.NullString
	err := s.DB.QueryRow(`SELECT COUNT(1),
		(SELECT exception FROM import_staging_rows WHERE batch_id=? AND COALESCE(exception,'') != ''
		 ORDER BY row_index LIMIT 1)
		FROM import_staging_rows WHERE batch_id=? AND COALESCE(exception,'') != ''`,
		batchID, batchID).Scan(&n, &first)
	return n, first.String, err
}

type MapBatchInput struct {
	BatchID   int64
	AccountID int64
	ProfileID int64 // 0 = save new from fields
	Profile   MappingProfileInput
	SaveAs    string // optional new profile name
}

// MapAndStage applies mapping, parses CSV into staging rows, applies rules + match hints.
func (s *Store) MapAndStage(in MapBatchInput) error {
	raw, err := s.BatchRawCSV(in.BatchID)
	if err != nil {
		return err
	}
	profileID := in.ProfileID
	if profileID == 0 {
		name := in.SaveAs
		if name == "" {
			name = in.Profile.Name
		}
		if name == "" {
			name = "Import profile"
		}
		in.Profile.Name = name
		if in.Profile.AccountID == nil {
			aid := in.AccountID
			in.Profile.AccountID = &aid
		}
		profileID, err = s.UpsertMappingProfile(0, in.Profile)
		if err != nil {
			return err
		}
	}
	prof, err := s.GetMappingProfile(profileID)
	if err != nil {
		return err
	}

	ip := toImportProfile(*prof)
	headers, rows, err := importcsv.DetectHeaders(strings.NewReader(raw), ip.Delimiter, prof.HasHeader)
	if err != nil {
		return err
	}
	if missing := importcsv.MissingColumns(headers, ip); len(missing) > 0 {
		if in.ProfileID == 0 {
			_ = s.DeleteMappingProfile(profileID)
		}
		return fmt.Errorf("mapping uses column(s) not in this file: %q — pick the columns again", missing)
	}
	parsed := importcsv.ParseRows(headers, rows, ip)
	rules, err := s.ListPayeeRules()
	if err != nil {
		return err
	}
	sg, err := s.NewSuggester()
	if err != nil {
		return err
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM import_staging_rows WHERE batch_id=?`, in.BatchID); err != nil {
		return err
	}

	hj, _ := json.Marshal(headers)
	if _, err := tx.Exec(`UPDATE import_batches SET account_id=?, profile_id=?, status='mapped',
		headers_json=?, row_count=? WHERE id=?`,
		in.AccountID, profileID, string(hj), len(parsed), in.BatchID); err != nil {
		return err
	}

	for _, pr := range parsed {
		rawJSON, _ := json.Marshal(pr.Raw)
		fp := ""
		if pr.Error == "" {
			fp = importcsv.Fingerprint(in.AccountID, pr.Date, pr.Amount, pr.Payee)
		}
		action := "import"
		needsReview := 1
		var matchTx, dupTx, ruleID, catID sql.NullInt64
		var ruleName, person, rename, tags, categoryName string
		person = "shared"
		exception := pr.Error

		if pr.Error == "" {
			if rule := MatchPayeeRule(rules, pr.Payee, pr.Notes, pr.Amount, in.AccountID); rule != nil {
				ruleID = sql.NullInt64{Int64: rule.ID, Valid: true}
				ruleName = rule.Name
				if rule.SetCategoryID != nil {
					catID = sql.NullInt64{Int64: *rule.SetCategoryID, Valid: true}
					categoryName = rule.SetCategoryName
				}
				if rule.SetPerson != "" {
					person = rule.SetPerson
				}
				if rule.RenamePayee != "" {
					rename = rule.RenamePayee
				}
				tags = rule.SetTags
			} else if u := sg.Usual(pr.Payee); u != nil {
				catID = sql.NullInt64{Int64: u.ID, Valid: true}
				ruleName = UsualCategoryLabel
			}
			// Duplicate advisory
			if dupID, summary, ok := findFingerprintTx(tx, fp, in.AccountID); ok {
				dupTx = sql.NullInt64{Int64: dupID, Valid: true}
				_ = summary
				action = "skip" // default suggestion; human can change
			}
			// Manual ↔ import match (±5 days, same account+amount, no import batch)
			if mid, summary, ok := findManualMatch(tx, in.AccountID, pr.Amount, pr.Date, 5); ok {
				matchTx = sql.NullInt64{Int64: mid, Valid: true}
				_ = summary
				if action != "skip" {
					action = "merge"
				}
			}
			if !catID.Valid {
				needsReview = 1
			} else if action == "import" {
				needsReview = 1 // imported/rule-applied still go to review until confirmed
			}
		} else {
			action = "skip"
		}

		_, err = tx.Exec(`INSERT INTO import_staging_rows(
			batch_id, row_index, raw_json, transaction_date, amount, payee_name, notes, fingerprint,
			action, match_tx_id, matched_rule_id, matched_rule_name, category_id, person,
			rename_payee, tag_names, exception, duplicate_tx_id, needs_review)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			in.BatchID, pr.Index, string(rawJSON), nullStr(pr.Date), nullableInt64(pr.Amount, pr.Error == ""),
			nullStr(pr.Payee), nullStr(pr.Notes), nullStr(fp),
			action, matchTx, ruleID, nullStr(ruleName), catID, person,
			nullStr(rename), nullStr(tags), nullStr(exception), dupTx, needsReview)
		if err != nil {
			return err
		}
		_ = categoryName
	}

	// Cross-account transfer pairing: opposite amounts within ±5 days vs existing txs on other accounts
	if err := suggestTransferPairs(tx, in.BatchID, in.AccountID); err != nil {
		return err
	}

	return tx.Commit()
}

func toImportProfile(prof models.MappingProfile) importcsv.Profile {
	return importcsv.Profile{
		DateColumn:   prof.DateColumn,
		DateFormat:   prof.DateFormat,
		AmountMode:   prof.AmountMode,
		AmountColumn: prof.AmountColumn,
		DebitColumn:  prof.DebitColumn,
		CreditColumn: prof.CreditColumn,
		PayeeColumn:  prof.PayeeColumn,
		NotesColumn:  prof.NotesColumn,
		InvertSign:   prof.InvertSign,
		HasHeader:    prof.HasHeader,
		Delimiter:    DelimRune(prof.Delimiter),
	}
}

// MatchingProfile picks the saved profile that fits headers best: every mapped
// column must exist; more mapped columns wins, then the most recent.
func MatchingProfile(profiles []models.MappingProfile, headers []string, hasHeader bool) *models.MappingProfile {
	var best *models.MappingProfile
	bestScore := 0
	for i := range profiles {
		p := profiles[i]
		if p.DateColumn == "" || p.HasHeader != hasHeader {
			continue
		}
		ip := toImportProfile(p)
		cols := []string{p.DateColumn, p.PayeeColumn, p.NotesColumn}
		if p.AmountMode == "debit_credit" {
			if p.DebitColumn == "" && p.CreditColumn == "" {
				continue
			}
			cols = append(cols, p.DebitColumn, p.CreditColumn)
		} else {
			if p.AmountColumn == "" {
				continue
			}
			cols = append(cols, p.AmountColumn)
		}
		if len(importcsv.MissingColumns(headers, ip)) > 0 {
			continue
		}
		score := 0
		for _, c := range cols {
			if c != "" {
				score++
			}
		}
		if score > bestScore || (score == bestScore && best != nil && p.ID > best.ID) {
			best, bestScore = &profiles[i], score
		}
	}
	return best
}

// UsualCategoryLabel marks staging rows categorized from past transactions
// rather than by a rule.
const UsualCategoryLabel = "Usual category"

func nullableInt64(v int64, ok bool) any {
	if !ok {
		return nil
	}
	return v
}

func findFingerprintTx(tx *sql.Tx, fp string, accountID int64) (int64, string, bool) {
	if fp == "" {
		return 0, "", false
	}
	var id int64
	var date, payee string
	var amount int64
	err := tx.QueryRow(`SELECT id, transaction_date, COALESCE(payee_name,''), amount FROM transactions
		WHERE deleted_at IS NULL AND import_fingerprint=? AND account_id=?
		ORDER BY id DESC LIMIT 1`, fp, accountID).Scan(&id, &date, &payee, &amount)
	if err != nil {
		return 0, "", false
	}
	return id, fmt.Sprintf("%s %s %d", date, payee, amount), true
}

func findManualMatch(tx *sql.Tx, accountID, amount int64, date string, windowDays int) (int64, string, bool) {
	rows, err := tx.Query(`SELECT id, transaction_date, COALESCE(payee_name,''), amount FROM transactions
		WHERE deleted_at IS NULL AND account_id=? AND amount=? AND import_batch_id IS NULL
		AND transfer_id IS NULL`, accountID, amount)
	if err != nil {
		return 0, "", false
	}
	defer rows.Close()
	for rows.Next() {
		var id, amt int64
		var d, payee string
		if err := rows.Scan(&id, &d, &payee, &amt); err != nil {
			return 0, "", false
		}
		if importcsv.DateWithin(date, d, windowDays) {
			return id, fmt.Sprintf("%s · %s", d, payee), true
		}
	}
	return 0, "", false
}

// unlinkedTxSQL restricts transactions to ones not already in a transfer.
const unlinkedTxSQL = `deleted_at IS NULL AND transfer_id IS NULL
	AND id NOT IN (SELECT from_tx_id FROM transfers UNION ALL SELECT to_tx_id FROM transfers)`

// suggestTransferPairs pairs staged rows with opposite-amount transactions on
// other accounts within ±5 days. Closest dates are paired first and each staged
// row and each existing transaction is used at most once.
func suggestTransferPairs(tx *sql.Tx, batchID, accountID int64) error {
	rows, err := tx.Query(`SELECT id, transaction_date, amount FROM import_staging_rows
		WHERE batch_id=? AND exception IS NULL AND action='import' AND amount != 0`, batchID)
	if err != nil {
		return err
	}
	type st struct {
		id   int64
		date string
		amt  int64
	}
	var staging []st
	for rows.Next() {
		var r st
		if err := rows.Scan(&r.id, &r.date, &r.amt); err != nil {
			rows.Close()
			return err
		}
		staging = append(staging, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	type cand struct {
		rowID, txID int64
		dist        float64
	}
	var cands []cand
	for _, r := range staging {
		crow, err := tx.Query(`SELECT id, ABS(julianday(transaction_date) - julianday(?)) FROM transactions
			WHERE account_id != ? AND amount = ? AND `+unlinkedTxSQL+`
			AND ABS(julianday(transaction_date) - julianday(?)) <= 5`,
			r.date, accountID, -r.amt, r.date)
		if err != nil {
			return err
		}
		for crow.Next() {
			c := cand{rowID: r.id}
			if err := crow.Scan(&c.txID, &c.dist); err != nil {
				crow.Close()
				return err
			}
			cands = append(cands, c)
		}
		crow.Close()
		if err := crow.Err(); err != nil {
			return err
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		if cands[i].rowID != cands[j].rowID {
			return cands[i].rowID < cands[j].rowID
		}
		return cands[i].txID > cands[j].txID
	})
	usedRow, usedTx := map[int64]bool{}, map[int64]bool{}
	for _, c := range cands {
		if usedRow[c.rowID] || usedTx[c.txID] {
			continue
		}
		usedRow[c.rowID], usedTx[c.txID] = true, true
		if _, err := tx.Exec(`UPDATE import_staging_rows SET action='transfer_pair', transfer_tx_id=?,
			match_tx_id=NULL WHERE id=? AND action='import'`, c.txID, c.rowID); err != nil {
			return err
		}
	}
	return nil
}

type StagingFilter struct {
	BatchID        int64
	Page           int
	PageSize       int
	ExceptionsOnly bool
	Action         string
}

func (s *Store) ListStagingRows(f StagingFilter) ([]models.StagingRow, int, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = 25
	}
	where := `WHERE s.batch_id=?`
	args := []any{f.BatchID}
	if f.ExceptionsOnly {
		where += ` AND (s.exception IS NOT NULL AND s.exception != '' OR s.duplicate_tx_id IS NOT NULL OR s.action != 'import')`
	}
	if f.Action != "" {
		where += ` AND s.action=?`
		args = append(args, f.Action)
	}
	var total int
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM import_staging_rows s `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (f.Page - 1) * f.PageSize
	q := `SELECT s.id, s.batch_id, s.row_index, s.raw_json, COALESCE(s.transaction_date,''), COALESCE(s.amount,0),
		COALESCE(s.payee_name,''), COALESCE(s.notes,''), COALESCE(s.fingerprint,''), s.action,
		s.match_tx_id, s.transfer_tx_id, s.matched_rule_id, COALESCE(s.matched_rule_name,''),
		s.category_id, COALESCE(c.name,''), s.person, COALESCE(s.rename_payee,''), COALESCE(s.tag_names,''),
		COALESCE(s.exception,''), s.duplicate_tx_id, s.needs_review
		FROM import_staging_rows s
		LEFT JOIN categories c ON c.id = s.category_id
		` + where + ` ORDER BY s.row_index ASC LIMIT ? OFFSET ?`
	args = append(args, f.PageSize, offset)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []models.StagingRow
	for rows.Next() {
		var r models.StagingRow
		var match, xfer, rule, cat, dup sql.NullInt64
		var needs int
		if err := rows.Scan(&r.ID, &r.BatchID, &r.RowIndex, &r.RawJSON, &r.TransactionDate, &r.Amount,
			&r.PayeeName, &r.Notes, &r.Fingerprint, &r.Action,
			&match, &xfer, &rule, &r.MatchedRuleName, &cat, &r.CategoryName, &r.Person,
			&r.RenamePayee, &r.TagNames, &r.Exception, &dup, &needs); err != nil {
			return nil, 0, err
		}
		if match.Valid {
			v := match.Int64
			r.MatchTxID = &v
			r.MatchSummary = s.txShort(v)
		}
		if xfer.Valid {
			v := xfer.Int64
			r.TransferTxID = &v
		}
		if rule.Valid {
			v := rule.Int64
			r.MatchedRuleID = &v
		}
		if cat.Valid {
			v := cat.Int64
			r.CategoryID = &v
		}
		if dup.Valid {
			v := dup.Int64
			r.DuplicateTxID = &v
			r.DuplicateSummary = s.txShort(v)
		}
		r.NeedsReview = needs == 1
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (s *Store) GetStagingRow(id int64) (*models.StagingRow, error) {
	row := s.DB.QueryRow(`SELECT s.id, s.batch_id, s.row_index, s.raw_json, COALESCE(s.transaction_date,''), COALESCE(s.amount,0),
		COALESCE(s.payee_name,''), COALESCE(s.notes,''), COALESCE(s.fingerprint,''), s.action,
		s.match_tx_id, s.transfer_tx_id, s.matched_rule_id, COALESCE(s.matched_rule_name,''),
		s.category_id, COALESCE(c.name,''), s.person, COALESCE(s.rename_payee,''), COALESCE(s.tag_names,''),
		COALESCE(s.exception,''), s.duplicate_tx_id, s.needs_review
		FROM import_staging_rows s
		LEFT JOIN categories c ON c.id = s.category_id
		WHERE s.id=?`, id)
	var r models.StagingRow
	var match, xfer, rule, cat, dup sql.NullInt64
	var needs int
	if err := row.Scan(&r.ID, &r.BatchID, &r.RowIndex, &r.RawJSON, &r.TransactionDate, &r.Amount,
		&r.PayeeName, &r.Notes, &r.Fingerprint, &r.Action,
		&match, &xfer, &rule, &r.MatchedRuleName, &cat, &r.CategoryName, &r.Person,
		&r.RenamePayee, &r.TagNames, &r.Exception, &dup, &needs); err != nil {
		return nil, err
	}
	if match.Valid {
		v := match.Int64
		r.MatchTxID = &v
		r.MatchSummary = s.txShort(v)
	}
	if xfer.Valid {
		v := xfer.Int64
		r.TransferTxID = &v
	}
	if rule.Valid {
		v := rule.Int64
		r.MatchedRuleID = &v
	}
	if cat.Valid {
		v := cat.Int64
		r.CategoryID = &v
	}
	if dup.Valid {
		v := dup.Int64
		r.DuplicateTxID = &v
		r.DuplicateSummary = s.txShort(v)
	}
	r.NeedsReview = needs == 1
	return &r, nil
}

func (s *Store) txShort(id int64) string {
	var date, payee string
	var amount int64
	err := s.DB.QueryRow(`SELECT transaction_date, COALESCE(payee_name,''), amount FROM transactions WHERE id=?`, id).
		Scan(&date, &payee, &amount)
	if err != nil {
		return fmt.Sprintf("#%d", id)
	}
	return fmt.Sprintf("#%d · %s · %s", id, date, payee)
}

func (s *Store) SetStagingAction(rowID int64, action string, matchTxID *int64) error {
	switch action {
	case "import", "skip", "merge", "transfer_pair":
	default:
		return fmt.Errorf("invalid action")
	}
	_, err := s.DB.Exec(`UPDATE import_staging_rows SET action=?, match_tx_id=COALESCE(?, match_tx_id) WHERE id=?`,
		action, matchTxID, rowID)
	return err
}

func (s *Store) BulkStagingAction(batchID int64, rowIDs []int64, action string) error {
	if len(rowIDs) == 0 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range rowIDs {
		_, err = tx.Exec(`UPDATE import_staging_rows SET action=? WHERE id=? AND batch_id=?`, action, id, batchID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpdateStagingRow(rowID int64, categoryID *int64, person, payee, notes string, action string) error {
	if person == "" {
		person = "shared"
	}
	if action == "" {
		action = "import"
	}
	_, err := s.DB.Exec(`UPDATE import_staging_rows SET category_id=?, person=?,
		payee_name=COALESCE(NULLIF(?,''), payee_name), notes=?, action=?,
		rename_payee=CASE WHEN ? != '' THEN ? ELSE rename_payee END,
		needs_review=CASE WHEN ? IS NULL THEN 1 ELSE 0 END
		WHERE id=?`,
		categoryID, person, payee, nullStr(notes), action, payee, payee, categoryID, rowID)
	return err
}

// CommitImport creates/merges transactions from staging rows.
func (s *Store) CommitImport(batchID, personID int64) (int, error) {
	b, err := s.GetImportBatch(batchID)
	if err != nil {
		return 0, err
	}
	if b.Status == "committed" {
		return 0, fmt.Errorf("batch already committed")
	}
	if b.AccountID == nil {
		return 0, fmt.Errorf("batch has no account")
	}
	accountID := *b.AccountID

	rows, _, err := s.ListStagingRows(StagingFilter{BatchID: batchID, Page: 1, PageSize: 100000})
	if err != nil {
		return 0, err
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	imported := 0
	for _, r := range rows {
		if r.Exception != "" || r.Action == "skip" {
			continue
		}
		switch r.Action {
		case "merge":
			if r.MatchTxID == nil {
				continue
			}
			if err := mergeManualImport(tx, *r.MatchTxID, r, batchID, personID); err != nil {
				return 0, err
			}
			imported++
		case "transfer_pair":
			if err := commitTransferPair(tx, accountID, r, batchID, personID); err != nil {
				return 0, err
			}
			imported++
		default: // import
			if err := commitNewImport(tx, accountID, r, batchID, personID); err != nil {
				return 0, err
			}
			imported++
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.Exec(`UPDATE import_batches SET status='committed', imported_count=?, committed_at=?
		WHERE id=? AND status != 'committed'`, imported, now, batchID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, fmt.Errorf("batch already committed")
	}
	return imported, tx.Commit()
}

func commitNewImport(tx *sql.Tx, accountID int64, r models.StagingRow, batchID, personID int64) error {
	payee := r.PayeeName
	if r.RenamePayee != "" {
		payee = r.RenamePayee
	}
	var payeeID *int64
	if strings.TrimSpace(payee) != "" {
		pid, err := ensurePayeeTx(tx, payee, r.CategoryID, &accountID)
		if err != nil {
			return err
		}
		payeeID = &pid
	}
	needs := 1
	if !r.NeedsReview {
		needs = 0
	}
	var cat any
	if r.CategoryID != nil {
		cat = *r.CategoryID
	}
	res, err := tx.Exec(`INSERT INTO transactions(
		account_id, payee_id, payee_name, notes, amount, transaction_date, person, status,
		needs_review, category_id, is_split, created_by, updated_by, import_batch_id, import_fingerprint)
		VALUES (?,?,?,?,?,?,?, 'cleared', ?, ?, 0, ?, ?, ?, ?)`,
		accountID, payeeID, nullStr(payee), nullStr(r.Notes), r.Amount, r.TransactionDate, r.Person,
		needs, cat, personID, personID, batchID, nullStr(r.Fingerprint))
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	if r.CategoryID != nil {
		_, err = tx.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, display_order)
			VALUES (?,?,?,?,0)`, id, *r.CategoryID, r.Person, r.Amount)
		if err != nil {
			return err
		}
	}
	for _, tn := range strings.Split(r.TagNames, ",") {
		tn = strings.TrimSpace(tn)
		if tn == "" {
			continue
		}
		tid, err := ensureTagTx(tx, tn)
		if err != nil {
			return err
		}
		_, _ = tx.Exec(`INSERT OR IGNORE INTO transaction_tags(transaction_id, tag_id) VALUES (?,?)`, id, tid)
	}
	return nil
}

func mergeManualImport(tx *sql.Tx, manualID int64, r models.StagingRow, batchID, personID int64) error {
	// Keep manual category/notes/person/payee; adopt bank date + cleared; attach fingerprint/batch.
	_, err := tx.Exec(`UPDATE transactions SET
		transaction_date=?, status='cleared', needs_review=0,
		import_batch_id=?, import_fingerprint=?,
		updated_by=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id=? AND deleted_at IS NULL`,
		r.TransactionDate, batchID, nullStr(r.Fingerprint), personID, manualID)
	return err
}

func commitTransferPair(tx *sql.Tx, accountID int64, r models.StagingRow, batchID, personID int64) error {
	if r.TransferTxID == nil {
		return commitNewImport(tx, accountID, r, batchID, personID)
	}
	otherID := *r.TransferTxID
	var otherAmt int64
	err := tx.QueryRow(`SELECT amount FROM transactions WHERE id=? AND account_id != ? AND `+unlinkedTxSQL,
		otherID, accountID).Scan(&otherAmt)
	if err != nil || r.Amount == 0 || otherAmt != -r.Amount {
		// Counterpart gone, edited, or already paired (by this or an earlier
		// commit): import as a plain transaction instead.
		return commitNewImport(tx, accountID, r, batchID, personID)
	}
	xferCat, err := categoryIDByKindNameTx(tx, "transfer", "Transfer")
	if err != nil {
		return err
	}

	payee := r.PayeeName
	if r.RenamePayee != "" {
		payee = r.RenamePayee
	}
	res, err := tx.Exec(`INSERT INTO transactions(
		account_id, payee_name, notes, amount, transaction_date, person, status,
		needs_review, category_id, is_split, created_by, updated_by, import_batch_id, import_fingerprint)
		VALUES (?,?,?,?,?, 'shared', 'cleared', 0, ?, 0, ?, ?, ?, ?)`,
		accountID, nullStr(payee), nullStr(r.Notes), r.Amount, r.TransactionDate,
		xferCat, personID, personID, batchID, nullStr(r.Fingerprint))
	if err != nil {
		return err
	}
	newID, _ := res.LastInsertId()
	_, err = tx.Exec(`INSERT INTO transaction_splits(transaction_id, category_id, person, amount, display_order)
		VALUES (?,?, 'shared', ?, 0)`, newID, xferCat, r.Amount)
	if err != nil {
		return err
	}
	fromID, toID := otherID, newID
	if r.Amount < 0 {
		fromID, toID = newID, otherID
	}
	tres, err := tx.Exec(`INSERT INTO transfers(from_tx_id, to_tx_id) VALUES (?,?) ON CONFLICT DO NOTHING`, fromID, toID)
	if err != nil {
		return err
	}
	if n, _ := tres.RowsAffected(); n == 0 {
		_, err = tx.Exec(`DELETE FROM transaction_splits WHERE transaction_id=?`, newID)
		if err == nil {
			_, err = tx.Exec(`UPDATE transactions SET category_id=NULL, needs_review=1 WHERE id=?`, newID)
		}
		return err
	}
	xferID, _ := tres.LastInsertId()
	if _, err := tx.Exec(`UPDATE transactions SET transfer_id=? WHERE id IN (?,?)`, xferID, fromID, toID); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE transactions SET category_id=?, needs_review=0,
		updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, xferCat, otherID)
	return err
}

// UndoImportBatch soft-deletes transactions created by the batch and clears merge tags.
func (s *Store) UndoImportBatch(batchID, personID int64) error {
	b, err := s.GetImportBatch(batchID)
	if err != nil {
		return err
	}
	if b.Status != "committed" {
		return fmt.Errorf("only committed batches can be undone")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Merged rows: strip import markers only (manual data stays)
	_, err = tx.Exec(`UPDATE transactions SET import_batch_id=NULL, import_fingerprint=NULL, updated_at=?
		WHERE import_batch_id=? AND deleted_at IS NULL
		AND created_at < ?`, now, batchID, b.CommittedAt)
	if err != nil {
		return err
	}

	// Newly inserted rows (created at/after commit): soft-delete + unlink transfers
	rows, err := tx.Query(`SELECT id, transfer_id FROM transactions
		WHERE import_batch_id=? AND deleted_at IS NULL`, batchID)
	if err != nil {
		return err
	}
	var toDelete []int64
	var transferIDs []int64
	for rows.Next() {
		var id int64
		var xfer sql.NullInt64
		if err := rows.Scan(&id, &xfer); err != nil {
			rows.Close()
			return err
		}
		toDelete = append(toDelete, id)
		if xfer.Valid {
			transferIDs = append(transferIDs, xfer.Int64)
		}
	}
	rows.Close()

	for _, xid := range transferIDs {
		var fromID, toID int64
		err := tx.QueryRow(`SELECT from_tx_id, to_tx_id FROM transfers WHERE id=?`, xid).Scan(&fromID, &toID)
		if err == nil {
			_, _ = tx.Exec(`UPDATE transactions SET transfer_id=NULL WHERE id IN (?,?)`, fromID, toID)
			_, _ = tx.Exec(`DELETE FROM transfers WHERE id=?`, xid)
		}
	}
	for _, id := range toDelete {
		_, err = tx.Exec(`UPDATE transactions SET deleted_at=?, updated_by=?, updated_at=?, import_batch_id=NULL
			WHERE id=?`, now, personID, now, id)
		if err != nil {
			return err
		}
	}

	_, err = tx.Exec(`UPDATE import_batches SET status='undone', undone_at=? WHERE id=?`, now, batchID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
